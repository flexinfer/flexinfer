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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestModelDeploymentFinalizer(t *testing.T) {
	// Test that the finalizer constant is properly defined
	assert.Equal(t, "flexinfer.ai/cleanup", aiv1alpha1.ModelDeploymentFinalizer,
		"ModelDeploymentFinalizer should have the correct value")
	assert.NotEmpty(t, aiv1alpha1.ModelDeploymentFinalizer,
		"ModelDeploymentFinalizer should not be empty")
}

func TestConditionTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{
			name:     "ConditionTypeReady",
			constant: aiv1alpha1.ConditionTypeReady,
			expected: "Ready",
		},
		{
			name:     "ConditionTypeGPUAllocated",
			constant: aiv1alpha1.ConditionTypeGPUAllocated,
			expected: "GPUAllocated",
		},
		{
			name:     "ConditionTypeModelLoaded",
			constant: aiv1alpha1.ConditionTypeModelLoaded,
			expected: "ModelLoaded",
		},
		{
			name:     "ConditionTypeEndpointReady",
			constant: aiv1alpha1.ConditionTypeEndpointReady,
			expected: "EndpointReady",
		},
		{
			name:     "ConditionTypeProgressing",
			constant: aiv1alpha1.ConditionTypeProgressing,
			expected: "Progressing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant,
				"Condition type %s should have the correct value", tt.name)
			assert.NotEmpty(t, tt.constant,
				"Condition type %s should not be empty", tt.name)
		})
	}
}

func TestConditionReasonConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{
			name:     "ReasonReconciling",
			constant: aiv1alpha1.ReasonReconciling,
			expected: "Reconciling",
		},
		{
			name:     "ReasonGPUAllocated",
			constant: aiv1alpha1.ReasonGPUAllocated,
			expected: "GPUAllocated",
		},
		{
			name:     "ReasonGPUAllocationFailed",
			constant: aiv1alpha1.ReasonGPUAllocationFailed,
			expected: "GPUAllocationFailed",
		},
		{
			name:     "ReasonDeploymentReady",
			constant: aiv1alpha1.ReasonDeploymentReady,
			expected: "DeploymentReady",
		},
		{
			name:     "ReasonServiceReady",
			constant: aiv1alpha1.ReasonServiceReady,
			expected: "ServiceReady",
		},
		{
			name:     "ReasonModelLoadFailed",
			constant: aiv1alpha1.ReasonModelLoadFailed,
			expected: "ModelLoadFailed",
		},
		{
			name:     "ReasonValidationFailed",
			constant: aiv1alpha1.ReasonValidationFailed,
			expected: "ValidationFailed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant,
				"Reason %s should have the correct value", tt.name)
			assert.NotEmpty(t, tt.constant,
				"Reason %s should not be empty", tt.name)
		})
	}
}

func TestModelDeploymentPhaseConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant aiv1alpha1.ModelDeploymentPhase
		expected string
	}{
		{
			name:     "ModelDeploymentPhasePending",
			constant: aiv1alpha1.ModelDeploymentPhasePending,
			expected: "Pending",
		},
		{
			name:     "ModelDeploymentPhaseRunning",
			constant: aiv1alpha1.ModelDeploymentPhaseRunning,
			expected: "Running",
		},
		{
			name:     "ModelDeploymentPhaseFailed",
			constant: aiv1alpha1.ModelDeploymentPhaseFailed,
			expected: "Failed",
		},
		{
			name:     "ModelDeploymentPhaseTerminating",
			constant: aiv1alpha1.ModelDeploymentPhaseTerminating,
			expected: "Terminating",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.constant),
				"Phase %s should have the correct value", tt.name)
			assert.NotEmpty(t, string(tt.constant),
				"Phase %s should not be empty", tt.name)
		})
	}
}

func TestModelDeploymentStatusStructure(t *testing.T) {
	// Test that the enhanced status structure can be properly initialized
	status := aiv1alpha1.ModelDeploymentStatus{
		Phase: aiv1alpha1.ModelDeploymentPhaseRunning,
		Conditions: []metav1.Condition{
			{
				Type:               aiv1alpha1.ConditionTypeReady,
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Reason:             aiv1alpha1.ReasonDeploymentReady,
				Message:            "All resources are ready",
			},
		},
		AllocatedGPU: &aiv1alpha1.GPUAllocation{
			Node:     "worker-1",
			Device:   "0",
			Type:     "NVIDIA RTX 4090",
			MemoryMB: 24576,
		},
		Endpoints: &aiv1alpha1.ModelEndpoints{
			Internal: "test-model.default.svc.cluster.local:11434",
			External: "https://model.example.com",
		},
		Metrics: &aiv1alpha1.ModelMetrics{
			TotalRequests: 1000,
			AvgLatencyMs:  25.5,
			ErrorRate:     0.01,
		},
		TokensPerSecond: "150.75",
	}

	// Verify all fields are accessible and properly typed
	assert.Equal(t, aiv1alpha1.ModelDeploymentPhaseRunning, status.Phase)
	assert.Len(t, status.Conditions, 1)
	assert.Equal(t, aiv1alpha1.ConditionTypeReady, status.Conditions[0].Type)

	assert.NotNil(t, status.AllocatedGPU)
	assert.Equal(t, "worker-1", status.AllocatedGPU.Node)
	assert.Equal(t, "0", status.AllocatedGPU.Device)
	assert.Equal(t, "NVIDIA RTX 4090", status.AllocatedGPU.Type)
	assert.Equal(t, int64(24576), status.AllocatedGPU.MemoryMB)

	assert.NotNil(t, status.Endpoints)
	assert.Equal(t, "test-model.default.svc.cluster.local:11434", status.Endpoints.Internal)
	assert.Equal(t, "https://model.example.com", status.Endpoints.External)

	assert.NotNil(t, status.Metrics)
	assert.Equal(t, int64(1000), status.Metrics.TotalRequests)
	assert.Equal(t, 25.5, status.Metrics.AvgLatencyMs)
	assert.Equal(t, 0.01, status.Metrics.ErrorRate)

	assert.Equal(t, "150.75", status.TokensPerSecond)
}

func TestGPUAllocationStructure(t *testing.T) {
	tests := []struct {
		name         string
		allocation   aiv1alpha1.GPUAllocation
		expectedNode string
		expectedType string
		expectedMem  int64
	}{
		{
			name: "NVIDIA GPU allocation",
			allocation: aiv1alpha1.GPUAllocation{
				Node:     "gpu-node-1",
				Device:   "0",
				Type:     "NVIDIA RTX 4090",
				MemoryMB: 24576,
			},
			expectedNode: "gpu-node-1",
			expectedType: "NVIDIA RTX 4090",
			expectedMem:  24576,
		},
		{
			name: "AMD GPU allocation",
			allocation: aiv1alpha1.GPUAllocation{
				Node:     "gpu-node-2",
				Device:   "1",
				Type:     "AMD MI250X",
				MemoryMB: 65536,
			},
			expectedNode: "gpu-node-2",
			expectedType: "AMD MI250X",
			expectedMem:  65536,
		},
		{
			name: "Minimal GPU allocation",
			allocation: aiv1alpha1.GPUAllocation{
				Node:   "minimal-node",
				Device: "0",
			},
			expectedNode: "minimal-node",
			expectedType: "",
			expectedMem:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedNode, tt.allocation.Node)
			assert.Equal(t, tt.expectedType, tt.allocation.Type)
			assert.Equal(t, tt.expectedMem, tt.allocation.MemoryMB)
		})
	}
}

func TestModelEndpointsStructure(t *testing.T) {
	tests := []struct {
		name             string
		endpoints        aiv1alpha1.ModelEndpoints
		expectedInternal string
		expectedExternal string
	}{
		{
			name: "Full endpoint configuration",
			endpoints: aiv1alpha1.ModelEndpoints{
				Internal: "llama-model.default.svc.cluster.local:11434",
				External: "https://api.example.com/models/llama",
			},
			expectedInternal: "llama-model.default.svc.cluster.local:11434",
			expectedExternal: "https://api.example.com/models/llama",
		},
		{
			name: "Internal only",
			endpoints: aiv1alpha1.ModelEndpoints{
				Internal: "private-model.production.svc.cluster.local:8080",
			},
			expectedInternal: "private-model.production.svc.cluster.local:8080",
			expectedExternal: "",
		},
		{
			name:             "Empty endpoints",
			endpoints:        aiv1alpha1.ModelEndpoints{},
			expectedInternal: "",
			expectedExternal: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedInternal, tt.endpoints.Internal)
			assert.Equal(t, tt.expectedExternal, tt.endpoints.External)
		})
	}
}

func TestModelMetricsStructure(t *testing.T) {
	tests := []struct {
		name             string
		metrics          aiv1alpha1.ModelMetrics
		expectedRequests int64
		expectedLatency  float64
		expectedError    float64
	}{
		{
			name: "Production metrics",
			metrics: aiv1alpha1.ModelMetrics{
				TotalRequests: 10000,
				AvgLatencyMs:  45.7,
				ErrorRate:     0.005,
			},
			expectedRequests: 10000,
			expectedLatency:  45.7,
			expectedError:    0.005,
		},
		{
			name: "High error rate scenario",
			metrics: aiv1alpha1.ModelMetrics{
				TotalRequests: 100,
				AvgLatencyMs:  120.0,
				ErrorRate:     0.15,
			},
			expectedRequests: 100,
			expectedLatency:  120.0,
			expectedError:    0.15,
		},
		{
			name: "Perfect performance",
			metrics: aiv1alpha1.ModelMetrics{
				TotalRequests: 50000,
				AvgLatencyMs:  12.3,
				ErrorRate:     0.0,
			},
			expectedRequests: 50000,
			expectedLatency:  12.3,
			expectedError:    0.0,
		},
		{
			name:             "Zero metrics",
			metrics:          aiv1alpha1.ModelMetrics{},
			expectedRequests: 0,
			expectedLatency:  0.0,
			expectedError:    0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedRequests, tt.metrics.TotalRequests)
			assert.Equal(t, tt.expectedLatency, tt.metrics.AvgLatencyMs)
			assert.Equal(t, tt.expectedError, tt.metrics.ErrorRate)
		})
	}
}

func TestBenchmarkSpecStructure(t *testing.T) {
	tests := []struct {
		name                     string
		benchmarkSpec            *aiv1alpha1.BenchmarkSpec
		expectedWarmupIterations *int32
		expectedMinDuration      *metav1.Duration
	}{
		{
			name: "Full benchmark configuration",
			benchmarkSpec: &aiv1alpha1.BenchmarkSpec{
				WarmupIterations: func() *int32 { i := int32(5); return &i }(),
				MinDuration:      &metav1.Duration{Duration: metav1.Duration{Duration: 300000000000}.Duration}, // 5 minutes
			},
			expectedWarmupIterations: func() *int32 { i := int32(5); return &i }(),
		},
		{
			name: "Minimal benchmark configuration",
			benchmarkSpec: &aiv1alpha1.BenchmarkSpec{
				WarmupIterations: func() *int32 { i := int32(2); return &i }(),
			},
			expectedWarmupIterations: func() *int32 { i := int32(2); return &i }(),
			expectedMinDuration:      nil,
		},
		{
			name:                     "Nil benchmark spec",
			benchmarkSpec:            nil,
			expectedWarmupIterations: nil,
			expectedMinDuration:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.benchmarkSpec == nil {
				assert.Nil(t, tt.benchmarkSpec)
				return
			}

			if tt.expectedWarmupIterations != nil {
				assert.NotNil(t, tt.benchmarkSpec.WarmupIterations)
				assert.Equal(t, *tt.expectedWarmupIterations, *tt.benchmarkSpec.WarmupIterations)
			} else {
				assert.Nil(t, tt.benchmarkSpec.WarmupIterations)
			}

			if tt.expectedMinDuration != nil {
				assert.NotNil(t, tt.benchmarkSpec.MinDuration)
			} else {
				assert.Nil(t, tt.benchmarkSpec.MinDuration)
			}
		})
	}
}

func TestModelDeploymentSpecStructure(t *testing.T) {
	// Test that ModelDeploymentSpec can be properly initialized with all fields
	spec := aiv1alpha1.ModelDeploymentSpec{
		Backend:  "ollama",
		Model:    "llama3:8b",
		Replicas: func() *int32 { i := int32(3); return &i }(),
		Benchmark: &aiv1alpha1.BenchmarkSpec{
			WarmupIterations: func() *int32 { i := int32(5); return &i }(),
		},
	}

	assert.Equal(t, "ollama", spec.Backend)
	assert.Equal(t, "llama3:8b", spec.Model)
	assert.NotNil(t, spec.Replicas)
	assert.Equal(t, int32(3), *spec.Replicas)
	assert.NotNil(t, spec.Benchmark)
	assert.NotNil(t, spec.Benchmark.WarmupIterations)
	assert.Equal(t, int32(5), *spec.Benchmark.WarmupIterations)
}

func TestConstantUniqueness(t *testing.T) {
	// Test that all condition type constants are unique
	conditionTypes := []string{
		aiv1alpha1.ConditionTypeReady,
		aiv1alpha1.ConditionTypeGPUAllocated,
		aiv1alpha1.ConditionTypeModelLoaded,
		aiv1alpha1.ConditionTypeEndpointReady,
		aiv1alpha1.ConditionTypeProgressing,
	}

	seen := make(map[string]bool)
	for _, conditionType := range conditionTypes {
		assert.False(t, seen[conditionType], "Condition type %s should be unique", conditionType)
		seen[conditionType] = true
	}

	// Test that all reason constants are unique
	reasons := []string{
		aiv1alpha1.ReasonReconciling,
		aiv1alpha1.ReasonGPUAllocated,
		aiv1alpha1.ReasonGPUAllocationFailed,
		aiv1alpha1.ReasonDeploymentReady,
		aiv1alpha1.ReasonServiceReady,
		aiv1alpha1.ReasonModelLoadFailed,
		aiv1alpha1.ReasonValidationFailed,
	}

	seenReasons := make(map[string]bool)
	for _, reason := range reasons {
		assert.False(t, seenReasons[reason], "Reason %s should be unique", reason)
		seenReasons[reason] = true
	}

	// Test that all phase constants are unique
	phases := []aiv1alpha1.ModelDeploymentPhase{
		aiv1alpha1.ModelDeploymentPhasePending,
		aiv1alpha1.ModelDeploymentPhaseRunning,
		aiv1alpha1.ModelDeploymentPhaseFailed,
		aiv1alpha1.ModelDeploymentPhaseTerminating,
	}

	seenPhases := make(map[aiv1alpha1.ModelDeploymentPhase]bool)
	for _, phase := range phases {
		assert.False(t, seenPhases[phase], "Phase %s should be unique", phase)
		seenPhases[phase] = true
	}
}

func TestConstantNaming(t *testing.T) {
	// Test that constants follow expected naming conventions
	tests := []struct {
		name     string
		constant string
		prefix   string
	}{
		{
			name:     "Ready condition follows naming",
			constant: aiv1alpha1.ConditionTypeReady,
			prefix:   "",
		},
		{
			name:     "GPU condition follows naming",
			constant: aiv1alpha1.ConditionTypeGPUAllocated,
			prefix:   "",
		},
		{
			name:     "GPU reason follows naming",
			constant: aiv1alpha1.ReasonGPUAllocated,
			prefix:   "",
		},
		{
			name:     "Validation reason follows naming",
			constant: aiv1alpha1.ReasonValidationFailed,
			prefix:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test that constants are well-formed (no spaces, proper case)
			assert.NotContains(t, tt.constant, " ", "Constant should not contain spaces")
			assert.NotEmpty(t, tt.constant, "Constant should not be empty")

			// Test that the first character is uppercase (PascalCase)
			if len(tt.constant) > 0 {
				firstChar := tt.constant[0]
				assert.True(t, firstChar >= 'A' && firstChar <= 'Z',
					"Constant should start with uppercase letter")
			}
		})
	}
}
