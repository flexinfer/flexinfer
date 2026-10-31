package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/validation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// envelope decodes a proxy error body, failing the test unless it carries the
// flexinfer block with all six keys.
func envelope(t *testing.T, rec *httptest.ResponseRecorder) validation.OpenAIError {
	t.Helper()
	var raw struct {
		Error struct {
			FlexInfer map[string]json.RawMessage `json:"flexinfer"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw), rec.Body.String())
	for _, key := range []string{"phase", "loading_substage", "eta_seconds", "retry_after_seconds", "reroute_to", "queue_position"} {
		_, ok := raw.Error.FlexInfer[key]
		assert.True(t, ok, "flexinfer.%s missing: %s", key, rec.Body.String())
	}
	var resp validation.OpenAIErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Error.FlexInfer, rec.Body.String())
	return resp.Error
}

// assertRetryAfter checks the header and the block agree on want seconds.
func assertRetryAfter(t *testing.T, rec *httptest.ResponseRecorder, e validation.OpenAIError, want int) {
	t.Helper()
	assert.Equal(t, strconv.Itoa(want), rec.Header().Get("Retry-After"))
	require.NotNil(t, e.FlexInfer.RetryAfterSeconds)
	assert.Equal(t, want, *e.FlexInfer.RetryAfterSeconds)
}

func loadingModel(name string, substage aiv1alpha2.LoadingSubstage, loadingFor time.Duration, lastLoad string) *aiv1alpha2.Model {
	started := metav1.NewTime(time.Now().Add(-loadingFor))
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://a/b"},
		Status: aiv1alpha2.ModelStatus{
			Phase:             aiv1alpha2.ModelPhaseLoading,
			LoadingSubstage:   substage,
			LoadingProgressAt: &started,
			Conditions: []metav1.Condition{{
				Type: aiv1alpha2.ConditionModelReady, Status: metav1.ConditionFalse,
				Reason: aiv1alpha2.ReasonStartingBackend, LastTransitionTime: started,
			}},
		},
	}
	if lastLoad != "" {
		m.Status.Metrics = &aiv1alpha2.MetricsStatus{LoadTimeSeconds: lastLoad}
	}
	return m
}

// stalledQueue registers a cold-start queue with no processor, so a request
// waits out its own deadline and the handler, not the processor, answers.
func stalledQueue(p *Proxy, model string, capacity int) *RequestQueue {
	q := &RequestQueue{model: model, items: make(chan *QueuedRequest, capacity), created: time.Now()}
	p.queues.Store(model, q)
	return q
}

func TestEnvelopeModelColdStartingOnPreferWait(t *testing.T) {
	m := loadingModel("workhorse", aiv1alpha2.LoadingSubstageCompiling, 100*time.Second, "300")
	p := newGateTestProxy(t, &countingActivator{}, m)
	stalledQueue(p, "workhorse", 4)

	req := chatReq("workhorse")
	req.Header.Set("Prefer", "wait=0")
	rec := httptest.NewRecorder()
	p.handleRequest(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	e := envelope(t, rec)
	assert.Equal(t, validation.CodeModelColdStarting, e.Code)
	assert.Equal(t, "Loading", *e.FlexInfer.Phase)
	assert.Equal(t, "Compiling", *e.FlexInfer.LoadingSubstage)
	require.NotNil(t, e.FlexInfer.ETASeconds)
	assert.InDelta(t, 200, *e.FlexInfer.ETASeconds, 2, "p50 300 s minus 100 s already loading")
	assertRetryAfter(t, rec, e, retryAfterColdStartMax)
	assert.Nil(t, e.FlexInfer.RerouteTo)
	assert.Equal(t, "Loading/Compiling", rec.Header().Get(headerActivation))
	assert.Equal(t, "wait=0", rec.Header().Get("Preference-Applied"))
}

func TestEnvelopeModelColdStartingOnQueueTimeoutKeeps504(t *testing.T) {
	m := loadingModel("workhorse", aiv1alpha2.LoadingSubstageLoadingWeights, time.Second, "")
	p := newGateTestProxy(t, &countingActivator{}, m)
	p.queueTimeout = 20 * time.Millisecond
	stalledQueue(p, "workhorse", 4)

	rec := httptest.NewRecorder()
	p.handleRequest(rec, chatReq("workhorse"))

	require.Equal(t, http.StatusGatewayTimeout, rec.Code)
	e := envelope(t, rec)
	assert.Equal(t, validation.ErrorTypeTimeout, e.Type)
	assert.Equal(t, validation.CodeModelColdStarting, e.Code)
	assert.Contains(t, e.Message, "Timeout waiting for model to become ready")
	assert.Nil(t, e.FlexInfer.ETASeconds, "no load history: the ETA is unknown, not invented")
	assertRetryAfter(t, rec, e, retryAfterColdStartMax)
}

func TestEnvelopeQueueFull(t *testing.T) {
	m := loadingModel("workhorse", aiv1alpha2.LoadingSubstageImagePulling, time.Second, "")
	p := newGateTestProxy(t, &countingActivator{}, m)
	stalledQueue(p, "workhorse", 0)

	rec := httptest.NewRecorder()
	p.handleRequest(rec, chatReq("workhorse"))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	e := envelope(t, rec)
	assert.Equal(t, validation.CodeQueueFull, e.Code)
	assert.Equal(t, "Service overloaded, please retry", e.Message)
	assertRetryAfter(t, rec, e, retryAfterColdStartMax)
	assert.Equal(t, "Loading/ImagePulling", rec.Header().Get(headerActivation))
}

func TestEnvelopeModelParked(t *testing.T) {
	t.Run("behind a primary is 503", func(t *testing.T) {
		m := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "starved", Namespace: "default",
				Annotations: map[string]string{rerouteHintAnnotation: "starved-5930k"}},
			Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://a/b"},
			Status: aiv1alpha2.ModelStatus{
				Phase:           aiv1alpha2.ModelPhasePreempted,
				LoadingSubstage: aiv1alpha2.LoadingSubstagePreempted,
				SharedGroup: &aiv1alpha2.SharedGroupStatus{
					State: "Queued", PreemptedBy: aiv1alpha2.PreemptedByPrimaryPrefix + "leader",
				},
			},
		}
		p := newGateTestProxy(t, &countingActivator{}, m)
		rec := httptest.NewRecorder()
		p.handleRequest(rec, chatReq("starved"))

		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		e := envelope(t, rec)
		assert.Equal(t, validation.CodeModelParked, e.Code)
		assert.Contains(t, e.Message, "parked behind a higher-priority primary")
		assert.Equal(t, "Preempted", *e.FlexInfer.Phase)
		assert.Nil(t, e.FlexInfer.ETASeconds)
		assertRetryAfter(t, rec, e, retryAfterParked)
		require.NotNil(t, e.FlexInfer.RerouteTo)
		assert.Equal(t, "starved-5930k", *e.FlexInfer.RerouteTo)
	})
	t.Run("litellm disabled is 404 without a retry hint", func(t *testing.T) {
		p := newGateTestProxy(t, &countingActivator{}, parkedModel("parked", "Idle"))
		rec := httptest.NewRecorder()
		p.handleRequest(rec, chatReq("parked"))

		require.Equal(t, http.StatusNotFound, rec.Code)
		e := envelope(t, rec)
		assert.Equal(t, validation.ErrorTypeNotFound, e.Type)
		assert.Equal(t, validation.CodeModelParked, e.Code)
		assert.Equal(t, "Idle", *e.FlexInfer.Phase)
		assert.Nil(t, e.FlexInfer.RetryAfterSeconds)
		assert.Empty(t, rec.Header().Get("Retry-After"))
	})
}

func TestEnvelopeModelPreempted(t *testing.T) {
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "creative", Namespace: "default"},
		Spec:       aiv1alpha2.ModelSpec{Backend: "diffusers", Source: "pvc://a/b"},
		Status: aiv1alpha2.ModelStatus{
			Phase:           aiv1alpha2.ModelPhasePreempted,
			LoadingSubstage: aiv1alpha2.LoadingSubstagePreempted,
			Metrics:         &aiv1alpha2.MetricsStatus{LoadTimeSeconds: "90"},
			SharedGroup:     &aiv1alpha2.SharedGroupStatus{State: "Queued", PreemptedBy: "warm"},
		},
	}
	p := newGateTestProxy(t, &countingActivator{}, m)
	stalledQueue(p, "creative", 4)

	req := chatReq("creative")
	req.Header.Set("Prefer", "respond-async, wait=0")
	rec := httptest.NewRecorder()
	p.handleRequest(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	e := envelope(t, rec)
	assert.Equal(t, validation.CodeModelPreempted, e.Code)
	assert.Nil(t, e.FlexInfer.ETASeconds, "a preempted lane waits on the election, not a clock")
	assertRetryAfter(t, rec, e, retryAfterPreempted)
	assert.Equal(t, "Preempted/Preempted", rec.Header().Get(headerActivation))
}

// drainOne queues one request on a fresh queue for model and fails the queue
// with err, returning what the client received.
func drainOne(t *testing.T, p *Proxy, model string, err error) *httptest.ResponseRecorder {
	t.Helper()
	q := &RequestQueue{model: model, items: make(chan *QueuedRequest, 1)}
	rec := httptest.NewRecorder()
	q.items <- &QueuedRequest{w: rec, r: chatReq(model), modelName: model, done: make(chan struct{})}
	queueDepth.WithLabelValues(model).Inc()
	p.drainQueueWithError(q, err)
	return rec
}

func TestEnvelopeModelFailed(t *testing.T) {
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "broken", Namespace: "default"},
		Spec:       aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://a/b"},
		Status:     aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseFailed, Message: "GPU degraded"},
	}
	p := newGateTestProxy(t, &countingActivator{}, m)

	rec := drainOne(t, p, "broken", &ModelFailedError{Model: "broken", Message: "GPU degraded"})

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	e := envelope(t, rec)
	assert.Equal(t, validation.CodeModelFailed, e.Code)
	assert.Contains(t, e.Message, "Failed to activate model")
	assert.Equal(t, "Failed", *e.FlexInfer.Phase)
	assert.Nil(t, e.FlexInfer.ETASeconds)
	assertRetryAfter(t, rec, e, retryAfterFailed)
}

func TestEnvelopeStalledLoad(t *testing.T) {
	m := loadingModel("wedged", aiv1alpha2.LoadingSubstageLoadingWeights, 10*time.Minute, "")
	p := newGateTestProxy(t, &countingActivator{}, m)

	rec := drainOne(t, p, "wedged", fmt.Errorf("activation: %w", &StalledLoadError{
		Model: "wedged", Substage: aiv1alpha2.LoadingSubstageLoadingWeights, ProgressAge: 3 * time.Minute,
	}))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	e := envelope(t, rec)
	assert.Equal(t, validation.CodeStalledLoad, e.Code)
	assert.Contains(t, e.Message, "load stalled at substage=LoadingWeights")
	assert.Equal(t, "LoadingWeights", *e.FlexInfer.LoadingSubstage)
	assertRetryAfter(t, rec, e, defaultStalledLoadRetryAfter)
	assert.Equal(t, "Loading/LoadingWeights", rec.Header().Get(headerActivation))
}

func TestEnvelopeActivationFailureNamesTheLaneState(t *testing.T) {
	m := loadingModel("slow", aiv1alpha2.LoadingSubstageCompiling, time.Minute, "")
	p := newGateTestProxy(t, &countingActivator{}, m)

	rec := drainOne(t, p, "slow", errors.New("timeout waiting for model to become ready (after 10m0s)"))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	e := envelope(t, rec)
	assert.Equal(t, validation.CodeModelColdStarting, e.Code)
	assert.Contains(t, e.Message, "Failed to activate model: timeout waiting")
}

func TestEnvelopeLaneUnresponsive(t *testing.T) {
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "silent", Namespace: "default"},
		Spec:       aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://a/b"},
		Status:     aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseReady},
	}
	p := newGateTestProxy(t, &countingActivator{}, m)
	p.laneConfig = Config{LaneBreakerThreshold: 3}
	b := p.lane("silent")
	b.mu.Lock()
	b.open, b.until = true, time.Now().Add(40*time.Second)
	b.mu.Unlock()

	rec := httptest.NewRecorder()
	require.True(t, p.rejectLane(rec, chatReq("silent"), "silent", "silent", nil))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	e := envelope(t, rec)
	assert.Equal(t, validation.CodeLaneUnresponsive, e.Code)
	assert.Equal(t, "Ready", *e.FlexInfer.Phase)
	assertRetryAfter(t, rec, e, 40)
	require.NotNil(t, e.FlexInfer.ETASeconds)
	assert.Equal(t, 40, *e.FlexInfer.ETASeconds, "an open breaker serves again at its probe, not after a load")
}

func TestEnvelopeLaneUnresponsiveOnFirstByteBudget(t *testing.T) {
	t.Setenv(envMaxForwardAttempts, "1")
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer upstream.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &Proxy{ctx: ctx, laneConfig: Config{LaneBreakerThreshold: 3, LaneFirstByteBudget: 10 * time.Millisecond}}
	p.directLoadTargets.Store("silent", upstream.URL)

	rec := httptest.NewRecorder()
	p.forwardWithRetry(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), "silent", "silent", 8000, nil, nil)

	require.Equal(t, http.StatusGatewayTimeout, rec.Code)
	e := envelope(t, rec)
	assert.Equal(t, upstreamErrorType, e.Type)
	assert.Equal(t, validation.CodeLaneUnresponsive, e.Code)
	assert.Equal(t, "upstream first byte budget exceeded", e.Message)
	assert.Empty(t, rec.Header().Get("Retry-After"), "a slow lane is not told to be retried at once")
}

func TestEnvelopeContextOverflow(t *testing.T) {
	f := newOverflowFixture(t, http.StatusBadRequest, vllmInputContextError, overflowToBig, nil, aiv1alpha2.ModelPhaseIdle)

	rec := f.serve(t)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	e := envelope(t, rec)
	assert.Equal(t, validation.ErrorTypeInvalidRequest, e.Type)
	assert.Equal(t, validation.CodeContextOverflow, e.Code)
	assert.Contains(t, e.Message, "longer than the maximum model length of 32768", "the runtime's message is kept")
	require.NotNil(t, e.FlexInfer.RerouteTo)
	assert.Equal(t, "big", *e.FlexInfer.RerouteTo)
	assert.Equal(t, "Idle", *e.FlexInfer.Phase, "the block describes the overflow lane")
	assert.Nil(t, e.FlexInfer.RetryAfterSeconds)
}

func TestEnvelopeAdmissionRejected(t *testing.T) {
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "short", Namespace: "default",
			Annotations: map[string]string{admissionAnnotation: admissionAnnotationValue}},
		Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://a/b",
			Config: &apiextensionsv1.JSON{Raw: []byte(`{"maxModelLen":1024}`)}},
		Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseReady},
	}
	p := newGateTestProxy(t, &countingActivator{}, m)
	p.admission = &admissionFilter{Enabled: true, DefaultMaxTokens: 256}

	body, _ := json.Marshal(map[string]any{
		"model": "short", "max_tokens": 512,
		"messages": []map[string]string{{"role": "user", "content": strings.Repeat("token ", 4000)}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleRequest(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	e := envelope(t, rec)
	assert.Equal(t, validation.CodeAdmissionRejected, e.Code)
	assert.Equal(t, "Ready", *e.FlexInfer.Phase)
	assert.Nil(t, e.FlexInfer.RetryAfterSeconds, "a 413 is fixed by the caller, not by waiting")
	assert.Contains(t, rec.Body.String(), `"admission":{`, "the admission extension stays")
}

// The cold-start path stamps X-FlexInfer-Activation on the writer the queue
// later serves through, so the eventual 200 carries it too.
func TestActivationHeaderRidesTheEventual200(t *testing.T) {
	m := loadingModel("workhorse", aiv1alpha2.LoadingSubstageLoadingWeights, time.Second, "")
	p := newGateTestProxy(t, &countingActivator{}, m)
	p.queueTimeout = 5 * time.Second
	q := stalledQueue(p, "workhorse", 4)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		p.handleRequest(rec, chatReq("workhorse"))
		close(done)
	}()
	var qr *QueuedRequest
	select {
	case qr = <-q.items:
	case <-time.After(2 * time.Second):
		t.Fatal("request never queued")
	}
	require.True(t, qr.responded.CompareAndSwap(false, true))
	assert.Equal(t, "Loading/LoadingWeights", qr.w.Header().Get(headerActivation))
	qr.w.WriteHeader(http.StatusOK)
	close(qr.done)
	<-done
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Loading/LoadingWeights", rec.Header().Get(headerActivation))
}

func TestLoadETA(t *testing.T) {
	p := &Proxy{}
	now := time.Now()
	cases := []struct {
		name string
		m    *aiv1alpha2.Model
		want *int
	}{
		{"idle lane waits a whole p50", &aiv1alpha2.Model{Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseIdle, Metrics: &aiv1alpha2.MetricsStatus{LoadTimeSeconds: "120.4"}}}, intPtr(121)},
		{"loading lane subtracts elapsed", loadingModel("x", aiv1alpha2.LoadingSubstageLoadingWeights, 100*time.Second, "300"), intPtr(200)},
		{"overdue lane holds the substage floor", loadingModel("x", aiv1alpha2.LoadingSubstageHealthCheckPending, 400*time.Second, "300"), intPtr(5)},
		{"ready lane is zero", &aiv1alpha2.Model{Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseReady}}, intPtr(0)},
		{"no history is unknown", loadingModel("x", aiv1alpha2.LoadingSubstageCompiling, time.Second, ""), nil},
		{"failed lane is unknown", &aiv1alpha2.Model{Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseFailed, Metrics: &aiv1alpha2.MetricsStatus{LoadTimeSeconds: "60"}}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := p.loadETA(tc.m, now)
			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.InDelta(t, *tc.want, *got, 1)
		})
	}
}

func TestPreferredWait(t *testing.T) {
	cases := map[string]struct {
		header string
		want   time.Duration
		ok     bool
	}{
		"absent":             {"", 0, false},
		"zero":               {"wait=0", 0, true},
		"with respond-async": {"respond-async, wait=15", 15 * time.Second, true},
		"with params":        {`wait="20"; foo=bar`, 20 * time.Second, true},
		"negative ignored":   {"wait=-1", 0, false},
		"other preference":   {"return=minimal", 0, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			if tc.header != "" {
				r.Header.Set("Prefer", tc.header)
			}
			got, ok := preferredWait(r)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestLoadLatencyCacheReadsP50ByServingPath(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		assert.Contains(t, q, `namespace="flexinfer-system"`)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(q, "histogram_quantile") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"model":"workhorse","serving_path":"deployment"},"value":[1,"767"]},
				{"metric":{"model":"workhorse","serving_path":"runtime"},"value":[1,"95"]},
				{"metric":{"model":"never","serving_path":"runtime"},"value":[1,"NaN"]}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"model":"workhorse","serving_path":"deployment"},"value":[1,"12"]},
			{"metric":{"model":"workhorse","serving_path":"runtime"},"value":[1,"3"]}]}}`))
	}))
	defer prom.Close()

	c := newLoadLatencyCache(prom.URL+"/", "flexinfer-system")
	require.NoError(t, c.refresh(context.Background()))

	v, ok := c.lookup("workhorse", servingPathRuntime)
	assert.True(t, ok)
	assert.Equal(t, 95.0, v)
	v, ok = c.lookup("workhorse", "")
	assert.True(t, ok)
	assert.Equal(t, 767.0, v, "with no path known, the most-used path wins")
	_, ok = c.lookup("never", servingPathRuntime)
	assert.False(t, ok, "a lane with no samples has no p50")
	assert.Nil(t, newLoadLatencyCache("", "x"), "no URL, no cache")

	p := &Proxy{loadLatency: c}
	m := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "workhorse"},
		Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseIdle, Metrics: &aiv1alpha2.MetricsStatus{LoadTimeSeconds: "10"}}}
	p.directLoadTargets.Store("workhorse", "http://10.0.0.1:8000")
	got, ok := p.loadP50(m)
	assert.True(t, ok)
	assert.Equal(t, 95.0, got, "Prometheus outranks the last load on the Model")
}

func intPtr(v int) *int { return &v }
