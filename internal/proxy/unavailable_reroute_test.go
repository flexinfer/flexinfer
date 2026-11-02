package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/benchmarkconfig"
	"github.com/flexinfer/flexinfer/pkg/constants"
)

// rerouteModel is a Model with a served name, gpu.priority and phase.
func rerouteModel(name string, priority int32, phase aiv1alpha2.ModelPhase, ann map[string]string) *aiv1alpha2.Model {
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Annotations: ann},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/" + name,
			Config:  &apiextensionsv1.JSON{Raw: []byte(`{"servedModelName":"` + name + `-served"}`)},
			GPU:     &aiv1alpha2.GPUSpec{Priority: &priority},
		},
		Status: aiv1alpha2.ModelStatus{Phase: phase},
	}
}

// queuedBehind marks m as a queued shared-group member preempted by holder.
func queuedBehind(m *aiv1alpha2.Model, holder string) *aiv1alpha2.Model {
	m.Status.Phase = aiv1alpha2.ModelPhasePreempted
	m.Status.SharedGroup = &aiv1alpha2.SharedGroupStatus{GroupName: "g", State: "Queued", PreemptedBy: holder}
	return m
}

// rerouteFixture is the 5930k/XTX workhorse pair in miniature: "home" names
// "twin" as its unavailable-reroute lane, and twin is served by an httptest
// upstream through the direct-load fast path. A request for home that
// reaches twin's upstream was rerouted; one that touches demand on the
// activator took home's own cold-start path.
type rerouteFixture struct {
	p         *Proxy
	act       *countingActivator
	twinHits  atomic.Int32
	twinModel atomic.Value
}

func newRerouteFixture(t *testing.T, objs ...client.Object) *rerouteFixture {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(objs...).WithStatusSubresource(objs...).Build()

	f := &rerouteFixture{act: &countingActivator{}}
	twin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.twinHits.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.twinModel.Store(body["model"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(twin.Close)

	f.p = newGateTestProxyWithClient(t, f.act, k8sClient)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f.p.ctx, f.p.cancel = ctx, cancel
	f.p.directLoadTargets.Store("twin", twin.URL)
	return f
}

func (f *rerouteFixture) serve(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.p.handleRequest(rec, req)
	return rec
}

var rerouteToTwin = map[string]string{annotationUnavailableRerouteTo: "twin"}

func TestUnavailableRerouteServesAFailedLaneOnItsReadyTwin(t *testing.T) {
	home := rerouteModel("home", 900, aiv1alpha2.ModelPhaseFailed, rerouteToTwin)
	f := newRerouteFixture(t, home, rerouteModel("twin", 300, aiv1alpha2.ModelPhaseReady, nil))
	before := testutil.ToFloat64(unavailableRerouteTotal.WithLabelValues("home", "twin", unavailableFailed, "rerouted"))

	rec := f.serve(t, chatReq("home"))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"ok"`)
	assert.Equal(t, "home->twin", rec.Header().Get(headerUnavailableReroute))
	assert.EqualValues(t, 1, f.twinHits.Load())
	assert.Equal(t, "twin-served", f.twinModel.Load(), "the request carries the twin's served name")
	assert.Equal(t, 0, f.act.touches, "a rerouted request never enters the home lane's cold start")
	assert.Equal(t, before+1, testutil.ToFloat64(unavailableRerouteTotal.WithLabelValues("home", "twin", unavailableFailed, "rerouted")))
}

func TestUnavailableRerouteServesAParkedLaneOnItsReadyTwin(t *testing.T) {
	for name, holder := range map[string]string{
		"behind a warm primary": aiv1alpha2.PreemptedByPrimaryPrefix + "creative",
		"under a GPU lease":     "gpu-lease/train",
		"under a gaming hold":   "gaming/session",
	} {
		t.Run(name, func(t *testing.T) {
			home := queuedBehind(rerouteModel("home", 300, "", rerouteToTwin), holder)
			f := newRerouteFixture(t, home, rerouteModel("twin", 900, aiv1alpha2.ModelPhaseReady, nil))

			rec := f.serve(t, chatReq("home"))

			assert.Equal(t, http.StatusOK, rec.Code, "the twin serves instead of the parked 503")
			assert.Equal(t, "home->twin", rec.Header().Get(headerUnavailableReroute))
			assert.EqualValues(t, 1, f.twinHits.Load())
		})
	}
}

func TestUnavailableRerouteServesALanePreemptedByAHigherPriorityHolder(t *testing.T) {
	// The XTX warm lane (300) behind a creative render (500): it would queue
	// until the render idles out, so the 5930k workhorse serves instead.
	home := queuedBehind(rerouteModel("home", 300, "", rerouteToTwin), "creative")
	f := newRerouteFixture(t, home,
		rerouteModel("creative", 500, aiv1alpha2.ModelPhaseReady, nil),
		rerouteModel("twin", 900, aiv1alpha2.ModelPhaseReady, nil))
	before := testutil.ToFloat64(unavailableRerouteTotal.WithLabelValues("home", "twin", unavailablePreempted, "rerouted"))

	rec := f.serve(t, chatReq("home"))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.EqualValues(t, 1, f.twinHits.Load())
	assert.Equal(t, 0, f.act.touches)
	assert.Equal(t, before+1, testutil.ToFloat64(unavailableRerouteTotal.WithLabelValues("home", "twin", unavailablePreempted, "rerouted")))
}

func TestUnavailableRerouteLeavesAPromotableLaneToItsColdStart(t *testing.T) {
	for name, holderPriority := range map[string]int32{"equal": 300, "lower": 200} {
		t.Run(name, func(t *testing.T) {
			// Demand swaps an equal- or lower-priority holder out, so the
			// lane's own cold start is the right path.
			home := queuedBehind(rerouteModel("home", 300, "", rerouteToTwin), "holder")
			f := newRerouteFixture(t, home,
				rerouteModel("holder", holderPriority, aiv1alpha2.ModelPhaseReady, nil),
				rerouteModel("twin", 900, aiv1alpha2.ModelPhaseReady, nil))

			rec := f.serve(t, chatReq("home"))

			assert.Empty(t, rec.Header().Get(headerUnavailableReroute))
			assert.EqualValues(t, 0, f.twinHits.Load())
			assert.Positive(t, f.act.touches, "the request entered home's cold start")
		})
	}
}

func TestUnavailableRerouteTakesTheNormalPathWhenTheTwinIsNotReady(t *testing.T) {
	// Both 27B lanes down: the home lane's own cold start fails fast as before
	// and records the demand a failover-for stand-in inherits.
	home := rerouteModel("home", 900, aiv1alpha2.ModelPhaseFailed, rerouteToTwin)
	twin := queuedBehind(rerouteModel("twin", 300, "", nil), "creative")
	f := newRerouteFixture(t, home, twin, rerouteModel("creative", 500, aiv1alpha2.ModelPhaseReady, nil))
	before := testutil.ToFloat64(unavailableRerouteTotal.WithLabelValues("home", "twin", unavailableFailed, "target_unavailable"))

	rec := f.serve(t, chatReq("home"))

	assert.Empty(t, rec.Header().Get(headerUnavailableReroute))
	assert.EqualValues(t, 0, f.twinHits.Load(), "a twin that is not Ready is never cold-started from here")
	assert.Positive(t, f.act.touches, "demand lands on the requested lane")
	assert.Equal(t, before+1, testutil.ToFloat64(unavailableRerouteTotal.WithLabelValues("home", "twin", unavailableFailed, "target_unavailable")))
}

func TestUnavailableRerouteRunsUnderTheTwinLane(t *testing.T) {
	budget := map[string]string{"flexinfer.ai/lane-first-byte-budget": "450s"}
	home := rerouteModel("home", 900, aiv1alpha2.ModelPhaseFailed, rerouteToTwin)
	f := newRerouteFixture(t, home, rerouteModel("twin", 300, aiv1alpha2.ModelPhaseReady, budget))
	f.p.laneConfig.LaneBreakerThreshold = 3
	f.p.laneConfig.LaneFirstByteBudget = 2 * time.Minute

	rec := f.serve(t, chatReq("home"))

	assert.Equal(t, http.StatusOK, rec.Code)
	lane := f.p.lane("twin")
	lane.mu.Lock()
	defer lane.mu.Unlock()
	assert.Equal(t, 450*time.Second, lane.budget, "the reroute runs under the twin's annotated budget")
}

func TestUnavailableRerouteSkipsATwinWithAnOpenBreaker(t *testing.T) {
	home := rerouteModel("home", 900, aiv1alpha2.ModelPhaseFailed, rerouteToTwin)
	f := newRerouteFixture(t, home, rerouteModel("twin", 300, aiv1alpha2.ModelPhaseReady, nil))
	f.p.laneConfig.LaneBreakerThreshold = 3
	lane := f.p.lane("twin")
	lane.mu.Lock()
	lane.open, lane.until = true, time.Now().Add(time.Minute)
	lane.mu.Unlock()

	rec := f.serve(t, chatReq("home"))

	assert.Empty(t, rec.Header().Get(headerUnavailableReroute))
	assert.EqualValues(t, 0, f.twinHits.Load(), "a tripped twin is not handed failover traffic")
	assert.Positive(t, f.act.touches)
}

func TestUnavailableRerouteSkipsATwinThatDoesNotServeThePath(t *testing.T) {
	home := rerouteModel("home", 900, aiv1alpha2.ModelPhaseFailed, rerouteToTwin)
	twin := rerouteModel("twin", 300, aiv1alpha2.ModelPhaseReady,
		map[string]string{constants.AnnotationServePaths: "/v1/audio/transcriptions"})
	f := newRerouteFixture(t, home, twin)

	rec := f.serve(t, chatReq("home"))

	assert.Empty(t, rec.Header().Get(headerUnavailableReroute))
	assert.EqualValues(t, 0, f.twinHits.Load())
}

func TestUnavailableRerouteNeverMovesBackgroundWork(t *testing.T) {
	// Background work (benchmarks, evals) measures the lane it names.
	home := rerouteModel("home", 900, aiv1alpha2.ModelPhaseFailed, rerouteToTwin)
	f := newRerouteFixture(t, home, rerouteModel("twin", 300, aiv1alpha2.ModelPhaseReady, nil))
	req := chatReq("home")
	req.Header.Set(benchmarkconfig.HeaderInternalWorkloadClass, benchmarkconfig.WorkloadClassBackground)

	rec := f.serve(t, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.EqualValues(t, 0, f.twinHits.Load())
}

func TestUnavailableRerouteWithoutTheAnnotationIsUnchanged(t *testing.T) {
	home := rerouteModel("home", 900, aiv1alpha2.ModelPhaseFailed, nil)
	f := newRerouteFixture(t, home, rerouteModel("twin", 300, aiv1alpha2.ModelPhaseReady, nil))

	rec := f.serve(t, chatReq("home"))

	assert.Empty(t, rec.Header().Get(headerUnavailableReroute))
	assert.EqualValues(t, 0, f.twinHits.Load())
	assert.Positive(t, f.act.touches, "the Failed lane takes its cold start as before")
}

func TestUnavailableRerouteHappensOnce(t *testing.T) {
	// The twin's own reroute lane is never consulted: it is served directly.
	home := rerouteModel("home", 900, aiv1alpha2.ModelPhaseFailed, rerouteToTwin)
	twin := rerouteModel("twin", 300, aiv1alpha2.ModelPhaseReady, map[string]string{annotationUnavailableRerouteTo: "home"})
	f := newRerouteFixture(t, home, twin)

	rec := f.serve(t, chatReq("home"))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "home->twin", rec.Header().Get(headerUnavailableReroute))
	assert.EqualValues(t, 1, f.twinHits.Load())
}

func TestUnavailableReason(t *testing.T) {
	p := &Proxy{client: newPolicyTestClient(t, rerouteModel("high", 500, aiv1alpha2.ModelPhaseReady, nil)), namespace: "default"}
	ctx := context.Background()

	assert.Equal(t, unavailableFailed, p.unavailableReason(ctx, rerouteModel("m", 300, aiv1alpha2.ModelPhaseFailed, nil)))
	assert.Equal(t, unavailablePreempted, p.unavailableReason(ctx, queuedBehind(rerouteModel("m", 300, "", nil), "high")))
	assert.Equal(t, unavailableParked, p.unavailableReason(ctx, queuedBehind(rerouteModel("m", 300, "", nil), "primary/high")))
	assert.Empty(t, p.unavailableReason(ctx, queuedBehind(rerouteModel("m", 600, "", nil), "high")), "outranks its holder")
	assert.Empty(t, p.unavailableReason(ctx, queuedBehind(rerouteModel("m", 300, "", nil), "gone")), "holder not found")
	assert.Empty(t, p.unavailableReason(ctx, rerouteModel("m", 300, aiv1alpha2.ModelPhaseIdle, nil)), "scaled to zero")
	active := queuedBehind(rerouteModel("m", 300, "", nil), "high")
	active.Status.SharedGroup.State = "Active"
	assert.Empty(t, p.unavailableReason(ctx, active), "elected and loading")
}
