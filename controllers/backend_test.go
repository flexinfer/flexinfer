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
	"strings"
	"testing"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
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

func TestGetBackendImage_VLLM_NVIDIA(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "vllm",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				},
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "vllm/vllm-openai:latest"
	if image != expected {
		t.Errorf("Expected %s, got %s", expected, image)
	}
}

func TestGetBackendImage_VLLM_AMD(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "vllm",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				},
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "registry.harbor.lan/library/vllm-api:rocm-navi"
	if image != expected {
		t.Errorf("Expected %s, got %s", expected, image)
	}
}

func TestGetBackendImage_VLLM_EnvOverride(t *testing.T) {
	os.Setenv("DEFAULT_VLLM_IMAGE", "custom/vllm:cuda-custom")
	defer os.Unsetenv("DEFAULT_VLLM_IMAGE")

	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "vllm",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				},
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "custom/vllm:cuda-custom"
	if image != expected {
		t.Errorf("Expected %s, got %s", expected, image)
	}
}

func TestGetBackendImage_VLLM_AMD_EnvOverride(t *testing.T) {
	os.Setenv("DEFAULT_VLLM_IMAGE_AMD", "custom/vllm:rocm-custom")
	defer os.Unsetenv("DEFAULT_VLLM_IMAGE_AMD")

	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "vllm",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				},
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "custom/vllm:rocm-custom"
	if image != expected {
		t.Errorf("Expected %s, got %s", expected, image)
	}
}

func TestGetBackendImage_LlamaCpp_NVIDIA(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "llamacpp",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				},
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "ghcr.io/ggerganov/llama.cpp:server-cuda"
	if image != expected {
		t.Errorf("Expected %s, got %s", expected, image)
	}
}

func TestGetBackendImage_LlamaCpp_AMD(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "llamacpp",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				},
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "ghcr.io/ggerganov/llama.cpp:server-rocm"
	if image != expected {
		t.Errorf("Expected %s, got %s", expected, image)
	}
}

func TestGetBackendImage_LlamaCpp_EnvOverride(t *testing.T) {
	os.Setenv("DEFAULT_LLAMA_CPP_IMAGE", "custom/llamacpp:cuda-custom")
	defer os.Unsetenv("DEFAULT_LLAMA_CPP_IMAGE")

	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "llamacpp",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				},
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "custom/llamacpp:cuda-custom"
	if image != expected {
		t.Errorf("Expected %s, got %s", expected, image)
	}
}

func TestGetBackendImage_LlamaCpp_AMD_EnvOverride(t *testing.T) {
	os.Setenv("DEFAULT_LLAMA_CPP_IMAGE_AMD", "custom/llamacpp:rocm-custom")
	defer os.Unsetenv("DEFAULT_LLAMA_CPP_IMAGE_AMD")

	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "llamacpp",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				},
			},
		},
	}

	image := r.getBackendImage(m)
	expected := "custom/llamacpp:rocm-custom"
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

	// Verify expected arguments (default mode, no model-lib, default overrides)
	expectedArgs := []string{
		"serve",
		"Qwen3-14B-q4f16_1-MLC",
		"--host", "0.0.0.0",
		"--mode", "local",
		"--overrides", "prefill_chunk_size=512;max_total_seq_length=16384",
	}

	if len(args) != len(expectedArgs) {
		t.Errorf("Expected %d args, got %d: %v", len(expectedArgs), len(args), args)
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

func TestGetMLCMode(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	tests := []struct {
		name     string
		mlcllm   *aiv1alpha1.MLCLLMSpec
		envValue string
		expected string
	}{
		{
			name:     "default mode",
			mlcllm:   nil,
			expected: "local",
		},
		{
			name: "CRD mode takes precedence",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				Mode: "server",
			},
			expected: "server",
		},
		{
			name:     "env var mode",
			mlcllm:   nil,
			envValue: "interactive",
			expected: "interactive",
		},
		{
			name: "CRD overrides env var",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				Mode: "server",
			},
			envValue: "interactive",
			expected: "server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv("DEFAULT_MLC_LLM_MODE", tt.envValue)
				defer os.Unsetenv("DEFAULT_MLC_LLM_MODE")
			}

			m := &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "mlc-llm",
					MLCLLM:  tt.mlcllm,
				},
			}

			result := r.getMLCMode(m)
			if result != tt.expected {
				t.Errorf("getMLCMode() = %s, expected %s", result, tt.expected)
			}
		})
	}
}

func TestGetMLCModelLib(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	tests := []struct {
		name     string
		mlcllm   *aiv1alpha1.MLCLLMSpec
		envValue string
		expected string
	}{
		{
			name:     "no model lib",
			mlcllm:   nil,
			expected: "",
		},
		{
			name: "CRD model lib",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				ModelLibPath: "/models/maxwell-lib.so",
			},
			expected: "/models/maxwell-lib.so",
		},
		{
			name:     "env var model lib",
			mlcllm:   nil,
			envValue: "/default/lib.so",
			expected: "/default/lib.so",
		},
		{
			name: "CRD overrides env var",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				ModelLibPath: "/models/custom.so",
			},
			envValue: "/default/lib.so",
			expected: "/models/custom.so",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv("DEFAULT_MLC_LLM_MODEL_LIB", tt.envValue)
				defer os.Unsetenv("DEFAULT_MLC_LLM_MODEL_LIB")
			}

			m := &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "mlc-llm",
					MLCLLM:  tt.mlcllm,
				},
			}

			result := r.getMLCModelLib(m)
			if result != tt.expected {
				t.Errorf("getMLCModelLib() = %s, expected %s", result, tt.expected)
			}
		})
	}
}

func TestGetMLCGPUMemory(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	tests := []struct {
		name         string
		mlcllm       *aiv1alpha1.MLCLLMSpec
		nodeSelector map[string]string
		envValue     string
		expected     string
	}{
		{
			name:     "default GPU memory",
			mlcllm:   nil,
			expected: "23068672000",
		},
		{
			name: "CRD GPU memory",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				GPUMemoryBytes: ptrTo(int64(10000000000)),
			},
			expected: "10000000000",
		},
		{
			name:     "env var GPU memory",
			mlcllm:   nil,
			envValue: "8000000000",
			expected: "8000000000",
		},
		{
			name:   "Maxwell auto-detect",
			mlcllm: nil,
			nodeSelector: map[string]string{
				"nvidia.com/gpu.arch": "Maxwell",
			},
			expected: "5000000000",
		},
		{
			name: "CRD overrides Maxwell auto-detect",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				GPUMemoryBytes: ptrTo(int64(4000000000)),
			},
			nodeSelector: map[string]string{
				"nvidia.com/gpu.arch": "Maxwell",
			},
			expected: "4000000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv("DEFAULT_MLC_GPU_SIZE_BYTES", tt.envValue)
				defer os.Unsetenv("DEFAULT_MLC_GPU_SIZE_BYTES")
			}

			m := &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend:      "mlc-llm",
					MLCLLM:       tt.mlcllm,
					NodeSelector: tt.nodeSelector,
				},
			}

			result := r.getMLCGPUMemory(m)
			if result != tt.expected {
				t.Errorf("getMLCGPUMemory() = %s, expected %s", result, tt.expected)
			}
		})
	}
}

func TestGetMLCJITPolicy(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	tests := []struct {
		name     string
		mlcllm   *aiv1alpha1.MLCLLMSpec
		envValue string
		expected string
	}{
		{
			name:     "no JIT policy",
			mlcllm:   nil,
			expected: "",
		},
		{
			name: "CRD JIT policy",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				JITPolicy: "OFF",
			},
			expected: "OFF",
		},
		{
			name:     "env var JIT policy",
			mlcllm:   nil,
			envValue: "READONLY",
			expected: "READONLY",
		},
		{
			name: "CRD overrides env var",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				JITPolicy: "OFF",
			},
			envValue: "READONLY",
			expected: "OFF",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv("DEFAULT_MLC_JIT_POLICY", tt.envValue)
				defer os.Unsetenv("DEFAULT_MLC_JIT_POLICY")
			}

			m := &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "mlc-llm",
					MLCLLM:  tt.mlcllm,
				},
			}

			result := r.getMLCJITPolicy(m)
			if result != tt.expected {
				t.Errorf("getMLCJITPolicy() = %s, expected %s", result, tt.expected)
			}
		})
	}
}

func TestBuildMLCOverrides(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	tests := []struct {
		name     string
		mlcllm   *aiv1alpha1.MLCLLMSpec
		expected string
	}{
		{
			name:     "default overrides",
			mlcllm:   nil,
			expected: "prefill_chunk_size=512;max_total_seq_length=16384",
		},
		{
			name: "custom prefill chunk size",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				Overrides: &aiv1alpha1.MLCOverrides{
					PrefillChunkSize: ptrTo(int32(256)),
				},
			},
			expected: "prefill_chunk_size=256;max_total_seq_length=16384",
		},
		{
			name: "custom max total seq length",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				Overrides: &aiv1alpha1.MLCOverrides{
					MaxTotalSeqLength: ptrTo(int32(8192)),
				},
			},
			expected: "prefill_chunk_size=512;max_total_seq_length=8192",
		},
		{
			name: "with context window size",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				Overrides: &aiv1alpha1.MLCOverrides{
					ContextWindowSize: ptrTo(int32(4096)),
				},
			},
			expected: "context_window_size=4096;prefill_chunk_size=512;max_total_seq_length=16384",
		},
		{
			name: "with raw overrides",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				Overrides: &aiv1alpha1.MLCOverrides{
					Raw: "temperature=0.7",
				},
			},
			expected: "prefill_chunk_size=512;max_total_seq_length=16384;temperature=0.7",
		},
		{
			name: "all options combined",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				Overrides: &aiv1alpha1.MLCOverrides{
					PrefillChunkSize:  ptrTo(int32(256)),
					MaxTotalSeqLength: ptrTo(int32(4096)),
					ContextWindowSize: ptrTo(int32(2048)),
					Raw:               "top_p=0.9",
				},
			},
			expected: "context_window_size=2048;prefill_chunk_size=256;max_total_seq_length=4096;top_p=0.9",
		},
		{
			name: "with max_num_sequence",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				Overrides: &aiv1alpha1.MLCOverrides{
					MaxNumSequence: ptrTo(int32(2)),
				},
			},
			expected: "max_num_sequence=2;prefill_chunk_size=512;max_total_seq_length=16384",
		},
		{
			name: "with gpu_memory_utilization",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				Overrides: &aiv1alpha1.MLCOverrides{
					GPUMemoryUtilization: "0.85",
				},
			},
			expected: "gpu_memory_utilization=0.85;prefill_chunk_size=512;max_total_seq_length=16384",
		},
		{
			name: "server mode optimized config",
			mlcllm: &aiv1alpha1.MLCLLMSpec{
				Mode: "server",
				Overrides: &aiv1alpha1.MLCOverrides{
					MaxNumSequence:       ptrTo(int32(2)),
					MaxTotalSeqLength:    ptrTo(int32(131072)),
					PrefillChunkSize:     ptrTo(int32(2048)),
					GPUMemoryUtilization: "0.85",
				},
			},
			expected: "max_num_sequence=2;gpu_memory_utilization=0.85;prefill_chunk_size=2048;max_total_seq_length=131072",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "mlc-llm",
					MLCLLM:  tt.mlcllm,
				},
			}

			result := r.buildMLCOverrides(m)
			if result != tt.expected {
				t.Errorf("buildMLCOverrides() = %s, expected %s", result, tt.expected)
			}
		})
	}
}

func TestGetBackendArgs_MlcLlm_WithMLCLLMSpec(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
			Model:   "Qwen3-0.6B-q0f32-MLC",
			MLCLLM: &aiv1alpha1.MLCLLMSpec{
				Mode:         "server",
				ModelLibPath: "/models/maxwell-lib.so",
				Overrides: &aiv1alpha1.MLCOverrides{
					PrefillChunkSize:  ptrTo(int32(256)),
					MaxTotalSeqLength: ptrTo(int32(4096)),
				},
			},
		},
	}

	args := r.getBackendArgs(m)

	// Verify expected arguments
	expectedArgs := []string{
		"serve",
		"Qwen3-0.6B-q0f32-MLC",
		"--host", "0.0.0.0",
		"--mode", "server",
		"--model-lib", "/models/maxwell-lib.so",
		"--overrides", "prefill_chunk_size=256;max_total_seq_length=4096",
	}

	if len(args) != len(expectedArgs) {
		t.Errorf("Expected %d args, got %d: %v", len(expectedArgs), len(args), args)
		return
	}

	for i, arg := range args {
		if arg != expectedArgs[i] {
			t.Errorf("Arg %d: expected %s, got %s", i, expectedArgs[i], arg)
		}
	}
}

func TestGetBackendEnv_MlcLlm_WithMLCLLMSpec(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	gpuMem := int64(5000000000)
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
			MLCLLM: &aiv1alpha1.MLCLLMSpec{
				GPUMemoryBytes: &gpuMem,
				JITPolicy:      "OFF",
			},
		},
	}

	env := r.getBackendEnv(m)

	// Should have 2 env vars: MLC_GPU_SIZE_BYTES and MLC_JIT_POLICY
	if len(env) != 2 {
		t.Errorf("Expected 2 env vars, got %d", len(env))
		return
	}

	// Check MLC_GPU_SIZE_BYTES
	if env[0].Name != "MLC_GPU_SIZE_BYTES" || env[0].Value != "5000000000" {
		t.Errorf("Expected MLC_GPU_SIZE_BYTES=5000000000, got %s=%s", env[0].Name, env[0].Value)
	}

	// Check MLC_JIT_POLICY
	if env[1].Name != "MLC_JIT_POLICY" || env[1].Value != "OFF" {
		t.Errorf("Expected MLC_JIT_POLICY=OFF, got %s=%s", env[1].Name, env[1].Value)
	}
}

func TestGetBackendEnv_MlcLlm_MaxwellAutoDetect(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
			NodeSelector: map[string]string{
				"nvidia.com/gpu.compute.major": "5",
			},
		},
	}

	env := r.getBackendEnv(m)

	// Should have 1 env var with Maxwell auto-detected GPU memory
	if len(env) != 1 {
		t.Errorf("Expected 1 env var, got %d", len(env))
		return
	}

	if env[0].Name != "MLC_GPU_SIZE_BYTES" || env[0].Value != "5000000000" {
		t.Errorf("Expected MLC_GPU_SIZE_BYTES=5000000000 for Maxwell, got %s=%s", env[0].Name, env[0].Value)
	}
}

// ptrTo is a helper function to create a pointer to a value
func ptrTo[T any](v T) *T {
	return &v
}

func TestBuildVLLMArgs(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	tests := []struct {
		name     string
		vllm     *aiv1alpha1.VLLMSpec
		expected []string
	}{
		{
			name:     "default args",
			vllm:     nil,
			expected: []string{"--model", "test-model", "--host", "0.0.0.0"},
		},
		{
			name: "tensor parallel",
			vllm: &aiv1alpha1.VLLMSpec{
				TensorParallelSize: ptrTo(int32(2)),
			},
			expected: []string{"--model", "test-model", "--host", "0.0.0.0", "--tensor-parallel-size", "2"},
		},
		{
			name: "dtype and quantization",
			vllm: &aiv1alpha1.VLLMSpec{
				Dtype:        "float16",
				Quantization: "awq",
			},
			expected: []string{"--model", "test-model", "--host", "0.0.0.0", "--dtype", "float16", "--quantization", "awq"},
		},
		{
			name: "max model len and memory",
			vllm: &aiv1alpha1.VLLMSpec{
				MaxModelLen:          ptrTo(int32(8192)),
				GPUMemoryUtilization: ptrTo("0.8"),
			},
			expected: []string{"--model", "test-model", "--host", "0.0.0.0", "--max-model-len", "8192", "--gpu-memory-utilization", "0.8"},
		},
		{
			name: "enforce eager and trust remote code",
			vllm: &aiv1alpha1.VLLMSpec{
				EnforceEager:    ptrTo(true),
				TrustRemoteCode: ptrTo(true),
			},
			expected: []string{"--model", "test-model", "--host", "0.0.0.0", "--enforce-eager", "--trust-remote-code"},
		},
		{
			name: "all options",
			vllm: &aiv1alpha1.VLLMSpec{
				TensorParallelSize:   ptrTo(int32(4)),
				Dtype:                "bfloat16",
				MaxModelLen:          ptrTo(int32(4096)),
				GPUMemoryUtilization: ptrTo("0.9"),
				MaxNumSeqs:           ptrTo(int32(256)),
				SwapSpace:            ptrTo(int32(4)),
			},
			expected: []string{
				"--model", "test-model", "--host", "0.0.0.0",
				"--tensor-parallel-size", "4",
				"--dtype", "bfloat16",
				"--max-model-len", "4096",
				"--gpu-memory-utilization", "0.9",
				"--max-num-seqs", "256",
				"--swap-space", "4",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "vllm",
					VLLM:    tt.vllm,
				},
			}

			result := r.buildVLLMArgs(m, "test-model")
			if len(result) != len(tt.expected) {
				t.Errorf("buildVLLMArgs() got %d args, expected %d: %v", len(result), len(tt.expected), result)
				return
			}
			for i, arg := range result {
				if arg != tt.expected[i] {
					t.Errorf("Arg %d: expected %s, got %s", i, tt.expected[i], arg)
				}
			}
		})
	}
}

func TestBuildVLLMArgs_AMDOptimizations(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	tests := []struct {
		name        string
		vllm        *aiv1alpha1.VLLMSpec
		gpuResource corev1.ResourceName
		expected    []string
	}{
		{
			name:        "AMD GPU no default attention backend (nil spec)",
			vllm:        nil,
			gpuResource: GPUResourceAMD,
			expected:    []string{"--model", "test-model", "--host", "0.0.0.0"},
		},
		{
			name:        "AMD GPU no default attention backend (empty spec)",
			vllm:        &aiv1alpha1.VLLMSpec{},
			gpuResource: GPUResourceAMD,
			expected:    []string{"--model", "test-model", "--host", "0.0.0.0"},
		},
		{
			name:        "NVIDIA GPU no default attention backend",
			vllm:        nil,
			gpuResource: GPUResourceNVIDIA,
			expected:    []string{"--model", "test-model", "--host", "0.0.0.0"},
		},
		{
			name: "prefix caching enabled",
			vllm: &aiv1alpha1.VLLMSpec{
				EnablePrefixCaching: ptrTo(true),
			},
			gpuResource: GPUResourceNVIDIA,
			expected:    []string{"--model", "test-model", "--host", "0.0.0.0", "--enable-prefix-caching"},
		},
		{
			name: "KV cache dtype int8 (alias to fp8 on AMD)",
			vllm: &aiv1alpha1.VLLMSpec{
				KVCacheDtype: "int8",
			},
			gpuResource: GPUResourceAMD,
			expected:    []string{"--model", "test-model", "--host", "0.0.0.0", "--kv-cache-dtype", "fp8", "--calculate-kv-scales"},
		},
		{
			name: "KV cache dtype fp8 with scales disabled",
			vllm: &aiv1alpha1.VLLMSpec{
				KVCacheDtype:      "fp8",
				CalculateKVScales: ptrTo(false),
			},
			gpuResource: GPUResourceAMD,
			expected:    []string{"--model", "test-model", "--host", "0.0.0.0", "--kv-cache-dtype", "fp8", "--no-calculate-kv-scales"},
		},
		{
			name: "explicit attention backend for AMD GPU",
			vllm: &aiv1alpha1.VLLMSpec{
				AttentionBackend: "ROCM_FLASH",
			},
			gpuResource: GPUResourceAMD,
			expected:    []string{"--model", "test-model", "--host", "0.0.0.0", "--attention-backend", "ROCM_FLASH"},
		},
		{
			name: "CPU offload",
			vllm: &aiv1alpha1.VLLMSpec{
				CPUOffloadGB: ptrTo(int32(8)),
			},
			gpuResource: GPUResourceNVIDIA,
			expected:    []string{"--model", "test-model", "--host", "0.0.0.0", "--cpu-offload-gb", "8"},
		},
		{
			name: "chunked prefill enabled",
			vllm: &aiv1alpha1.VLLMSpec{
				EnableChunkedPrefill: ptrTo(true),
			},
			gpuResource: GPUResourceNVIDIA,
			expected:    []string{"--model", "test-model", "--host", "0.0.0.0", "--enable-chunked-prefill"},
		},
		{
			name: "block size tuning",
			vllm: &aiv1alpha1.VLLMSpec{
				BlockSize: ptrTo(int32(8)),
			},
			gpuResource: GPUResourceNVIDIA,
			expected:    []string{"--model", "test-model", "--host", "0.0.0.0", "--block-size", "8"},
		},
		{
			name: "RoPE scaling linear",
			vllm: &aiv1alpha1.VLLMSpec{
				RopeScaling: &aiv1alpha1.VLLMRopeScaling{
					Type:   "linear",
					Factor: "2.0",
				},
			},
			gpuResource: GPUResourceNVIDIA,
			expected:    []string{"--model", "test-model", "--host", "0.0.0.0", "--rope-scaling-type", "linear", "--rope-scaling-factor", "2.0"},
		},
		{
			name: "full AMD optimization stack",
			vllm: &aiv1alpha1.VLLMSpec{
				EnablePrefixCaching:  ptrTo(true),
				KVCacheDtype:         "int8",
				EnableChunkedPrefill: ptrTo(true),
				BlockSize:            ptrTo(int32(16)),
				GPUMemoryUtilization: ptrTo("0.85"),
			},
			gpuResource: GPUResourceAMD,
			expected: []string{
				"--model", "test-model", "--host", "0.0.0.0",
				"--gpu-memory-utilization", "0.85",
				"--enable-prefix-caching",
				"--kv-cache-dtype", "fp8",
				"--calculate-kv-scales",
				"--enable-chunked-prefill",
				"--block-size", "16",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "vllm",
					VLLM:    tt.vllm,
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							tt.gpuResource: resource.MustParse("1"),
						},
					},
				},
			}

			result := r.buildVLLMArgs(m, "test-model")
			if len(result) != len(tt.expected) {
				t.Errorf("buildVLLMArgs() got %d args, expected %d\ngot: %v\nexpected: %v",
					len(result), len(tt.expected), result, tt.expected)
				return
			}
			for i, arg := range result {
				if arg != tt.expected[i] {
					t.Errorf("Arg %d: expected %s, got %s", i, tt.expected[i], arg)
				}
			}
		})
	}
}

func TestValidateVLLMSpecCompatibility(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	tests := []struct {
		name        string
		vllm        *aiv1alpha1.VLLMSpec
		gpuResource corev1.ResourceName
		expectError bool
		errorMsg    string
	}{
		{
			name:        "nil spec is valid",
			vllm:        nil,
			gpuResource: GPUResourceNVIDIA,
			expectError: false,
		},
		{
			name:        "empty spec is valid",
			vllm:        &aiv1alpha1.VLLMSpec{},
			gpuResource: GPUResourceAMD,
			expectError: false,
		},
		{
			name: "RoPE type without factor fails",
			vllm: &aiv1alpha1.VLLMSpec{
				RopeScaling: &aiv1alpha1.VLLMRopeScaling{
					Type: "linear",
				},
			},
			gpuResource: GPUResourceNVIDIA,
			expectError: true,
			errorMsg:    "vllm.ropeScaling.factor is required",
		},
		{
			name: "RoPE factor without type fails",
			vllm: &aiv1alpha1.VLLMSpec{
				RopeScaling: &aiv1alpha1.VLLMRopeScaling{
					Factor: "2.0",
				},
			},
			gpuResource: GPUResourceNVIDIA,
			expectError: true,
			errorMsg:    "vllm.ropeScaling.type is required",
		},
		{
			name: "RoPE with both type and factor is valid",
			vllm: &aiv1alpha1.VLLMSpec{
				RopeScaling: &aiv1alpha1.VLLMRopeScaling{
					Type:   "linear",
					Factor: "2.0",
				},
			},
			gpuResource: GPUResourceNVIDIA,
			expectError: false,
		},
		{
			name: "CPU offload with tensor parallelism fails",
			vllm: &aiv1alpha1.VLLMSpec{
				CPUOffloadGB:       ptrTo(int32(8)),
				TensorParallelSize: ptrTo(int32(2)),
			},
			gpuResource: GPUResourceNVIDIA,
			expectError: true,
			errorMsg:    "vllm.cpuOffloadGB is not compatible with tensor parallelism",
		},
		{
			name: "CPU offload without tensor parallelism is valid",
			vllm: &aiv1alpha1.VLLMSpec{
				CPUOffloadGB: ptrTo(int32(8)),
			},
			gpuResource: GPUResourceNVIDIA,
			expectError: false,
		},
		{
			name: "full AMD config is valid",
			vllm: &aiv1alpha1.VLLMSpec{
				EnablePrefixCaching:  ptrTo(true),
				KVCacheDtype:         "int8",
				EnableChunkedPrefill: ptrTo(true),
				RopeScaling: &aiv1alpha1.VLLMRopeScaling{
					Type:   "linear",
					Factor: "2.0",
				},
			},
			gpuResource: GPUResourceAMD,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "vllm",
					VLLM:    tt.vllm,
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							tt.gpuResource: resource.MustParse("1"),
						},
					},
				},
			}

			err := r.validateVLLMSpecCompatibility(m)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error containing '%s', got nil", tt.errorMsg)
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error containing '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got '%s'", err.Error())
				}
			}
		})
	}
}

func TestBuildLlamaCppArgs(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	tests := []struct {
		name     string
		llamacpp *aiv1alpha1.LlamaCppSpec
		expected []string
	}{
		{
			name:     "default args",
			llamacpp: nil,
			expected: []string{"--model", "test-model", "--host", "0.0.0.0"},
		},
		{
			name: "context size and GPU layers",
			llamacpp: &aiv1alpha1.LlamaCppSpec{
				ContextSize: ptrTo(int32(4096)),
				NGPULayers:  ptrTo(int32(35)),
			},
			expected: []string{"--model", "test-model", "--host", "0.0.0.0", "--ctx-size", "4096", "--n-gpu-layers", "35"},
		},
		{
			name: "batch size and threads",
			llamacpp: &aiv1alpha1.LlamaCppSpec{
				BatchSize: ptrTo(int32(512)),
				Threads:   ptrTo(int32(8)),
			},
			expected: []string{"--model", "test-model", "--host", "0.0.0.0", "--batch-size", "512", "--threads", "8"},
		},
		{
			name: "flash attention",
			llamacpp: &aiv1alpha1.LlamaCppSpec{
				FlashAttention: ptrTo(true),
			},
			expected: []string{"--model", "test-model", "--host", "0.0.0.0", "--flash-attn"},
		},
		{
			name: "main GPU and RoPE settings",
			llamacpp: &aiv1alpha1.LlamaCppSpec{
				MainGPU:       ptrTo(int32(0)),
				RopeFreqBase:  "10000.0",
				RopeFreqScale: "1.0",
			},
			expected: []string{"--model", "test-model", "--host", "0.0.0.0", "--main-gpu", "0", "--rope-freq-base", "10000.0", "--rope-freq-scale", "1.0"},
		},
		{
			name: "full GPU offload",
			llamacpp: &aiv1alpha1.LlamaCppSpec{
				ContextSize:    ptrTo(int32(8192)),
				NGPULayers:     ptrTo(int32(999)),
				FlashAttention: ptrTo(true),
				BatchSize:      ptrTo(int32(1024)),
			},
			expected: []string{
				"--model", "test-model", "--host", "0.0.0.0",
				"--ctx-size", "8192",
				"--n-gpu-layers", "999",
				"--batch-size", "1024",
				"--flash-attn",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend:  "llamacpp",
					LlamaCpp: tt.llamacpp,
				},
			}

			result := r.buildLlamaCppArgs(m, "test-model")
			if len(result) != len(tt.expected) {
				t.Errorf("buildLlamaCppArgs() got %d args, expected %d: %v", len(result), len(tt.expected), result)
				return
			}
			for i, arg := range result {
				if arg != tt.expected[i] {
					t.Errorf("Arg %d: expected %s, got %s", i, tt.expected[i], arg)
				}
			}
		})
	}
}

func TestGetBackendArgs_VLLM(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "vllm",
			Model:   "meta-llama/Llama-2-7b-hf",
			VLLM: &aiv1alpha1.VLLMSpec{
				Dtype:                "float16",
				MaxModelLen:          ptrTo(int32(4096)),
				GPUMemoryUtilization: ptrTo("0.9"),
			},
		},
	}

	args := r.getBackendArgs(m)

	// Should use buildVLLMArgs
	if len(args) < 4 {
		t.Errorf("Expected at least 4 args, got %d: %v", len(args), args)
		return
	}

	if args[0] != "--model" || args[1] != "meta-llama/Llama-2-7b-hf" {
		t.Errorf("Expected --model meta-llama/Llama-2-7b-hf, got %s %s", args[0], args[1])
	}
}

func TestGetBackendArgs_LlamaCpp(t *testing.T) {
	r := &ModelDeploymentReconciler{}

	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "llamacpp",
			Model:   "llama-2-7b.gguf",
			LlamaCpp: &aiv1alpha1.LlamaCppSpec{
				ContextSize: ptrTo(int32(4096)),
				NGPULayers:  ptrTo(int32(35)),
			},
		},
	}

	args := r.getBackendArgs(m)

	// Should use buildLlamaCppArgs
	if len(args) < 4 {
		t.Errorf("Expected at least 4 args, got %d: %v", len(args), args)
		return
	}

	if args[0] != "--model" || args[1] != "llama-2-7b.gguf" {
		t.Errorf("Expected --model llama-2-7b.gguf, got %s %s", args[0], args[1])
	}
}

// --- OCI Source Tests ---

func TestIsOCISource(t *testing.T) {
	tests := []struct {
		source   string
		expected bool
	}{
		{"oci://registry.example.com/models/llama3:v1", true},
		{"oras://harbor.lan/models/model@sha256:abc", true},
		{"huggingface://meta-llama/Llama-2-7b", false},
		{"mlc://mlc-ai/Qwen3-0.6B-q4f16_1-MLC", false},
		{"HF://mlc-ai/model", false},
		{"oci://ghcr.io/flexinfer/models/qwen:latest", true},
		{"", false},
	}
	for _, tt := range tests {
		if got := isOCISource(tt.source); got != tt.expected {
			t.Errorf("isOCISource(%q) = %v, want %v", tt.source, got, tt.expected)
		}
	}
}

func TestParseOCISource(t *testing.T) {
	tests := []struct {
		source   string
		expected string
	}{
		{"oci://registry.example.com/models/llama3:v1", "registry.example.com/models/llama3:v1"},
		{"oras://harbor.lan/models/model@sha256:abc", "harbor.lan/models/model@sha256:abc"},
		{"oci://ghcr.io/flexinfer/models/qwen:latest", "ghcr.io/flexinfer/models/qwen:latest"},
	}
	for _, tt := range tests {
		if got := parseOCISource(tt.source); got != tt.expected {
			t.Errorf("parseOCISource(%q) = %q, want %q", tt.source, got, tt.expected)
		}
	}
}

func TestJobForOCIDownload_NoAuth(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	r := &ModelCacheReconciler{Scheme: scheme}
	m := &aiv1alpha1.ModelCache{
		Spec: aiv1alpha1.ModelCacheSpec{
			Source: "oci://ghcr.io/flexinfer/models/llama3:v1",
		},
	}
	m.Name = "test-cache"
	m.Namespace = "default"

	job, err := r.jobForOCIDownload(m, "test-pvc", "test-model")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check job name
	if job.Name != "test-cache-downloader" {
		t.Errorf("Expected job name 'test-cache-downloader', got %s", job.Name)
	}

	// Check container image
	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != "ghcr.io/oras-project/oras:v1.2.2" {
		t.Errorf("Expected ORAS image, got %s", container.Image)
	}

	// Check volume count - should only have model-store, no docker-config
	if len(job.Spec.Template.Spec.Volumes) != 1 {
		t.Errorf("Expected 1 volume (model-store), got %d", len(job.Spec.Template.Spec.Volumes))
	}

	// Check volume mounts - should only have model-store
	if len(container.VolumeMounts) != 1 {
		t.Errorf("Expected 1 volume mount, got %d", len(container.VolumeMounts))
	}
}

func TestJobForOCIDownload_WithAuth(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	r := &ModelCacheReconciler{Scheme: scheme}
	secretName := "harbor-creds"
	m := &aiv1alpha1.ModelCache{
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:               "oci://registry.harbor.lan/models/llama3:v1",
			OCIRegistrySecretRef: &secretName,
		},
	}
	m.Name = "test-cache"
	m.Namespace = "default"

	job, err := r.jobForOCIDownload(m, "test-pvc", "test-model")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check volume count - should have model-store and docker-config
	if len(job.Spec.Template.Spec.Volumes) != 2 {
		t.Errorf("Expected 2 volumes (model-store, docker-config), got %d", len(job.Spec.Template.Spec.Volumes))
	}

	// Check for docker-config volume
	foundDockerConfig := false
	for _, vol := range job.Spec.Template.Spec.Volumes {
		if vol.Name == "docker-config" {
			foundDockerConfig = true
			if vol.Secret == nil || vol.Secret.SecretName != "harbor-creds" {
				t.Error("docker-config volume should reference harbor-creds secret")
			}
		}
	}
	if !foundDockerConfig {
		t.Error("Expected docker-config volume to be present")
	}

	// Check volume mounts
	container := job.Spec.Template.Spec.Containers[0]
	if len(container.VolumeMounts) != 2 {
		t.Errorf("Expected 2 volume mounts, got %d", len(container.VolumeMounts))
	}

	// Check docker-config mount
	foundDockerMount := false
	for _, mount := range container.VolumeMounts {
		if mount.Name == "docker-config" {
			foundDockerMount = true
			if mount.MountPath != "/root/.docker" {
				t.Errorf("docker-config should mount at /root/.docker, got %s", mount.MountPath)
			}
			if !mount.ReadOnly {
				t.Error("docker-config mount should be read-only")
			}
		}
	}
	if !foundDockerMount {
		t.Error("Expected docker-config volume mount to be present")
	}
}

func TestJobForOCIDownload_EnvOverride(t *testing.T) {
	// Set custom ORAS image
	os.Setenv("ORAS_DOWNLOADER_IMAGE", "my-registry/custom-oras:v2.0.0")
	defer os.Unsetenv("ORAS_DOWNLOADER_IMAGE")

	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	r := &ModelCacheReconciler{Scheme: scheme}
	m := &aiv1alpha1.ModelCache{
		Spec: aiv1alpha1.ModelCacheSpec{
			Source: "oci://ghcr.io/flexinfer/models/llama3:v1",
		},
	}
	m.Name = "test-cache"
	m.Namespace = "default"

	job, err := r.jobForOCIDownload(m, "test-pvc", "test-model")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	expected := "my-registry/custom-oras:v2.0.0"
	if container.Image != expected {
		t.Errorf("Expected image %s from env override, got %s", expected, container.Image)
	}
}

// ===== NodeLocal DaemonSet Tests =====

func TestDaemonSetForNodeLocal_HuggingFace(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	r := &ModelCacheReconciler{Scheme: scheme}
	m := &aiv1alpha1.ModelCache{
		Spec: aiv1alpha1.ModelCacheSpec{
			Source: "huggingface://meta-llama/Llama-2-7b-chat-hf",
		},
	}
	m.Name = "llama2-local"
	m.Namespace = "default"

	ds, err := r.daemonSetForNodeLocal(m, "/var/lib/flexinfer/models/llama2-local", "/var/lib/flexinfer/models")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check DaemonSet name
	if ds.Name != "llama2-local-syncer" {
		t.Errorf("Expected DaemonSet name 'llama2-local-syncer', got %s", ds.Name)
	}

	// Check container image - should use python for HuggingFace
	container := ds.Spec.Template.Spec.Containers[0]
	if container.Image != "python:3.10-slim" {
		t.Errorf("Expected python:3.10-slim for HuggingFace source, got %s", container.Image)
	}

	// Check download script uses snapshot_download (huggingface_hub)
	if len(container.Args) == 0 || !strings.Contains(container.Args[0], "snapshot_download") {
		t.Error("Expected download script to contain 'snapshot_download'")
	}

	// Check default node selector (GPU nodes)
	if ds.Spec.Template.Spec.NodeSelector == nil {
		t.Fatal("Expected default node selector to be set")
	}
	if ds.Spec.Template.Spec.NodeSelector["nvidia.com/gpu.present"] != "true" {
		t.Error("Expected default node selector for GPU nodes")
	}

	// Check hostPath volume
	foundHostPath := false
	for _, vol := range ds.Spec.Template.Spec.Volumes {
		if vol.Name == "model-cache" && vol.HostPath != nil {
			foundHostPath = true
			if vol.HostPath.Path != "/var/lib/flexinfer/models" {
				t.Errorf("Expected hostPath to be /var/lib/flexinfer/models, got %s", vol.HostPath.Path)
			}
		}
	}
	if !foundHostPath {
		t.Error("Expected model-cache hostPath volume")
	}

	// Check labels
	if ds.Labels["app.kubernetes.io/name"] != "modelcache-syncer" {
		t.Error("Expected app.kubernetes.io/name label")
	}
	if ds.Labels["app.kubernetes.io/instance"] != "llama2-local" {
		t.Error("Expected app.kubernetes.io/instance label")
	}
}

func TestDaemonSetForNodeLocal_MLC(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	r := &ModelCacheReconciler{Scheme: scheme}
	m := &aiv1alpha1.ModelCache{
		Spec: aiv1alpha1.ModelCacheSpec{
			Source: "mlc://mlc-ai/Qwen3-0.6B-q4f16_1-MLC",
		},
	}
	m.Name = "qwen-mlc"
	m.Namespace = "ai"

	ds, err := r.daemonSetForNodeLocal(m, "/mnt/nvme/models/qwen-mlc", "/mnt/nvme/models")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check container image - should use debian for MLC (git clone)
	container := ds.Spec.Template.Spec.Containers[0]
	if container.Image != "debian:bookworm-slim" {
		t.Errorf("Expected debian:bookworm-slim for MLC source, got %s", container.Image)
	}

	// Check download script contains git clone
	if len(container.Args) == 0 || !strings.Contains(container.Args[0], "git clone") {
		t.Error("Expected download script to contain 'git clone'")
	}

	// Check marker file creation
	if !strings.Contains(container.Args[0], "touch \"$MARKER\"") {
		t.Error("Expected download script to create marker file")
	}

	// Check sleep loop for long-running DaemonSet pod
	if !strings.Contains(container.Args[0], "while true; do sleep 3600; done") {
		t.Error("Expected download script to have sleep loop")
	}
}

func TestDaemonSetForNodeLocal_OCI(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	r := &ModelCacheReconciler{Scheme: scheme}
	m := &aiv1alpha1.ModelCache{
		Spec: aiv1alpha1.ModelCacheSpec{
			Source: "oci://ghcr.io/flexinfer/models/llama3:v1",
		},
	}
	m.Name = "llama3-oci"
	m.Namespace = "default"

	ds, err := r.daemonSetForNodeLocal(m, "/var/lib/flexinfer/models/llama3-oci", "/var/lib/flexinfer/models")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check container image - should use ORAS
	container := ds.Spec.Template.Spec.Containers[0]
	if container.Image != "ghcr.io/oras-project/oras:v1.2.2" {
		t.Errorf("Expected ORAS image for OCI source, got %s", container.Image)
	}

	// Check download script contains oras pull
	if len(container.Args) == 0 || !strings.Contains(container.Args[0], "oras pull") {
		t.Error("Expected download script to contain 'oras pull'")
	}

	// Check no docker-config volume (no auth secret)
	for _, vol := range ds.Spec.Template.Spec.Volumes {
		if vol.Name == "docker-config" {
			t.Error("Should not have docker-config volume without auth secret")
		}
	}
}

func TestDaemonSetForNodeLocal_OCIWithAuth(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	r := &ModelCacheReconciler{Scheme: scheme}
	secretName := "harbor-creds"
	m := &aiv1alpha1.ModelCache{
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:               "oci://registry.harbor.lan/models/llama3:v1",
			OCIRegistrySecretRef: &secretName,
		},
	}
	m.Name = "llama3-private"
	m.Namespace = "default"

	ds, err := r.daemonSetForNodeLocal(m, "/var/lib/flexinfer/models/llama3-private", "/var/lib/flexinfer/models")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check docker-config volume exists
	foundDockerConfig := false
	for _, vol := range ds.Spec.Template.Spec.Volumes {
		if vol.Name == "docker-config" {
			foundDockerConfig = true
			if vol.Secret == nil || vol.Secret.SecretName != "harbor-creds" {
				t.Error("docker-config volume should reference harbor-creds secret")
			}
		}
	}
	if !foundDockerConfig {
		t.Error("Expected docker-config volume for OCI with auth")
	}

	// Check docker-config mount
	container := ds.Spec.Template.Spec.Containers[0]
	foundDockerMount := false
	for _, mount := range container.VolumeMounts {
		if mount.Name == "docker-config" {
			foundDockerMount = true
			if mount.MountPath != "/root/.docker" {
				t.Errorf("docker-config should mount at /root/.docker, got %s", mount.MountPath)
			}
		}
	}
	if !foundDockerMount {
		t.Error("Expected docker-config volume mount")
	}
}

func TestDaemonSetForNodeLocal_CustomNodeSelector(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	r := &ModelCacheReconciler{Scheme: scheme}
	m := &aiv1alpha1.ModelCache{
		Spec: aiv1alpha1.ModelCacheSpec{
			Source: "huggingface://meta-llama/Llama-2-7b-chat-hf",
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": "gpu-node-1",
				"tier":                   "inference",
			},
		},
	}
	m.Name = "llama2-specific"
	m.Namespace = "default"

	ds, err := r.daemonSetForNodeLocal(m, "/var/lib/flexinfer/models/llama2-specific", "/var/lib/flexinfer/models")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check custom node selector is used
	nodeSelector := ds.Spec.Template.Spec.NodeSelector
	if nodeSelector["kubernetes.io/hostname"] != "gpu-node-1" {
		t.Error("Expected custom hostname selector")
	}
	if nodeSelector["tier"] != "inference" {
		t.Error("Expected custom tier selector")
	}

	// Ensure default GPU selector is NOT present when custom is specified
	if _, exists := nodeSelector["nvidia.com/gpu.present"]; exists {
		t.Error("Should not have default GPU selector when custom selector is provided")
	}
}

func TestDaemonSetForNodeLocal_CustomHostPath(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	r := &ModelCacheReconciler{Scheme: scheme}
	customPath := "/mnt/nvme/ai-models"
	m := &aiv1alpha1.ModelCache{
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:   "huggingface://meta-llama/Llama-2-7b-chat-hf",
			HostPath: &customPath,
		},
	}
	m.Name = "llama2-nvme"
	m.Namespace = "default"

	ds, err := r.daemonSetForNodeLocal(m, "/mnt/nvme/ai-models/llama2-nvme", "/mnt/nvme/ai-models")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check hostPath volume uses custom path
	foundHostPath := false
	for _, vol := range ds.Spec.Template.Spec.Volumes {
		if vol.Name == "model-cache" && vol.HostPath != nil {
			foundHostPath = true
			if vol.HostPath.Path != "/mnt/nvme/ai-models" {
				t.Errorf("Expected hostPath to be /mnt/nvme/ai-models, got %s", vol.HostPath.Path)
			}
		}
	}
	if !foundHostPath {
		t.Error("Expected model-cache hostPath volume")
	}

	// Check download script uses custom path
	container := ds.Spec.Template.Spec.Containers[0]
	if !strings.Contains(container.Args[0], "/mnt/nvme/ai-models/llama2-nvme") {
		t.Error("Expected download script to reference custom path")
	}
}

func TestDaemonSetForNodeLocal_WithHFToken(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	r := &ModelCacheReconciler{Scheme: scheme}
	secretName := "hf-token"
	m := &aiv1alpha1.ModelCache{
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:    "huggingface://meta-llama/Llama-2-7b-chat-hf",
			SecretRef: &secretName,
		},
	}
	m.Name = "llama2-auth"
	m.Namespace = "default"

	ds, err := r.daemonSetForNodeLocal(m, "/var/lib/flexinfer/models/llama2-auth", "/var/lib/flexinfer/models")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check HF_TOKEN env var
	container := ds.Spec.Template.Spec.Containers[0]
	foundHFToken := false
	for _, env := range container.Env {
		if env.Name == "HF_TOKEN" {
			foundHFToken = true
			if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil {
				t.Error("HF_TOKEN should reference secret")
			}
			if env.ValueFrom.SecretKeyRef.Name != "hf-token" {
				t.Errorf("Expected secret name hf-token, got %s", env.ValueFrom.SecretKeyRef.Name)
			}
		}
	}
	if !foundHFToken {
		t.Error("Expected HF_TOKEN environment variable")
	}
}

// ===== ModelDeployment Volume Type Tests =====

func TestDeploymentForModelDeployment_NodeLocalVolume(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	r := &ModelDeploymentReconciler{Scheme: scheme}
	replicas := int32(1)
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:  "vllm",
			Model:    "llama3",
			Replicas: &replicas,
		},
	}
	m.Name = "llama3-deployment"
	m.Namespace = "default"

	// NodeLocal path format: absolute path
	volumeName := "/var/lib/flexinfer/models/llama3-local"

	dep, err := r.deploymentForModelDeployment(m, volumeName, true)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check volume type is hostPath
	if len(dep.Spec.Template.Spec.Volumes) != 1 {
		t.Fatalf("Expected 1 volume, got %d", len(dep.Spec.Template.Spec.Volumes))
	}

	vol := dep.Spec.Template.Spec.Volumes[0]
	if vol.HostPath == nil {
		t.Fatal("Expected hostPath volume for NodeLocal storage")
	}
	if vol.HostPath.Path != "/var/lib/flexinfer/models/llama3-local" {
		t.Errorf("Expected hostPath to be /var/lib/flexinfer/models/llama3-local, got %s", vol.HostPath.Path)
	}

	// Check mount is read-only
	container := dep.Spec.Template.Spec.Containers[0]
	if len(container.VolumeMounts) != 1 {
		t.Fatalf("Expected 1 volume mount, got %d", len(container.VolumeMounts))
	}
	if !container.VolumeMounts[0].ReadOnly {
		t.Error("Expected volume mount to be read-only")
	}
	if container.VolumeMounts[0].MountPath != "/models" {
		t.Errorf("Expected mount path /models, got %s", container.VolumeMounts[0].MountPath)
	}
}

func TestDeploymentForModelDeployment_SharedPVCVolume(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	r := &ModelDeploymentReconciler{Scheme: scheme}
	replicas := int32(1)
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:  "vllm",
			Model:    "llama3",
			Replicas: &replicas,
		},
	}
	m.Name = "llama3-deployment"
	m.Namespace = "default"

	// SharedPVC path format: "pvcName:subPath"
	volumeName := "model-cache-pvc:llama3-model"

	dep, err := r.deploymentForModelDeployment(m, volumeName, true)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check volume type is PVC
	if len(dep.Spec.Template.Spec.Volumes) != 1 {
		t.Fatalf("Expected 1 volume, got %d", len(dep.Spec.Template.Spec.Volumes))
	}

	vol := dep.Spec.Template.Spec.Volumes[0]
	if vol.PersistentVolumeClaim == nil {
		t.Fatal("Expected PVC volume for SharedPVC storage")
	}
	if vol.PersistentVolumeClaim.ClaimName != "model-cache-pvc" {
		t.Errorf("Expected PVC name model-cache-pvc, got %s", vol.PersistentVolumeClaim.ClaimName)
	}

	// Check subPath is set
	container := dep.Spec.Template.Spec.Containers[0]
	if container.VolumeMounts[0].SubPath != "llama3-model" {
		t.Errorf("Expected subPath llama3-model, got %s", container.VolumeMounts[0].SubPath)
	}
}

func TestDeploymentForModelDeployment_LegacyPVCVolume(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	r := &ModelDeploymentReconciler{Scheme: scheme}
	replicas := int32(1)
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:  "ollama",
			Model:    "mistral",
			Replicas: &replicas,
		},
	}
	m.Name = "mistral-deployment"
	m.Namespace = "default"

	// Legacy format: just PVC name (no subPath)
	volumeName := "mistral-deployment"

	dep, err := r.deploymentForModelDeployment(m, volumeName, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check volume type is PVC without subPath
	vol := dep.Spec.Template.Spec.Volumes[0]
	if vol.PersistentVolumeClaim == nil {
		t.Fatal("Expected PVC volume")
	}
	if vol.PersistentVolumeClaim.ClaimName != "mistral-deployment" {
		t.Errorf("Expected PVC name mistral-deployment, got %s", vol.PersistentVolumeClaim.ClaimName)
	}

	// Check no subPath
	container := dep.Spec.Template.Spec.Containers[0]
	if container.VolumeMounts[0].SubPath != "" {
		t.Errorf("Expected no subPath for legacy PVC, got %s", container.VolumeMounts[0].SubPath)
	}

	// Check not read-only for legacy
	if container.VolumeMounts[0].ReadOnly {
		t.Error("Expected volume mount to be read-write for legacy PVC")
	}
}

func TestValidateBackendGPUCompatibility_VLLMOnMaxwell(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "vllm",
			NodeSelector: map[string]string{
				"nvidia.com/gpu.compute.major": "5",
			},
		},
	}

	err := r.validateBackendGPUCompatibility(m)
	if err == nil {
		t.Error("Expected error for vLLM on Maxwell, got nil")
	}
	if !strings.Contains(err.Error(), "vLLM backend is not supported on Maxwell") {
		t.Errorf("Expected vLLM Maxwell error message, got: %v", err)
	}
}

func TestValidateBackendGPUCompatibility_VLLMOnMaxwellArch(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "vllm",
			NodeSelector: map[string]string{
				"nvidia.com/gpu.arch": "Maxwell",
			},
		},
	}

	err := r.validateBackendGPUCompatibility(m)
	if err == nil {
		t.Error("Expected error for vLLM on Maxwell (via arch label), got nil")
	}
}

func TestValidateBackendGPUCompatibility_VLLMOnModernGPU(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "vllm",
			NodeSelector: map[string]string{
				"nvidia.com/gpu.compute.major": "8",
			},
		},
	}

	err := r.validateBackendGPUCompatibility(m)
	if err != nil {
		t.Errorf("Expected no error for vLLM on Ampere, got: %v", err)
	}
}

func TestValidateBackendGPUCompatibility_MLCOnMaxwellWithoutLib(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
			NodeSelector: map[string]string{
				"nvidia.com/gpu.compute.major": "5",
			},
		},
	}

	err := r.validateBackendGPUCompatibility(m)
	if err == nil {
		t.Error("Expected error for MLC-LLM on Maxwell without modelLibPath, got nil")
	}
	if !strings.Contains(err.Error(), "requires a pre-compiled modelLibPath") {
		t.Errorf("Expected modelLibPath error message, got: %v", err)
	}
}

func TestValidateBackendGPUCompatibility_MLCOnMaxwellWithLib(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "mlc-llm",
			NodeSelector: map[string]string{
				"nvidia.com/gpu.compute.major": "5",
			},
			MLCLLM: &aiv1alpha1.MLCLLMSpec{
				ModelLibPath: "/models/model-lib.so",
			},
		},
	}

	err := r.validateBackendGPUCompatibility(m)
	if err != nil {
		t.Errorf("Expected no error for MLC-LLM on Maxwell with modelLibPath, got: %v", err)
	}
}

func TestValidateBackendGPUCompatibility_OllamaOnMaxwell(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "ollama",
			NodeSelector: map[string]string{
				"nvidia.com/gpu.compute.major": "5",
			},
		},
	}

	err := r.validateBackendGPUCompatibility(m)
	if err != nil {
		t.Errorf("Expected no error for Ollama on Maxwell, got: %v", err)
	}
}

func TestValidateBackendGPUCompatibility_LlamaCppOnMaxwell(t *testing.T) {
	r := &ModelDeploymentReconciler{}
	m := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "llamacpp",
			NodeSelector: map[string]string{
				"nvidia.com/gpu.compute.major": "5",
			},
		},
	}

	err := r.validateBackendGPUCompatibility(m)
	if err != nil {
		t.Errorf("Expected no error for llama.cpp on Maxwell, got: %v", err)
	}
}

func TestGetGPUArchitecture(t *testing.T) {
	tests := []struct {
		name         string
		nodeSelector map[string]string
		expected     string
	}{
		{
			name:         "NVIDIA compute capability major only",
			nodeSelector: map[string]string{"nvidia.com/gpu.compute.major": "5"},
			expected:     "sm_50",
		},
		{
			name:         "NVIDIA compute capability major and minor",
			nodeSelector: map[string]string{"nvidia.com/gpu.compute.major": "8", "nvidia.com/gpu.compute.minor": "9"},
			expected:     "sm_89",
		},
		{
			name:         "NVIDIA arch label",
			nodeSelector: map[string]string{"nvidia.com/gpu.arch": "Maxwell"},
			expected:     "Maxwell",
		},
		{
			name:         "AMD arch label",
			nodeSelector: map[string]string{"amd.com/gpu.arch": "gfx1100"},
			expected:     "gfx1100",
		},
		{
			name:         "no selector",
			nodeSelector: nil,
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ModelDeploymentReconciler{}
			m := &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					NodeSelector: tt.nodeSelector,
				},
			}

			arch := r.getGPUArchitecture(m)
			if arch != tt.expected {
				t.Errorf("Expected architecture %s, got %s", tt.expected, arch)
			}
		})
	}
}

func TestGetGPUVendor(t *testing.T) {
	tests := []struct {
		name      string
		resources corev1.ResourceRequirements
		expected  string
	}{
		{
			name: "AMD GPU",
			resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")},
			},
			expected: "AMD",
		},
		{
			name: "NVIDIA GPU",
			resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
			},
			expected: "NVIDIA",
		},
		{
			name: "Intel GPU",
			resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{"intel.com/gpu": resource.MustParse("1")},
			},
			expected: "Intel",
		},
		{
			name:      "No GPU (defaults to NVIDIA)",
			resources: corev1.ResourceRequirements{},
			expected:  "NVIDIA",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ModelDeploymentReconciler{}
			m := &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Resources: tt.resources,
				},
			}

			vendor := r.getGPUVendor(m)
			if vendor != tt.expected {
				t.Errorf("Expected vendor %s, got %s", tt.expected, vendor)
			}
		})
	}
}
