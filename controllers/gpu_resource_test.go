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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestGetResourceRequirements(t *testing.T) {
	tests := []struct {
		name                   string
		modelDeployment        *aiv1alpha1.ModelDeployment
		expectedGPURequests    string
		expectedGPULimits      string
		expectedCPURequests    *resource.Quantity
		expectedMemoryRequests *resource.Quantity
		expectedCPULimits      *resource.Quantity
		expectedMemoryLimits   *resource.Quantity
	}{
		{
			name: "basic model deployment with no resource specs",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "ollama",
					Model:   "llama3:8b",
				},
			},
			expectedGPURequests: "1",
			expectedGPULimits:   "1",
		},
		{
			name: "model deployment with CPU and memory specifications",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model-with-resources",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "vllm",
					Model:   "llama3:70b",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("8Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("4"),
							corev1.ResourceMemory: resource.MustParse("16Gi"),
						},
					},
				},
			},
			expectedGPURequests:    "1",
			expectedGPULimits:      "1",
			expectedCPURequests:    resource.NewQuantity(2, resource.DecimalSI),
			expectedMemoryRequests: resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
			expectedCPULimits:      resource.NewQuantity(4, resource.DecimalSI),
			expectedMemoryLimits:   resource.NewQuantity(16*1024*1024*1024, resource.BinarySI),
		},
		{
			name: "model deployment with partial resource specifications",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model-partial-resources",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "tgi",
					Model:   "mistral:7b",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
			expectedGPURequests:  "1",
			expectedGPULimits:    "1",
			expectedCPURequests:  resource.NewQuantity(1, resource.DecimalSI),
			expectedMemoryLimits: resource.NewQuantity(4*1024*1024*1024, resource.BinarySI),
		},
		{
			name: "model deployment with zero resource values (should be ignored)",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model-zero-resources",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "ollama",
					Model:   "phi:3b",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("0"),
							corev1.ResourceMemory: resource.MustParse("0"),
						},
					},
				},
			},
			expectedGPURequests: "1",
			expectedGPULimits:   "1",
			// Zero values should be ignored, so CPU and Memory should be nil
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ModelDeploymentReconciler{
				Scheme: runtime.NewScheme(),
			}

			requirements := reconciler.getResourceRequirements(tt.modelDeployment)

			// Verify GPU resources are always added
			gpuRequests := requirements.Requests["nvidia.com/gpu"]
			assert.Equal(t, tt.expectedGPURequests, gpuRequests.String(), "GPU requests should match expected value")

			gpuLimits := requirements.Limits["nvidia.com/gpu"]
			assert.Equal(t, tt.expectedGPULimits, gpuLimits.String(), "GPU limits should match expected value")

			// Verify CPU requests
			if tt.expectedCPURequests != nil {
				cpuRequests := requirements.Requests[corev1.ResourceCPU]
				assert.Equal(t, tt.expectedCPURequests.String(), cpuRequests.String(), "CPU requests should match expected value")
			} else {
				_, exists := requirements.Requests[corev1.ResourceCPU]
				assert.False(t, exists, "CPU requests should not exist when not specified or zero")
			}

			// Verify Memory requests
			if tt.expectedMemoryRequests != nil {
				memoryRequests := requirements.Requests[corev1.ResourceMemory]
				assert.Equal(t, tt.expectedMemoryRequests.String(), memoryRequests.String(), "Memory requests should match expected value")
			} else {
				_, exists := requirements.Requests[corev1.ResourceMemory]
				assert.False(t, exists, "Memory requests should not exist when not specified or zero")
			}

			// Verify CPU limits
			if tt.expectedCPULimits != nil {
				cpuLimits := requirements.Limits[corev1.ResourceCPU]
				assert.Equal(t, tt.expectedCPULimits.String(), cpuLimits.String(), "CPU limits should match expected value")
			} else {
				_, exists := requirements.Limits[corev1.ResourceCPU]
				assert.False(t, exists, "CPU limits should not exist when not specified or zero")
			}

			// Verify Memory limits
			if tt.expectedMemoryLimits != nil {
				memoryLimits := requirements.Limits[corev1.ResourceMemory]
				assert.Equal(t, tt.expectedMemoryLimits.String(), memoryLimits.String(), "Memory limits should match expected value")
			} else {
				_, exists := requirements.Limits[corev1.ResourceMemory]
				assert.False(t, exists, "Memory limits should not exist when not specified or zero")
			}
		})
	}
}

func TestGetNodeSelector(t *testing.T) {
	tests := []struct {
		name                 string
		modelDeployment      *aiv1alpha1.ModelDeployment
		expectedNodeSelector map[string]string
	}{
		{
			name: "basic model deployment",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "ollama",
					Model:   "llama3:8b",
				},
			},
			expectedNodeSelector: map[string]string{
				"flexinfer.ai/gpu-present": "true",
			},
		},
		{
			name: "vllm backend",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vllm",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend:  "vllm",
					Model:    "mistral:7b",
					Replicas: pointer.Int32(2),
				},
			},
			expectedNodeSelector: map[string]string{
				"flexinfer.ai/gpu-present": "true",
			},
		},
		{
			name: "tgi backend",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-tgi",
					Namespace: "production",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "tgi",
					Model:   "codellama:34b",
				},
			},
			expectedNodeSelector: map[string]string{
				"flexinfer.ai/gpu-present": "true",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ModelDeploymentReconciler{
				Scheme: runtime.NewScheme(),
			}

			nodeSelector := reconciler.getNodeSelector(tt.modelDeployment)

			assert.Equal(t, tt.expectedNodeSelector, nodeSelector, "Node selector should ensure GPU scheduling")
		})
	}
}

func TestValidateGPUResources(t *testing.T) {
	tests := []struct {
		name            string
		modelDeployment *aiv1alpha1.ModelDeployment
		expectError     bool
		errorMessage    string
	}{
		{
			name: "valid ollama backend",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ollama",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "ollama",
					Model:   "llama3:8b",
				},
			},
			expectError: false,
		},
		{
			name: "valid vllm backend",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vllm",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "vllm",
					Model:   "mistral:7b",
				},
			},
			expectError: false,
		},
		{
			name: "valid tgi backend",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-tgi",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "tgi",
					Model:   "codellama:34b",
				},
			},
			expectError: false,
		},
		{
			name: "unsupported backend",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-unsupported",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "unsupported-backend",
					Model:   "some-model",
				},
			},
			expectError:  true,
			errorMessage: "backend unsupported-backend is not supported for GPU workloads",
		},
		{
			name: "empty backend",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-empty-backend",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "",
					Model:   "some-model",
				},
			},
			expectError: false,
		},
		{
			name: "case sensitive backend validation",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-case-sensitive",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "OLLAMA", // Uppercase should fail
					Model:   "llama3:8b",
				},
			},
			expectError:  true,
			errorMessage: "backend OLLAMA is not supported for GPU workloads",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &ModelDeploymentReconciler{
				Scheme: runtime.NewScheme(),
			}

			err := reconciler.validateGPUResources(tt.modelDeployment)

			if tt.expectError {
				require.Error(t, err, "Expected validation to fail")
				assert.Equal(t, tt.errorMessage, err.Error(), "Error message should match expected")
			} else {
				require.NoError(t, err, "Expected validation to pass")
			}
		})
	}
}

func TestGPUResourceAllocationIntegration(t *testing.T) {
	// Integration test to verify that all GPU resource functions work together
	reconciler := &ModelDeploymentReconciler{
		Scheme: runtime.NewScheme(),
	}

	modelDeployment := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "integration-test",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "ollama",
			Model:   "llama3:8b",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
		},
	}

	// Test validation
	err := reconciler.validateGPUResources(modelDeployment)
	require.NoError(t, err, "GPU validation should pass")

	// Test node selector
	nodeSelector := reconciler.getNodeSelector(modelDeployment)
	expectedNodeSelector := map[string]string{
		"flexinfer.ai/gpu-present": "true",
	}
	assert.Equal(t, expectedNodeSelector, nodeSelector, "Node selector should ensure GPU nodes")

	// Test resource requirements
	requirements := reconciler.getResourceRequirements(modelDeployment)

	// Verify GPU resources are added
	gpuRequests := requirements.Requests["nvidia.com/gpu"]
	assert.Equal(t, "1", gpuRequests.String(), "GPU requests should be 1")

	gpuLimits := requirements.Limits["nvidia.com/gpu"]
	assert.Equal(t, "1", gpuLimits.String(), "GPU limits should be 1")

	// Verify original CPU/Memory resources are preserved
	cpuRequests := requirements.Requests[corev1.ResourceCPU]
	assert.Equal(t, "2", cpuRequests.String(), "CPU requests should be preserved")

	memoryRequests := requirements.Requests[corev1.ResourceMemory]
	assert.Equal(t, "4Gi", memoryRequests.String(), "Memory requests should be preserved")
}
