package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/internal/routing"
	"github.com/flexinfer/flexinfer/pkg/constants"
	"github.com/flexinfer/flexinfer/pkg/validation"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/validation/path"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// parkedModel returns a Model with spec.litellm.enabled=false and the given phase.
func parkedModel(name, phase string) *aiv1alpha2.Model {
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "pvc://x/y",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{
				Enabled:         boolPtr(false),
				ServedModelName: name,
				Aliases:         []string{name + "-alias"},
			},
		},
		Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhase(phase)},
	}
}

func TestLitellmExplicitlyDisabled(t *testing.T) {
	cases := []struct {
		name string
		ll   *aiv1alpha2.LiteLLMSpec
		want bool
	}{
		{"nil litellm block (fleet member)", nil, false},
		{"enabled nil (default true)", &aiv1alpha2.LiteLLMSpec{}, false},
		{"enabled true", &aiv1alpha2.LiteLLMSpec{Enabled: boolPtr(true)}, false},
		{"enabled false (parked)", &aiv1alpha2.LiteLLMSpec{Enabled: boolPtr(false)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &aiv1alpha2.Model{Spec: aiv1alpha2.ModelSpec{LiteLLM: tc.ll}}
			assert.Equal(t, tc.want, litellmExplicitlyDisabled(m))
		})
	}
	assert.False(t, litellmExplicitlyDisabled(nil))
}

func TestModelServesPath(t *testing.T) {
	audioOnly := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{constants.AnnotationServePaths: "/v1/audio/transcriptions"},
	}}
	multi := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{constants.AnnotationServePaths: " /v1/audio/transcriptions , /v1/audio/translations "},
	}}
	noAnno := &aiv1alpha2.Model{}

	assert.True(t, modelServesPath(noAnno, "/v1/chat/completions"), "no annotation serves all paths")
	assert.True(t, modelServesPath(audioOnly, "/v1/audio/transcriptions"))
	assert.False(t, modelServesPath(audioOnly, "/v1/chat/completions"), "chat to audio-only model is rejected")
	assert.True(t, modelServesPath(multi, "/v1/audio/translations"), "whitespace-trimmed multi-path match")
	assert.True(t, modelServesPath(nil, "/v1/chat/completions"))
}

// --- catalog + alias filtering ---

func newPolicyTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestHandleModels_HidesParkedModels(t *testing.T) {
	RegisterMetrics()
	served := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "gemma4-served", Namespace: "default"},
		Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://a/b",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{Enabled: boolPtr(true)}},
	}
	parked := parkedModel("qwen3-14b-parked", "Idle")

	p := &Proxy{client: newPolicyTestClient(t, served, parked), namespace: "default"}

	rec := httptest.NewRecorder()
	p.handleModels(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp OpenAIModelsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	ids := map[string]bool{}
	for _, m := range resp.Data {
		ids[m.ID] = true
	}
	assert.True(t, ids["gemma4-served"], "served model should be advertised")
	assert.False(t, ids["qwen3-14b-parked"], "parked model must be hidden from /v1/models")
}

func TestResolveModelAlias_SkipsParkedModels(t *testing.T) {
	served := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "gemma4-served", Namespace: "default"},
		Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://a/b",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{Enabled: boolPtr(true), Aliases: []string{"quality-chat"}}},
	}
	parked := parkedModel("qwen3-14b-parked", "Idle") // alias "qwen3-14b-parked-alias"

	r := NewModelResolver(newPolicyTestClient(t, served, parked), "default")
	ctx := context.Background()

	assert.Equal(t, "gemma4-served", r.ResolveModelAlias(ctx, "quality-chat"),
		"live alias resolves to its model")
	assert.Equal(t, "qwen3-14b-parked-alias", r.ResolveModelAlias(ctx, "qwen3-14b-parked-alias"),
		"parked model alias must NOT resolve (returns input unchanged)")
}

// --- handleRequest gates (no demand-touch / no queue) ---

func newGateTestProxy(t *testing.T, act ModelActivator, objs ...client.Object) *Proxy {
	t.Helper()
	return newGateTestProxyWithClient(t, act, newPolicyTestClient(t, objs...))
}

// newGateTestProxyWithClient is newGateTestProxy over a caller-supplied client,
// for tests that need to shape the Kubernetes API's error behavior.
func newGateTestProxyWithClient(t *testing.T, act ModelActivator, c client.Client) *Proxy {
	t.Helper()
	RegisterMetrics()
	return &Proxy{
		client:           c,
		namespace:        "default",
		maxQueueSize:     100,
		queueTimeout:     2 * time.Second,
		coldStartTimeout: 2 * time.Second,
		router:           routing.NewRouter(),
		resolver:         NewModelResolver(c, "default"),
		activator:        act,
		admission:        &admissionFilter{}, // disabled (zero value)
	}
}

func chatReq(model string) *http.Request {
	body, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestHandleRequest_ParkedModel_404NoColdStart(t *testing.T) {
	act := &countingActivator{}
	parked := parkedModel("qwen3-14b-parked", "Preempted")
	p := newGateTestProxy(t, act, parked)

	rec := httptest.NewRecorder()
	p.handleRequest(rec, chatReq("qwen3-14b-parked"))

	assert.Equal(t, http.StatusNotFound, rec.Code, "parked model returns 404 fast")
	assert.Equal(t, 0, act.touches, "parked model must NOT touch demand")
	_, queued := p.queues.Load("qwen3-14b-parked")
	assert.False(t, queued, "parked model must NOT create a cold-start queue")
}

func TestHandleRequest_AudioOnlyModel_ChatProbe404NoColdStart(t *testing.T) {
	act := &countingActivator{}
	whisper := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name: "whisper", Namespace: "default",
			Annotations: map[string]string{constants.AnnotationServePaths: "/v1/audio/transcriptions"},
		},
		Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://a/b",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{Enabled: boolPtr(true)}},
		Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhasePending},
	}
	p := newGateTestProxy(t, act, whisper)

	rec := httptest.NewRecorder()
	p.handleRequest(rec, chatReq("whisper")) // chat probe to an ASR-only model

	assert.Equal(t, http.StatusNotFound, rec.Code, "chat probe to audio-only model returns 404")
	assert.Equal(t, 0, act.touches, "chat probe must NOT warm an audio-only model")
	_, queued := p.queues.Load("whisper")
	assert.False(t, queued, "chat probe must NOT create a cold-start queue for whisper")
}

// TestHandleRequest_ParkedBehindPrimary_503NoColdStart covers the durable gate:
// a still-advertised shared-group member that the controller marked statically
// un-promotable (PreemptedBy "primary/<leader>") must fast-fail 503 instead of
// spinning a doomed cold-start the election would immediately kill.
func TestHandleRequest_ParkedBehindPrimary_503NoColdStart(t *testing.T) {
	act := &countingActivator{}
	starved := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "whisper-starved", Namespace: "default"},
		Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://a/b",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{Enabled: boolPtr(true)}}, // still advertised
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhasePreempted,
			SharedGroup: &aiv1alpha2.SharedGroupStatus{
				GroupName:   "7900xtx-textgen",
				State:       "Queued",
				PreemptedBy: aiv1alpha2.PreemptedByPrimaryPrefix + "gemma4-26b-a4b-gptq",
			},
		},
	}
	p := newGateTestProxy(t, act, starved)

	rec := httptest.NewRecorder()
	p.handleRequest(rec, chatReq("whisper-starved"))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "statically-parked member returns 503 fast")
	assert.Equal(t, 0, act.touches, "parked-behind-primary must NOT touch demand")
	_, queued := p.queues.Load("whisper-starved")
	assert.False(t, queued, "parked-behind-primary must NOT create a cold-start queue")
}

// TestHandleRequest_OrdinaryPreemption_StillColdStarts guards against
// over-eager fast-fail: a transient preemption (bare PreemptedBy, no "primary/"
// prefix) is promotable by demand, so it MUST still cold-start.
func TestHandleRequest_OrdinaryPreemption_StillColdStarts(t *testing.T) {
	act := &countingActivator{}
	preempted := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-preempted", Namespace: "default"},
		Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://a/b",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{Enabled: boolPtr(true)}},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhasePreempted,
			SharedGroup: &aiv1alpha2.SharedGroupStatus{
				GroupName:   "7900xtx-textgen",
				State:       "Queued",
				PreemptedBy: "gemma4-26b-a4b-gptq", // bare name: promotable on demand
			},
		},
	}
	p := newGateTestProxy(t, act, preempted)

	rec := httptest.NewRecorder()
	p.handleRequest(rec, chatReq("qwen-preempted"))

	// The fast-fail gate never touches demand; the cold-start path does. A
	// non-zero touch count proves the request proceeded to cold-start (promotable
	// on demand) rather than being short-circuited as parked-behind-primary. The
	// fake activator then times out in ~1ms, which is the expected cold-start
	// outcome here — not the gate firing.
	assert.Positive(t, act.touches,
		"ordinary preemption must proceed to cold-start (touch demand), not fast-fail")
}

// --- invalid Kubernetes resource names (LiteLLM gateway aliases etc.) ---

func TestIsValidModelResourceName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"qwen3-8b", true},
		{"gemma4-26b-a4b-gptq", true},
		{"whisper.large-v3", true},
		{"or/kimi-k3", false},        // LiteLLM gateway alias, not a CR name
		{"or/kimi-k2.7-code", false}, // same shape, observed 2026-09-07
		{"Qwen3-8B", false},          // uppercase is rejected by the API server
		{"qwen3_8b", false},          // underscore is not DNS-1123
		{"-leading-dash", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isValidModelResourceName(tc.name))
		})
	}
}

// clientGoNameRejectingClient wraps the fake client with client-go's real
// behavior for names that are not valid URL path segments: the REST client
// fails the request with a plain fmt error before ever dialing the API server
// (see k8s.io/client-go/rest.(*Request).Name). errors.IsNotFound is false for
// that error, which is exactly what used to turn a bad alias into a 500.
func clientGoNameRejectingClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if msgs := path.IsValidPathSegmentName(key.Name); len(msgs) != 0 {
					return fmt.Errorf("invalid resource name %q: %v", key.Name, msgs)
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
}

func decodeOpenAIError(t *testing.T, rec *httptest.ResponseRecorder) validation.OpenAIError {
	t.Helper()
	var resp validation.OpenAIErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), "body: %s", rec.Body.String())
	return resp.Error
}

// TestHandleRequest_InvalidResourceName_404NotFound reproduces the 2026-09-07
// report: a caller dialed the proxy with LiteLLM gateway aliases ("or/kimi-k3",
// "or/kimi-k2.7-code") that no Model advertises. A slash can never appear in a
// Kubernetes resource name, so the lookup is a not-found by construction — the
// proxy must answer 404 model_not_found and count the request as not_found,
// not 500 server_error counted as error (which feeds the HighErrorRate alert).
func TestHandleRequest_InvalidResourceName_404NotFound(t *testing.T) {
	for _, name := range []string{"or/kimi-k3", "or/kimi-k2.7-code"} {
		t.Run(name, func(t *testing.T) {
			act := &countingActivator{}
			p := newGateTestProxyWithClient(t, act, clientGoNameRejectingClient(t))

			notFoundBefore := promtestutil.ToFloat64(requestsTotal.WithLabelValues(name, "not_found"))
			errorBefore := promtestutil.ToFloat64(requestsTotal.WithLabelValues(name, "error"))

			rec := httptest.NewRecorder()
			p.handleRequest(rec, chatReq(name))

			assert.Equal(t, http.StatusNotFound, rec.Code, "slash-containing model id is not-found, not an internal error")
			e := decodeOpenAIError(t, rec)
			assert.Equal(t, validation.ErrorTypeNotFound, e.Type)
			assert.Equal(t, validation.CodeModelNotFound, e.Code)
			assert.Contains(t, e.Message, name)

			assert.Equal(t, notFoundBefore+1,
				promtestutil.ToFloat64(requestsTotal.WithLabelValues(name, "not_found")),
				"request must be counted as not_found")
			assert.Equal(t, errorBefore,
				promtestutil.ToFloat64(requestsTotal.WithLabelValues(name, "error")),
				"request must NOT be counted as error")

			assert.Equal(t, 0, act.touches, "unknown alias must NOT touch demand")
			_, queued := p.queues.Load(name)
			assert.False(t, queued, "unknown alias must NOT create a cold-start queue")
		})
	}
}

// TestHandleRequest_UnknownModel_CountedNotFound pins the label for the plain
// not-found path (valid name, no Model and no ModelDeployment): it is a 404 and
// must share the not_found status with the invalid-name gate above.
func TestHandleRequest_UnknownModel_CountedNotFound(t *testing.T) {
	const name = "no-such-model-anywhere"
	p := newGateTestProxy(t, &countingActivator{})

	notFoundBefore := promtestutil.ToFloat64(requestsTotal.WithLabelValues(name, "not_found"))
	errorBefore := promtestutil.ToFloat64(requestsTotal.WithLabelValues(name, "error"))

	rec := httptest.NewRecorder()
	p.handleRequest(rec, chatReq(name))

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, notFoundBefore+1, promtestutil.ToFloat64(requestsTotal.WithLabelValues(name, "not_found")))
	assert.Equal(t, errorBefore, promtestutil.ToFloat64(requestsTotal.WithLabelValues(name, "error")))
}

// TestHandleRequest_APIServerError_Still500 guards the other side of the
// boundary: a genuine API-server failure on the Model lookup (timeout, 5xx)
// must remain an internal error counted as error — the not-found treatment is
// reserved for names that cannot exist.
func TestHandleRequest_APIServerError_Still500(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"timeout", apierrors.NewTimeoutError("the request timed out", 0)},
		{"internal", apierrors.NewInternalError(fmt.Errorf("etcd unavailable"))},
		{"service unavailable", apierrors.NewServiceUnavailable("apiserver shutting down")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const model = "qwen3-8b-flaky-api"
			scheme := runtime.NewScheme()
			require.NoError(t, clientgoscheme.AddToScheme(scheme))
			require.NoError(t, aiv1alpha1.AddToScheme(scheme))
			require.NoError(t, aiv1alpha2.AddToScheme(scheme))
			c := fake.NewClientBuilder().WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return tc.err
					},
				}).Build()
			act := &countingActivator{}
			p := newGateTestProxyWithClient(t, act, c)

			errorBefore := promtestutil.ToFloat64(requestsTotal.WithLabelValues(model, "error"))
			notFoundBefore := promtestutil.ToFloat64(requestsTotal.WithLabelValues(model, "not_found"))

			rec := httptest.NewRecorder()
			p.handleRequest(rec, chatReq(model))

			assert.Equal(t, http.StatusInternalServerError, rec.Code, "API-server failure stays an internal error")
			e := decodeOpenAIError(t, rec)
			assert.Equal(t, validation.ErrorTypeServer, e.Type)
			assert.Equal(t, validation.CodeServerError, e.Code)

			assert.Equal(t, errorBefore+1, promtestutil.ToFloat64(requestsTotal.WithLabelValues(model, "error")))
			assert.Equal(t, notFoundBefore, promtestutil.ToFloat64(requestsTotal.WithLabelValues(model, "not_found")))
			assert.Equal(t, 0, act.touches, "API-server failure must NOT touch demand")
		})
	}
}
