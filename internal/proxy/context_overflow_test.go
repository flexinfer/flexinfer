package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// vLLM 0.32's three prompt-length rejections, as the API server returns them.
const (
	vllmRendererContextError = `{"error":{"message":"This model's maximum context length is 32768 tokens. However, your prompt contains 40001 input tokens, which exceeds this limit even without any output tokens. Please reduce the length of the input prompt.","type":"BadRequestError","param":"input_tokens","code":400}}`
	vllmInputContextError    = `{"error":{"message":"The decoder prompt (length 40001) is longer than the maximum model length of 32768. Make sure that max_model_len is no smaller than the number of text tokens.","type":"BadRequestError","code":400}}`
	vllmMaxTokensContextErr  = `{"error":{"message":"Input length (40001) exceeds model's maximum context length (32768).","type":"BadRequestError","code":400}}`
)

func TestIsContextLengthError(t *testing.T) {
	for _, body := range []string{vllmRendererContextError, vllmInputContextError, vllmMaxTokensContextErr} {
		assert.True(t, isContextLengthError([]byte(body)), body)
	}
	for _, body := range []string{
		`{"error":{"message":"temperature must be non-negative","code":400}}`,
		`{"error":{"message":"Invalid JSON"}}`,
		``,
	} {
		assert.False(t, isContextLengthError([]byte(body)), body)
	}
}

func TestOverflowCaptureWriterPassesThroughOtherStatuses(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("X-Before", "kept")
	cw := newOverflowCaptureWriter(rec)
	cw.Header().Set("Content-Type", "text/event-stream")
	cw.WriteHeader(http.StatusOK)
	_, err := cw.Write([]byte("data: one\n\n"))
	require.NoError(t, err)
	cw.Flush()

	assert.False(t, cw.captured())
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, rec.Flushed)
	assert.Equal(t, "data: one\n\n", rec.Body.String())
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, "kept", rec.Header().Get("X-Before"))
}

func TestOverflowCaptureWriterHoldsBackA400(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := newOverflowCaptureWriter(rec)
	cw.Header().Set("Content-Type", "application/json")
	cw.WriteHeader(http.StatusBadRequest)
	_, err := cw.Write([]byte(vllmInputContextError))
	require.NoError(t, err)

	assert.True(t, cw.captured())
	assert.Equal(t, 0, rec.Body.Len(), "nothing reaches the client while held")
	assert.Empty(t, rec.Header().Get("Content-Type"), "held headers stay off the client")

	require.NoError(t, cw.flushCaptured())
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, vllmInputContextError, rec.Body.String())
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestOverflowCaptureWriterReleasesAnOversized400(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := newOverflowCaptureWriter(rec)
	cw.WriteHeader(http.StatusBadRequest)
	big := strings.Repeat("x", maxCapturedErrorBytes+1)
	_, err := cw.Write([]byte(big))
	require.NoError(t, err)

	assert.False(t, cw.captured())
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, len(big), rec.Body.Len())
}

// overflowFixture is a proxy with two Ready Models, each served by its own
// httptest upstream through the direct-load fast path.
type overflowFixture struct {
	p          *Proxy
	small, big *httptest.Server
	smallHits  atomic.Int32
	bigHits    atomic.Int32
	bigModel   atomic.Value
}

func newOverflowFixture(t *testing.T, smallStatus int, smallBody string, smallAnn, bigAnn map[string]string, bigPhase aiv1alpha2.ModelPhase) *overflowFixture {
	t.Helper()
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	model := func(name, served string, ann map[string]string, phase aiv1alpha2.ModelPhase) *aiv1alpha2.Model {
		return &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Annotations: ann},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://test/" + name,
				Config:  &apiextensionsv1.JSON{Raw: []byte(`{"servedModelName":"` + served + `"}`)},
			},
			Status: aiv1alpha2.ModelStatus{Phase: phase},
		}
	}
	small := model("small", "small-served", smallAnn, aiv1alpha2.ModelPhaseReady)
	big := model("big", "big-served", bigAnn, bigPhase)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(small, big).WithStatusSubresource(small, big).Build()

	f := &overflowFixture{}
	f.small = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.smallHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(smallStatus)
		_, _ = io.WriteString(w, smallBody)
	}))
	t.Cleanup(f.small.Close)
	f.big = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.bigHits.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.bigModel.Store(body["model"])
		if bigAnn[annotationContextOverflowTo] != "" {
			// A target that itself overflows must not chain the request on.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, vllmInputContextError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(f.big.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f.p = &Proxy{
		client:    k8sClient,
		namespace: "default",
		resolver:  NewModelResolver(k8sClient, "default"),
		ctx:       ctx,
		cancel:    cancel,
	}
	f.p.directLoadTargets.Store("small", f.small.URL)
	f.p.directLoadTargets.Store("big", f.big.URL)
	return f
}

func (f *overflowFixture) serve(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"small","messages":[{"role":"user","content":"long"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.p.trackAndServe(rec, req, "small", time.Now())
	return rec
}

var overflowToBig = map[string]string{annotationContextOverflowTo: "big"}

func TestServeProxyReroutesContextOverflow(t *testing.T) {
	f := newOverflowFixture(t, http.StatusBadRequest, vllmRendererContextError, overflowToBig, nil, aiv1alpha2.ModelPhaseReady)
	before := testutil.ToFloat64(contextOverflowTotal.WithLabelValues("small", "big", "rerouted"))

	rec := f.serve(t)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"ok"`)
	assert.Equal(t, "small->big", rec.Header().Get(headerContextOverflow))
	assert.EqualValues(t, 1, f.smallHits.Load())
	assert.EqualValues(t, 1, f.bigHits.Load())
	assert.Equal(t, "big-served", f.bigModel.Load(), "the replay carries the target's served name")
	assert.Equal(t, before+1, testutil.ToFloat64(contextOverflowTotal.WithLabelValues("small", "big", "rerouted")))
}

func TestServeProxyReturnsTheOriginal400WhenTheTargetIsNotReady(t *testing.T) {
	f := newOverflowFixture(t, http.StatusBadRequest, vllmInputContextError, overflowToBig, nil, aiv1alpha2.ModelPhaseIdle)
	before := testutil.ToFloat64(contextOverflowTotal.WithLabelValues("small", "big", "target_unavailable"))

	rec := f.serve(t)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":"context_overflow"`, "typed, naming the overflow lane")
	assert.Contains(t, rec.Body.String(), "longer than the maximum model length of 32768")
	assert.Empty(t, rec.Header().Get(headerContextOverflow))
	assert.EqualValues(t, 0, f.bigHits.Load(), "an idle target is never cold-started")
	assert.Equal(t, before+1, testutil.ToFloat64(contextOverflowTotal.WithLabelValues("small", "big", "target_unavailable")))
}

func TestServeProxyPassesThroughOther400s(t *testing.T) {
	other := `{"error":{"message":"temperature must be non-negative","code":400}}`
	f := newOverflowFixture(t, http.StatusBadRequest, other, overflowToBig, nil, aiv1alpha2.ModelPhaseReady)

	rec := f.serve(t)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, other, rec.Body.String())
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.EqualValues(t, 0, f.bigHits.Load())
}

func TestServeProxyWithoutAnOverflowLaneIsUnchanged(t *testing.T) {
	f := newOverflowFixture(t, http.StatusBadRequest, vllmRendererContextError, nil, nil, aiv1alpha2.ModelPhaseReady)

	rec := f.serve(t)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, vllmRendererContextError, rec.Body.String())
	assert.EqualValues(t, 0, f.bigHits.Load())
}

func TestServeProxyOverflowsOnlyOnce(t *testing.T) {
	backToSmall := map[string]string{annotationContextOverflowTo: "small"}
	f := newOverflowFixture(t, http.StatusBadRequest, vllmRendererContextError, overflowToBig, backToSmall, aiv1alpha2.ModelPhaseReady)

	rec := f.serve(t)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "the target's own rejection reaches the client")
	assert.Equal(t, vllmInputContextError, rec.Body.String())
	assert.EqualValues(t, 1, f.smallHits.Load())
	assert.EqualValues(t, 1, f.bigHits.Load())
}

func TestServeProxySuccessPassesThroughWithAnOverflowLane(t *testing.T) {
	f := newOverflowFixture(t, http.StatusOK, `{"choices":[]}`, overflowToBig, nil, aiv1alpha2.ModelPhaseReady)

	rec := f.serve(t)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `{"choices":[]}`, rec.Body.String())
	assert.Empty(t, rec.Header().Get(headerContextOverflow))
	assert.EqualValues(t, 0, f.bigHits.Load())
}

func TestServeProxyReroutesUnderTheTargetLaneBudget(t *testing.T) {
	budget := map[string]string{"flexinfer.ai/lane-first-byte-budget": "450s"}
	f := newOverflowFixture(t, http.StatusBadRequest, vllmRendererContextError, overflowToBig, budget, aiv1alpha2.ModelPhaseReady)
	f.p.laneConfig.LaneBreakerThreshold = 3
	f.p.laneConfig.LaneFirstByteBudget = 2 * time.Minute

	rec := f.serve(t)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.EqualValues(t, 1, f.bigHits.Load())
	lane := f.p.lane("big")
	lane.mu.Lock()
	defer lane.mu.Unlock()
	assert.Equal(t, 450*time.Second, lane.budget, "the replay runs under the target's annotated budget, not the default")
}

func TestServeProxyKeepsTheOriginal400WhenTheTargetBreakerIsOpen(t *testing.T) {
	f := newOverflowFixture(t, http.StatusBadRequest, vllmRendererContextError, overflowToBig, nil, aiv1alpha2.ModelPhaseReady)
	f.p.laneConfig.LaneBreakerThreshold = 3
	lane := f.p.lane("big")
	lane.mu.Lock()
	lane.open, lane.until = true, time.Now().Add(time.Minute)
	lane.mu.Unlock()
	before := testutil.ToFloat64(contextOverflowTotal.WithLabelValues("small", "big", "target_unavailable"))

	rec := f.serve(t)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":"context_overflow"`)
	assert.Contains(t, rec.Body.String(), `"reroute_to":"big"`)
	assert.Contains(t, rec.Body.String(), "maximum context length is 32768 tokens")
	assert.EqualValues(t, 0, f.bigHits.Load(), "a tripped lane is not handed over-length prompts")
	assert.Equal(t, before+1, testutil.ToFloat64(contextOverflowTotal.WithLabelValues("small", "big", "target_unavailable")))
}
