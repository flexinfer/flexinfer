package controllers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The runtime path observes cold-start and shared swap-in latency with
// serving_path=runtime once per transition to Ready, so S6b can compare it with the
// dedicated-Deployment path; steady Ready passes observe nothing.
func TestReconcileViaRuntime_ReadyTransitionObservesRuntimePathLatency(t *testing.T) {
	loadStarted := metav1.NewTime(time.Now().Add(-90 * time.Second))
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "workhorse-metrics", Namespace: "flexinfer-system", Generation: 2},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://org/model",
			GPU:     &aiv1alpha2.GPUSpec{Shared: "gfx1100-xtx"},
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseLoading,
			Conditions: []metav1.Condition{{
				Type: aiv1alpha2.ConditionModelReady, Status: metav1.ConditionFalse, Reason: aiv1alpha2.ReasonPreempted,
				LastTransitionTime: loadStarted, ObservedGeneration: 2,
			}},
			SharedGroup: &aiv1alpha2.SharedGroupStatus{GroupName: "gfx1100-xtx", State: "Active"},
		},
	}
	h := newRuntimeHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(RuntimeModelStatus{Name: model.Name, State: "Ready", Port: 8001})
	}, model)

	coldLabels := map[string]string{"model": model.Name, "namespace": model.Namespace, "serving_path": "runtime"}
	swapLabels := map[string]string{"model": model.Name, "group": "gfx1100-xtx", "serving_path": "runtime"}
	coldBefore := histogramSampleCount(t, "flexinfer_model_cold_start_duration_seconds", coldLabels)
	swapBefore := histogramSampleCount(t, "flexinfer_model_swap_duration_seconds", swapLabels)

	current, _ := h.reconcile(t, model)
	require.Equal(t, aiv1alpha2.ModelPhaseReady, current.Status.Phase)
	require.Equal(t, coldBefore+1, histogramSampleCount(t, "flexinfer_model_cold_start_duration_seconds", coldLabels))
	require.Equal(t, swapBefore+1, histogramSampleCount(t, "flexinfer_model_swap_duration_seconds", swapLabels))

	// A steady Ready pass must not re-observe.
	_, _ = h.reconcile(t, current)
	require.Equal(t, coldBefore+1, histogramSampleCount(t, "flexinfer_model_cold_start_duration_seconds", coldLabels))
	require.Equal(t, swapBefore+1, histogramSampleCount(t, "flexinfer_model_swap_duration_seconds", swapLabels))
}
