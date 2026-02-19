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

	dto "github.com/prometheus/client_model/go"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

func TestExtractModelFromSource(t *testing.T) {
	tests := []struct {
		source   string
		expected string
	}{
		{"HF://mlc-ai/Qwen3-8B-q4f16_1-MLC", "mlc-ai/Qwen3-8B-q4f16_1-MLC"},
		{"ollama://llama3:8b", "llama3:8b"},
		{"file:///models/qwen3.gguf", "/models/qwen3.gguf"},
		{"pvc://model-cache/qwen3", "/qwen3"},
		{"simple-model", "simple-model"},
	}

	for _, tt := range tests {
		result := extractModelFromSource(tt.source)
		if result != tt.expected {
			t.Errorf("extractModelFromSource(%q) = %q, want %q", tt.source, result, tt.expected)
		}
	}
}

func TestDesiredReplicasServerless(t *testing.T) {
	r := &ModelReconciler{}
	vllmBackend, _ := backend.Get("vllm")

	model := &aiv1alpha2.Model{
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
		},
	}

	// Serverless defaults to enabled; no activity => min replicas (default 0).
	if got := r.desiredReplicas(model, vllmBackend); got != 0 {
		t.Errorf("desiredReplicas() = %d, want 0 (no activity, serverless)", got)
	}

	// Recent activity => scale up to 1.
	recentTime := metav1.Time{Time: time.Now().Add(-1 * time.Minute)}
	model.Status.LastActiveTime = &recentTime
	if got := r.desiredReplicas(model, vllmBackend); got != 1 {
		t.Errorf("desiredReplicas() = %d, want 1 (recent activity)", got)
	}

	// Old activity => scale back down to min replicas (0).
	oldTime := metav1.Time{Time: time.Now().Add(-10 * time.Minute)}
	model.Status.LastActiveTime = &oldTime
	if got := r.desiredReplicas(model, vllmBackend); got != 0 {
		t.Errorf("desiredReplicas() = %d, want 0 (old activity)", got)
	}

	// Warm start via MinReplicas=1 keeps it running even without activity.
	enabled := true
	minOne := int32(1)
	model.Spec.Serverless = &aiv1alpha2.ServerlessSpec{
		Enabled:     &enabled,
		MinReplicas: &minOne,
	}
	model.Status.LastActiveTime = nil
	if got := r.desiredReplicas(model, vllmBackend); got != 1 {
		t.Errorf("desiredReplicas() = %d, want 1 (minReplicas=1)", got)
	}

	// Serverless disabled => always 1.
	disabled := false
	model.Spec.Serverless.Enabled = &disabled
	model.Spec.Serverless.MinReplicas = nil
	if got := r.desiredReplicas(model, vllmBackend); got != 1 {
		t.Errorf("desiredReplicas() = %d, want 1 (serverless disabled)", got)
	}
}

func TestChooseSharedGroupLeader(t *testing.T) {
	now := time.Now()
	shared := "test-shared"
	high := int32(200)
	mid := int32(100)
	low := int32(50)

	models := []*aiv1alpha2.Model{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "low"},
			Spec:       aiv1alpha2.ModelSpec{GPU: &aiv1alpha2.GPUSpec{Shared: shared, Priority: &low}},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "high"},
			Spec:       aiv1alpha2.ModelSpec{GPU: &aiv1alpha2.GPUSpec{Shared: shared, Priority: &high}},
		},
	}

	leader := chooseSharedGroupLeader(models, now)
	if leader == nil || leader.Name != "high" {
		t.Fatalf("expected high-priority fallback leader, got %v", leader)
	}

	readyLow := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "ready-low"},
		Spec:       aiv1alpha2.ModelSpec{GPU: &aiv1alpha2.GPUSpec{Shared: shared, Priority: &low}},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseReady,
		},
	}
	recentHigh := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "recent-high"},
		Spec:       aiv1alpha2.ModelSpec{GPU: &aiv1alpha2.GPUSpec{Shared: shared, Priority: &high}},
		Status: aiv1alpha2.ModelStatus{
			LastActiveTime: &metav1.Time{Time: now.Add(-30 * time.Second)},
		},
	}
	leader = chooseSharedGroupLeader([]*aiv1alpha2.Model{readyLow, recentHigh}, now)
	if leader == nil || leader.Name != "ready-low" {
		t.Fatalf("expected ready model to win over recent activity, got %v", leader)
	}

	oldHigh := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "old-high"},
		Spec:       aiv1alpha2.ModelSpec{GPU: &aiv1alpha2.GPUSpec{Shared: shared, Priority: &high}},
		Status: aiv1alpha2.ModelStatus{
			LastActiveTime: &metav1.Time{Time: now.Add(-10 * time.Minute)},
		},
	}
	recentMid := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "recent-mid"},
		Spec:       aiv1alpha2.ModelSpec{GPU: &aiv1alpha2.GPUSpec{Shared: shared, Priority: &mid}},
		Status: aiv1alpha2.ModelStatus{
			LastActiveTime: &metav1.Time{Time: now.Add(-20 * time.Second)},
		},
	}
	leader = chooseSharedGroupLeader([]*aiv1alpha2.Model{oldHigh, recentMid}, now)
	if leader == nil || leader.Name != "recent-mid" {
		t.Fatalf("expected recent active model to win when none are ready, got %v", leader)
	}
}

func TestQueuePositionForSharedModel(t *testing.T) {
	shared := "test-shared"
	p200 := int32(200)
	p100 := int32(100)
	p50 := int32(50)

	active := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "active"},
		Spec:       aiv1alpha2.ModelSpec{GPU: &aiv1alpha2.GPUSpec{Shared: shared, Priority: &p200}},
	}
	queuedA := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "queued-a"},
		Spec:       aiv1alpha2.ModelSpec{GPU: &aiv1alpha2.GPUSpec{Shared: shared, Priority: &p100}},
	}
	queuedB := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "queued-b"},
		Spec:       aiv1alpha2.ModelSpec{GPU: &aiv1alpha2.GPUSpec{Shared: shared, Priority: &p50}},
	}
	group := []*aiv1alpha2.Model{queuedB, active, queuedA}

	if pos := queuePositionForSharedModel("active", active, group); pos != 0 {
		t.Fatalf("expected active model queue position 0, got %d", pos)
	}
	if pos := queuePositionForSharedModel("queued-a", active, group); pos != 1 {
		t.Fatalf("expected queued-a queue position 1, got %d", pos)
	}
	if pos := queuePositionForSharedModel("queued-b", active, group); pos != 2 {
		t.Fatalf("expected queued-b queue position 2, got %d", pos)
	}
}

func TestHandleSharedGPU_NoSelfElectionWhenNoModelReady(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add flexinfer scheme: %v", err)
	}

	shared := "test-shared"
	highPrio := int32(200)
	lowPrio := int32(50)

	high := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "high",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			GPU: &aiv1alpha2.GPUSpec{
				Shared:   shared,
				Priority: &highPrio,
			},
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhasePending,
		},
	}
	low := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "low",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			GPU: &aiv1alpha2.GPUSpec{
				Shared:   shared,
				Priority: &lowPrio,
			},
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhasePending,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		WithRuntimeObjects(high, low).
		Build()
	r := &ModelReconciler{
		Client: fakeClient,
		Scheme: s,
	}
	ctx := context.Background()

	lowObj := &aiv1alpha2.Model{}
	if err := fakeClient.Get(ctx, client.ObjectKeyFromObject(low), lowObj); err != nil {
		t.Fatalf("get low model: %v", err)
	}
	if _, err := r.handleSharedGPU(ctx, lowObj); err != nil {
		t.Fatalf("handleSharedGPU(low) error: %v", err)
	}

	updatedLow := &aiv1alpha2.Model{}
	if err := fakeClient.Get(ctx, client.ObjectKeyFromObject(low), updatedLow); err != nil {
		t.Fatalf("get updated low model: %v", err)
	}
	if updatedLow.Status.SharedGroup == nil {
		t.Fatalf("expected shared group status to be set")
	}
	if updatedLow.Status.SharedGroup.State != "Queued" {
		t.Fatalf("expected low model state Queued, got %q", updatedLow.Status.SharedGroup.State)
	}
	if updatedLow.Status.SharedGroup.PreemptedBy != "high" {
		t.Fatalf("expected low model preemptedBy high, got %q", updatedLow.Status.SharedGroup.PreemptedBy)
	}
	if updatedLow.Status.SharedGroup.QueuePosition != 1 {
		t.Fatalf("expected low model queue position 1, got %d", updatedLow.Status.SharedGroup.QueuePosition)
	}
}

func TestDetectGPU_UsesOnlyReadyNodes(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add flexinfer scheme: %v", err)
	}

	notReadyNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "amd-not-ready",
			Labels: map[string]string{"gpu.amd.com/gpu-architecture": "gfx1100"},
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceName("amd.com/gpu"): resourceMustParse("1"),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			},
		},
	}
	readyNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "amd-ready",
			Labels: map[string]string{"gpu.amd.com/gpu-architecture": "gfx1100"},
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceName("amd.com/gpu"): resourceMustParse("1"),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}

	r := &ModelReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(s).
			WithRuntimeObjects(notReadyNode, readyNode).
			Build(),
		Scheme: s,
	}
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			GPU: &aiv1alpha2.GPUSpec{
				Vendor: aiv1alpha2.GPUVendorAMD,
			},
		},
	}

	vendor, arch, err := r.detectGPU(context.Background(), model)
	if err != nil {
		t.Fatalf("detectGPU() unexpected error: %v", err)
	}
	if vendor != backend.GPUVendorAMD {
		t.Fatalf("detectGPU() vendor=%v, want %v", vendor, backend.GPUVendorAMD)
	}
	if arch != "gfx1100" {
		t.Fatalf("detectGPU() arch=%q, want %q", arch, "gfx1100")
	}
}

func TestDetectGPU_ReturnsNoMatchingWhenOnlyNotReadyNodesMatch(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add flexinfer scheme: %v", err)
	}

	notReadyNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "amd-not-ready",
			Labels: map[string]string{"gpu.amd.com/gpu-architecture": "gfx1100"},
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceName("amd.com/gpu"): resourceMustParse("1"),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionUnknown},
			},
		},
	}

	r := &ModelReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(s).
			WithRuntimeObjects(notReadyNode).
			Build(),
		Scheme: s,
	}
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			GPU: &aiv1alpha2.GPUSpec{
				Vendor: aiv1alpha2.GPUVendorAMD,
			},
		},
	}

	if _, _, err := r.detectGPU(context.Background(), model); err == nil {
		t.Fatalf("expected no matching nodes error, got nil")
	} else if !isNoMatchingNodesError(err) {
		t.Fatalf("expected noMatchingNodesError, got %T (%v)", err, err)
	}
}

func TestPruneFailedModelPods_DeletesOnlyOldFailedPods(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add flexinfer scheme: %v", err)
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
		},
	}

	matchLabels := map[string]string{
		"app.kubernetes.io/name":       "model",
		"app.kubernetes.io/instance":   "test-model",
		"app.kubernetes.io/managed-by": "flexinfer",
		"flexinfer.ai/model":           "test-model",
		"flexinfer.ai/backend":         "mlc-llm",
	}

	oldFailed := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "old-failed",
			Namespace:         "default",
			Labels:            matchLabels,
			CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
		},
		Status: corev1.PodStatus{
			Phase:  corev1.PodFailed,
			Reason: "UnexpectedAdmissionError",
		},
	}
	recentFailed := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "recent-failed",
			Namespace:         "default",
			Labels:            matchLabels,
			CreationTimestamp: metav1.NewTime(time.Now().Add(-1 * time.Minute)),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
		},
	}
	running := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "running",
			Namespace:         "default",
			Labels:            matchLabels,
			CreationTimestamp: metav1.NewTime(time.Now().Add(-20 * time.Minute)),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(model, oldFailed, recentFailed, running).
		Build()
	r := &ModelReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	if err := r.pruneFailedModelPods(context.Background(), model); err != nil {
		t.Fatalf("pruneFailedModelPods() error: %v", err)
	}

	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(oldFailed), &corev1.Pod{}); err == nil {
		t.Fatalf("expected old failed pod to be deleted")
	} else if !errors.IsNotFound(err) {
		t.Fatalf("unexpected error checking old failed pod: %v", err)
	}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(recentFailed), &corev1.Pod{}); err != nil {
		t.Fatalf("expected recent failed pod to remain, got error: %v", err)
	}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(running), &corev1.Pod{}); err != nil {
		t.Fatalf("expected running pod to remain, got error: %v", err)
	}
}

func resourceMustParse(raw string) resource.Quantity {
	q, err := resource.ParseQuantity(raw)
	if err != nil {
		panic(err)
	}
	return q
}

func TestGetIdleTimeout(t *testing.T) {
	vllmBackend, _ := backend.Get("vllm")
	comfyBackend, _ := backend.Get("comfyui")

	// Test default timeout from backend
	model := &aiv1alpha2.Model{
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
		},
	}

	timeout := getIdleTimeout(model, vllmBackend)
	if timeout != vllmBackend.DefaultIdleTimeout() {
		t.Errorf("Expected default timeout %v, got %v", vllmBackend.DefaultIdleTimeout(), timeout)
	}

	// Test custom timeout
	customTimeout := 15 * time.Minute
	model.Spec.Serverless = &aiv1alpha2.ServerlessSpec{
		IdleTimeout: &metav1.Duration{Duration: customTimeout},
	}

	timeout = getIdleTimeout(model, vllmBackend)
	if timeout != customTimeout {
		t.Errorf("Expected custom timeout %v, got %v", customTimeout, timeout)
	}

	// Test image generation backend has longer default
	imgModel := &aiv1alpha2.Model{
		Spec: aiv1alpha2.ModelSpec{
			Backend: "comfyui",
		},
	}

	timeout = getIdleTimeout(imgModel, comfyBackend)
	if timeout != comfyBackend.DefaultIdleTimeout() {
		t.Errorf("Expected comfyui default timeout %v, got %v", comfyBackend.DefaultIdleTimeout(), timeout)
	}
}

func TestLabelsForModel(t *testing.T) {
	r := &ModelReconciler{}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen3-8b",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
		},
	}

	labels := r.labelsForModel(model)

	if labels["flexinfer.ai/model"] != "qwen3-8b" {
		t.Errorf("Expected model label 'qwen3-8b', got %q", labels["flexinfer.ai/model"])
	}

	if labels["flexinfer.ai/backend"] != "mlc-llm" {
		t.Errorf("Expected backend label 'mlc-llm', got %q", labels["flexinfer.ai/backend"])
	}

	if _, ok := labels["flexinfer.ai/gpu-group"]; ok {
		t.Error("Should not have gpu-group label when not shared")
	}

	// Test with shared GPU
	model.Spec.GPU = &aiv1alpha2.GPUSpec{
		Shared: "homelab-gpu",
	}

	labels = r.labelsForModel(model)
	if labels["flexinfer.ai/gpu-group"] != "homelab-gpu" {
		t.Errorf("Expected gpu-group label 'homelab-gpu', got %q", labels["flexinfer.ai/gpu-group"])
	}
}

func TestBuildBackendModelSpec(t *testing.T) {
	r := &ModelReconciler{}
	b, ok := backend.Get("mlc-llm")
	if !ok {
		t.Fatal("mlc-llm backend not found")
	}

	model := &aiv1alpha2.Model{
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			Source:  "HF://mlc-ai/Qwen3-8B-q4f16_1-MLC",
		},
	}

	spec := r.buildBackendModelSpec(model, b, backend.GPUVendorAMD)

	if spec.Model != "mlc-ai/Qwen3-8B-q4f16_1-MLC" {
		t.Errorf("Expected model 'mlc-ai/Qwen3-8B-q4f16_1-MLC', got %q", spec.Model)
	}

	if spec.GPUVendor != backend.GPUVendorAMD {
		t.Errorf("Expected GPU vendor AMD, got %v", spec.GPUVendor)
	}

	if spec.ModelPath != "" {
		t.Errorf("Expected empty model path for HF source, got %q", spec.ModelPath)
	}
}

func TestBuildBackendModelSpec_LlamaCppHFUsesGGUFFile(t *testing.T) {
	r := &ModelReconciler{}
	b, ok := backend.Get("llamacpp")
	if !ok {
		t.Fatal("llamacpp backend not found")
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name: "tinyllama-cpu",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://TheBloke/TinyLlama-1.1B-Chat-v1.0-GGUF",
			Config: &apiextensionsv1.JSON{
				Raw: []byte(`{"ggufFile":"tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf"}`),
			},
		},
		Status: aiv1alpha2.ModelStatus{
			Cache: &aiv1alpha2.CacheStatus{
				PVCName: "tinyllama-cpu-cache",
				Ready:   true,
			},
		},
	}

	spec := r.buildBackendModelSpec(model, b, backend.GPUVendorCPU)
	if spec.ModelPath != "/models/tinyllama-cpu/tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf" {
		t.Fatalf("ModelPath = %q, want %q", spec.ModelPath, "/models/tinyllama-cpu/tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf")
	}
}

func TestResolveBackendStoragePlan_DiffusersHFSharedPVC(t *testing.T) {
	b, ok := backend.Get("diffusers")
	if !ok {
		t.Fatal("diffusers backend not found")
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "sdxl-turbo"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "diffusers",
			Source:  "HF://stabilityai/sdxl-turbo",
			Cache: &aiv1alpha2.CacheSpec{
				Strategy: "SharedPVC",
			},
		},
		Status: aiv1alpha2.ModelStatus{
			Cache: &aiv1alpha2.CacheStatus{
				PVCName: "sdxl-cache",
				Ready:   true,
			},
		},
	}

	plan := resolveBackendStoragePlan(model, b, nil)
	if plan.ModelPath != "/models/sdxl-turbo" {
		t.Fatalf("ModelPath = %q, want %q", plan.ModelPath, "/models/sdxl-turbo")
	}
	if plan.ModelVolumeSubPath != "sdxl-turbo" {
		t.Fatalf("ModelVolumeSubPath = %q, want %q", plan.ModelVolumeSubPath, "sdxl-turbo")
	}
	if plan.HFCacheBasePath != "/models/.cache/huggingface" {
		t.Fatalf("HFCacheBasePath = %q, want %q", plan.HFCacheBasePath, "/models/.cache/huggingface")
	}
}

func TestResolveBackendStoragePlan_PVCAndFileSources(t *testing.T) {
	b, ok := backend.Get("mlc-llm")
	if !ok {
		t.Fatal("mlc-llm backend not found")
	}

	pvcModel := &aiv1alpha2.Model{
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			Source:  "pvc://models-pvc/qwen3",
		},
	}
	pvcPlan := resolveBackendStoragePlan(pvcModel, b, nil)
	if pvcPlan.ModelPath != "/models/qwen3" {
		t.Fatalf("PVC ModelPath = %q, want %q", pvcPlan.ModelPath, "/models/qwen3")
	}

	fileModel := &aiv1alpha2.Model{
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			Source:  "file:///models/qwen3.gguf",
		},
	}
	filePlan := resolveBackendStoragePlan(fileModel, b, nil)
	if filePlan.ModelPath != "/models/qwen3.gguf" {
		t.Fatalf("file ModelPath = %q, want %q", filePlan.ModelPath, "/models/qwen3.gguf")
	}
}

func TestResolveGGUFFile(t *testing.T) {
	if got := resolveGGUFFile(map[string]interface{}{"ggufFile": "models/tinyllama.gguf"}); got != "models/tinyllama.gguf" {
		t.Fatalf("resolveGGUFFile(ggufFile) = %q", got)
	}
	if got := resolveGGUFFile(map[string]interface{}{"modelFile": "legacy/tinyllama.gguf"}); got != "legacy/tinyllama.gguf" {
		t.Fatalf("resolveGGUFFile(modelFile) = %q", got)
	}
	if got := resolveGGUFFile(map[string]interface{}{"ggufFile": "../escape.gguf"}); got != "" {
		t.Fatalf("resolveGGUFFile traversal = %q, want empty", got)
	}
}

func TestResolveHFDownloadOptions_LlamaCppAddsGGUFAndMmproj(t *testing.T) {
	model := &aiv1alpha2.Model{
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://TheBloke/TinyLlama-1.1B-Chat-v1.0-GGUF",
			Config: &apiextensionsv1.JSON{
				Raw: []byte(`{
					"ggufFile":"models/tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf",
					"mmproj":"proj/mmproj-Q8_0.gguf"
				}`),
			},
		},
	}

	opts := resolveHFDownloadOptions(model)
	if got, want := len(opts.allowPatterns), 2; got != want {
		t.Fatalf("allowPatterns len = %d, want %d (%v)", got, want, opts.allowPatterns)
	}
	if opts.allowPatterns[0] != "models/tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf" {
		t.Fatalf("allowPatterns[0] = %q", opts.allowPatterns[0])
	}
	if opts.allowPatterns[1] != "proj/mmproj-Q8_0.gguf" {
		t.Fatalf("allowPatterns[1] = %q", opts.allowPatterns[1])
	}
	if opts.revision != "" {
		t.Fatalf("revision = %q, want empty", opts.revision)
	}
}

func TestResolveHFDownloadOptions_RespectsPatternOverrides(t *testing.T) {
	model := &aiv1alpha2.Model{
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://TeichAI/GLM-4.7-Flash-Claude-Opus-4.5-High-Reasoning-Distill-GGUF",
			Config: &apiextensionsv1.JSON{
				Raw: []byte(`{
					"ggufFile":"glm-4.7-flash-claude-4.5-opus.q4_k_m.gguf",
					"hfAllowPatterns":["README.md","/glm-4.7-flash-claude-4.5-opus.q4_k_m.gguf","README.md"],
					"hfIgnorePatterns":"*.png, .gitattributes",
					"hfRevision":"main"
				}`),
			},
		},
	}

	opts := resolveHFDownloadOptions(model)

	if got, want := len(opts.allowPatterns), 2; got != want {
		t.Fatalf("allowPatterns len = %d, want %d (%v)", got, want, opts.allowPatterns)
	}
	if opts.allowPatterns[0] != "README.md" {
		t.Fatalf("allowPatterns[0] = %q", opts.allowPatterns[0])
	}
	if opts.allowPatterns[1] != "glm-4.7-flash-claude-4.5-opus.q4_k_m.gguf" {
		t.Fatalf("allowPatterns[1] = %q", opts.allowPatterns[1])
	}

	if got, want := len(opts.ignorePatterns), 2; got != want {
		t.Fatalf("ignorePatterns len = %d, want %d (%v)", got, want, opts.ignorePatterns)
	}
	if opts.ignorePatterns[0] != "*.png" || opts.ignorePatterns[1] != ".gitattributes" {
		t.Fatalf("ignorePatterns = %v", opts.ignorePatterns)
	}
	if opts.revision != "main" {
		t.Fatalf("revision = %q, want main", opts.revision)
	}
}

func TestJobForPrefetch_IncludesHFPatternEnv(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add flexinfer scheme: %v", err)
	}

	r := &ModelReconciler{Scheme: s}
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "glm47",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://TeichAI/GLM-4.7-Flash-Claude-Opus-4.5-High-Reasoning-Distill-GGUF",
			Config: &apiextensionsv1.JSON{
				Raw: []byte(`{
					"ggufFile":"glm-4.7-flash-claude-4.5-opus.q4_k_m.gguf",
					"hfRevision":"main"
				}`),
			},
		},
	}

	job, err := r.jobForPrefetch(model, "glm47-cache", "glm47")
	if err != nil {
		t.Fatalf("jobForPrefetch() error: %v", err)
	}

	env := map[string]string{}
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	if env["HF_ALLOW_PATTERNS"] == "" {
		t.Fatalf("HF_ALLOW_PATTERNS env var missing: %+v", env)
	}
	if env["HF_REVISION"] != "main" {
		t.Fatalf("HF_REVISION = %q, want main", env["HF_REVISION"])
	}
	if env["HF_ALLOW_PATTERNS"] != "[\"glm-4.7-flash-claude-4.5-opus.q4_k_m.gguf\"]" {
		t.Fatalf("HF_ALLOW_PATTERNS = %s", env["HF_ALLOW_PATTERNS"])
	}
}

func TestValidateBackendGPUCompatibility_Maxwell(t *testing.T) {
	r := &ModelReconciler{}

	// vLLM should be rejected on Maxwell.
	vllmBackend, _ := backend.Get("vllm")
	model := &aiv1alpha2.Model{
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://mistralai/Mistral-7B-Instruct-v0.3",
		},
	}
	if err := r.validateBackendGPUCompatibility(model, vllmBackend, backend.GPUVendorNVIDIA, "sm_52"); err == nil {
		t.Fatalf("expected vllm on Maxwell to error, got nil")
	}

	// MLC-LLM should require a modelLibPath unless we can infer /models/<name>/maxwell-lib.so.
	mlcBackend, _ := backend.Get("mlc-llm")
	model = &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen3-0.6b"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			Source:  "HF://mlc-ai/Qwen3-0.6B-q0f32-MLC",
		},
	}
	if err := r.validateBackendGPUCompatibility(model, mlcBackend, backend.GPUVendorNVIDIA, "sm_52"); err == nil {
		t.Fatalf("expected mlc-llm on Maxwell without lib path to error, got nil")
	}

	// Explicit modelLibPath should pass.
	cfg := map[string]interface{}{
		"modelLibPath": "/models/qwen3-0.6b/maxwell-lib.so",
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	model.Spec.Config = &apiextensionsv1.JSON{Raw: raw}
	if err := r.validateBackendGPUCompatibility(model, mlcBackend, backend.GPUVendorNVIDIA, "sm_52"); err != nil {
		t.Fatalf("expected explicit modelLibPath to pass, got: %v", err)
	}

	// Conventional default under /models/<name> should pass when HF SharedPVC is materialized.
	model.Spec.Config = nil
	model.Status.Cache = &aiv1alpha2.CacheStatus{PVCName: "mlc-models"}
	model.Spec.Cache = &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"}
	if err := r.validateBackendGPUCompatibility(model, mlcBackend, backend.GPUVendorNVIDIA, "sm_52"); err != nil {
		t.Fatalf("expected /models/<name>/maxwell-lib.so default to pass, got: %v", err)
	}
}

func TestEnsureDeploymentCPUDoesNotRequestGPU(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add flexinfer scheme: %v", err)
	}

	b, ok := backend.Get("llamacpp")
	if !ok {
		t.Fatal("llamacpp backend not found")
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cpu-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "pvc://models-pvc/tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf",
			GPU: &aiv1alpha2.GPUSpec{
				Vendor: aiv1alpha2.GPUVendorCPU,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		Build()

	r := &ModelReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	ctx := context.Background()
	if err := r.ensureDeployment(ctx, model, b, backend.GPUVendorCPU, "", 1); err != nil {
		t.Fatalf("ensureDeployment() error: %v", err)
	}

	created := &appsv1.Deployment{}
	if err := fakeClient.Get(ctx, client.ObjectKey{Name: model.Name, Namespace: model.Namespace}, created); err != nil {
		t.Fatalf("failed to fetch created deployment: %v", err)
	}

	podSpec := created.Spec.Template.Spec
	if podSpec.RuntimeClassName != nil {
		t.Fatalf("RuntimeClassName = %v, want nil", *podSpec.RuntimeClassName)
	}
	if podSpec.AutomountServiceAccountToken == nil || *podSpec.AutomountServiceAccountToken {
		t.Fatalf("AutomountServiceAccountToken = %v, want false", podSpec.AutomountServiceAccountToken)
	}
	for _, tol := range podSpec.Tolerations {
		if tol.Key == "dedicated" && tol.Value == "gpu" {
			t.Fatalf("unexpected GPU toleration: %#v", tol)
		}
	}
	if len(podSpec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(podSpec.Containers))
	}

	c := podSpec.Containers[0]
	if c.Image != "ghcr.io/ggerganov/llama.cpp:server" {
		t.Fatalf("Image = %q, want %q", c.Image, "ghcr.io/ggerganov/llama.cpp:server")
	}
	if _, ok := c.Resources.Limits[corev1.ResourceName("nvidia.com/gpu")]; ok {
		t.Fatalf("unexpected nvidia.com/gpu limit in resources: %#v", c.Resources.Limits)
	}
	if _, ok := c.Resources.Limits[corev1.ResourceName("amd.com/gpu")]; ok {
		t.Fatalf("unexpected amd.com/gpu limit in resources: %#v", c.Resources.Limits)
	}
}

func TestEnsureServicePreservesClusterIP(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add flexinfer scheme: %v", err)
	}

	b, ok := backend.Get("mlc-llm")
	if !ok {
		t.Fatal("mlc-llm backend not found")
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
		},
	}

	clusterIP := "10.43.0.10"
	existing := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name,
			Namespace: model.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: clusterIP,
			ClusterIPs: []string{
				clusterIP,
			},
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 1234,
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(existing).
		Build()

	r := &ModelReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	ctx := context.Background()
	if err := r.ensureService(ctx, model, b); err != nil {
		t.Fatalf("ensureService() error: %v", err)
	}

	updated := &corev1.Service{}
	if err := fakeClient.Get(ctx, client.ObjectKeyFromObject(existing), updated); err != nil {
		t.Fatalf("failed to fetch updated service: %v", err)
	}

	if updated.Spec.ClusterIP != clusterIP {
		t.Fatalf("expected clusterIP %q, got %q", clusterIP, updated.Spec.ClusterIP)
	}
	if len(updated.Spec.ClusterIPs) != 1 || updated.Spec.ClusterIPs[0] != clusterIP {
		t.Fatalf("expected clusterIPs [%q], got %#v", clusterIP, updated.Spec.ClusterIPs)
	}

	// Verify ports are updated (from 1234 to backend port 8000)
	expectedPort := b.Port()
	if len(updated.Spec.Ports) != 1 {
		t.Fatalf("expected 1 port, got %d", len(updated.Spec.Ports))
	}
	if updated.Spec.Ports[0].Port != expectedPort {
		t.Fatalf("expected port %d, got %d", expectedPort, updated.Spec.Ports[0].Port)
	}

	// Verify selector is set
	if updated.Spec.Selector == nil {
		t.Fatal("expected selector to be set")
	}
	if updated.Spec.Selector["flexinfer.ai/model"] != model.Name {
		t.Fatalf("expected selector flexinfer.ai/model=%s, got %v", model.Name, updated.Spec.Selector)
	}
}

func TestEnsureDeploymentPreservesSelectorAndMatchesTemplate(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add flexinfer scheme: %v", err)
	}

	b, ok := backend.Get("mlc-llm")
	if !ok {
		t.Fatal("mlc-llm backend not found")
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			Source:  "pvc://mlc-models-nfs/Qwen3-14B-q4f16_1-MLC",
			GPU: &aiv1alpha2.GPUSpec{
				Vendor: aiv1alpha2.GPUVendorAMD,
				Count:  func() *int32 { v := int32(1); return &v }(),
			},
		},
	}

	// Simulate an older deployment selector that included gpu-group.
	selector := map[string]string{
		"app.kubernetes.io/name":       "model",
		"app.kubernetes.io/instance":   model.Name,
		"app.kubernetes.io/managed-by": "flexinfer",
		"flexinfer.ai/model":           model.Name,
		"flexinfer.ai/backend":         model.Spec.Backend,
		"flexinfer.ai/gpu-group":       "homelab-7900xtx",
	}

	existing := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name,
			Namespace: model.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: selector,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/instance": model.Name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "model", Image: "example"},
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(existing).
		Build()

	r := &ModelReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	ctx := context.Background()
	if err := r.ensureDeployment(ctx, model, b, backend.GPUVendorAMD, "gfx1100", 2); err != nil {
		t.Fatalf("ensureDeployment() error: %v", err)
	}

	updated := &appsv1.Deployment{}
	if err := fakeClient.Get(ctx, client.ObjectKeyFromObject(existing), updated); err != nil {
		t.Fatalf("failed to fetch updated deployment: %v", err)
	}

	if updated.Spec.Selector == nil || updated.Spec.Selector.MatchLabels == nil {
		t.Fatal("expected deployment selector to be set")
	}
	if updated.Spec.Selector.MatchLabels["flexinfer.ai/gpu-group"] != "homelab-7900xtx" {
		t.Fatalf("expected selector to preserve gpu-group, got %#v", updated.Spec.Selector.MatchLabels)
	}
	for k, v := range updated.Spec.Selector.MatchLabels {
		if updated.Spec.Template.Labels[k] != v {
			t.Fatalf("expected template label %q=%q to match selector, got %q", k, v, updated.Spec.Template.Labels[k])
		}
	}
}

func TestEnsureDeploymentMultiReplicaIncludesSpreadingConstraints(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add flexinfer scheme: %v", err)
	}

	b, ok := backend.Get("mlc-llm")
	if !ok {
		t.Fatal("mlc-llm backend not found")
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spread-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			Source:  "pvc://mlc-models-nfs/Qwen3-14B-q4f16_1-MLC",
			GPU: &aiv1alpha2.GPUSpec{
				Vendor: aiv1alpha2.GPUVendorAMD,
				Count:  func() *int32 { v := int32(1); return &v }(),
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		Build()

	r := &ModelReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	ctx := context.Background()
	if err := r.ensureDeployment(ctx, model, b, backend.GPUVendorAMD, "gfx1100", 2); err != nil {
		t.Fatalf("ensureDeployment() error: %v", err)
	}

	created := &appsv1.Deployment{}
	if err := fakeClient.Get(ctx, client.ObjectKeyFromObject(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: model.Name, Namespace: model.Namespace}}), created); err != nil {
		t.Fatalf("failed to fetch created deployment: %v", err)
	}

	if created.Spec.Template.Spec.Affinity == nil || created.Spec.Template.Spec.Affinity.PodAntiAffinity == nil {
		t.Fatal("expected pod anti-affinity to be set for multi-replica model")
	}
	if len(created.Spec.Template.Spec.TopologySpreadConstraints) == 0 {
		t.Fatal("expected topology spread constraints to be set for multi-replica model")
	}
}

// TestSetModelCondition verifies that setModelCondition correctly adds and updates conditions.
func TestSetModelCondition(t *testing.T) {
	tests := []struct {
		name            string
		existingConds   []metav1.Condition
		condType        string
		status          bool
		reason          string
		message         string
		expectCondCount int
		expectStatus    metav1.ConditionStatus
	}{
		{
			name:            "add new condition to empty list",
			existingConds:   nil,
			condType:        aiv1alpha2.ConditionModelSchedulable,
			status:          true,
			reason:          aiv1alpha2.ReasonSchedulable,
			message:         "Model can be scheduled",
			expectCondCount: 1,
			expectStatus:    metav1.ConditionTrue,
		},
		{
			name: "update existing condition status",
			existingConds: []metav1.Condition{
				{
					Type:               aiv1alpha2.ConditionModelSchedulable,
					Status:             metav1.ConditionTrue,
					Reason:             aiv1alpha2.ReasonSchedulable,
					Message:            "Was schedulable",
					LastTransitionTime: metav1.Now(),
				},
			},
			condType:        aiv1alpha2.ConditionModelSchedulable,
			status:          false,
			reason:          aiv1alpha2.ReasonNoMatchingNodes,
			message:         "No matching nodes",
			expectCondCount: 1,
			expectStatus:    metav1.ConditionFalse,
		},
		{
			name: "add different condition type",
			existingConds: []metav1.Condition{
				{
					Type:               aiv1alpha2.ConditionModelSchedulable,
					Status:             metav1.ConditionTrue,
					Reason:             aiv1alpha2.ReasonSchedulable,
					Message:            "Schedulable",
					LastTransitionTime: metav1.Now(),
				},
			},
			condType:        aiv1alpha2.ConditionModelReady,
			status:          false,
			reason:          aiv1alpha2.ReasonStartingBackend,
			message:         "Backend is starting",
			expectCondCount: 2,
			expectStatus:    metav1.ConditionFalse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-model",
					Namespace:  "default",
					Generation: 1,
				},
				Status: aiv1alpha2.ModelStatus{
					Conditions: tt.existingConds,
				},
			}

			setModelCondition(model, tt.condType, tt.status, tt.reason, tt.message)

			if len(model.Status.Conditions) != tt.expectCondCount {
				t.Fatalf("expected %d conditions, got %d", tt.expectCondCount, len(model.Status.Conditions))
			}

			// Find the condition we just set
			var found *metav1.Condition
			for i := range model.Status.Conditions {
				if model.Status.Conditions[i].Type == tt.condType {
					found = &model.Status.Conditions[i]
					break
				}
			}

			if found == nil {
				t.Fatalf("condition %s not found", tt.condType)
			}

			if found.Status != tt.expectStatus {
				t.Errorf("expected status %s, got %s", tt.expectStatus, found.Status)
			}
			if found.Reason != tt.reason {
				t.Errorf("expected reason %s, got %s", tt.reason, found.Reason)
			}
			if found.Message != tt.message {
				t.Errorf("expected message %q, got %q", tt.message, found.Message)
			}
		})
	}
}

func TestResolveFlashLoaderConfig_GlobalDefaults(t *testing.T) {
	t.Setenv("DEFAULT_FLASH_LOADER_ENABLED", "true")
	t.Setenv("DEFAULT_FLASH_LOADER_IMAGE", "registry.example/flash-loader:rocm")
	t.Setenv("DEFAULT_FLASH_LOADER_CONCURRENCY", "9")
	t.Setenv("DEFAULT_FLASH_LOADER_TMPFS_SIZE_LIMIT", "12Gi")

	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add v1alpha1 scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add v1alpha2 scheme: %v", err)
	}

	r := &ModelReconciler{
		Client: fake.NewClientBuilder().WithScheme(s).Build(),
		Scheme: s,
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "test-model", Namespace: "default"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			Source:  "HF://mlc-ai/Qwen3-8B-q4f32_1-MLC",
			Cache: &aiv1alpha2.CacheSpec{
				Strategy: "SharedPVC",
			},
		},
	}

	cfg := r.resolveFlashLoaderConfig(context.Background(), model)
	if !cfg.Enabled {
		t.Fatal("expected flash-loader to be enabled via global default")
	}
	if cfg.Image != "registry.example/flash-loader:rocm" {
		t.Fatalf("expected flash-loader image override, got %q", cfg.Image)
	}
	if cfg.Concurrency != 9 {
		t.Fatalf("expected flash-loader concurrency 9, got %d", cfg.Concurrency)
	}
	if cfg.TmpfsSizeLimit == nil || cfg.TmpfsSizeLimit.String() != "12Gi" {
		t.Fatalf("expected tmpfs size limit 12Gi, got %v", cfg.TmpfsSizeLimit)
	}
}

func TestResolveFlashLoaderConfig_ModelCacheOverrides(t *testing.T) {
	t.Setenv("DEFAULT_FLASH_LOADER_ENABLED", "true")
	t.Setenv("DEFAULT_FLASH_LOADER_IMAGE", "registry.example/flash-loader:default")
	t.Setenv("DEFAULT_FLASH_LOADER_CONCURRENCY", "4")

	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add v1alpha1 scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add v1alpha2 scheme: %v", err)
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "override-model", Namespace: "default"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			Source:  "HF://mlc-ai/Qwen3-8B-q4f32_1-MLC",
			Cache: &aiv1alpha2.CacheSpec{
				Strategy: "SharedPVC",
			},
		},
	}

	t.Run("disables when modelcache sets enabled=false", func(t *testing.T) {
		mc := &aiv1alpha1.ModelCache{
			ObjectMeta: metav1.ObjectMeta{Name: "override-model", Namespace: "default"},
			Spec: aiv1alpha1.ModelCacheSpec{
				Source: "HF://mlc-ai/Qwen3-8B-q4f32_1-MLC",
				FlashLoader: &aiv1alpha1.FlashLoaderSpec{
					Enabled: false,
				},
			},
		}
		r := &ModelReconciler{
			Client: fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(mc).Build(),
			Scheme: s,
		}
		cfg := r.resolveFlashLoaderConfig(context.Background(), model)
		if cfg.Enabled {
			t.Fatal("expected flash-loader to be disabled by modelcache override")
		}
	})

	t.Run("uses modelcache image and concurrency when enabled", func(t *testing.T) {
		size := "16Gi"
		mc := &aiv1alpha1.ModelCache{
			ObjectMeta: metav1.ObjectMeta{Name: "override-model", Namespace: "default"},
			Spec: aiv1alpha1.ModelCacheSpec{
				Source: "HF://mlc-ai/Qwen3-8B-q4f32_1-MLC",
				FlashLoader: &aiv1alpha1.FlashLoaderSpec{
					Enabled:        true,
					Image:          "registry.example/flash-loader:custom",
					Concurrency:    7,
					TmpfsSizeLimit: &size,
				},
			},
		}
		r := &ModelReconciler{
			Client: fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(mc).Build(),
			Scheme: s,
		}
		cfg := r.resolveFlashLoaderConfig(context.Background(), model)
		if !cfg.Enabled {
			t.Fatal("expected flash-loader enabled from modelcache")
		}
		if cfg.Image != "registry.example/flash-loader:custom" {
			t.Fatalf("expected modelcache image override, got %q", cfg.Image)
		}
		if cfg.Concurrency != 7 {
			t.Fatalf("expected modelcache concurrency 7, got %d", cfg.Concurrency)
		}
		if cfg.TmpfsSizeLimit == nil || cfg.TmpfsSizeLimit.String() != "16Gi" {
			t.Fatalf("expected tmpfs size limit 16Gi, got %v", cfg.TmpfsSizeLimit)
		}
	})
}

func TestUpdateStatusFromDeployment_EmitsLatencyMetrics(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add v1alpha2 scheme: %v", err)
	}

	replicas := int32(1)
	now := time.Now()
	modelName := "latency-model-" + now.Format("150405")
	readyTransition := metav1.NewTime(now.Add(-20 * time.Second))
	preemptedAt := metav1.NewTime(now.Add(-35 * time.Second))

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:       modelName,
			Namespace:  "default",
			Generation: 1,
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			Source:  "pvc://models-pvc/qwen3",
			GPU: &aiv1alpha2.GPUSpec{
				Shared: "fast-models",
			},
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseLoading,
			SharedGroup: &aiv1alpha2.SharedGroupStatus{
				GroupName:   "fast-models",
				State:       "Active",
				PreemptedAt: &preemptedAt,
			},
			Conditions: []metav1.Condition{
				{
					Type:               aiv1alpha2.ConditionModelReady,
					Status:             metav1.ConditionFalse,
					Reason:             aiv1alpha2.ReasonPreempted,
					Message:            "Model was preempted by higher priority model",
					LastTransitionTime: readyTransition,
					ObservedGeneration: 1,
				},
			},
		},
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelName,
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
		},
		Status: appsv1.DeploymentStatus{
			ReadyReplicas: 1,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(model, deployment).
		WithStatusSubresource(&aiv1alpha2.Model{}, &appsv1.Deployment{}).
		Build()

	r := &ModelReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	key := client.ObjectKey{Name: modelName, Namespace: "default"}
	current := &aiv1alpha2.Model{}
	if err := fakeClient.Get(context.Background(), key, current); err != nil {
		t.Fatalf("failed to get model: %v", err)
	}

	coldLabels := map[string]string{
		"model":          modelName,
		"namespace":      "default",
		"backend":        "mlc-llm",
		"cache_strategy": "Memory",
	}
	swapLabels := map[string]string{
		"model":     modelName,
		"namespace": "default",
		"backend":   "mlc-llm",
		"group":     "fast-models",
	}

	beforeCold := histogramSampleCount(t, "flexinfer_model_cold_start_duration_seconds", coldLabels)
	beforeSwap := histogramSampleCount(t, "flexinfer_model_swap_duration_seconds", swapLabels)

	if err := r.updateStatusFromDeployment(context.Background(), current); err != nil {
		t.Fatalf("updateStatusFromDeployment() error: %v", err)
	}

	afterCold := histogramSampleCount(t, "flexinfer_model_cold_start_duration_seconds", coldLabels)
	afterSwap := histogramSampleCount(t, "flexinfer_model_swap_duration_seconds", swapLabels)
	if afterCold <= beforeCold {
		t.Fatalf("expected cold-start histogram sample count to increase (before=%d after=%d)", beforeCold, afterCold)
	}
	if afterSwap <= beforeSwap {
		t.Fatalf("expected swap histogram sample count to increase (before=%d after=%d)", beforeSwap, afterSwap)
	}

	updated := &aiv1alpha2.Model{}
	if err := fakeClient.Get(context.Background(), key, updated); err != nil {
		t.Fatalf("failed to get updated model: %v", err)
	}
	if updated.Status.Phase != aiv1alpha2.ModelPhaseReady {
		t.Fatalf("expected model phase Ready, got %s", updated.Status.Phase)
	}
	if updated.Status.SharedGroup == nil || updated.Status.SharedGroup.PreemptedAt != nil {
		t.Fatalf("expected shared-group preemptedAt to be cleared after swap metrics emission")
	}
}

func histogramSampleCount(t *testing.T, metricName string, labels map[string]string) uint64 {
	t.Helper()
	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != metricName {
			continue
		}
		for _, metric := range family.GetMetric() {
			if labelSetMatches(metric, labels) {
				if metric.GetHistogram() != nil {
					return metric.GetHistogram().GetSampleCount()
				}
				return 0
			}
		}
	}
	return 0
}

func labelSetMatches(metric *dto.Metric, want map[string]string) bool {
	if metric == nil {
		return false
	}
	for k, v := range want {
		found := false
		for _, l := range metric.GetLabel() {
			if l.GetName() == k && l.GetValue() == v {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
