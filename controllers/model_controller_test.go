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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

func TestShouldScaleToZero(t *testing.T) {
	// Model with recent activity should not scale
	recentTime := metav1.Time{Time: time.Now().Add(-1 * time.Minute)}
	model := &aiv1alpha2.Model{
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
		},
		Status: aiv1alpha2.ModelStatus{
			LastActiveTime: &recentTime,
		},
	}

	if shouldScaleToZero(model) {
		t.Error("Model with recent activity should not scale to zero")
	}

	// Model with old activity should scale
	oldTime := metav1.Time{Time: time.Now().Add(-10 * time.Minute)}
	model.Status.LastActiveTime = &oldTime

	if !shouldScaleToZero(model) {
		t.Error("Model with old activity should scale to zero")
	}

	// Model with no activity timestamp should not scale
	model.Status.LastActiveTime = nil
	if shouldScaleToZero(model) {
		t.Error("Model with no activity timestamp should not scale to zero")
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

	model := &aiv1alpha2.Model{
		Spec: aiv1alpha2.ModelSpec{
			Backend: "mlc-llm",
			Source:  "HF://mlc-ai/Qwen3-8B-q4f16_1-MLC",
		},
	}

	spec := r.buildBackendModelSpec(model, backend.GPUVendorAMD)

	if spec.Model != "mlc-ai/Qwen3-8B-q4f16_1-MLC" {
		t.Errorf("Expected model 'mlc-ai/Qwen3-8B-q4f16_1-MLC', got %q", spec.Model)
	}

	if spec.GPUVendor != backend.GPUVendorAMD {
		t.Errorf("Expected GPU vendor AMD, got %v", spec.GPUVendor)
	}

	if spec.ModelPath != "/models" {
		t.Errorf("Expected model path '/models', got %q", spec.ModelPath)
	}
}
