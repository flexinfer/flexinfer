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
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/metrics"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
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

	configJSON, _ := json.Marshal(map[string]any{
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
	if model.Status.KVCache.OriginalConfig == nil {
		t.Fatal("expected OriginalConfig to be set")
	}
	var original map[string]int32
	if err := json.Unmarshal(model.Status.KVCache.OriginalConfig.Raw, &original); err != nil {
		t.Fatalf("failed to unmarshal OriginalConfig: %v", err)
	}
	if original["maxNumSeqs"] != 8 {
		t.Fatalf("expected OriginalConfig.maxNumSeqs=8, got %d", original["maxNumSeqs"])
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
	originalLen := int32(8192)
	reducedLen := int32(4096)
	model.Status.KVCache = &aiv1alpha2.KVCacheStatus{
		Reconfigured:            true,
		ReconfiguredAt:          &past,
		OriginalMaxNumSeqs:      &originalSeqs,
		ReconfiguredMaxNumSeqs:  &reducedSeqs,
		OriginalMaxModelLen:     &originalLen,
		ReconfiguredMaxModelLen: &reducedLen,
		Pressure:                true,
		LastAction:              "Reconfigured:maxNumSeqs=8->4,maxModelLen=8192->4096",
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
	if model.Status.KVCache.ReconfiguredMaxModelLen != nil {
		t.Fatal("expected ReconfiguredMaxModelLen=nil after restore")
	}
	if model.Status.KVCache.OriginalMaxModelLen != nil {
		t.Fatal("expected OriginalMaxModelLen=nil after restore")
	}
	if model.Status.KVCache.OriginalConfig != nil {
		t.Fatal("expected OriginalConfig=nil after restore")
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
	configJSON, _ := json.Marshal(map[string]any{
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

func TestKVCacheReconfigure_ReduceMaxLenStrategy(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.95")
	model.Spec.KVCache.ReconfigureStrategy = aiv1alpha2.KVCacheReconfigureStrategyReduceMaxLen

	configJSON, _ := json.Marshal(map[string]any{
		"maxNumSeqs":  8,
		"maxModelLen": 16384,
	})
	model.Spec.Config = &apiextensionsv1.JSON{Raw: configJSON}

	r.reconcileKVCachePressure(context.Background(), model)

	if !model.Status.KVCache.Reconfigured {
		t.Fatal("expected Reconfigured=true")
	}
	if model.Status.KVCache.ReconfiguredMaxNumSeqs != nil {
		t.Fatalf("expected maxNumSeqs unchanged for ReduceMaxLen, got %v", *model.Status.KVCache.ReconfiguredMaxNumSeqs)
	}
	if model.Status.KVCache.OriginalMaxModelLen == nil || *model.Status.KVCache.OriginalMaxModelLen != 16384 {
		t.Fatalf("expected OriginalMaxModelLen=16384, got %v", model.Status.KVCache.OriginalMaxModelLen)
	}
	if model.Status.KVCache.ReconfiguredMaxModelLen == nil || *model.Status.KVCache.ReconfiguredMaxModelLen != 8192 {
		t.Fatalf("expected ReconfiguredMaxModelLen=8192, got %v", model.Status.KVCache.ReconfiguredMaxModelLen)
	}
	if model.Status.KVCache.LastAction != "Reconfigured:maxModelLen=16384->8192" {
		t.Fatalf("unexpected LastAction: %s", model.Status.KVCache.LastAction)
	}
}

func TestKVCacheReconfigure_BothStrategy(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.95")
	model.Spec.KVCache.ReconfigureStrategy = aiv1alpha2.KVCacheReconfigureStrategyBoth

	configJSON, _ := json.Marshal(map[string]any{
		"maxNumSeqs":  8,
		"maxModelLen": 8192,
	})
	model.Spec.Config = &apiextensionsv1.JSON{Raw: configJSON}

	r.reconcileKVCachePressure(context.Background(), model)

	if model.Status.KVCache.ReconfiguredMaxNumSeqs == nil || *model.Status.KVCache.ReconfiguredMaxNumSeqs != 4 {
		t.Fatalf("expected ReconfiguredMaxNumSeqs=4, got %v", model.Status.KVCache.ReconfiguredMaxNumSeqs)
	}
	if model.Status.KVCache.ReconfiguredMaxModelLen == nil || *model.Status.KVCache.ReconfiguredMaxModelLen != 4096 {
		t.Fatalf("expected ReconfiguredMaxModelLen=4096, got %v", model.Status.KVCache.ReconfiguredMaxModelLen)
	}
	if model.Status.KVCache.LastAction != "Reconfigured:maxNumSeqs=8->4,maxModelLen=8192->4096" {
		t.Fatalf("unexpected LastAction: %s", model.Status.KVCache.LastAction)
	}
	var original map[string]int32
	if err := json.Unmarshal(model.Status.KVCache.OriginalConfig.Raw, &original); err != nil {
		t.Fatalf("failed to unmarshal OriginalConfig: %v", err)
	}
	if original["maxNumSeqs"] != 8 || original["maxModelLen"] != 8192 {
		t.Fatalf("unexpected OriginalConfig: %v", original)
	}
}

func TestKVCacheReconfigure_MinimumMaxModelLen(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.95")
	model.Spec.KVCache.ReconfigureStrategy = aiv1alpha2.KVCacheReconfigureStrategyReduceMaxLen

	configJSON, _ := json.Marshal(map[string]any{
		"maxModelLen": 1024,
	})
	model.Spec.Config = &apiextensionsv1.JSON{Raw: configJSON}

	r.reconcileKVCachePressure(context.Background(), model)

	if model.Status.KVCache.ReconfiguredMaxModelLen == nil || *model.Status.KVCache.ReconfiguredMaxModelLen != 1024 {
		t.Fatalf("expected ReconfiguredMaxModelLen=1024 minimum, got %v", model.Status.KVCache.ReconfiguredMaxModelLen)
	}
}

func TestApplyKVCacheReconfigureOverrides(t *testing.T) {
	maxSeqs := int32(4)
	maxLen := int32(4096)
	model := &aiv1alpha2.Model{
		Status: aiv1alpha2.ModelStatus{
			KVCache: &aiv1alpha2.KVCacheStatus{
				Reconfigured:            true,
				ReconfiguredMaxNumSeqs:  &maxSeqs,
				ReconfiguredMaxModelLen: &maxLen,
			},
		},
	}
	spec := &backend.ModelSpec{Config: map[string]any{"temperature": 0.7}}

	applyKVCacheReconfigureOverrides(model, spec)

	if got := spec.Config["maxNumSeqs"]; got != float64(4) {
		t.Fatalf("expected maxNumSeqs override 4, got %v", got)
	}
	if got := spec.Config["maxModelLen"]; got != float64(4096) {
		t.Fatalf("expected maxModelLen override 4096, got %v", got)
	}
	if got := spec.Config["temperature"]; got != 0.7 {
		t.Fatalf("expected existing config to be preserved, got %v", got)
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

func TestKVCacheEvict_IncrementsEvictionCounter(t *testing.T) {
	r, model := kvCacheTestSetup(t, "0.90")
	model.Spec.KVCache.PressurePolicy = aiv1alpha2.KVCachePressurePolicyEvict

	counter := metrics.KVCachePressureEvictionsTotal.WithLabelValues(model.Name, model.Namespace)
	before := promtestutil.ToFloat64(counter)

	// First eviction transition increments the counter once.
	r.reconcileKVCachePressure(context.Background(), model)
	if got := promtestutil.ToFloat64(counter) - before; got != 1 {
		t.Fatalf("expected eviction counter to increment by 1, got %v", got)
	}

	// Subsequent reconciles while already evicted (EvictActive) must not double-count.
	r.reconcileKVCachePressure(context.Background(), model)
	if got := promtestutil.ToFloat64(counter) - before; got != 1 {
		t.Fatalf("expected eviction counter to stay at 1 while EvictActive, got %v", got)
	}
}
