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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// ModelDeploymentFinalizer is the finalizer used for ModelDeployment cleanup
	ModelDeploymentFinalizer = "flexinfer.ai/cleanup"
)

// Condition types for ModelDeployment status
const (
	// ConditionTypeReady indicates the ModelDeployment is ready to serve requests
	ConditionTypeReady = "Ready"

	// ConditionTypeGPUAllocated indicates a GPU has been allocated
	ConditionTypeGPUAllocated = "GPUAllocated"

	// ConditionTypeModelLoaded indicates the model has been loaded successfully
	ConditionTypeModelLoaded = "ModelLoaded"

	// ConditionTypeEndpointReady indicates the service endpoint is ready
	ConditionTypeEndpointReady = "EndpointReady"

	// ConditionTypeProgressing indicates the ModelDeployment is progressing
	ConditionTypeProgressing = "Progressing"
)

// Condition reasons
const (
	// ReasonReconciling indicates the resource is being reconciled
	ReasonReconciling = "Reconciling"

	// ReasonGPUAllocated indicates GPU has been successfully allocated
	ReasonGPUAllocated = "GPUAllocated"

	// ReasonGPUAllocationFailed indicates GPU allocation failed
	ReasonGPUAllocationFailed = "GPUAllocationFailed"

	// ReasonDeploymentReady indicates the deployment is ready
	ReasonDeploymentReady = "DeploymentReady"

	// ReasonServiceReady indicates the service is ready
	ReasonServiceReady = "ServiceReady"

	// ReasonModelLoadFailed indicates model loading failed
	ReasonModelLoadFailed = "ModelLoadFailed"

	// ReasonValidationFailed indicates validation failed
	ReasonValidationFailed = "ValidationFailed"
)

// ModelDeploymentSpec defines the desired state of ModelDeployment
// +kubebuilder:object:generate=true
type ModelDeploymentSpec struct {
	// Backend is the name of the LLM backend to use (e.g., ollama, vllm).
	// +kubebuilder:validation:Required
	Backend string `json:"backend"`

	// Model is the identifier for the model to be deployed (e.g., llama3:8b).
	// +kubebuilder:validation:Required
	Model string `json:"model"`

	// Replicas is the number of desired pods.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	Replicas *int32 `json:"replicas,omitempty"`

	// MinReplicas is the minimum number of replicas to scale down to (e.g., 0 for serverless).
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// IdleTimeoutSeconds is the duration in seconds before scaling down to MinReplicas.
	// +kubebuilder:default=300
	// +kubebuilder:validation:Minimum=0
	IdleTimeoutSeconds *int32 `json:"idleTimeoutSeconds,omitempty"`

	// Resources defines the resources required by the model.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Benchmark defines tuning knobs for the benchmarking process.
	// +optional
	Benchmark *BenchmarkSpec `json:"benchmark,omitempty"`

	// ModelCacheRef references a ModelCache object to use for model storage.
	// If set, the deployment will use the cached model volume instead of creating its own.
	// +optional
	ModelCacheRef *string `json:"modelCacheRef,omitempty"`

	// LiteLLM configures integration with the LiteLLM proxy.
	// When enabled, the controller adds annotations that allow LiteLLM to discover this model.
	// +optional
	LiteLLM *LiteLLMSpec `json:"litellm,omitempty"`

	// NodeSelector is a map of key-value pairs used to select nodes for scheduling.
	// This maps directly to the pod's nodeSelector field.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

// LiteLLMSpec configures LiteLLM proxy integration.
// +kubebuilder:object:generate=true
type LiteLLMSpec struct {
	// Enabled controls whether LiteLLM annotations are added to the deployment.
	// When true, the controller adds litellm.flexinfer.ai/* annotations.
	// +kubebuilder:default=true
	Enabled *bool `json:"enabled,omitempty"`

	// ServedModelName is the model name exposed to LiteLLM clients.
	// Defaults to the deployment name if not specified.
	// +optional
	ServedModelName string `json:"servedModelName,omitempty"`

	// Aliases is a list of additional model aliases that LiteLLM will route to this deployment.
	// +optional
	Aliases []string `json:"aliases,omitempty"`

	// CopilotAlias is an alias with drop_params=true for Copilot/IDE compatibility.
	// +optional
	CopilotAlias string `json:"copilotAlias,omitempty"`
}

// BenchmarkSpec defines the tuning knobs for the benchmarking process.
// +kubebuilder:object:generate=true
type BenchmarkSpec struct {
	// WarmupIterations is the number of warmup iterations to run before the main benchmark.
	// +kubebuilder:default=2
	WarmupIterations *int32 `json:"warmupIterations,omitempty"`

	// MinDuration is the minimum duration for the benchmark.
	// The benchmark will run for at least this duration or for a minimum number of iterations, whichever comes first.
	// +optional
	MinDuration *metav1.Duration `json:"minDuration,omitempty"`

	// BatchSize is the target number of tokens to generate per benchmark request.
	// +kubebuilder:default=128
	// +kubebuilder:validation:Minimum=1
	BatchSize *int32 `json:"batchSize,omitempty"`

	// Iterations is the number of measurement iterations to run (in addition to warmup).
	// The benchmark may run longer than this if MinDuration has not been satisfied.
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=1
	Iterations *int32 `json:"iterations,omitempty"`
}

// ModelDeploymentStatus defines the observed state of ModelDeployment
// +kubebuilder:object:generate=true
type ModelDeploymentStatus struct {
	// Phase represents the current phase of the ModelDeployment
	// +optional
	Phase ModelDeploymentPhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations of the ModelDeployment's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// AllocatedGPU contains information about the allocated GPU
	// +optional
	AllocatedGPU *GPUAllocation `json:"allocatedGPU,omitempty"`

	// Endpoints defines the access endpoints for the model.
	// +optional
	Endpoints *ModelEndpoints `json:"endpoints,omitempty"`

	// LastAccessTime is the timestamp of the last request to the model.
	// +optional
	LastAccessTime *metav1.Time `json:"lastAccessTime,omitempty"`

	// Metrics contains runtime metrics for the deployment
	// +optional
	Metrics *ModelMetrics `json:"metrics,omitempty"`

	// TokensPerSecond is the measured tokens per second for the model on a specific device class.
	// Stored as a string to avoid precision issues with floats.
	// +optional
	TokensPerSecond string `json:"tokensPerSecond,omitempty"`
}

// ModelDeploymentPhase represents the current phase of a ModelDeployment
type ModelDeploymentPhase string

const (
	// ModelDeploymentPhasePending indicates the ModelDeployment is being processed
	ModelDeploymentPhasePending ModelDeploymentPhase = "Pending"
	// ModelDeploymentPhaseRunning indicates the ModelDeployment is running
	ModelDeploymentPhaseRunning ModelDeploymentPhase = "Running"
	// ModelDeploymentPhaseFailed indicates the ModelDeployment has failed
	ModelDeploymentPhaseFailed ModelDeploymentPhase = "Failed"
	// ModelDeploymentPhaseTerminating indicates the ModelDeployment is being terminated
	ModelDeploymentPhaseTerminating ModelDeploymentPhase = "Terminating"
)

// GPUAllocation represents the GPU allocation details
// +kubebuilder:object:generate=true
type GPUAllocation struct {
	// Node is the name of the node where the GPU is allocated
	// +optional
	Node string `json:"node,omitempty"`

	// Device is the GPU device index
	// +optional
	Device string `json:"device,omitempty"`

	// Type is the GPU type/model
	// +optional
	Type string `json:"type,omitempty"`

	// MemoryMB is the GPU memory in megabytes
	// +optional
	MemoryMB int64 `json:"memoryMB,omitempty"`
}

// ModelEndpoints represents the service endpoints
// +kubebuilder:object:generate=true
type ModelEndpoints struct {
	// Internal is the internal cluster endpoint
	// +optional
	Internal string `json:"internal,omitempty"`

	// External is the external endpoint if exposed
	// +optional
	External string `json:"external,omitempty"`
}

// ModelMetrics represents runtime metrics
// +kubebuilder:object:generate=true
type ModelMetrics struct {
	// TokensPerSecond is the current generation speed.
	// +optional
	TokensPerSecond string `json:"tokensPerSecond,omitempty"`

	// AvgModelLoadTime is the average time to load the model.
	// +optional
	AvgModelLoadTime string `json:"avgModelLoadTime,omitempty"`

	// AvgLatencyMs is the average latency in milliseconds
	// +optional
	AvgLatencyMs string `json:"avgLatencyMs,omitempty"`

	// ErrorRate is the error rate as a percentage
	// +optional
	ErrorRate string `json:"errorRate,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Backend",type="string",JSONPath=".spec.backend"
//+kubebuilder:printcolumn:name="Model",type="string",JSONPath=".spec.model"
//+kubebuilder:printcolumn:name="Replicas",type="integer",JSONPath=".spec.replicas"
//+kubebuilder:printcolumn:name="TPS",type="string",JSONPath=".status.tokensPerSecond"

// ModelDeployment is the Schema for the modeldeployments API
type ModelDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelDeploymentSpec   `json:"spec,omitempty"`
	Status ModelDeploymentStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ModelDeploymentList contains a list of ModelDeployment
type ModelDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelDeployment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModelDeployment{}, &ModelDeploymentList{})
}
