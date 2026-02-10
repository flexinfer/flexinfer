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

package v1alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LoRAAdapterFinalizer is the finalizer used for LoRAAdapter cleanup.
const LoRAAdapterFinalizer = "flexinfer.ai/lora-cleanup"

// LoRAAdapterPhase represents the lifecycle state of a LoRA adapter.
type LoRAAdapterPhase string

const (
	// LoRAAdapterPhasePending means the adapter has been created but not yet processed.
	LoRAAdapterPhasePending LoRAAdapterPhase = "Pending"
	// LoRAAdapterPhaseCaching means adapter weights are being downloaded/cached.
	LoRAAdapterPhaseCaching LoRAAdapterPhase = "Caching"
	// LoRAAdapterPhaseLoaded means the adapter is loaded in the backend and ready.
	LoRAAdapterPhaseLoaded LoRAAdapterPhase = "Loaded"
	// LoRAAdapterPhaseUnloading means the adapter is being removed from replicas.
	LoRAAdapterPhaseUnloading LoRAAdapterPhase = "Unloading"
	// LoRAAdapterPhaseFailed means the adapter failed to load.
	LoRAAdapterPhaseFailed LoRAAdapterPhase = "Failed"
)

// LoRAAdapterSourceType identifies where adapter weights are stored.
type LoRAAdapterSourceType string

const (
	// LoRASourceHuggingFace downloads from HuggingFace Hub.
	LoRASourceHuggingFace LoRAAdapterSourceType = "HuggingFace"
	// LoRASourceOCI downloads from an OCI registry.
	LoRASourceOCI LoRAAdapterSourceType = "OCI"
	// LoRASourceLocalPath uses a path already available on the model volume.
	LoRASourceLocalPath LoRAAdapterSourceType = "LocalPath"
)

// LoRAAdapterSource specifies where to find the adapter weights.
// +kubebuilder:object:generate=true
type LoRAAdapterSource struct {
	// Type is the source type for adapter weights.
	// +kubebuilder:validation:Enum=HuggingFace;OCI;LocalPath
	// +kubebuilder:validation:Required
	Type LoRAAdapterSourceType `json:"type"`

	// URI is the source identifier.
	// For HuggingFace: "org/adapter-name"
	// For OCI: "registry.example.com/adapters/lora:v1"
	// For LocalPath: "/models/adapters/my-lora"
	// +kubebuilder:validation:Required
	URI string `json:"uri"`
}

// LoRAAdapterSpec defines the desired state of a LoRA adapter.
// +kubebuilder:object:generate=true
type LoRAAdapterSpec struct {
	// ModelRef is the name of the parent Model CR this adapter attaches to.
	// +kubebuilder:validation:Required
	ModelRef string `json:"modelRef"`

	// AdapterName is the unique name used in API requests to select this adapter.
	// This is the value clients pass in the "model" field to use the adapter.
	// +kubebuilder:validation:Required
	AdapterName string `json:"adapterName"`

	// Source specifies where to find the adapter weights.
	// +kubebuilder:validation:Required
	Source LoRAAdapterSource `json:"source"`

	// MaxRank limits the LoRA rank for this adapter.
	// +optional
	MaxRank *int `json:"maxRank,omitempty"`

	// Preload causes the adapter to be loaded immediately when the model starts,
	// rather than waiting for the first request.
	// +optional
	Preload bool `json:"preload,omitempty"`
}

// LoRAAdapterStatus defines the observed state of a LoRA adapter.
// +kubebuilder:object:generate=true
type LoRAAdapterStatus struct {
	// Phase is the current adapter lifecycle phase.
	// +optional
	Phase LoRAAdapterPhase `json:"phase,omitempty"`

	// Conditions represent the latest observations of the adapter's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LoadedReplicas is the number of model replicas where this adapter is loaded.
	// +optional
	LoadedReplicas int32 `json:"loadedReplicas,omitempty"`

	// TotalReplicas is the total number of ready replicas for the parent model.
	// +optional
	TotalReplicas int32 `json:"totalReplicas,omitempty"`

	// Message is a human-friendly status message.
	// +optional
	Message string `json:"message,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=lora
//+kubebuilder:printcolumn:name="Model",type="string",JSONPath=".spec.modelRef"
//+kubebuilder:printcolumn:name="Adapter",type="string",JSONPath=".spec.adapterName"
//+kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
//+kubebuilder:printcolumn:name="Loaded",type="integer",JSONPath=".status.loadedReplicas"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// LoRAAdapter is the Schema for managing LoRA adapter lifecycle.
// It enables declarative hot-swapping of LoRA adapters on running models.
type LoRAAdapter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LoRAAdapterSpec   `json:"spec,omitempty"`
	Status LoRAAdapterStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// LoRAAdapterList contains a list of LoRAAdapter.
type LoRAAdapterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LoRAAdapter `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LoRAAdapter{}, &LoRAAdapterList{})
}
