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

// ModelDeploymentSpec defines the desired state of ModelDeployment
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

	// Resources defines the resources required by the model.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Benchmark defines tuning knobs for the benchmarking process.
	// +optional
	Benchmark *BenchmarkSpec `json:"benchmark,omitempty"`
}

// BenchmarkSpec defines the tuning knobs for the benchmarking process.
type BenchmarkSpec struct {
	// WarmupIterations is the number of warmup iterations to run before the main benchmark.
	// +kubebuilder:default=2
	WarmupIterations *int32 `json:"warmupIterations,omitempty"`

	// MinDuration is the minimum duration for the benchmark.
	// The benchmark will run for at least this duration or for a minimum number of iterations, whichever comes first.
	// +optional
	MinDuration *metav1.Duration `json:"minDuration,omitempty"`
}

// ModelDeploymentStatus defines the observed state of ModelDeployment
type ModelDeploymentStatus struct {
	// Conditions represent the latest available observations of the ModelDeployment's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// TokensPerSecond is the measured tokens per second for the model on a specific device class.
	// +optional
	TokensPerSecond float64 `json:"tokensPerSecond,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Backend",type="string",JSONPath=".spec.backend"
//+kubebuilder:printcolumn:name="Model",type="string",JSONPath=".spec.model"
//+kubebuilder:printcolumn:name="Replicas",type="integer",JSONPath=".spec.replicas"
//+kubebuilder:printcolumn:name="TPS",type="number",JSONPath=".status.tokensPerSecond"

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