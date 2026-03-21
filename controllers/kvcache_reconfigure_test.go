/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func kvCacheTestSetup(t *testing.T, utilization string, objects ...runtime.Object) (*ModelReconciler, *aiv1alpha2.Model) {
	t.Helper()

	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add flexinfer scheme: %v", err)
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-1",
			Annotations: map[string]string{
				AnnotationKVCacheUsage: utilization,
			},
		},
	}

	configJSON, _ := json.Marshal(map[string]interface{}{
		"maxNumSeqs": 8,
	})

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "test-model", Namespace: "default"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/model",
			Config:  &apiextensionsv1.JSON{Raw: configJSON},
			KVCache: &aiv1alpha2.KVCacheSpec{
				PressurePolicy: aiv1alpha2.KVCachePressurePolicyReconfigure,
				HighWatermark:  resourceQuantity("0.85"),
				LowWatermark:   resourceQuantity("0.60"),
			},
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseReady,
			GPU: &aiv1alpha2.GPUStatus{
				Node: "gpu-node-1",
			},
		},
	}

	allObjects := append([]runtime.Object{node, model}, objects...)

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		WithRuntimeObjects(allObjects...).
		Build()

	recorder := record.NewFakeRecorder(10)
	r := &ModelReconciler{
		Client:   fakeClient,
		Scheme:   s,
		Recorder: recorder,
	}

	return r, model
}

func resourceQuantity(val string) *resource.Quantity {
	q := resource.MustParse(val)
	return &q
}

func TestKVCacheReconfigure_TriggersOnHighPressure(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.90")

	r.reconcileKVCachePressure(context.Background(), model)

	if !model.Status.KVCache.Reconfigured {
		t.Fatal("expected Reconfigured=true after high pressure")
	}
	if model.Status.KVCache.OriginalMaxNumSeqs == nil || *model.Status.KVCache.OriginalMaxNumSeqs != 8 {
		t.Fatalf("expected OriginalMaxNumSeqs=8, got %v", model.Status.KVCache.OriginalMaxNumSeqs)
	}
	if model.Status.KVCache.ReconfiguredMaxNumSeqs == nil || *model.Status.KVCache.ReconfiguredMaxNumSeqs != 4 {
		t.Fatalf("expected ReconfiguredMaxNumSeqs=4 (50%% of 8), got %v", model.Status.KVCache.ReconfiguredMaxNumSeqs)
	}
	if model.Status.KVCache.ReconfiguredAt == nil {
		t.Fatal("expected ReconfiguredAt to be set")
	}
	if !model.Status.KVCache.Pressure {
		t.Fatal("expected Pressure=true")
	}
}

func TestKVCacheReconfigure_NoDoubleReduce(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.92")

	// First trigger
	r.reconcileKVCachePressure(context.Background(), model)
	if !model.Status.KVCache.Reconfigured {
		t.Fatal("expected Reconfigured=true")
	}
	reducedSeqs := *model.Status.KVCache.ReconfiguredMaxNumSeqs

	// Second trigger at higher pressure — should NOT reduce further
	r.reconcileKVCachePressure(context.Background(), model)
	if *model.Status.KVCache.ReconfiguredMaxNumSeqs != reducedSeqs {
		t.Fatalf("expected ReconfiguredMaxNumSeqs unchanged at %d, got %d",
			reducedSeqs, *model.Status.KVCache.ReconfiguredMaxNumSeqs)
	}
	if model.Status.KVCache.LastAction != "ReconfigureActive" {
		t.Fatalf("expected LastAction=ReconfigureActive, got %s", model.Status.KVCache.LastAction)
	}
}

func TestKVCacheReconfigure_RestoresAfterCooldown(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.50") // below low watermark (0.60)

	// Simulate a previous reconfigure with expired cooldown
	past := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	originalSeqs := int32(8)
	reducedSeqs := int32(4)
	model.Status.KVCache = &aiv1alpha2.KVCacheStatus{
		Reconfigured:           true,
		ReconfiguredAt:         &past,
		OriginalMaxNumSeqs:     &originalSeqs,
		ReconfiguredMaxNumSeqs: &reducedSeqs,
		Pressure:               true,
		LastAction:             "Reconfigured:maxNumSeqs=8->4",
	}

	r.reconcileKVCachePressure(context.Background(), model)

	if model.Status.KVCache.Reconfigured {
		t.Fatal("expected Reconfigured=false after restore")
	}
	if model.Status.KVCache.ReconfiguredMaxNumSeqs != nil {
		t.Fatal("expected ReconfiguredMaxNumSeqs=nil after restore")
	}
	if model.Status.KVCache.OriginalMaxNumSeqs != nil {
		t.Fatal("expected OriginalMaxNumSeqs=nil after restore")
	}
	if model.Status.KVCache.LastAction != "Restored" {
		t.Fatalf("expected LastAction=Restored, got %s", model.Status.KVCache.LastAction)
	}
	if model.Status.KVCache.Pressure {
		t.Fatal("expected Pressure=false after restore")
	}
}

func TestKVCacheReconfigure_NoRestoreBeforeCooldown(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.50") // below low watermark

	// Simulate a recent reconfigure (cooldown not elapsed)
	recent := metav1.NewTime(time.Now().Add(-1 * time.Minute)) // only 1 min ago, default cooldown is 5m
	originalSeqs := int32(8)
	reducedSeqs := int32(4)
	model.Status.KVCache = &aiv1alpha2.KVCacheStatus{
		Reconfigured:           true,
		ReconfiguredAt:         &recent,
		OriginalMaxNumSeqs:     &originalSeqs,
		ReconfiguredMaxNumSeqs: &reducedSeqs,
		Pressure:               true,
	}

	r.reconcileKVCachePressure(context.Background(), model)

	if !model.Status.KVCache.Reconfigured {
		t.Fatal("expected Reconfigured=true (cooldown not elapsed)")
	}
	if model.Status.KVCache.ReconfiguredMaxNumSeqs == nil || *model.Status.KVCache.ReconfiguredMaxNumSeqs != 4 {
		t.Fatal("expected ReconfiguredMaxNumSeqs=4 (unchanged during cooldown)")
	}
}

func TestKVCacheReconfigure_NoPressureBelowWatermark(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.70") // between low (0.60) and high (0.85)

	r.reconcileKVCachePressure(context.Background(), model)

	if model.Status.KVCache.Reconfigured {
		t.Fatal("expected Reconfigured=false when below high watermark")
	}
	if model.Status.KVCache.Pressure {
		t.Fatal("expected Pressure=false when below high watermark")
	}
}

func TestKVCacheReconfigure_ObservePolicyDoesNotReconfigure(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.90")
	model.Spec.KVCache.PressurePolicy = aiv1alpha2.KVCachePressurePolicyObserve

	r.reconcileKVCachePressure(context.Background(), model)

	if model.Status.KVCache.Reconfigured {
		t.Fatal("expected Reconfigured=false with Observe policy")
	}
	if model.Status.KVCache.LastAction != "Observed" {
		t.Fatalf("expected LastAction=Observed, got %s", model.Status.KVCache.LastAction)
	}
}

func TestKVCacheReconfigure_MinimumOneSequence(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.95")

	// Set maxNumSeqs=1 — reducing by 50% should clamp to 1
	configJSON, _ := json.Marshal(map[string]interface{}{
		"maxNumSeqs": 1,
	})
	model.Spec.Config = &apiextensionsv1.JSON{Raw: configJSON}

	r.reconcileKVCachePressure(context.Background(), model)

	if !model.Status.KVCache.Reconfigured {
		t.Fatal("expected Reconfigured=true")
	}
	if model.Status.KVCache.ReconfiguredMaxNumSeqs == nil || *model.Status.KVCache.ReconfiguredMaxNumSeqs != 1 {
		t.Fatalf("expected ReconfiguredMaxNumSeqs=1 (minimum), got %v", model.Status.KVCache.ReconfiguredMaxNumSeqs)
	}
}

// --- Evict Policy Tests ---

func TestKVCacheEvict_TriggersOnHighPressure(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.90")
	model.Spec.KVCache.PressurePolicy = aiv1alpha2.KVCachePressurePolicyEvict

	r.reconcileKVCachePressure(context.Background(), model)

	if !model.Status.KVCache.Evicted {
		t.Fatal("expected Evicted=true after high pressure")
	}
	if model.Status.KVCache.EvictedAt == nil {
		t.Fatal("expected EvictedAt to be set")
	}
	if model.Status.KVCache.LastAction != "Evicted" {
		t.Fatalf("expected LastAction=Evicted, got %s", model.Status.KVCache.LastAction)
	}
}

func TestKVCacheEvict_DesiredReplicasReturnsZero(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.90")
	model.Spec.KVCache.PressurePolicy = aiv1alpha2.KVCachePressurePolicyEvict

	// Trigger eviction
	r.reconcileKVCachePressure(context.Background(), model)

	// desiredReplicas should return 0 while evicted
	replicas := r.desiredReplicas(model, nil)
	if replicas != 0 {
		t.Fatalf("expected desiredReplicas=0 while evicted, got %d", replicas)
	}
}

func TestKVCacheEvict_NoDoubleEvict(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.95")
	model.Spec.KVCache.PressurePolicy = aiv1alpha2.KVCachePressurePolicyEvict

	r.reconcileKVCachePressure(context.Background(), model)
	evictedAt := model.Status.KVCache.EvictedAt.Time

	// Second trigger — should not re-evict
	r.reconcileKVCachePressure(context.Background(), model)
	if model.Status.KVCache.EvictedAt.Time != evictedAt {
		t.Fatal("expected EvictedAt unchanged on second trigger")
	}
	if model.Status.KVCache.LastAction != "EvictActive" {
		t.Fatalf("expected LastAction=EvictActive, got %s", model.Status.KVCache.LastAction)
	}
}

func TestKVCacheEvict_RestoresAfterCooldown(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.50") // doesn't matter for evict restore

	past := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	model.Status.KVCache = &aiv1alpha2.KVCacheStatus{
		Evicted:   true,
		EvictedAt: &past,
		Pressure:  true,
	}

	r.reconcileKVCachePressure(context.Background(), model)

	if model.Status.KVCache.Evicted {
		t.Fatal("expected Evicted=false after cooldown restore")
	}
	if model.Status.KVCache.EvictedAt != nil {
		t.Fatal("expected EvictedAt=nil after restore")
	}
	if model.Status.KVCache.LastAction != "EvictRestored" {
		t.Fatalf("expected LastAction=EvictRestored, got %s", model.Status.KVCache.LastAction)
	}
}

func TestKVCacheEvict_NoRestoreBeforeCooldown(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.50")

	recent := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	model.Status.KVCache = &aiv1alpha2.KVCacheStatus{
		Evicted:   true,
		EvictedAt: &recent,
		Pressure:  true,
	}

	r.reconcileKVCachePressure(context.Background(), model)

	if !model.Status.KVCache.Evicted {
		t.Fatal("expected Evicted=true (cooldown not elapsed)")
	}
}
