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
	"os"
	"testing"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestGetBackendImage_MlcLlm_AMD(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				},
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "ghcr.io/mlc-ai/mlc-llm:rocm"
	if image != expected {
		t.Errorf("Expected %s, got %s", expected, image)
	}
}

func TestGetBackendImage_MlcLlm_NVIDIA(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				},
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "ghcr.io/mlc-ai/mlc-llm:cuda"
	if image != expected {
		t.Errorf("Expected %s, got %s", expected, image)
	}
}

func TestGetBackendImage_MlcLlm_Maxwell(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				},
			},
			NodeSelector: map[string]string{
				"nvidia.com/gpu.compute.major": "5",
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "registry.harbor.lan/flexinfer/mlc-llm:cuda-maxwell-v7"
	if image != expected {
		t.Errorf("Expected %s, got %s", expected, image)
	}
}

func TestGetBackendImage_MlcLlm_MaxwellArch(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				},
			},
			NodeSelector: map[string]string{
				"nvidia.com/gpu.arch": "Maxwell",
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "registry.harbor.lan/flexinfer/mlc-llm:cuda-maxwell-v7"
	if image != expected {
		t.Errorf("Expected %s, got %s", expected, image)
	}
}

func TestGetBackendImage_MlcLlm_EnvOverride(t *testing.T) {
	// Test AMD env override
	os.Setenv("DEFAULT_MLC_LLM_IMAGE_AMD", "custom/mlc:rocm-custom")
	defer os.Unsetenv("DEFAULT_MLC_LLM_IMAGE_AMD")

	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				},
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "custom/mlc:rocm-custom"
	if image != expected {
		t.Errorf("Expected %s, got %s", expected, image)
	}
}

func TestGetBackendImage_MlcLlm_MaxwellEnvOverride(t *testing.T) {
	os.Setenv("DEFAULT_MLC_LLM_IMAGE_MAXWELL", "custom/mlc:maxwell-custom")
	defer os.Unsetenv("DEFAULT_MLC_LLM_IMAGE_MAXWELL")

	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				},
			},
			NodeSelector: map[string]string{
				"nvidia.com/gpu.compute.major": "5",
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "custom/mlc:maxwell-custom"
	if image != expected {
		t.Errorf("Expected %s, got %s", expected, image)
	}
}

func TestGetBackendArgs_MlcLlm(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
			Model:   "Qwen3-14B-q4f16_1-MLC",
		},
	}

	args := r.getBackendArgs(m)

	// Verify expected arguments
	expectedArgs := []string{
		"serve",
		"Qwen3-14B-q4f16_1-MLC",
		"--host", "0.0.0.0",
		"--mode", "local",
		"--overrides", "prefill_chunk_size=512;max_total_seq_length=16384",
	}

	if len(args) != len(expectedArgs) {
		t.Errorf("Expected %d args, got %d", len(expectedArgs), len(args))
		return
	}

	for i, arg := range args {
		if arg != expectedArgs[i] {
			t.Errorf("Arg %d: expected %s, got %s", i, expectedArgs[i], arg)
		}
	}
}

func TestGetBackendArgs_MlcLlm_WithModelCache(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	cacheRef := "my-model-cache"
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:       "mlc-llm",
			Model:         "Qwen3-14B-q4f16_1-MLC",
			ModelCacheRef: &cacheRef,
		},
	}

	args := r.getBackendArgs(m)

	// When ModelCacheRef is set, model path should be /models
	if args[1] != "/models" {
		t.Errorf("Expected model path to be /models when ModelCacheRef is set, got %s", args[1])
	}
}

func TestGetBackendEnv_MlcLlm(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
		},
	}

	env := r.getBackendEnv(m)

	// Should have MLC_GPU_SIZE_BYTES
	if len(env) != 1 {
		t.Errorf("Expected 1 env var, got %d", len(env))
		return
	}

	if env[0].Name != "MLC_GPU_SIZE_BYTES" {
		t.Errorf("Expected MLC_GPU_SIZE_BYTES, got %s", env[0].Name)
	}

	if env[0].Value != "23068672000" {
		t.Errorf("Expected 23068672000, got %s", env[0].Value)
	}
}

func TestGetBackendEnv_Ollama(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "ollama",
		},
	}

	env := r.getBackendEnv(m)

	// Should have OLLAMA_HOST
	if len(env) != 1 {
		t.Errorf("Expected 1 env var, got %d", len(env))
		return
	}

	if env[0].Name != "OLLAMA_HOST" {
		t.Errorf("Expected OLLAMA_HOST, got %s", env[0].Name)
	}

	if env[0].Value != "0.0.0.0" {
		t.Errorf("Expected 0.0.0.0, got %s", env[0].Value)
	}
}

func TestGetBackendCommand_MlcLlm(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
		},
	}

	cmd := r.getBackendCommand(m)

	// Should return ["python", "-m", "mlc_llm"]
	expected := []string{"python", "-m", "mlc_llm"}
	if len(cmd) != len(expected) {
		t.Errorf("Expected %d command parts, got %d", len(expected), len(cmd))
		return
	}

	for i, part := range cmd {
		if part != expected[i] {
			t.Errorf("Command part %d: expected %s, got %s", i, expected[i], part)
		}
	}
}

func TestGetBackendCommand_Ollama(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "ollama",
		},
	}

	cmd := r.getBackendCommand(m)

	// Ollama uses default entrypoint, should return nil
	if cmd != nil {
		t.Errorf("Expected nil command for ollama, got %v", cmd)
	}
}

func TestGetBackendPort_MlcLlm(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
		},
	}

	port := r.getBackendPort(m)

	// MLC-LLM uses port 8000
	if port != 8000 {
		t.Errorf("Expected port 8000, got %d", port)
	}
}

func TestGetBackendPort_Ollama(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "ollama",
		},
	}

	port := r.getBackendPort(m)

	// Ollama uses port 11434
	if port != 11434 {
		t.Errorf("Expected port 11434, got %d", port)
	}
}

func TestIsMaxwellGPU(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	tests := []struct {
		name         string
		nodeSelector map[string]string
		expected     bool
	}{
		{
			name:         "nil node selector",
			nodeSelector: nil,
			expected:     false,
		},
		{
			name:         "empty node selector",
			nodeSelector: map[string]string{},
			expected:     false,
		},
		{
			name: "compute major 5",
			nodeSelector: map[string]string{
				"nvidia.com/gpu.compute.major": "5",
			},
			expected: true,
		},
		{
			name: "compute major 6 (Pascal)",
			nodeSelector: map[string]string{
				"nvidia.com/gpu.compute.major": "6",
			},
			expected: false,
		},
		{
			name: "arch Maxwell",
			nodeSelector: map[string]string{
				"nvidia.com/gpu.arch": "Maxwell",
			},
			expected: true,
		},
		{
			name: "arch maxwell lowercase",
			nodeSelector: map[string]string{
				"nvidia.com/gpu.arch": "maxwell",
			},
			expected: true,
		},
		{
			name: "arch Pascal",
			nodeSelector: map[string]string{
				"nvidia.com/gpu.arch": "Pascal",
			},
			expected: false,
		},
		{
			name: "other selectors only",
			nodeSelector: map[string]string{
				"kubernetes.io/hostname": "node1",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					NodeSelector: tt.nodeSelector,
				},
			}

			result := r.isMaxwellGPU(m)
			if result != tt.expected {
				t.Errorf("isMaxwellGPU() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestCanonicalBackend(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"mlc-llm", "mlc-llm"},
		{"mlc", "mlc-llm"},
		{"MLC", "mlc-llm"},
		{"llama.cpp", "llamacpp"},
		{"ollama", "ollama"},
		{"vllm", "vllm"},
		{"VLLM", "VLLM"}, // Only specific aliases are normalized
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := canonicalBackend(tt.input)
			if result != tt.expected {
				t.Errorf("canonicalBackend(%s) = %s, expected %s", tt.input, result, tt.expected)
			}
		})
	}
}
