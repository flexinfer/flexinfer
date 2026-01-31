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
	// StorageStrategyMemory caches the model in RAM (/dev/shm) on each node for faster loading
	StorageStrategyMemory StorageStrategy = "Memory"
)

// EvictionPolicy defines the cache eviction strategy
type EvictionPolicy string

const (
	// EvictionPolicyLRU evicts least recently used caches first
	EvictionPolicyLRU EvictionPolicy = "LRU"
	// EvictionPolicyLFU evicts least frequently used caches first
	EvictionPolicyLFU EvictionPolicy = "LFU"
	// EvictionPolicyFIFO evicts oldest caches first (by creation time)
	EvictionPolicyFIFO EvictionPolicy = "FIFO"
	// EvictionPolicyNone disables automatic eviction
	EvictionPolicyNone EvictionPolicy = "None"
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

	// === LRU Eviction Configuration ===

	// EvictionPolicy defines the cache eviction strategy when storage pressure occurs.
	// Supports LRU (least recently used), LFU (least frequently used), FIFO (oldest first),
	// or None to disable automatic eviction.
	// +kubebuilder:default="LRU"
	// +kubebuilder:validation:Enum=LRU;LFU;FIFO;None
	// +optional
	EvictionPolicy EvictionPolicy `json:"evictionPolicy,omitempty"`

	// EvictionThresholdPercent is the storage utilization percentage that triggers eviction.
	// When /dev/shm usage exceeds this threshold, the controller evicts caches based on policy.
	// Only applies to Memory storage strategy.
	// +kubebuilder:default=85
	// +kubebuilder:validation:Minimum=50
	// +kubebuilder:validation:Maximum=99
	// +optional
	EvictionThresholdPercent *int32 `json:"evictionThresholdPercent,omitempty"`

	// MaxCacheSizeBytes limits the maximum size this cache can occupy.
	// Used for capacity planning and eviction decisions.
	// +optional
	MaxCacheSizeBytes *int64 `json:"maxCacheSizeBytes,omitempty"`

	// ModelGroup associates this cache with a named group for coordinated eviction.
	// Caches in the same group are evicted together when any member exceeds thresholds.
	// Useful for models with shared tokenizers or adapters.
	// +optional
	ModelGroup *string `json:"modelGroup,omitempty"`

	// RetentionPriority influences eviction order within the same policy tier.
	// Higher values (0-100) mean the cache is kept longer.
	// Production models should have higher priority than experimental ones.
	// +kubebuilder:default=50
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	RetentionPriority *int32 `json:"retentionPriority,omitempty"`
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

	// === LRU Eviction Observability ===

	// LastAccessTime is the most recent time this cache was accessed by a ModelDeployment.
	// Updated when a deployment references this cache.
	// +optional
	LastAccessTime *metav1.Time `json:"lastAccessTime,omitempty"`

	// EvictionCount tracks how many times this cache has been evicted since creation.
	// Useful for identifying hot/cold models and tuning retention priorities.
	// +optional
	EvictionCount int64 `json:"evictionCount,omitempty"`

	// CacheSizeBytes is the actual size of the cached model data.
	// Updated during provisioning and periodically during reconciliation.
	// +optional
	CacheSizeBytes int64 `json:"cacheSizeBytes,omitempty"`

	// ResidencySeconds is the cumulative time this cache has been resident in memory.
	// Resets when cache is evicted. Useful for cost attribution.
	// +optional
	ResidencySeconds int64 `json:"residencySeconds,omitempty"`

	// CacheHitRate is the ratio of successful cache lookups to total requests.
	// Format: "0.95" (95% hit rate). Useful for tuning eviction policies.
	// +optional
	CacheHitRate string `json:"cacheHitRate,omitempty"`

	// ResidentSince is the timestamp when this cache became resident in memory.
	// Nil when cache is not currently resident. Used for residency calculations.
	// +optional
	ResidentSince *metav1.Time `json:"residentSince,omitempty"`

	// AccessCount tracks total number of accesses to this cache.
	// Used for LFU eviction policy and hit rate calculations.
	// +optional
	AccessCount int64 `json:"accessCount,omitempty"`

	// === OCI Registry Status ===

	// OCIDigest is the immutable digest of the OCI artifact that was pulled.
	// Format: sha256:<hash>. Only set for OCI/ORAS sources.
	// +optional
	OCIDigest string `json:"ociDigest,omitempty"`

	// OCIPulledAt is the timestamp when the OCI artifact was last pulled.
	// Useful for tracking freshness and triggering re-pulls on tag updates.
	// +optional
	OCIPulledAt *metav1.Time `json:"ociPulledAt,omitempty"`

	// OCIRegistry is the registry hostname where the artifact was pulled from.
	// Extracted from the source URL for observability.
	// +optional
	OCIRegistry string `json:"ociRegistry,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
//+kubebuilder:printcolumn:name="Strategy",type="string",JSONPath=".spec.storageStrategy"
//+kubebuilder:printcolumn:name="Ready",type="integer",JSONPath=".status.readyNodes"
//+kubebuilder:printcolumn:name="Total",type="integer",JSONPath=".status.totalNodes"
//+kubebuilder:printcolumn:name="Path",type="string",JSONPath=".status.path"
//+kubebuilder:printcolumn:name="Size",type="integer",JSONPath=".status.cacheSizeBytes",priority=1
//+kubebuilder:printcolumn:name="LastAccess",type="date",JSONPath=".status.lastAccessTime",priority=1
//+kubebuilder:printcolumn:name="Evictions",type="integer",JSONPath=".status.evictionCount",priority=1

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
