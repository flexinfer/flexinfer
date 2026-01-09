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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StorageStrategy defines how the model is stored
type StorageStrategy string

const (
	// StorageStrategyAuto lets the controller decide based on cluster capabilities
	StorageStrategyAuto StorageStrategy = "Auto"
	// StorageStrategyNodeLocal caches the model on each node's local disk
	StorageStrategyNodeLocal StorageStrategy = "NodeLocal"
	// StorageStrategySharedPVC uses a ReadWriteMany PVC (requires capable storage class)
	StorageStrategySharedPVC StorageStrategy = "SharedPVC"
	// StorageStrategyEphemeral downloads the model on pod startup (no persistent caching)
	StorageStrategyEphemeral StorageStrategy = "Ephemeral"
)

// ModelCachePhase describes the lifecycle of the cache
type ModelCachePhase string

const (
	// ModelCachePhasePending means the cache request is received but not processed
	ModelCachePhasePending ModelCachePhase = "Pending"
	// ModelCachePhaseInitializing means the storage is being provisioned
	ModelCachePhaseInitializing ModelCachePhase = "Initializing"
	// ModelCachePhaseProvisoning means the model is being downloaded/synced
	ModelCachePhaseProvisioning ModelCachePhase = "Provisioning"
	// ModelCachePhaseReady means the model is ready to be used
	ModelCachePhaseReady ModelCachePhase = "Ready"
	// ModelCachePhaseFailed means something went wrong
	ModelCachePhaseFailed ModelCachePhase = "Failed"
)

// ModelCacheSpec defines the desired state of ModelCache
// +kubebuilder:object:generate=true
type ModelCacheSpec struct {
	// Source is the URI of the model to cache.
	// Formats:
	//   - huggingface://meta-llama/Llama-2-7b-chat-hf (standard HF models)
	//   - mlc://mlc-ai/Qwen3-0.6B-q4f16_1-MLC (MLC-compiled models)
	//   - HF://mlc-ai/Qwen3-0.6B-q4f16_1-MLC (MLC shorthand for HuggingFace)
	//   - oci://registry.example.com/models/llama3:v1 (OCI artifact)
	//   - oras://registry.example.com/models/llama3@sha256:... (OCI with digest)
	// +kubebuilder:validation:Required
	Source string `json:"source"`

	// StorageStrategy defines the caching strategy
	// +kubebuilder:default="Auto"
	StorageStrategy StorageStrategy `json:"storageStrategy,omitempty"`

	// ClusterStorageClassName is the storage class to use for SharedPVC strategy
	// +optional
	ClusterStorageClassName *string `json:"clusterStorageClassName,omitempty"`

	// ExistingClaimName references an existing PVC to use instead of creating one.
	// When set, the controller skips PVC creation and uses the referenced claim.
	// The model will be downloaded to a subdirectory named after the ModelCache.
	// +optional
	ExistingClaimName *string `json:"existingClaimName,omitempty"`

	// ModelPath is the subdirectory within the PVC where the model is stored.
	// Defaults to the ModelCache name if not specified.
	// +optional
	ModelPath *string `json:"modelPath,omitempty"`

	// StorageSize is the requested storage size for newly created PVCs.
	// Ignored when existingClaimName is set.
	// +kubebuilder:default="50Gi"
	// +optional
	StorageSize *string `json:"storageSize,omitempty"`

	// SecretRef is the name of the secret containing authentication credentials (key: HF_TOKEN)
	// +optional
	SecretRef *string `json:"secretRef,omitempty"`

	// OCIRegistrySecretRef is the name of a kubernetes.io/dockerconfigjson secret
	// for authenticating to OCI registries. Used when source begins with oci:// or oras://
	// +optional
	OCIRegistrySecretRef *string `json:"ociRegistrySecretRef,omitempty"`

	// NodeSelector restricts which nodes should cache the model.
	// Used with NodeLocal strategy. Defaults to nodes with nvidia.com/gpu.present=true.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// HostPath is the base directory for NodeLocal storage.
	// Defaults to /var/lib/flexinfer/models if not specified.
	// +optional
	HostPath *string `json:"hostPath,omitempty"`
}

// ModelCacheStatus defines the observed state of ModelCache
// +kubebuilder:object:generate=true
type ModelCacheStatus struct {
	// Phase represents the current lifecycle state
	// +optional
	Phase ModelCachePhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations of the state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Path is the path where the model is accessible (volume name or host path)
	// +optional
	Path string `json:"path,omitempty"`

	// SizeBytes is the size of the cached model
	// +optional
	SizeBytes string `json:"sizeBytes,omitempty"`

	// ReadyNodes is the count of nodes where the model is fully cached (NodeLocal strategy)
	// +optional
	ReadyNodes int32 `json:"readyNodes,omitempty"`

	// TotalNodes is the count of nodes that should have the model cached (NodeLocal strategy)
	// +optional
	TotalNodes int32 `json:"totalNodes,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
//+kubebuilder:printcolumn:name="Strategy",type="string",JSONPath=".spec.storageStrategy"
//+kubebuilder:printcolumn:name="Ready",type="integer",JSONPath=".status.readyNodes"
//+kubebuilder:printcolumn:name="Total",type="integer",JSONPath=".status.totalNodes"
//+kubebuilder:printcolumn:name="Path",type="string",JSONPath=".status.path"

// ModelCache is the Schema for the modelcaches API
type ModelCache struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelCacheSpec   `json:"spec,omitempty"`
	Status ModelCacheStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ModelCacheList contains a list of ModelCache
type ModelCacheList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelCache `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModelCache{}, &ModelCacheList{})
}
