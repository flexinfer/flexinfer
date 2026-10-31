package proxy

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/validation"
)

// Lane QoS enforcement (D3 MR 2).
//
// A lane enforces classes when it opts in: spec.config.schedulingPolicy:
// priority (the proxy gate plus vLLM's per-request priority, MR 3) or the
// annotation flexinfer.ai/qos: proxy (the gate alone, the fallback when vLLM
// preemption does not pay). flexinfer.ai/qos: off turns both off. Every other
// lane treats every class as interactive and is served exactly as before.
//
// Why the proxy holds a seat back. vLLM 0.32's scheduler does not preempt a
// running sequence to free a seat: when len(running) reaches max_num_seqs the
// waiting loop breaks (vllm/v1/core/sched/scheduler.py), and priority only
// orders the waiting queue and picks the victim when KV blocks run out. On a
// two-seat lane two background generations would therefore hold both seats
// and an interactive turn would wait for one of them to finish. The gate
// admits background and batch work only while it leaves
// flexinfer.ai/qos-interactive-reserve seats (default 1 when the lane has two
// or more) free for interactive requests, which are never held.
//
// The gate counts this proxy replica's requests only; it assumes one proxy
// replica and one lane replica (seats = spec.config.maxNumSeqs).

const (
	// annotationQoS opts a lane into the proxy gate ("proxy") or out of all
	// QoS enforcement ("off").
	annotationQoS = "flexinfer.ai/qos"
	// annotationQoSReserve is the number of seats held for interactive
	// requests.
	annotationQoSReserve = "flexinfer.ai/qos-interactive-reserve"

	headerQueuePosition = "X-FlexInfer-Queue-Position"

	defaultQoSQueueWait  = 60 * time.Second
	defaultQoSQueueDepth = 32
	// qosRetryAfterSeed is the assumed request duration before a lane has
	// completed any gated request.
	qosRetryAfterSeed = 30 * time.Second
	qosRetryAfterMax  = 300
)

type qosMode string

const (
	qosModeOff      qosMode = "off"
	qosModeProxy    qosMode = "proxy"
	qosModePriority qosMode = "priority"
)

// qosLanePolicy is how one lane treats classes.
type qosLanePolicy struct {
	mode    qosMode
	seats   int
	reserve int
}

// qosLanePolicyFor derives the lane's policy from its Model.
func qosLanePolicyFor(m *aiv1alpha2.Model) qosLanePolicy {
	off := qosLanePolicy{mode: qosModeOff}
	if m == nil {
		return off
	}
	ann := strings.ToLower(strings.TrimSpace(m.Annotations[annotationQoS]))
	policy := qosLanePolicy{seats: m.Spec.ConfigInt("maxNumSeqs", 0)}
	switch {
	case ann == string(qosModeOff):
		return off
	case strings.EqualFold(m.Spec.ConfigString("schedulingPolicy", ""), "priority"):
		policy.mode = qosModePriority
	case ann == string(qosModeProxy):
		policy.mode = qosModeProxy
	default:
		return off
	}
	if policy.seats >= 2 {
		policy.reserve = 1
	}
	if v, err := strconv.Atoi(strings.TrimSpace(m.Annotations[annotationQoSReserve])); err == nil && v >= 0 {
		policy.reserve = v
	}
	if policy.reserve > policy.seats-1 {
		policy.reserve = max(policy.seats-1, 0)
	}
	return policy
}

// gated reports whether the proxy gate applies: the lane opted in and
// declares its seat count.
func (l qosLanePolicy) gated() bool {
	return l.mode != qosModeOff && l.seats > 0
}

// backgroundSeats is how many requests may be in flight before background
// and batch work waits.
func (l qosLanePolicy) backgroundSeats() int {
	return l.seats - l.reserve
}

// rank orders the gate's wait queue: background before batch. Interactive
// requests never wait.
func (c qosClass) rank() int {
	switch c {
	case qosBackground:
		return 1
	case qosBatch:
		return 2
	}
	return 0
}

// Gate outcomes, the outcome label of flexinfer_proxy_qos_decisions_total.
const (
	qosOutcomeAdmitted = "admitted" // took a seat without waiting
	qosOutcomeQueued   = "queued"   // took a seat after waiting
	qosOutcomeRejected = "rejected" // queue full or wait expired: 503 queue_full
	qosOutcomeCanceled = "canceled" // the caller left while waiting
)

type qosWaiter struct {
	class    qosClass
	seq      uint64
	ready    chan struct{}
	admitted bool
}

// qosGate is one lane's in-flight count and wait queue.
type qosGate struct {
	mu       sync.Mutex
	inflight int
	limit    int
	waiters  []*qosWaiter // ordered by (rank, seq)
	seq      uint64
	ewma     time.Duration // recent gated request duration, for Retry-After
}

// qosQueueFullError carries the caller's queue position and the suggested
// retry delay for the 503.
type qosQueueFullError struct {
	position   int
	retryAfter int
}

func (e *qosQueueFullError) Error() string {
	return fmt.Sprintf("qos queue full (position %d)", e.position)
}

// acquire takes a seat for one request. Interactive requests always take one
// at once. Background and batch requests take one while the lane has more
// than its reserve free; otherwise they wait in class-then-FIFO order for up
// to maxWait. The returned outcome is admitted, queued, rejected or canceled;
// a non-nil error means no seat was taken.
func (g *qosGate) acquire(ctx context.Context, class qosClass, policy qosLanePolicy, maxWait time.Duration, maxDepth int) (string, error) {
	g.mu.Lock()
	g.limit = policy.backgroundSeats()
	if class.rank() == 0 {
		g.inflight++
		g.mu.Unlock()
		return qosOutcomeAdmitted, nil
	}
	if len(g.waiters) >= maxDepth {
		err := g.queueFullLocked(len(g.waiters) + 1)
		g.mu.Unlock()
		return qosOutcomeRejected, err
	}
	g.seq++
	w := &qosWaiter{class: class, seq: g.seq, ready: make(chan struct{})}
	i := sort.Search(len(g.waiters), func(i int) bool {
		o := g.waiters[i]
		return o.class.rank() > class.rank() || (o.class.rank() == class.rank() && o.seq > w.seq)
	})
	g.waiters = append(g.waiters, nil)
	copy(g.waiters[i+1:], g.waiters[i:])
	g.waiters[i] = w
	g.wakeLocked()
	if w.admitted {
		g.mu.Unlock()
		return qosOutcomeAdmitted, nil
	}
	g.mu.Unlock()

	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	select {
	case <-w.ready:
		return qosOutcomeQueued, nil
	case <-timer.C:
		g.mu.Lock()
		defer g.mu.Unlock()
		if w.admitted {
			return qosOutcomeQueued, nil
		}
		return qosOutcomeRejected, g.queueFullLocked(g.removeLocked(w))
	case <-ctx.Done():
		g.mu.Lock()
		defer g.mu.Unlock()
		if w.admitted {
			g.inflight--
			g.wakeLocked()
		} else {
			g.removeLocked(w)
		}
		return qosOutcomeCanceled, ctx.Err()
	}
}

// release frees a seat taken by acquire and admits the next waiters.
func (g *qosGate) release(elapsed time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inflight--
	if g.ewma == 0 {
		g.ewma = elapsed
	} else {
		g.ewma = (4*g.ewma + elapsed) / 5
	}
	g.wakeLocked()
}

func (g *qosGate) wakeLocked() {
	for len(g.waiters) > 0 && g.inflight < g.limit {
		w := g.waiters[0]
		g.waiters = g.waiters[1:]
		w.admitted = true
		g.inflight++
		close(w.ready)
	}
}

// removeLocked drops a waiter and returns the 1-based position it held.
func (g *qosGate) removeLocked(w *qosWaiter) int {
	for i, o := range g.waiters {
		if o == w {
			g.waiters = append(g.waiters[:i], g.waiters[i+1:]...)
			return i + 1
		}
	}
	return len(g.waiters) + 1
}

// queueFullLocked builds the rejection: Retry-After is the recent request
// duration times the requests ahead per background seat, at least a second.
func (g *qosGate) queueFullLocked(position int) *qosQueueFullError {
	per := g.ewma
	if per == 0 {
		per = qosRetryAfterSeed
	}
	retry := math.Ceil(per.Seconds() * float64(position) / float64(max(g.limit, 1)))
	return &qosQueueFullError{position: position, retryAfter: int(min(max(retry, 1), qosRetryAfterMax))}
}

func (p *Proxy) qosGateFor(lane string) *qosGate {
	if g, ok := p.qosGates.Load(lane); ok {
		return g
	}
	g, _ := p.qosGates.LoadOrStore(lane, &qosGate{})
	return g
}

// qosLanePolicy looks up the lane's policy from the Model cache.
func (p *Proxy) qosLanePolicy(ctx context.Context, lane string) qosLanePolicy {
	if lane == "" {
		return qosLanePolicy{mode: qosModeOff}
	}
	m, err := p.getModel(ctx, lane)
	if err != nil {
		return qosLanePolicy{mode: qosModeOff}
	}
	return qosLanePolicyFor(m)
}

// qosAdmit runs the lane's gate for a request about to be forwarded. It
// returns the decision outcome and a release func; ok is false when the
// request was refused and the response has been written.
func (p *Proxy) qosAdmit(w http.ResponseWriter, r *http.Request, d *qosDecision) (outcome string, release func(), ok bool) {
	if d == nil || d.lane == "" {
		return qosOutcomeUnenforced, func() {}, true
	}
	policy := p.qosLanePolicy(r.Context(), d.lane)
	if !policy.gated() {
		return qosOutcomeUnenforced, func() {}, true
	}
	maxWait, maxDepth := p.qosQueueWait, p.qosQueueDepth
	if maxWait <= 0 {
		maxWait = defaultQoSQueueWait
	}
	if maxDepth <= 0 {
		maxDepth = defaultQoSQueueDepth
	}
	gate := p.qosGateFor(d.lane)
	outcome, err := gate.acquire(r.Context(), d.class, policy, maxWait, maxDepth)
	if err != nil {
		qosDecisionsTotal.WithLabelValues(string(d.class), d.lane, outcome).Inc()
		if full, isFull := err.(*qosQueueFullError); isFull {
			requestsTotal.WithLabelValues(d.lane, "qos_queue_full").Inc()
			writeQoSQueueFull(w, d, full)
		}
		return outcome, nil, false
	}
	start := time.Now()
	return outcome, func() { gate.release(time.Since(start)) }, true
}

// writeQoSQueueFull answers a refused background or batch request: 503 with
// Retry-After and the caller's queue position.
func writeQoSQueueFull(w http.ResponseWriter, d *qosDecision, full *qosQueueFullError) {
	w.Header().Set("Retry-After", strconv.Itoa(full.retryAfter))
	w.Header().Set(headerQueuePosition, strconv.Itoa(full.position))
	validation.WriteError(w, http.StatusServiceUnavailable,
		fmt.Sprintf("lane %q has no seat free for %s requests (queue position %d); retry after %ds",
			d.lane, d.class, full.position, full.retryAfter),
		"service_unavailable_error", validation.CodeQueueFull)
}
