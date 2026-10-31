package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func qosModel(annotations map[string]string, config string) *aiv1alpha2.Model {
	m := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "lane", Annotations: annotations}}
	if config != "" {
		m.Spec.Config = &apiextensionsv1.JSON{Raw: []byte(config)}
	}
	return m
}

func TestQoSLanePolicyFor(t *testing.T) {
	cases := []struct {
		name        string
		annotations map[string]string
		config      string
		want        qosLanePolicy
		gated       bool
	}{
		{"no opt-in", nil, `{"maxNumSeqs":2}`, qosLanePolicy{mode: qosModeOff}, false},
		{"fcfs is no opt-in", nil, `{"maxNumSeqs":2,"schedulingPolicy":"fcfs"}`, qosLanePolicy{mode: qosModeOff}, false},
		{"priority lane", nil, `{"maxNumSeqs":2,"schedulingPolicy":"priority"}`, qosLanePolicy{mode: qosModePriority, seats: 2, reserve: 1}, true},
		{"proxy-only lane", map[string]string{annotationQoS: "proxy"}, `{"maxNumSeqs":4}`, qosLanePolicy{mode: qosModeProxy, seats: 4, reserve: 1}, true},
		{"off overrides priority", map[string]string{annotationQoS: "off"}, `{"maxNumSeqs":2,"schedulingPolicy":"priority"}`, qosLanePolicy{mode: qosModeOff}, false},
		{"reserve zero", map[string]string{annotationQoS: "proxy", annotationQoSReserve: "0"}, `{"maxNumSeqs":2}`, qosLanePolicy{mode: qosModeProxy, seats: 2, reserve: 0}, true},
		{"reserve clamped below seats", map[string]string{annotationQoS: "proxy", annotationQoSReserve: "5"}, `{"maxNumSeqs":2}`, qosLanePolicy{mode: qosModeProxy, seats: 2, reserve: 1}, true},
		{"one seat holds nothing back", map[string]string{annotationQoS: "proxy"}, `{"maxNumSeqs":1}`, qosLanePolicy{mode: qosModeProxy, seats: 1, reserve: 0}, true},
		{"no seat count is not gated", nil, `{"schedulingPolicy":"priority"}`, qosLanePolicy{mode: qosModePriority}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := qosLanePolicyFor(qosModel(tc.annotations, tc.config))
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.gated, got.gated())
		})
	}
	assert.False(t, qosLanePolicyFor(nil).gated())
}

var twoSeats = qosLanePolicy{mode: qosModeProxy, seats: 2, reserve: 1}

func TestQoSGateInteractiveNeverWaits(t *testing.T) {
	g := &qosGate{}
	ctx := context.Background()
	outcome, err := g.acquire(ctx, qosBackground, twoSeats, time.Second, 8)
	require.NoError(t, err)
	assert.Equal(t, qosOutcomeAdmitted, outcome)
	for i := 0; i < 3; i++ {
		outcome, err = g.acquire(ctx, qosInteractive, twoSeats, time.Millisecond, 8)
		require.NoError(t, err)
		assert.Equal(t, qosOutcomeAdmitted, outcome, "interactive is forwarded even past the seat count; vLLM queues it")
	}
	assert.Equal(t, 4, g.inflight)
}

func TestQoSGateServesWaitersClassThenFIFO(t *testing.T) {
	g := &qosGate{}
	ctx := context.Background()
	_, err := g.acquire(ctx, qosBackground, twoSeats, time.Second, 8)
	require.NoError(t, err)

	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	enqueue := func(name string, class qosClass, waiting int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcome, err := g.acquire(ctx, class, twoSeats, 5*time.Second, 8)
			if assert.NoError(t, err) {
				assert.Equal(t, qosOutcomeQueued, outcome)
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
			}
		}()
		require.Eventually(t, func() bool {
			g.mu.Lock()
			defer g.mu.Unlock()
			return len(g.waiters) == waiting
		}, time.Second, time.Millisecond)
	}
	enqueue("batch-1", qosBatch, 1)
	enqueue("bg-2", qosBackground, 2)
	enqueue("bg-3", qosBackground, 3)

	for i := 1; i <= 3; i++ {
		g.release(time.Second)
		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(order) == i
		}, time.Second, time.Millisecond)
	}
	wg.Wait()
	assert.Equal(t, []string{"bg-2", "bg-3", "batch-1"}, order, "background before batch, FIFO within a class")
}

func TestQoSGateRejectsWhenWaitExpires(t *testing.T) {
	g := &qosGate{}
	ctx := context.Background()
	_, err := g.acquire(ctx, qosBackground, twoSeats, time.Second, 8)
	require.NoError(t, err)

	outcome, err := g.acquire(ctx, qosBackground, twoSeats, 20*time.Millisecond, 8)
	assert.Equal(t, qosOutcomeRejected, outcome)
	var full *qosQueueFullError
	require.ErrorAs(t, err, &full)
	assert.Equal(t, 1, full.position)
	assert.Equal(t, 30, full.retryAfter, "no completed request yet: seed duration per seat")
	assert.Empty(t, g.waiters)
	assert.Equal(t, 1, g.inflight)

	g.release(10 * time.Second)
	_, _ = g.acquire(ctx, qosBackground, twoSeats, time.Second, 8)
	_, err = g.acquire(ctx, qosBackground, twoSeats, 20*time.Millisecond, 8)
	require.ErrorAs(t, err, &full)
	assert.Equal(t, 10, full.retryAfter, "Retry-After follows the lane's recent request duration")
}

func TestQoSGateRejectsAtDepth(t *testing.T) {
	g := &qosGate{}
	ctx := context.Background()
	_, err := g.acquire(ctx, qosBackground, twoSeats, time.Second, 1)
	require.NoError(t, err)
	go func() { _, _ = g.acquire(ctx, qosBackground, twoSeats, time.Second, 1) }()
	require.Eventually(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return len(g.waiters) == 1
	}, time.Second, time.Millisecond)

	start := time.Now()
	outcome, err := g.acquire(ctx, qosBatch, twoSeats, time.Second, 1)
	assert.Equal(t, qosOutcomeRejected, outcome)
	var full *qosQueueFullError
	require.ErrorAs(t, err, &full)
	assert.Equal(t, 2, full.position)
	assert.Less(t, time.Since(start), 500*time.Millisecond, "a full queue refuses at once")
}

func TestQoSGateCanceledWaiterLeavesQueue(t *testing.T) {
	g := &qosGate{}
	_, err := g.acquire(context.Background(), qosBackground, twoSeats, time.Second, 8)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan string, 1)
	go func() {
		outcome, _ := g.acquire(ctx, qosBackground, twoSeats, 5*time.Second, 8)
		done <- outcome
	}()
	require.Eventually(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return len(g.waiters) == 1
	}, time.Second, time.Millisecond)
	cancel()
	assert.Equal(t, qosOutcomeCanceled, <-done)
	g.mu.Lock()
	defer g.mu.Unlock()
	assert.Empty(t, g.waiters)
	assert.Equal(t, 1, g.inflight)
}

func qosLaneTestProxy(t *testing.T, annotations map[string]string, config string) (*Proxy, *aiv1alpha2.Model) {
	t.Helper()
	p, model := workloadClassTestProxy(t, aiv1alpha2.ModelPhaseReady)
	model.Annotations = annotations
	model.Spec.Config = &apiextensionsv1.JSON{Raw: []byte(config)}
	require.NoError(t, p.client.Update(context.Background(), model))
	return p, model
}

func qosChatRequest(class string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerQoS, class)
	return req
}

func TestHandleRequestGatesBackgroundOnEnforcedLane(t *testing.T) {
	p, model := qosLaneTestProxy(t, map[string]string{annotationQoS: "proxy"}, `{"maxNumSeqs":2}`)
	p.qosQueueWait = 50 * time.Millisecond

	hold := make(chan struct{})
	arrived := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		if r.Header.Get("X-Test-Hold") == "1" {
			<-hold
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	p.directLoadTargets.Store(model.Name, server.URL)

	rejected := qosDecisionsTotal.WithLabelValues("background", model.Name, qosOutcomeRejected)
	before := testutil.ToFloat64(rejected)

	// One background request takes the only background seat and holds it.
	first := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		req := qosChatRequest("background")
		req.Header.Set("X-Test-Hold", "1")
		p.handleRequest(first, req)
		close(firstDone)
	}()
	<-arrived

	// A second background request waits out the queue bound and is refused.
	second := httptest.NewRecorder()
	p.handleRequest(second, qosChatRequest("background"))
	assert.Equal(t, http.StatusServiceUnavailable, second.Code)
	assert.Equal(t, "1", second.Header().Get(headerQueuePosition))
	assert.NotEmpty(t, second.Header().Get("Retry-After"))
	assert.Contains(t, second.Body.String(), `"queue_full"`)
	assert.Equal(t, before+1, testutil.ToFloat64(rejected))

	// The reserved seat still serves an interactive turn at once.
	third := httptest.NewRecorder()
	p.handleRequest(third, qosChatRequest("interactive"))
	assert.Equal(t, http.StatusOK, third.Code)

	close(hold)
	<-firstDone
	assert.Equal(t, http.StatusOK, first.Code)
}

func TestHandleRequestDoesNotGateUnenforcedLane(t *testing.T) {
	p, model := qosLaneTestProxy(t, nil, `{"maxNumSeqs":1}`)
	p.qosQueueWait = 20 * time.Millisecond
	hold := make(chan struct{})
	arrived := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		if r.Header.Get("X-Test-Hold") == "1" {
			<-hold
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	p.directLoadTargets.Store(model.Name, server.URL)

	firstDone := make(chan struct{})
	go func() {
		req := qosChatRequest("background")
		req.Header.Set("X-Test-Hold", "1")
		p.handleRequest(httptest.NewRecorder(), req)
		close(firstDone)
	}()
	<-arrived
	rec := httptest.NewRecorder()
	p.handleRequest(rec, qosChatRequest("background"))
	assert.Equal(t, http.StatusOK, rec.Code, "a lane without an opt-in forwards every class")
	close(hold)
	<-firstDone
}

func TestDrainQueueServesQoSRankOrder(t *testing.T) {
	p, model := workloadClassTestProxy(t, aiv1alpha2.ModelPhaseReady)
	var mu sync.Mutex
	var order []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		order = append(order, r.Header.Get("X-Test-Name"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	p.directLoadTargets.Store(model.Name, server.URL)

	queue := &RequestQueue{model: model.Name, items: make(chan *QueuedRequest, 8)}
	for _, item := range []struct {
		name string
		rank int
	}{{"batch", 2}, {"bg-1", 1}, {"chat-1", 0}, {"bg-2", 1}, {"chat-2", 0}} {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"test-model","messages":[]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Name", item.name)
		queue.items <- &QueuedRequest{w: httptest.NewRecorder(), r: req, modelName: model.Name, done: make(chan struct{}), rank: item.rank}
	}
	p.drainQueue(queue)
	assert.Equal(t, []string{"chat-1", "chat-2", "bg-1", "bg-2", "batch"}, order)
}
