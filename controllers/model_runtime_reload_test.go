package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestRuntimeSpecReloadReason(t *testing.T) {
	model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "lane", Generation: 5}}
	for _, tc := range []struct {
		name       string
		status     *RuntimeModelStatus
		specHash   string
		wantReload bool
	}{
		{"legacy runtime reports no hash", &RuntimeModelStatus{}, "new", false},
		{"hash drift without a spec edit", &RuntimeModelStatus{Generation: 5, SpecHash: "old"}, "new", false},
		{"spec edit outside the launch", &RuntimeModelStatus{Generation: 4, SpecHash: "same"}, "same", false},
		{"controller cache older than the runtime", &RuntimeModelStatus{Generation: 6, SpecHash: "old"}, "new", false},
		{"spec edit that changes the launch", &RuntimeModelStatus{Generation: 4, SpecHash: "old"}, "new", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason := runtimeSpecReloadReason(model, tc.status, tc.specHash)
			require.Equal(t, tc.wantReload, reason != "", reason)
		})
	}
}

// fakeReloadRuntime serves the model health endpoint from a mutable status and
// records every load and unload request.
type fakeReloadRuntime struct {
	mu       sync.Mutex
	health   RuntimeModelStatus
	busy     bool
	loads    []pkgrt.LoadPayload
	unloaded bool
}

func (f *fakeReloadRuntime) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(f.health)
	case http.MethodPost:
		var p pkgrt.LoadPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		f.loads = append(f.loads, p)
		if f.busy {
			f.health.Draining = true
			http.Error(w, "accepted requests still running", http.StatusConflict)
			return
		}
		f.health = RuntimeModelStatus{Name: f.health.Name, State: "Loading", Generation: p.Generation, SpecHash: p.SpecHash}
		w.WriteHeader(http.StatusAccepted)
	case http.MethodDelete:
		f.unloaded = true
		w.WriteHeader(http.StatusOK)
	}
}

func reloadTestModel(generation int64) *aiv1alpha2.Model {
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "rewrite-lane", Namespace: "flexinfer-system", Generation: generation},
		Spec:       aiv1alpha2.ModelSpec{Backend: "llamacpp", Source: "HF://org/model"},
		Status: aiv1alpha2.ModelStatus{
			Phase:    aiv1alpha2.ModelPhaseReady,
			Endpoint: "http://rewrite-lane.flexinfer-system.svc:8000",
			Conditions: []metav1.Condition{{
				Type: aiv1alpha2.ConditionModelReady, Status: metav1.ConditionTrue, Reason: "RuntimeReady",
				Message: "Model ready via runtime", ObservedGeneration: generation, LastTransitionTime: metav1.Now(),
			}},
		},
	}
}

// renderedSpecHash is the hash the controller would send for model now.
func renderedSpecHash(t *testing.T, h *runtimeHarness, model *aiv1alpha2.Model) string {
	t.Helper()
	b, ok := backend.Get(model.Spec.Backend)
	require.True(t, ok)
	p, err := h.r.buildRuntimeLoadPayload(context.Background(), model, b, backend.GPUVendorAMD, "gfx1100")
	require.NoError(t, err)
	return p.SpecHash
}

func TestReconcileViaRuntime_SpecEditReloadsInPlaceThroughDrain(t *testing.T) {
	model := reloadTestModel(2)
	fake := &fakeReloadRuntime{busy: true}
	h := newRuntimeHarness(t, fake.handle, model)
	fake.health = RuntimeModelStatus{Name: model.Name, State: "Ready", Port: 8000, Generation: 1, SpecHash: "loaded-gen-1"}

	// Pass 1: the edit is detected; accepted requests are still running.
	current, result := h.reconcile(t, model)
	require.Equal(t, requeueShort, result.RequeueAfter)
	require.Len(t, fake.loads, 1)
	require.True(t, fake.loads[0].Reload)
	require.Equal(t, int64(2), fake.loads[0].Generation)
	require.Equal(t, renderedSpecHash(t, h, model), fake.loads[0].SpecHash)
	require.Equal(t, aiv1alpha2.ModelPhaseLoading, current.Status.Phase, "the proxy queues while the lane reloads")
	require.Equal(t, aiv1alpha2.LoadingSubstageInitializing, current.Status.LoadingSubstage)
	ready := modelCondition(current.Status.Conditions, aiv1alpha2.ConditionModelReady)
	require.Equal(t, ReasonRuntimeReloading, ready.Reason)
	require.Empty(t, current.Status.Endpoint)
	require.Contains(t, eventReasons(h), EventRuntimeReload)

	// Pass 2: still draining. The reload is retried, never turned into an unload.
	current, _ = h.reconcile(t, current)
	require.Len(t, fake.loads, 2)
	require.True(t, fake.loads[1].Reload)
	require.False(t, fake.unloaded)
	require.Equal(t, ReasonRuntimeReloading, modelCondition(current.Status.Conditions, aiv1alpha2.ConditionModelReady).Reason)

	// Pass 3: drained, the runtime accepts and restarts the lane.
	fake.mu.Lock()
	fake.busy = false
	fake.mu.Unlock()
	current, _ = h.reconcile(t, current)
	require.Len(t, fake.loads, 3)
	require.False(t, fake.unloaded)
	require.Equal(t, aiv1alpha2.ModelPhaseLoading, current.Status.Phase)

	// The restarted instance reports the new generation; Ready, no further reload.
	fake.mu.Lock()
	fake.health.State = "Ready"
	fake.health.Port = 8000
	fake.mu.Unlock()
	current, _ = h.reconcile(t, current)
	require.Equal(t, aiv1alpha2.ModelPhaseReady, current.Status.Phase)
	_, _ = h.reconcile(t, current)
	require.Len(t, fake.loads, 3)
	require.Equal(t, 1, countReason(h, EventRuntimeReload))
}

func TestReconcileViaRuntime_SpecReloadGuards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(m *aiv1alpha2.Model, health *RuntimeModelStatus, renderedHash string)
	}{
		{"legacy runtime without a spec hash is adopted", func(_ *aiv1alpha2.Model, health *RuntimeModelStatus, _ string) {
			health.Generation, health.SpecHash = 0, ""
		}},
		{"spec edit that renders the same launch", func(_ *aiv1alpha2.Model, health *RuntimeModelStatus, renderedHash string) {
			health.SpecHash = renderedHash
		}},
		{"shared member that is not Active", func(m *aiv1alpha2.Model, _ *RuntimeModelStatus, _ string) {
			m.Spec.GPU = &aiv1alpha2.GPUSpec{Shared: "gfx1100-xtx"}
			m.Status.SharedGroup = &aiv1alpha2.SharedGroupStatus{GroupName: "gfx1100-xtx", State: "Queued"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := reloadTestModel(2)
			fake := &fakeReloadRuntime{}
			h := newRuntimeHarness(t, fake.handle, model)
			health := RuntimeModelStatus{Name: model.Name, State: "Ready", Port: 8000, Generation: 1, SpecHash: "loaded-gen-1"}
			tc.mutate(model, &health, renderedSpecHash(t, h, model))
			require.NoError(t, h.cl.Update(context.Background(), model))
			require.NoError(t, h.cl.Status().Update(context.Background(), model))
			fake.health = health
			current := &aiv1alpha2.Model{}
			require.NoError(t, h.cl.Get(context.Background(), client.ObjectKeyFromObject(model), current))

			current, _ = h.reconcile(t, current)
			require.Empty(t, fake.loads, "no reload may be sent")
			require.Equal(t, aiv1alpha2.ModelPhaseReady, current.Status.Phase)
			require.NotContains(t, eventReasons(h), EventRuntimeReload)
		})
	}
}

// A runtime that rejects the reload outright keeps the old instance serving.
func TestReconcileViaRuntime_RejectedReloadKeepsServing(t *testing.T) {
	model := reloadTestModel(2)
	h := newRuntimeHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.Error(w, "backend \"llamacpp\" does not support GPU vendor", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(RuntimeModelStatus{Name: model.Name, State: "Ready", Port: 8000, Generation: 1, SpecHash: "loaded-gen-1"})
	}, model)
	current, _ := h.reconcile(t, model)
	require.Equal(t, aiv1alpha2.ModelPhaseReady, current.Status.Phase)
	require.NotEmpty(t, current.Status.Endpoint)
	_, _ = h.reconcile(t, current)
	require.Equal(t, 1, countReason(h, "RuntimeReloadFailed"), "the rejection warning is throttled")
	require.Zero(t, countReason(h, EventRuntimeReload))
	for _, e := range h.recorder.Events {
		if e.Reason == "RuntimeReloadFailed" {
			require.Equal(t, corev1.EventTypeWarning, e.EventType)
		}
	}
}

func eventReasons(h *runtimeHarness) []string {
	out := make([]string, 0, len(h.recorder.Events))
	for _, e := range h.recorder.Events {
		out = append(out, e.Reason)
	}
	return out
}

func countReason(h *runtimeHarness, reason string) int {
	n := 0
	for _, e := range h.recorder.Events {
		if e.Reason == reason {
			n++
		}
	}
	return n
}
