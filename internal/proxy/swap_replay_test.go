package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
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

// runtimeDrainBody is what the runtime ingress writes while a lane drains
// (http.Error in internal/runtime/ingress.go).
const runtimeDrainBody = "model is draining; retry after activation\n"

func TestSwapCaptureWriterPassesThroughSuccess(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := newSwapCaptureWriter(rec)
	cw.Header().Set("Content-Type", "application/json")
	cw.WriteHeader(http.StatusOK)
	_, err := cw.Write([]byte(`{"data":[]}`))
	require.NoError(t, err)

	assert.False(t, cw.captured())
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `{"data":[]}`, rec.Body.String())
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestSwapCaptureWriterHoldsBackGatewayFailures(t *testing.T) {
	for _, code := range []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		rec := httptest.NewRecorder()
		cw := newSwapCaptureWriter(rec)
		cw.Header().Set("Retry-After", "1")
		cw.WriteHeader(code)
		_, err := cw.Write([]byte(runtimeDrainBody))
		require.NoError(t, err)

		assert.True(t, cw.captured(), code)
		assert.Equal(t, 0, rec.Body.Len(), "nothing reaches the client while held")
		require.NoError(t, cw.flushCaptured())
		assert.Equal(t, code, rec.Code)
		assert.Equal(t, runtimeDrainBody, rec.Body.String())
		assert.Equal(t, "1", rec.Header().Get("Retry-After"))
	}
	rec := httptest.NewRecorder()
	cw := newSwapCaptureWriter(rec)
	cw.WriteHeader(http.StatusInternalServerError)
	assert.False(t, cw.captured(), "a 500 is a lane error, not a swap")
}

func TestSwapSignal(t *testing.T) {
	now := time.Now()
	ready := func() *aiv1alpha2.Model {
		return &aiv1alpha2.Model{Status: aiv1alpha2.ModelStatus{
			Phase:       aiv1alpha2.ModelPhaseReady,
			SharedGroup: &aiv1alpha2.SharedGroupStatus{State: "Active"},
			Conditions: []metav1.Condition{{
				Type: aiv1alpha2.ConditionModelReady, Status: metav1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(now.Add(-time.Hour)),
			}},
		}}
	}

	assert.Equal(t, "", swapSignal(ready(), []byte(`{"error":"boom"}`), now), "a steady lane's failure is its own")
	assert.Equal(t, swapDraining, swapSignal(ready(), []byte(runtimeDrainBody), now))
	assert.Equal(t, swapDraining, swapSignal(nil, []byte(runtimeDrainBody), now))
	assert.Equal(t, "", swapSignal(nil, []byte("bad gateway"), now))

	parked := ready()
	parked.Status.SharedGroup.State = "Queued"
	parked.Status.Phase = aiv1alpha2.ModelPhasePreempted
	assert.Equal(t, swapParked, swapSignal(parked, []byte(runtimeDrainBody), now), "a park outranks the drain body")

	loading := ready()
	loading.Status.Phase = aiv1alpha2.ModelPhaseLoading
	assert.Equal(t, swapNotReady, swapSignal(loading, nil, now))

	preempted := ready()
	preempted.Status.SharedGroup.PreemptedAt = &metav1.Time{Time: now.Add(-10 * time.Second)}
	assert.Equal(t, swapRecentLoad, swapSignal(preempted, nil, now))

	reloaded := ready()
	reloaded.Status.Conditions[0].LastTransitionTime = metav1.NewTime(now.Add(-5 * time.Second))
	assert.Equal(t, swapRecentLoad, swapSignal(reloaded, nil, now))
}

func multipartBody(t *testing.T, fields map[string]string) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "a.wav")
	require.NoError(t, err)
	_, _ = fw.Write([]byte("RIFF"))
	for k, v := range fields {
		require.NoError(t, mw.WriteField(k, v))
	}
	require.NoError(t, mw.Close())
	return mw.FormDataContentType(), buf.Bytes()
}

func TestSwapReplayEligible(t *testing.T) {
	post := func(path, contentType string, body []byte) *http.Request {
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		return r
	}
	embed := []byte(`{"model":"gte","input":["a"]}`)
	assert.True(t, swapReplayEligible(post("/v1/embeddings", "application/json", embed), embed))
	assert.True(t, swapReplayEligible(post("/v1/rerank", "application/json", embed), embed))
	assert.False(t, swapReplayEligible(post("/v1/chat/completions", "application/json", embed), embed), "chat is not idempotent")
	assert.False(t, swapReplayEligible(httptest.NewRequest(http.MethodGet, "/v1/embeddings", nil), nil))

	streamJSON := []byte(`{"model":"gte","input":"a","stream":true}`)
	assert.False(t, swapReplayEligible(post("/v1/embeddings", "application/json", streamJSON), streamJSON))

	ct, plain := multipartBody(t, map[string]string{"model": "whisper"})
	assert.True(t, swapReplayEligible(post("/v1/audio/transcriptions", ct, plain), plain))
	ct, streamed := multipartBody(t, map[string]string{"model": "whisper", "stream": "true"})
	assert.False(t, swapReplayEligible(post("/v1/audio/transcriptions", ct, streamed), streamed), "a streamed transcription is never replayed")

	replayed := post("/v1/embeddings", "application/json", embed)
	replayed = replayed.WithContext(withSwapReplayed(replayed.Context()))
	assert.False(t, swapReplayEligible(replayed, embed), "a replay is never replayed again")

	t.Setenv(envSwapReplay, "false")
	assert.False(t, swapReplayEligible(post("/v1/embeddings", "application/json", embed), embed))
}

// swapFixture is a proxy with the gte lane and its CPU twin, each served by
// its own httptest upstream through the direct-load fast path. The gte
// upstream answers the drain 503 for its first drainFor embeddings and its
// health endpoint with healthStatus.
type swapFixture struct {
	p            *Proxy
	gteHits      atomic.Int32
	twinHits     atomic.Int32
	drainFor     atomic.Int32
	healthStatus atomic.Int32
	twinModel    atomic.Value
}

func newSwapFixture(t *testing.T, gteState string, gtePhase aiv1alpha2.ModelPhase, gteAnn map[string]string, twinPhase aiv1alpha2.ModelPhase) *swapFixture {
	t.Helper()
	RegisterMetrics()
	t.Setenv(envSwapReplayWait, "400ms")

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))
	model := func(name, served string, ann map[string]string, phase aiv1alpha2.ModelPhase, sg *aiv1alpha2.SharedGroupStatus) *aiv1alpha2.Model {
		return &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Annotations: ann},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "llamacpp",
				Source:  "HF://test/" + name,
				Config:  &apiextensionsv1.JSON{Raw: []byte(`{"servedModelName":"` + served + `"}`)},
			},
			Status: aiv1alpha2.ModelStatus{Phase: phase, SharedGroup: sg},
		}
	}
	gte := model("gte", "gte-served", gteAnn, gtePhase, &aiv1alpha2.SharedGroupStatus{GroupName: "vii", State: gteState})
	twin := model("twin", "twin-served", nil, twinPhase, nil)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(gte, twin).WithStatusSubresource(gte, twin).Build()

	f := &swapFixture{}
	f.healthStatus.Store(http.StatusOK)
	gteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == laneHealthPath {
			w.WriteHeader(int(f.healthStatus.Load()))
			return
		}
		n := f.gteHits.Add(1)
		if n <= f.drainFor.Load() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, strings.TrimSuffix(runtimeDrainBody, "\n"), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"embedding":[1]}],"served":"gte"}`)
	}))
	t.Cleanup(gteSrv.Close)
	twinSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.twinHits.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.twinModel.Store(body["model"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"embedding":[2]}],"served":"twin"}`)
	}))
	t.Cleanup(twinSrv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f.p = &Proxy{
		client:    k8sClient,
		namespace: "default",
		resolver:  NewModelResolver(k8sClient, "default"),
		ctx:       ctx,
		cancel:    cancel,
	}
	f.p.directLoadTargets.Store("gte", gteSrv.URL)
	f.p.directLoadTargets.Store("twin", twinSrv.URL)
	return f
}

func (f *swapFixture) serve(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gte","input":["a","b"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.p.trackAndServe(rec, req, "gte", time.Now())
	return rec
}

var rerouteToTwin = map[string]string{rerouteHintAnnotation: "twin"}

func replays(lane, outcome string) float64 {
	return testutil.ToFloat64(swapReplaysTotal.WithLabelValues(lane, outcome))
}

func TestServeProxyReplaysADrainOnTheSameLane(t *testing.T) {
	f := newSwapFixture(t, "Active", aiv1alpha2.ModelPhaseReady, nil, aiv1alpha2.ModelPhaseReady)
	f.drainFor.Store(1)
	before := replays("gte", replaySameLane)

	rec := f.serve(t, "/v1/embeddings")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"served":"gte"`)
	assert.Equal(t, "gte->gte", rec.Header().Get(headerSwapReplayed))
	assert.Empty(t, rec.Header().Get("Retry-After"), "the held-back drain headers never reach the client")
	assert.EqualValues(t, 2, f.gteHits.Load())
	assert.Equal(t, before+1, replays("gte", replaySameLane))
}

func TestServeProxyReroutesAParkedLaneToItsEquivalent(t *testing.T) {
	f := newSwapFixture(t, "Queued", aiv1alpha2.ModelPhasePreempted, rerouteToTwin, aiv1alpha2.ModelPhaseReady)
	f.drainFor.Store(1)
	before := replays("gte", replayReroute)

	rec := f.serve(t, "/v1/embeddings")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"served":"twin"`)
	assert.Equal(t, "gte->twin", rec.Header().Get(headerSwapReplayed))
	assert.EqualValues(t, 1, f.gteHits.Load())
	assert.EqualValues(t, 1, f.twinHits.Load())
	assert.Equal(t, "twin-served", f.twinModel.Load(), "the replay carries the equivalent's served name")
	assert.Equal(t, before+1, replays("gte", replayReroute))
}

func TestServeProxyFailsFastForAParkedLaneWithoutAnEquivalent(t *testing.T) {
	f := newSwapFixture(t, "Queued", aiv1alpha2.ModelPhasePreempted, nil, aiv1alpha2.ModelPhaseReady)
	f.drainFor.Store(1)
	before := replays("gte", replayNoLane)

	start := time.Now()
	rec := f.serve(t, "/v1/embeddings")

	assert.Less(t, time.Since(start), 300*time.Millisecond, "a parked lane is not waited for")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), runtimeDrainMarker)
	assert.Equal(t, "1", rec.Header().Get("Retry-After"))
	assert.Empty(t, rec.Header().Get(headerSwapReplayed))
	assert.Equal(t, before+1, replays("gte", replayNoLane))
}

func TestServeProxyFallsBackToTheEquivalentWhenTheLaneStaysDown(t *testing.T) {
	f := newSwapFixture(t, "Active", aiv1alpha2.ModelPhaseReady, rerouteToTwin, aiv1alpha2.ModelPhaseReady)
	f.drainFor.Store(1)
	f.healthStatus.Store(http.StatusServiceUnavailable)

	start := time.Now()
	rec := f.serve(t, "/v1/embeddings")

	assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond, "a draining lane is waited for first")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "gte->twin", rec.Header().Get(headerSwapReplayed))
	assert.EqualValues(t, 1, f.twinHits.Load())
}

func TestServeProxyReturnsTheDrainWhenNothingServesWithinTheBound(t *testing.T) {
	f := newSwapFixture(t, "Active", aiv1alpha2.ModelPhaseReady, rerouteToTwin, aiv1alpha2.ModelPhaseIdle)
	f.drainFor.Store(1)
	f.healthStatus.Store(http.StatusServiceUnavailable)
	before := replays("gte", replayNoLane)

	start := time.Now()
	rec := f.serve(t, "/v1/embeddings")

	assert.Less(t, time.Since(start), 2*time.Second, "bounded by FLEXINFER_PROXY_SWAP_REPLAY_WAIT")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), runtimeDrainMarker)
	assert.EqualValues(t, 0, f.twinHits.Load(), "an idle equivalent is never cold-started")
	assert.Equal(t, before+1, replays("gte", replayNoLane))
}

func TestServeProxyReplaysOnlyOnce(t *testing.T) {
	f := newSwapFixture(t, "Active", aiv1alpha2.ModelPhaseReady, nil, aiv1alpha2.ModelPhaseReady)
	f.drainFor.Store(10)
	before := replays("gte", replayFailed)

	rec := f.serve(t, "/v1/embeddings")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "the replay's own failure reaches the client")
	assert.Equal(t, "gte->gte", rec.Header().Get(headerSwapReplayed))
	assert.EqualValues(t, 2, f.gteHits.Load())
	assert.Equal(t, before+1, replays("gte", replayFailed))
}

func TestServeProxyPassesThroughAFailureWithoutASwap(t *testing.T) {
	f := newSwapFixture(t, "Active", aiv1alpha2.ModelPhaseReady, rerouteToTwin, aiv1alpha2.ModelPhaseReady)
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.gteHits.Add(1)
		http.Error(w, "upstream exploded", http.StatusBadGateway)
	}))
	t.Cleanup(errSrv.Close)
	f.p.directLoadTargets.Store("gte", errSrv.URL)

	rec := f.serve(t, "/v1/embeddings")

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "upstream exploded")
	assert.Empty(t, rec.Header().Get(headerSwapReplayed))
	assert.EqualValues(t, 1, f.gteHits.Load())
	assert.EqualValues(t, 0, f.twinHits.Load())
}

func TestServeProxyDoesNotHoldBackOtherPaths(t *testing.T) {
	f := newSwapFixture(t, "Active", aiv1alpha2.ModelPhaseReady, nil, aiv1alpha2.ModelPhaseReady)
	f.drainFor.Store(1)

	rec := f.serve(t, "/v1/chat/completions")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Empty(t, rec.Header().Get(headerSwapReplayed))
	assert.EqualValues(t, 1, f.gteHits.Load())
}

func TestServeProxySwapReplayCanBeTurnedOff(t *testing.T) {
	f := newSwapFixture(t, "Active", aiv1alpha2.ModelPhaseReady, nil, aiv1alpha2.ModelPhaseReady)
	f.drainFor.Store(1)
	t.Setenv(envSwapReplay, "false")

	rec := f.serve(t, "/v1/embeddings")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.EqualValues(t, 1, f.gteHits.Load())
}
