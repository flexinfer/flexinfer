package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	"github.com/flexinfer/flexinfer/pkg/validation"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// QueuedRequest represents a request waiting in queue during cold start.
type QueuedRequest struct {
	w          http.ResponseWriter
	r          *http.Request
	modelName  string
	done       chan struct{}
	err        error
	enqueuedAt time.Time
	responded  atomic.Bool
	// rank is the QoS class rank on a lane that enforces classes, 0 elsewhere;
	// drainQueue serves lower ranks first, FIFO within a rank.
	rank int
	// aborted is set by the drain before it closes done when serving the
	// request panicked after part of the response went out (serveDrained).
	aborted bool
}

// RequestQueue holds requests for a model during cold start.
//
// items is bounded by the proxy's maxQueueSize. Every send to and receive
// from items goes through mu, so a request that leaves the queue early can
// take its entry out (forget) without racing a drain: a slot is taken only
// while its request still waits.
//
// A queue leaves p.queues only through retireQueue or retireIdleQueue, which
// close it under mu and remove it with a compare-and-delete on the pointer:
// a closed queue takes no more requests, and one queue's processor can never
// unmap another queue registered later under the same model name.
type RequestQueue struct {
	model    string
	mu       sync.Mutex
	items    chan *QueuedRequest
	closed   bool // under mu: retired, takes no more requests
	created  time.Time
	draining atomic.Bool
	// processing is true from creation until processQueue returns. The stale
	// sweeper leaves a queue alone while it is set, however long the
	// processor waits for readiness and however empty the queue is.
	processing atomic.Bool
}

// offer adds qr to the queue. queued reports whether a slot was free; closed
// reports that the queue was retired, and the caller must get the model's
// current queue and offer again.
func (q *RequestQueue) offer(qr *QueuedRequest) (queued, closed bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false, true
	}
	select {
	case q.items <- qr:
		return true, false
	default:
		return false, false
	}
}

// retireQueue closes q, takes the requests still in it and removes q from
// p.queues if it is still the model's registered queue. The caller serves or
// fails the returned requests. Closing and taking under mu means no request
// can be offered to q after its last take.
func (p *Proxy) retireQueue(q *RequestQueue) []*QueuedRequest {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	var rest []*QueuedRequest
	for taking := true; taking; {
		select {
		case qr := <-q.items:
			rest = append(rest, qr)
		default:
			taking = false
		}
	}
	p.queues.CompareAndDelete(q.model, q)
	return rest
}

// retireIdleQueue retires q only if no processor runs for it and it holds no
// request, and reports whether it did.
func (p *Proxy) retireIdleQueue(q *RequestQueue) bool {
	if q.processing.Load() {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.processing.Load() || len(q.items) > 0 {
		return false
	}
	q.closed = true
	p.queues.CompareAndDelete(q.model, q)
	return true
}

// forget takes every request whose waiter has already claimed it (it timed
// out, was refused, or its client went away) out of the queue, so its slot is
// free for the next request instead of staying taken until a drain. The other
// entries keep their order. It returns how many entries it removed.
func (q *RequestQueue) forget() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	var keep []*QueuedRequest
	removed := 0
	for taking := true; taking; {
		select {
		case qr := <-q.items:
			if qr.responded.Load() {
				removed++
			} else {
				keep = append(keep, qr)
			}
		default:
			taking = false
		}
	}
	// mu blocks every other send, so the kept entries fit back.
	for _, qr := range keep {
		q.items <- qr
	}
	if removed > 0 {
		queueDepth.WithLabelValues(q.model).Sub(float64(removed))
	}
	return removed
}

// preemptedRefusal is the error handleColdStart returns after it answered a
// request with model_preempted at the end of the preempted wait. It is an
// expected answer, not a proxy fault, so the caller counts it apart from
// errors.
type preemptedRefusal struct {
	model, holder string
	waited        time.Duration
}

func (e *preemptedRefusal) Error() string {
	return fmt.Sprintf("model %s preempted by %s; refused after waiting %s", e.model, e.holder, e.waited)
}

// handleColdStart handles requests when model is scaled to zero.
func (p *Proxy) handleColdStart(ctx context.Context, w http.ResponseWriter, r *http.Request, modelName string, start time.Time) error {
	ctx, coldSpan := otel.Tracer("flexinfer/proxy").Start(ctx, "proxy.cold_start")
	defer coldSpan.End()
	coldSpan.SetAttributes(attribute.String("flexinfer.model", modelName))

	// Refresh demand for every cold-start request, even when a queue already
	// exists. A previous queue processor may still be waiting on readiness, so
	// relying only on queue creation can leave shared-GPU arbitration with stale
	// demand and prevent priority preemption.
	p.activator.TouchLastActiveTime(ctx, modelName)

	// Create queued request
	qr := &QueuedRequest{
		w:          w,
		r:          r,
		modelName:  modelName,
		done:       make(chan struct{}),
		enqueuedAt: time.Now(),
	}
	if d := qosDecisionFrom(ctx); d != nil && p.qosLanePolicy(ctx, modelName).gated() {
		qr.rank = d.class.rank()
	}

	// Add the request to the model's queue (non-blocking). A queue retired
	// between the lookup and the offer is already unmapped, so the next
	// lookup returns the model's current queue or creates one.
	var queue *RequestQueue
	queued := false
	for {
		queue = p.getOrCreateQueue(modelName)
		var closed bool
		if queued, closed = queue.offer(qr); !closed {
			break
		}
	}
	if !queued {
		queueRejectedTotal.WithLabelValues(modelName).Inc()
		m := p.peekModel(ctx, modelName)
		w.Header().Set(headerActivation, activationValue(m))
		p.writeLaneError(w, m, laneError{
			status:  http.StatusServiceUnavailable,
			code:    validation.CodeQueueFull,
			message: "Service overloaded, please retry",
		})
		return fmt.Errorf("queue full for model %s", modelName)
	}
	queuedRequestsTotal.WithLabelValues(modelName).Inc()
	queueDepth.WithLabelValues(modelName).Inc()
	slog.Debug("request queued", "model", modelName, "queue_depth", len(queue.items))

	// A queued foreground request is live demand even while shared election,
	// capacity, or an incumbent's request drain delays activation. Join the same
	// per-model heartbeat used by forwarding and release it on every exit,
	// including cancellation and queue timeout. It joins as demand only: the
	// request counts as interactive use of the lane (lane_release.go) when the
	// drain forwards it to the backend, not while it waits and not when the
	// proxy refuses it.
	stopHeartbeat := p.startDemandHeartbeat(modelName, false)
	defer stopHeartbeat()

	// Wait for request to be processed with timeout
	timeout := p.queueTimeout
	if coldStartTimeout := p.activator.GetColdStartTimeout(ctx, modelName); coldStartTimeout > timeout {
		timeout = coldStartTimeout
	}
	// A caller that sent Prefer: wait=N waits at most N seconds and then gets
	// a 503 model_cold_starting with the ETA; the activation it started goes on.
	preferWait, prefersWait := preferredWait(r)
	sentPrefer := prefersWait
	if prefersWait && preferWait < timeout {
		timeout = preferWait
		w.Header().Set("Preference-Applied", "wait="+strconv.Itoa(int(preferWait/time.Second)))
	} else {
		prefersWait = false
	}

	queueCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// A request without Prefer: wait, on a shared-GPU lane that a
	// higher-priority peer holds the card against, waits at most the
	// preempted wait (preempted_wait.go). A job's forward keeps the full wait:
	// the job runner has no client timeout, and its queued forward is the
	// demand that brings the lane back. When the bound fires the check runs
	// again, so a lane that won the card in the meantime keeps the request
	// for its load.
	var preemptedC <-chan time.Time
	var preemptedBound time.Duration
	if !sentPrefer && !isJobForward(ctx) {
		if m := p.peekModel(ctx, modelName); p.losingCardTo(ctx, m) != "" {
			if preemptedBound = p.preemptedWait(m); preemptedBound < timeout {
				if p.preemptedWaitTimer != nil {
					preemptedC = p.preemptedWaitTimer(preemptedBound)
				} else {
					timer := time.NewTimer(preemptedBound)
					defer timer.Stop()
					preemptedC = timer.C
				}
			}
		}
	}

	for {
		select {
		case <-qr.done:
			// Request was processed
			queueWaitDuration.WithLabelValues(modelName).Observe(time.Since(qr.enqueuedAt).Seconds())
			// Success metrics recorded by the processing goroutine
			return qr.finish()
		case <-preemptedC:
			preemptedC = nil
			m := p.peekModel(ctx, modelName)
			holder := p.losingCardTo(ctx, m)
			if holder == "" {
				// The lane may win the card now: wait for its load as
				// before, up to the full timeout.
				continue
			}
			if !qr.responded.CompareAndSwap(false, true) {
				// A drain claimed the request first and is serving it.
				continue
			}
			queue.forget()
			queueWaitDuration.WithLabelValues(modelName).Observe(time.Since(qr.enqueuedAt).Seconds())
			w.Header().Set(headerActivation, activationValue(m))
			p.writeLaneError(w, m, laneError{
				status: http.StatusServiceUnavailable,
				code:   modelStateCode(m),
				message: fmt.Sprintf("model %q is not ready after the preempted wait of %s: %q holds its shared GPU at a higher priority; the request is recorded as demand",
					modelName, preemptedBound, holder),
			})
			return &preemptedRefusal{model: modelName, holder: holder, waited: preemptedBound}
		case <-queueCtx.Done():
			// Distinguish an actual queue timeout from caller cancellation.
			queueWaitDuration.WithLabelValues(modelName).Observe(time.Since(qr.enqueuedAt).Seconds())
			// Claim the item before returning so a later queue drain cannot
			// forward a request whose client has already disconnected, then
			// take it out of the queue so its slot is free at once.
			if !qr.responded.CompareAndSwap(false, true) {
				// A drain claimed the request first and is writing its
				// response: wait until it is done with the writer.
				<-qr.done
				return qr.finish()
			}
			queue.forget()
			if stderrors.Is(queueCtx.Err(), context.DeadlineExceeded) {
				m := p.peekModel(ctx, modelName)
				w.Header().Set(headerActivation, activationValue(m))
				if prefersWait {
					p.writeLaneError(w, m, laneError{
						status:  http.StatusServiceUnavailable,
						code:    modelStateCode(m),
						message: fmt.Sprintf("model %q is not ready after the preferred wait of %s; activation continues", modelName, timeout),
					})
				} else {
					p.writeLaneError(w, m, laneError{
						status:  http.StatusGatewayTimeout,
						errType: validation.ErrorTypeTimeout,
						code:    modelStateCode(m),
						message: "Timeout waiting for model to become ready (waited " + timeout.String() + ")",
					})
				}
			}
			if stderrors.Is(queueCtx.Err(), context.Canceled) {
				return fmt.Errorf("queue canceled for model %s: %w", modelName, queueCtx.Err())
			}
			return fmt.Errorf("queue timeout for model %s", modelName)
		}
	}
}

// getOrCreateQueue returns an existing queue or creates a new one.
func (p *Proxy) getOrCreateQueue(modelName string) *RequestQueue {
	// Fast path: check if queue exists
	if queue, ok := p.queues.Load(modelName); ok {
		return queue
	}

	// Slow path: create new queue (with lock to prevent duplicates)
	p.queuesMu.Lock()
	defer p.queuesMu.Unlock()

	// Double-check after acquiring lock
	if queue, ok := p.queues.Load(modelName); ok {
		return queue
	}

	// Create new queue
	queue := &RequestQueue{
		model:   modelName,
		items:   make(chan *QueuedRequest, p.maxQueueSize),
		created: time.Now(),
	}
	// Set before the queue is visible, so the sweeper never sees a live
	// queue without its processor.
	queue.processing.Store(true)
	p.queues.Store(modelName, queue)

	// Start queue processor (handles scale-up and draining)
	go p.processQueue(queue)

	slog.Info("created request queue", "model", modelName)
	return queue
}

// processQueue handles scale-up and drains the queue when model is ready.
func (p *Proxy) processQueue(queue *RequestQueue) {
	defer queue.processing.Store(false)
	modelName := queue.model
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	slog.Info("starting queue processor", "model", modelName)

	activationStart := time.Now()

	// Record demand before attempting activation so the controller's
	// serverless reconcile loop does not immediately reap a direct-loaded model.
	p.activator.TouchLastActiveTime(ctx, modelName)

	// Fast path: try direct runtime load (bypasses controller reconcile loop).
	if p.directRuntimeEnabled && p.runtimeCache != nil {
		if p.tryDirectRuntimeLoad(ctx, modelName) {
			activationDurationSeconds.WithLabelValues(modelName, "direct", "success").Observe(time.Since(activationStart).Seconds())
			slog.Info("model ready via direct runtime load, draining queue", "model", modelName)
			queue.draining.Store(true)
			p.drainQueue(queue) // retires the queue
			slog.Info("queue processor finished (direct)", "model", modelName)

			// Async: update LastActiveTime so controller backfills status.
			go p.activator.TouchLastActiveTime(ctx, modelName)
			return
		}
		slog.Debug("direct runtime load failed or unavailable, falling back to controller path", "model", modelName)
	}

	// Slow path: existing controller-based activation with optional backoff
	var lastErr error
	maxAttempts := 1
	if p.backoffEnabled {
		maxAttempts = p.backoffMaxRetries + 1 // Initial attempt + retries
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Calculate backoff with jitter
			waitTime := p.calculateBackoff(attempt)
			slog.Info("retrying activation with backoff",
				"model", modelName,
				"attempt", attempt+1,
				"max_attempts", maxAttempts,
				"wait", waitTime.String())

			activationRetriesTotal.WithLabelValues(modelName).Inc()
			activationRetryWaitDuration.WithLabelValues(modelName).Observe(waitTime.Seconds())

			time.Sleep(waitTime)
		}

		// Trigger scale-up using singleflight to deduplicate
		_, err, _ := p.requestGroup.Do(modelName+"-scaleup", func() (any, error) {
			return nil, p.activator.TriggerScaleUp(ctx, modelName)
		})

		if err != nil {
			lastErr = fmt.Errorf("scale-up failed: %v", err)
			slog.Warn("activation attempt failed", "model", modelName, "attempt", attempt+1, "error", err)
			activationFailuresTotal.WithLabelValues(modelName, "scale_up").Inc()
			continue
		}

		// Wait for model to become ready
		if err := p.waitForReady(ctx, modelName); err != nil {
			lastErr = err
			slog.Warn("model failed to become ready", "model", modelName, "attempt", attempt+1, "error", err)
			reason := "ready_timeout"
			if ctx.Err() != nil {
				reason = "context_cancelled"
			}
			if _, notFound := isModelNotFoundError(err); notFound {
				// Terminal: no CR exists for this name, so backoff retries
				// only re-run the same doomed poll. Fail the queue now.
				activationFailuresTotal.WithLabelValues(modelName, "not_found").Inc()
				break
			}
			activationFailuresTotal.WithLabelValues(modelName, reason).Inc()
			continue
		}

		// Success!
		lastErr = nil
		break
	}

	if lastErr != nil {
		slog.Error("all activation attempts failed", "model", modelName, "attempts", maxAttempts, "error", lastErr)
		activationDurationSeconds.WithLabelValues(modelName, "", "failure").Observe(time.Since(activationStart).Seconds())
		p.drainQueueWithError(queue, lastErr)
		return
	}

	activationDurationSeconds.WithLabelValues(modelName, "", "success").Observe(time.Since(activationStart).Seconds())

	slog.Info("model ready, draining queue", "model", modelName)

	// Mark queue as draining (prevents new requests from being queued here)
	queue.draining.Store(true)

	// Drain queue - serve all pending requests, then retire it
	p.drainQueue(queue)
	slog.Info("queue processor finished", "model", modelName)
}

// drainQueue processes all pending requests: it takes everything queued so
// far, serves it in QoS rank order (FIFO within a rank), and repeats until
// the queue is empty. It then retires the queue (retireQueue) and serves
// whatever was offered between the last take and the retirement.
func (p *Proxy) drainQueue(queue *RequestQueue) {
	for retired := false; !retired; {
		batch := takeQueued(queue)
		if len(batch) == 0 {
			batch, retired = p.retireQueue(queue), true
		}
		sort.SliceStable(batch, func(i, j int) bool { return batch[i].rank < batch[j].rank })
		for _, qr := range batch {
			queueDepth.WithLabelValues(queue.model).Dec()
			// Only one goroutine may write a response for a queued request.
			if qr.responded.CompareAndSwap(false, true) {
				p.serveDrained(qr)
			}
			close(qr.done)
		}
	}
}

// takeQueued removes and returns every request currently in the queue.
func takeQueued(queue *RequestQueue) []*QueuedRequest {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	var batch []*QueuedRequest
	for {
		select {
		case qr := <-queue.items:
			batch = append(batch, qr)
		default:
			return batch
		}
	}
}

// drainQueueWithError rejects all pending requests with an error.
func (p *Proxy) drainQueueWithError(queue *RequestQueue, err error) {
	queue.draining.Store(true)
	stall, stalled := isStalledLoadError(err)
	_, notFound := isModelNotFoundError(err)
	var m *aiv1alpha2.Model
	if !notFound {
		ctx := p.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		m = p.peekModel(ctx, queue.model)
	}
	for retired := false; !retired; {
		batch := takeQueued(queue)
		if len(batch) == 0 {
			// Queue is empty: retire it and fail what was offered since the
			// last take.
			batch, retired = p.retireQueue(queue), true
		}
		for _, qr := range batch {
			queueDepth.WithLabelValues(queue.model).Dec()
			qr.err = err
			if qr.responded.CompareAndSwap(false, true) {
				switch {
				case stalled:
					qr.w.Header().Set(headerActivation, activationValue(m))
					p.writeLaneError(qr.w, m, laneError{
						status:     http.StatusServiceUnavailable,
						code:       validation.CodeStalledLoad,
						message:    stall.Error(),
						retryAfter: defaultStalledLoadRetryAfter,
					})
				case notFound:
					// Match the front door: a name with no backing CR is 404,
					// not a retryable 503.
					validation.WriteModelNotFound(qr.w, queue.model)
				default:
					code := modelStateCode(m)
					if _, failed := isModelFailedError(err); failed {
						code = validation.CodeModelFailed
					}
					qr.w.Header().Set(headerActivation, activationValue(m))
					p.writeLaneError(qr.w, m, laneError{
						status:  http.StatusServiceUnavailable,
						code:    code,
						message: fmt.Sprintf("Failed to activate model: %v", err),
					})
				}
			}
			close(qr.done)
		}
	}
}

// cleanupStaleQueues periodically removes stale queues.
func (p *Proxy) cleanupStaleQueues() {
	ticker := time.NewTicker(staleQueueCleanupInterval)
	defer ticker.Stop()

	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.sweepStaleQueues(time.Now())
		}
	}
}

// sweepStaleQueues retires queues older than 2x the queue timeout that hold
// no request and have no running processor. A processor can wait on
// readiness for a lane's whole coldStartTimeout (25 min on a preempted lane)
// while every client has left its queue; removing that queue would let the
// next request start a second processor for the same model, so the sweeper
// only collects queues whose processor has exited without retiring them.
func (p *Proxy) sweepStaleQueues(now time.Time) {
	p.queues.Range(func(key string, queue *RequestQueue) bool {
		if now.Sub(queue.created) > 2*p.queueTimeout && p.retireIdleQueue(queue) {
			slog.Debug("cleaned up stale queue", "model", key)
		}
		return true
	})
}

// waitForReady polls until the model is ready or timeout.
func (p *Proxy) waitForReady(ctx context.Context, modelName string) error {
	// Get the cold start timeout, preferring per-model configuration
	timeout := p.activator.GetColdStartTimeout(ctx, modelName)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	notFoundGrace := p.activationNotFoundGrace
	if notFoundGrace == 0 {
		notFoundGrace = defaultActivationNotFoundGrace
	}

	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	// Start of the current run of polls where neither the Model nor the
	// fallback ModelDeployment exists. Zero while either CR has been seen (or
	// a transient error left existence unconfirmed). Once the run outlasts
	// notFoundGrace the activation fails fast: no CR means nothing can ever
	// reconcile the model to Ready, so burning the full cold-start timeout
	// only strands the queued requests.
	var notFoundSince time.Time

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for model to become ready (after %v)", timeout)
		case <-ticker.C:
			// Check v1alpha2 Model first
			m, err := p.getModel(ctx, modelName)
			if err == nil {
				notFoundSince = time.Time{}
				if m.Status.Phase == aiv1alpha2.ModelPhaseReady {
					return nil
				}
				if failed := detectFailedModel(m); failed != nil {
					return failed
				}
				// Fail fast if the cold-start has obviously wedged on weight
				// loading (LoadingProgressAt timestamp hasn't advanced in long
				// enough that the proxy should stop queuing more work).
				if s := detectStalledLoad(m, defaultStalledLoadThreshold); s != nil {
					stalledLoadTotal.WithLabelValues(modelName, string(s.Substage)).Inc()
					return s
				}
				continue
			}
			if !errors.IsNotFound(err) {
				// Transient error: existence unconfirmed, so restart the
				// not-found clock rather than fast-failing on stale evidence.
				notFoundSince = time.Time{}
				slog.Warn("error checking model readiness", "model", modelName, "error", err)
				continue
			}

			// Fallback: v1alpha1 ModelDeployment (deprecated)
			md, err := p.getModelDeployment(ctx, modelName)
			if err != nil {
				if !errors.IsNotFound(err) {
					notFoundSince = time.Time{}
					slog.Warn("error checking model deployment readiness", "model", modelName, "error", err)
					continue
				}
				// Neither CR exists. Tolerate a short creation race, then
				// fail the activation instead of polling out the timeout.
				if notFoundSince.IsZero() {
					notFoundSince = time.Now()
				}
				if age := time.Since(notFoundSince); age >= notFoundGrace {
					return &ModelNotFoundError{Model: modelName, Age: age}
				}
				slog.Debug("model CR not found, waiting out creation grace window",
					"model", modelName, "grace", notFoundGrace)
				continue
			}
			notFoundSince = time.Time{}
			if isReady(md) {
				return nil
			}
		}
	}
}

// calculateBackoff returns the wait time for a given retry attempt using exponential backoff with jitter.
func (p *Proxy) calculateBackoff(attempt int) time.Duration {
	// Exponential backoff: initialWait * 2^attempt
	backoff := p.backoffInitialWait * time.Duration(1<<uint(attempt))

	// Cap at max wait
	if backoff > p.backoffMaxWait {
		backoff = p.backoffMaxWait
	}

	// Add jitter: random value between 0.5x and 1.5x of the backoff
	jitter := float64(backoff) * (0.5 + rand.Float64())
	return time.Duration(jitter)
}

// ── Direct runtime fast path ──────────────────────────────────────

// tryDirectRuntimeLoad attempts to load a model directly on a runtime pod,
// bypassing the controller reconcile loop. Returns true on success.
func (p *Proxy) tryDirectRuntimeLoad(ctx context.Context, modelName string) bool {
	slog.Info("direct load: evaluating runtime fast path", "model", modelName)

	// Fetch the v1alpha2 Model CR to get backend/source/nodeSelector.
	m, err := p.getModel(ctx, modelName)
	if err != nil {
		slog.Info("direct load: cannot fetch model CR", "model", modelName, "error", err)
		return false
	}
	// Shared-GPU election owns the cooldown and VRAM reservations. The direct
	// load path must wait for that grant instead of spawning a queued member
	// only for the controller to immediately preempt it again.
	if m.Spec.IsShared() && (m.Status.SharedGroup == nil || m.Status.SharedGroup.State != "Active") {
		return false
	}
	// Resolve backend.
	b, ok := backend.Get(m.Spec.Backend)
	if !ok {
		slog.Warn("direct load: unknown backend", "model", modelName, "backend", m.Spec.Backend)
		return false
	}

	// Find a ready runtime pod matching the model's nodeSelector.
	endpoint, err := p.runtimeCache.ForModel(ctx, m.Spec.NodeSelector)
	if err != nil || endpoint == nil {
		slog.Info("direct load: no runtime endpoint found", "model", modelName, "error", err)
		return false
	}
	slog.Info("direct load: matched runtime endpoint", "model", modelName, "runtimePod", endpoint.PodName, "node", endpoint.NodeName, "runtimeURL", endpoint.URL())
	profile := p.lookupGPUProfile(ctx, endpoint.GPUArch)
	if ok, reason := pkgrt.DirectRuntimeLoadEligibility(m, b.Name(), profile); !ok {
		// Expected branch: the model is not runtime-eligible (e.g. a SharedPVC
		// pvc:// source the node-local runtime can't mount), so activation falls
		// back to the controller Deployment path. Logged at Debug to match the
		// controller's V(1) level for the same condition (model_controller.go);
		// an INFO line here reads like a failure and has sent cold-start
		// investigations down the wrong path.
		slog.Debug("direct load: model not runtime-eligible, using controller path", "model", modelName, "reason", reason)
		return false
	}

	// Build the load payload with the controller's builder, LoRA options and
	// launch SpecHash included, so a load this path wins is the one the
	// controller would have sent.
	payload, err := DirectLoadPayload(ctx, p.client, m, b, profile)
	if err != nil {
		slog.Warn("direct load: failed to build payload", "model", modelName, "error", err)
		return false
	}

	// Send load request to runtime pod.
	slog.Info("direct load: sending runtime load request", "model", modelName, "endpoint", endpoint.URL(), "backend", b.Name())
	if err := p.loadOnRuntime(ctx, endpoint, modelName, payload); err != nil {
		slog.Warn("direct load: runtime load request failed", "model", modelName, "endpoint", endpoint.URL(), "error", err)
		return false
	}

	// Poll runtime health until ready. The runtime reports the model's actual
	// bound port — in multi-model mode each concurrent model gets a distinct
	// port, so we must route to the reported one rather than the fixed default.
	readyTimeout := modelColdStartTimeout(m, p.coldStartTimeout)
	slog.Info("direct load: waiting for runtime health", "model", modelName, "endpoint", endpoint.URL(), "timeout", readyTimeout)
	readyPort, err := p.waitForRuntimeReadyWithin(ctx, endpoint, modelName, readyTimeout)
	if err != nil {
		slog.Warn("direct load: runtime health poll failed", "model", modelName, "error", err)
		return false
	}

	// Store the direct routing target so serveProxy routes to the runtime pod
	// instead of the (non-existent) K8s Service. Prefer the runtime-reported
	// port; fall back to the fixed backend port for older runtimes that don't
	// report one (single-slot mode).
	backendPort := readyPort
	if backendPort == 0 {
		backendPort = pkgrt.RuntimePortForBackend(b)
	}
	targetURL := fmt.Sprintf("http://%s:%d", endpoint.PodIP, backendPort)
	p.directLoadTargets.Store(modelName, targetURL)
	slog.Info("direct load: registered routing target", "model", modelName, "target", targetURL, "port", backendPort)

	return true
}

// DirectLoadPayload renders the runtime load request the direct-load fast path
// sends for m: pkgrt.NewModelLoadPayload, the builder the controller's
// reconcile path uses, with the vendor the Model's spec pins. reader lists the
// Model's LoRAAdapters.
func DirectLoadPayload(ctx context.Context, reader client.Reader, m *aiv1alpha2.Model, b backend.Backend, profile *aiv1alpha2.GPUProfileSpec) ([]byte, error) {
	payload, err := pkgrt.NewModelLoadPayload(ctx, reader, m, b, pkgrt.ModelGPUVendor(m), profile)
	if err != nil {
		return nil, err
	}
	return payload.Marshal()
}

// modelColdStartTimeout is how long a load of m may take: its own
// serverless.coldStartTimeout when set (the same budget the payload hands the
// runtime as startupTimeoutSeconds), else fallback.
func modelColdStartTimeout(m *aiv1alpha2.Model, fallback time.Duration) time.Duration {
	if m != nil && m.Spec.Serverless != nil && m.Spec.Serverless.ColdStartTimeout != nil && m.Spec.Serverless.ColdStartTimeout.Duration > 0 {
		return m.Spec.Serverless.ColdStartTimeout.Duration
	}
	return fallback
}

func (p *Proxy) lookupGPUProfile(ctx context.Context, arch string) *aiv1alpha2.GPUProfileSpec {
	if arch == "" {
		return nil
	}

	list := &aiv1alpha2.GPUProfileList{}
	if err := p.client.List(ctx, list, client.InNamespace(p.namespace)); err != nil {
		slog.Debug("direct load: failed to list GPUProfiles", "arch", arch, "error", err)
		return nil
	}

	for i := range list.Items {
		if strings.EqualFold(list.Items[i].Spec.Architecture, arch) {
			profile := list.Items[i].Spec.DeepCopy()
			return profile
		}
	}
	return nil
}

// loadOnRuntime sends POST /api/v1/models/{name}/load to a runtime pod.
func (p *Proxy) loadOnRuntime(ctx context.Context, ep *pkgrt.RuntimeEndpoint, modelName string, payload []byte) error {
	url := fmt.Sprintf("%s/api/v1/models/%s/load", ep.URL(), modelName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating load request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending load request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("runtime load failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// waitForRuntimeReady polls GET /api/v1/models/{name}/health on the runtime
// pod until the model reaches "Ready" state or a timeout is exceeded. It returns
// the model's runtime-reported port (0 if the runtime doesn't report one).
func (p *Proxy) waitForRuntimeReady(ctx context.Context, ep *pkgrt.RuntimeEndpoint, modelName string) (int32, error) {
	return p.waitForRuntimeReadyWithin(ctx, ep, modelName, p.coldStartTimeout)
}

// waitForRuntimeReadyWithin is waitForRuntimeReady with an explicit timeout.
func (p *Proxy) waitForRuntimeReadyWithin(ctx context.Context, ep *pkgrt.RuntimeEndpoint, modelName string, timeout time.Duration) (int32, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	healthURL := fmt.Sprintf("%s/api/v1/models/%s/health", ep.URL(), modelName)
	client := &http.Client{Timeout: 5 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("timeout waiting for runtime model to become ready (after %v)", timeout)
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
			if err != nil {
				continue
			}
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			var status struct {
				State    string `json:"state"`
				Error    string `json:"error"`
				Port     int32  `json:"port"`
				Draining bool   `json:"draining"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
				_ = resp.Body.Close()
				continue
			}
			_ = resp.Body.Close()

			switch status.State {
			case "Ready":
				if !status.Draining && resp.StatusCode == http.StatusOK {
					return status.Port, nil
				}
			case "Failed":
				return 0, fmt.Errorf("runtime model failed: %s", status.Error)
			}
			// "Loading" — keep polling
		}
	}
}
