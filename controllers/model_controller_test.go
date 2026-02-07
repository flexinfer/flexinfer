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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
