package proxy

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
	"github.com/flexinfer/flexinfer/pkg/validation"
)

// touchCounter is a ModelActivator that is safe for concurrent requests.
type touchCounter struct{ touches atomic.Int64 }

func (a *touchCounter) TriggerScaleUp(context.Context, string) error { return nil }
func (a *touchCounter) TouchLastActiveTime(context.Context, string)  { a.touches.Add(1) }
func (a *touchCounter) GetColdStartTimeout(context.Context, string) time.Duration {
	return time.Millisecond
}
func (a *touchCounter) IsNodeTerminating(context.Context, string) bool { return false }

func priority(v int32) *int32 { return &v }

func ago(d time.Duration) *metav1.Time {
	t := metav1.NewTime(time.Now().Add(-d))
	return &t
}

func setWarmPolicy(m *aiv1alpha2.Model, policy string) {
	m.Spec.Config = &apiextensionsv1.JSON{Raw: []byte(`{"warmPolicy":"` + policy + `"}`)}
}

// sharedLane is a serverless member of shared group "xtx" at priority prio.
func sharedLane(name string, prio int32, phase aiv1alpha2.ModelPhase, sg *aiv1alpha2.SharedGroupStatus) *aiv1alpha2.Model {
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Annotations: map[string]string{}},
		Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://a/b",
			GPU: &aiv1alpha2.GPUSpec{Shared: "xtx", Priority: priority(prio)}},
		Status: aiv1alpha2.ModelStatus{Phase: phase, SharedGroup: sg},
	}
}

// warmBehindCreative is the KT-JOBS layout: the warm chat lane (300) is
// preempted by the creative lane (500), which holds the card.
func warmBehindCreative() (warm, creative *aiv1alpha2.Model) {
	warm = sharedLane("warm", 300, aiv1alpha2.ModelPhasePreempted,
		&aiv1alpha2.SharedGroupStatus{GroupName: "xtx", State: "Preempted", PreemptedBy: "creative"})
	creative = sharedLane("creative", 500, aiv1alpha2.ModelPhaseReady,
		&aiv1alpha2.SharedGroupStatus{GroupName: "xtx", State: "Active"})
	return warm, creative
}

// serve runs one request through the handler and fails the test if the
// handler is still holding it after limit.
func serve(t *testing.T, p *Proxy, req *http.Request, limit time.Duration) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.handleRequest(rec, req)
	}()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("request still held after %s", limit)
	}
	return rec
}

func TestLosingCardTo(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(warm, creative *aiv1alpha2.Model)
		want   string
	}{
		{name: "higher-priority Active holder", want: "creative"},
		{name: "lane Queued behind the holder", mutate: func(w, _ *aiv1alpha2.Model) {
			w.Status.Phase = aiv1alpha2.ModelPhasePending
			w.Status.SharedGroup.State = "Queued"
		}, want: "creative"},
		{name: "holder loading onto the card", mutate: func(_, c *aiv1alpha2.Model) {
			c.Status.Phase = aiv1alpha2.ModelPhaseLoading
		}, want: "creative"},
		{name: "lane holds the card and is loading", mutate: func(w, _ *aiv1alpha2.Model) {
			w.Status.Phase = aiv1alpha2.ModelPhaseLoading
			w.Status.SharedGroup.State = "Active"
		}},
		{name: "equal priority can win on demand", mutate: func(_, c *aiv1alpha2.Model) {
			c.Spec.GPU.Priority = priority(300)
		}},
		{name: "lower-priority holder", mutate: func(_, c *aiv1alpha2.Model) {
			c.Spec.GPU.Priority = priority(100)
		}},
		{name: "holder no longer Active", mutate: func(_, c *aiv1alpha2.Model) {
			c.Status.Phase = aiv1alpha2.ModelPhasePreempted
			c.Status.SharedGroup.State = "Preempted"
		}},
		{name: "holder in another group", mutate: func(_, c *aiv1alpha2.Model) {
			c.Spec.GPU.Shared = "other"
		}},
		{name: "park class prefix", mutate: func(w, _ *aiv1alpha2.Model) {
			w.Status.SharedGroup.PreemptedBy = "gpu-lease/train"
		}},
		{name: "no attribution", mutate: func(w, _ *aiv1alpha2.Model) {
			w.Status.SharedGroup.PreemptedBy = ""
		}},
		{name: "failover stand-in", mutate: func(w, _ *aiv1alpha2.Model) {
			w.Annotations[constants.AnnotationFailoverFor] = "down"
		}},
		{name: "force-promoted lane", mutate: func(w, _ *aiv1alpha2.Model) {
			force := true
			w.Spec.GPU.ForcePromotion = &force
		}},
		// Release: the job runner handed the card back; the controller acts
		// after its 45 s grace, while the holder still reads Active.
		{name: "holder with a standing release", mutate: func(_, c *aiv1alpha2.Model) {
			c.Status.LastActiveTime = ago(10 * time.Second)
			c.Status.ReleasedAt = ago(5 * time.Second)
		}},
		{name: "release in the same second as the last stamp still stands", mutate: func(_, c *aiv1alpha2.Model) {
			at := ago(5 * time.Second)
			c.Status.LastActiveTime, c.Status.ReleasedAt = at, at
		}},
		{name: "release answered by later demand", mutate: func(_, c *aiv1alpha2.Model) {
			c.Status.ReleasedAt = ago(10 * time.Second)
			c.Status.LastActiveTime = ago(5 * time.Second)
		}, want: "creative"},
		{name: "release overridden by an activation request", mutate: func(_, c *aiv1alpha2.Model) {
			c.Status.ReleasedAt = ago(5 * time.Second)
			c.Status.ActivationRequestedAt = ago(time.Minute)
		}, want: "creative"},
		// Warm primary: reclaims the card from a holder whose demand lapsed.
		{name: "warm primary behind a holder with fresh demand", mutate: func(w, c *aiv1alpha2.Model) {
			setWarmPolicy(w, "primary")
			c.Status.LastActiveTime = ago(30 * time.Second)
		}, want: "creative"},
		{name: "warm primary behind a holder whose demand lapsed", mutate: func(w, c *aiv1alpha2.Model) {
			setWarmPolicy(w, "primary")
			c.Status.LastActiveTime = ago(3 * time.Minute)
		}},
		{name: "warm primary behind a holder with no demand", mutate: func(w, _ *aiv1alpha2.Model) {
			setWarmPolicy(w, "primary")
		}},
		{name: "warm primary, holder demand lapsed past its shorter idle timeout", mutate: func(w, c *aiv1alpha2.Model) {
			setWarmPolicy(w, "primary")
			idle := metav1.Duration{Duration: 30 * time.Second}
			c.Spec.Serverless = &aiv1alpha2.ServerlessSpec{IdleTimeout: &idle}
			c.Status.LastActiveTime = ago(time.Minute)
		}},
		{name: "warm primary, holder kept fresh by an activation request", mutate: func(w, c *aiv1alpha2.Model) {
			setWarmPolicy(w, "primary")
			c.Status.LastActiveTime = ago(10 * time.Minute)
			c.Status.ActivationRequestedAt = ago(time.Minute)
		}, want: "creative"},
		{name: "on-demand lane behind an idle holder", mutate: func(w, c *aiv1alpha2.Model) {
			setWarmPolicy(w, "ondemand")
			c.Status.LastActiveTime = ago(10 * time.Minute)
		}, want: "creative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warm, creative := warmBehindCreative()
			if tt.mutate != nil {
				tt.mutate(warm, creative)
			}
			p := newGateTestProxy(t, &touchCounter{}, warm, creative)
			assert.Equal(t, tt.want, p.losingCardTo(context.Background(), warm))
		})
	}

	t.Run("holder missing from the cache", func(t *testing.T) {
		warm, _ := warmBehindCreative()
		p := newGateTestProxy(t, &touchCounter{}, warm)
		assert.Empty(t, p.losingCardTo(context.Background(), warm))
	})
}

func TestPreemptedWaitSetting(t *testing.T) {
	warm, _ := warmBehindCreative()
	p := &Proxy{}
	assert.Equal(t, defaultPreemptedWait, p.preemptedWait(warm))
	p.laneConfig.PreemptedWait = 7 * time.Second
	assert.Equal(t, 7*time.Second, p.preemptedWait(warm))
	warm.Annotations[annotationPreemptedWait] = "45s"
	assert.Equal(t, 45*time.Second, p.preemptedWait(warm))
	warm.Annotations[annotationPreemptedWait] = "soon"
	assert.Equal(t, 7*time.Second, p.preemptedWait(warm), "an unparsable annotation falls back to the setting")
}

// KT-JOBS bar 2: a chat request without Prefer: wait to a lane that a
// higher-priority lane holds the card against is answered at the bound with
// the typed 503, and its demand is recorded.
func TestPreemptedLaneAnswersAtTheBound(t *testing.T) {
	warm, creative := warmBehindCreative()
	act := &touchCounter{}
	p := newGateTestProxy(t, act, warm, creative)
	p.queueTimeout = time.Hour
	p.laneConfig.PreemptedWait = 20 * time.Millisecond
	q := stalledQueue(p, "warm", 4)

	rec := serve(t, p, chatReq("warm"), 10*time.Second)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	e := envelope(t, rec)
	assert.Equal(t, validation.CodeModelPreempted, e.Code)
	assert.Contains(t, e.Message, `"creative" holds its shared GPU`)
	assertRetryAfter(t, rec, e, retryAfterPreempted)
	assert.Equal(t, "Preempted", rec.Header().Get(headerActivation))
	assert.Empty(t, rec.Header().Get("Preference-Applied"))
	assert.Equal(t, int64(1), act.touches.Load(), "the refused request still stamps demand")
	assert.Empty(t, takeQueued(q), "the refused request gave its queue slot back")
}

func TestPreferWaitKeepsItsOwnWaitOnAPreemptedLane(t *testing.T) {
	warm, creative := warmBehindCreative()
	p := newGateTestProxy(t, &touchCounter{}, warm, creative)
	p.queueTimeout = time.Hour
	p.laneConfig.PreemptedWait = time.Millisecond
	stalledQueue(p, "warm", 4)

	req := chatReq("warm")
	req.Header.Set("Prefer", "wait=1")
	rec := serve(t, p, req, 10*time.Second)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	e := envelope(t, rec)
	assert.Equal(t, validation.CodeModelPreempted, e.Code)
	assert.Contains(t, e.Message, "preferred wait of 1s", "the caller's own wait applies, not the preempted wait")
	assert.Equal(t, "wait=1", rec.Header().Get("Preference-Applied"))
}

// A lane that can still win the card, and a job's forward, keep the full
// queue wait: the bound does not answer them.
func TestPreemptedWaitLeavesOtherWaitsAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		prep func(warm, creative *aiv1alpha2.Model, req *http.Request) *http.Request
	}{
		{name: "equal-priority holder", prep: func(_, c *aiv1alpha2.Model, r *http.Request) *http.Request {
			c.Spec.GPU.Priority = priority(300)
			return r
		}},
		{name: "lane holds the card and is loading", prep: func(w, _ *aiv1alpha2.Model, r *http.Request) *http.Request {
			w.Status.Phase = aiv1alpha2.ModelPhaseLoading
			w.Status.SharedGroup.State = "Active"
			return r
		}},
		{name: "job forward", prep: func(_, _ *aiv1alpha2.Model, r *http.Request) *http.Request {
			return r.WithContext(jobForwardContext(r.Context()))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warm, creative := warmBehindCreative()
			req := tc.prep(warm, creative, chatReq("warm"))
			p := newGateTestProxy(t, &touchCounter{}, warm, creative)
			p.queueTimeout = 200 * time.Millisecond
			p.laneConfig.PreemptedWait = time.Millisecond
			stalledQueue(p, "warm", 4)

			rec := serve(t, p, req, 10*time.Second)

			require.Equal(t, http.StatusGatewayTimeout, rec.Code, "the queue timeout answers, not the preempted wait: %s", rec.Body.String())
			assert.Contains(t, envelope(t, rec).Message, "Timeout waiting for model to become ready")
		})
	}
}

// The bound re-reads the lanes when it fires. The test owns the timer, so the
// status change lands before the bound fires on any runner: a lane that won the
// card, or a holder that released it, keeps the request; an unchanged lane
// gets the 503.
func TestPreemptedWaitRechecksWhenItFires(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(t *testing.T, p *Proxy)
		want503 bool
	}{
		{name: "lane won the card", change: func(t *testing.T, p *Proxy) {
			updateStatus(t, p, "warm", func(m *aiv1alpha2.Model) {
				m.Status.Phase = aiv1alpha2.ModelPhaseLoading
				m.Status.SharedGroup = &aiv1alpha2.SharedGroupStatus{GroupName: "xtx", State: "Active"}
			})
		}},
		{name: "holder released the card", change: func(t *testing.T, p *Proxy) {
			updateStatus(t, p, "creative", func(m *aiv1alpha2.Model) {
				m.Status.LastActiveTime = ago(10 * time.Second)
				m.Status.ReleasedAt = ago(time.Second)
			})
		}},
		{name: "nothing changed", change: func(*testing.T, *Proxy) {}, want503: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warm, creative := warmBehindCreative()
			p := newGateTestProxy(t, &touchCounter{}, warm, creative)
			p.queueTimeout = time.Hour
			armed := make(chan time.Duration, 1)
			fire := make(chan time.Time)
			p.preemptedWaitTimer = func(d time.Duration) <-chan time.Time {
				armed <- d
				return fire
			}
			stalledQueue(p, "warm", 4)

			ctx, leave := context.WithCancel(context.Background())
			defer leave()
			rec := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				p.handleRequest(rec, chatReq("warm").WithContext(ctx))
			}()
			select {
			case d := <-armed:
				assert.Equal(t, defaultPreemptedWait, d)
			case <-time.After(10 * time.Second):
				t.Fatal("the preempted wait was not armed")
			}

			tc.change(t, p)
			// The send returns only once the handler's select took it.
			fire <- time.Now()

			if tc.want503 {
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Fatal("request still held after the bound fired")
				}
				require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
				assert.Equal(t, validation.CodeModelPreempted, envelope(t, rec).Code)
				return
			}
			// The request keeps waiting for the load; the client leaves.
			leave()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("request did not return after its client left")
			}
			assert.Empty(t, rec.Body.String(), "the re-check kept the request: no 503 was written")
		})
	}
}

func updateStatus(t *testing.T, p *Proxy, name string, mutate func(*aiv1alpha2.Model)) {
	t.Helper()
	m := &aiv1alpha2.Model{}
	require.NoError(t, p.client.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: name}, m))
	mutate(m)
	require.NoError(t, p.client.Update(context.Background(), m))
}

// The reviewer's sweeper scenario: a processor waits on a preempted lane
// while every client has left its queue. The sweep must not unmap that queue,
// the next request must join it, and a finishing processor must not unmap a
// queue registered after its own.
func TestStaleQueueSweepSparesALiveProcessor(t *testing.T) {
	const model = "swept-lane"
	p := newGateTestProxy(t, &touchCounter{}, sharedLane(model, 300, aiv1alpha2.ModelPhasePreempted,
		&aiv1alpha2.SharedGroupStatus{GroupName: "xtx", State: "Preempted"}))
	p.queueTimeout = time.Second
	long := time.Now().Add(time.Hour)

	live := &RequestQueue{model: model, items: make(chan *QueuedRequest, 4), created: time.Now().Add(-time.Hour)}
	live.processing.Store(true) // its processor is still in waitForReady
	p.queues.Store(model, live)

	p.sweepStaleQueues(long)
	got, ok := p.queues.Load(model)
	require.True(t, ok, "an empty queue with a live processor survives the sweep")
	assert.Same(t, live, got)
	assert.Same(t, live, p.getOrCreateQueue(model), "the next request joins the live queue instead of starting a second processor")
	queued, closed := live.offer(&QueuedRequest{modelName: model, done: make(chan struct{})})
	assert.True(t, queued)
	assert.False(t, closed)
	takeQueued(live)

	// The old processor finishes after a newer queue took the name: its
	// retirement leaves the newer queue mapped.
	newer := &RequestQueue{model: model, items: make(chan *QueuedRequest, 4), created: time.Now()}
	newer.processing.Store(true)
	p.queues.Store(model, newer)
	live.processing.Store(false)
	p.drainQueue(live)
	got, ok = p.queues.Load(model)
	require.True(t, ok)
	assert.Same(t, newer, got, "a finishing processor does not unmap a different queue")
	_, closed = live.offer(&QueuedRequest{modelName: model, done: make(chan struct{})})
	assert.True(t, closed, "a retired queue takes no more requests")

	p.drainQueueWithError(live, stderrors.New("late failure"))
	got, _ = p.queues.Load(model)
	assert.Same(t, newer, got, "a failing processor does not unmap a different queue either")

	// A queue whose processor has exited and that holds nothing is swept.
	newer.processing.Store(false)
	newer.created = time.Now().Add(-time.Hour)
	p.sweepStaleQueues(long)
	_, ok = p.queues.Load(model)
	assert.False(t, ok, "an idle queue without a processor is collected")
	_, closed = newer.offer(&QueuedRequest{modelName: model, done: make(chan struct{})})
	assert.True(t, closed)
}

// Fix for the KT-JOBS slot leak: requests whose clients went away give their
// slots back, so a full queue of abandoned requests still admits the next.
func TestAbandonedQueuedRequestsFreeTheirSlots(t *testing.T) {
	const model = "abandoned-slots"
	const slots = 3
	lane := sharedLane(model, 300, aiv1alpha2.ModelPhasePending,
		&aiv1alpha2.SharedGroupStatus{GroupName: "xtx", State: "Queued"})
	p := newGateTestProxy(t, &touchCounter{}, lane)
	p.queueTimeout = time.Hour
	q := stalledQueue(p, model, slots)

	ctx, abandon := context.WithCancel(context.Background())
	defer abandon()
	// The waiters enter handleColdStart directly: concurrent handleRequest
	// calls on a fresh ModelResolver race in refreshServiceLabelCache, which
	// replaces its sync.Maps while other requests read them (not this fix).
	returned := make(chan error, slots)
	for range slots {
		go func() {
			req := chatReq(model).WithContext(ctx)
			returned <- p.handleColdStart(ctx, httptest.NewRecorder(), req, model, time.Now())
		}()
	}
	require.Eventually(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.items) == slots
	}, 5*time.Second, 5*time.Millisecond)

	full := serve(t, p, chatReq(model), 10*time.Second)
	require.Equal(t, http.StatusServiceUnavailable, full.Code)
	require.Equal(t, validation.CodeQueueFull, envelope(t, full).Code, "every slot is taken while the clients wait")

	abandon()
	for range slots {
		select {
		case <-returned:
		case <-time.After(10 * time.Second):
			t.Fatal("an abandoned request did not return")
		}
	}
	q.mu.Lock()
	left := len(q.items)
	q.mu.Unlock()
	assert.Zero(t, left, "abandoned requests left the queue")
	assert.Zero(t, promtestutil.ToFloat64(queueDepth.WithLabelValues(model)), "queue depth counts only waiting requests")

	next := chatReq(model)
	next.Header.Set("Prefer", "wait=0")
	rec := serve(t, p, next, 10*time.Second)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, validation.CodeModelPreempted, envelope(t, rec).Code, "the next request was admitted, not refused queue_full")
}

// A request the proxy refuses with a typed 503 is demand, not interactive
// use: it does not keep the job runner from releasing the lane.
func TestARefusedProbeDoesNotKeepAJobLane(t *testing.T) {
	video := sharedLane("video", 500, aiv1alpha2.ModelPhasePreempted,
		&aiv1alpha2.SharedGroupStatus{GroupName: "xtx", State: "Preempted", PreemptedBy: "creative"})
	idle := metav1.Duration{Duration: 15 * time.Minute}
	video.Spec.Serverless = &aiv1alpha2.ServerlessSpec{IdleTimeout: &idle}
	act := &touchCounter{}
	p := newGateTestProxy(t, act, video)
	stalledQueue(p, "video", 4)

	probe := chatReq("video")
	probe.Header.Set("Prefer", "wait=0")
	rec := serve(t, p, probe, 10*time.Second)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, int64(1), act.touches.Load(), "the probe is still demand")

	assert.Equal(t, laneReleaseRequested, p.releaseJobLane(context.Background(), "video"),
		"a refused probe is not a client using the lane")
}
