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
	"encoding/json"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ModelFinalizer is the finalizer used for Model cleanup
const ModelFinalizer = "flexinfer.ai/model-cleanup"

// Model phases
type ModelPhase string

const (
	// ModelPhaseIdle - model is not loaded (scaled to zero)
	ModelPhaseIdle ModelPhase = "Idle"
	// ModelPhasePending - model is being scheduled/loaded
	ModelPhasePending ModelPhase = "Pending"
	// ModelPhaseLoading - model container is starting
	ModelPhaseLoading ModelPhase = "Loading"
	// ModelPhaseReady - model is ready to serve requests
	ModelPhaseReady ModelPhase = "Ready"
	// ModelPhasePreempted - model was evicted by higher priority model
	ModelPhasePreempted ModelPhase = "Preempted"
	// ModelPhaseFailed - model failed to load
	ModelPhaseFailed ModelPhase = "Failed"
)

// Condition types for Model status
const (
	// ConditionModelCached indicates the model is cached on disk
	ConditionModelCached = "Cached"
	// ConditionModelLoaded indicates the model is loaded in memory
	ConditionModelLoaded = "Loaded"
	// ConditionModelReady indicates the model is ready to serve
	ConditionModelReady = "Ready"
	// ConditionModelSchedulable indicates whether the model can be scheduled
	ConditionModelSchedulable = "Schedulable"
	// ConditionConfigValid indicates whether the model's config is conflict-free
	ConditionConfigValid = "ConfigValid"
)

// Condition reasons for Model status
const (
	// ReasonNoMatchingNodes - no nodes match the model's GPU/selector requirements
	ReasonNoMatchingNodes = "NoMatchingNodes"
	// ReasonAmbiguousGPUVendor - multiple GPU vendors found, must specify gpu.vendor
	ReasonAmbiguousGPUVendor = "AmbiguousGPUVendor"
	// ReasonBackendUnsupported - backend does not support the detected GPU vendor
	ReasonBackendUnsupported = "BackendUnsupported"
	// ReasonVRAMInsufficient - model VRAM estimate exceeds GPU capacity
	ReasonVRAMInsufficient = "VRAMInsufficient"
	// ReasonSchedulable - model can be scheduled
	ReasonSchedulable = "Schedulable"
	// ReasonCacheNotReady - waiting for cache to be ready
	ReasonCacheNotReady = "CacheNotReady"
	// ReasonWaitingForActivation - model is idle, waiting for traffic
	ReasonWaitingForActivation = "WaitingForActivation"
	// ReasonStartingBackend - backend container is starting
	ReasonStartingBackend = "StartingBackend"
	// ReasonBackendReady - backend is ready to serve requests
	ReasonBackendReady = "BackendReady"
	// ReasonPreempted - model was preempted by higher priority model
	ReasonPreempted = "Preempted"
	// ReasonAliasConflict - litellm alias or copilotAlias conflicts with another model
	ReasonAliasConflict = "AliasConflict"
	// ReasonConfigValid - model config has no conflicts
	ReasonConfigValid = "ConfigValid"
)

// KVCachePressurePolicy defines how to react to KV-cache pressure.
type KVCachePressurePolicy string

const (
	// KVCachePressurePolicyObserve only monitors and emits events.
	KVCachePressurePolicyObserve KVCachePressurePolicy = "Observe"
	// KVCachePressurePolicyReconfigure patches backend args (e.g., increase swap-space).
	KVCachePressurePolicyReconfigure KVCachePressurePolicy = "Reconfigure"
	// KVCachePressurePolicyEvict scales down the lowest-priority replica under pressure.
	KVCachePressurePolicyEvict KVCachePressurePolicy = "Evict"
)

// KVCacheSpec configures KV-cache management policies for a model.
// FlexInfer observes cache pressure from agent metrics and reacts according to the policy.
// +kubebuilder:object:generate=true
type KVCacheSpec struct {
	// PressurePolicy defines how to react when KV-cache usage exceeds watermarks.
	// +kubebuilder:validation:Enum=Observe;Reconfigure;Evict
	// +kubebuilder:default=Observe
	// +optional
	PressurePolicy KVCachePressurePolicy `json:"pressurePolicy,omitempty"`

	// HighWatermark is the KV-cache utilization ratio that triggers the pressure policy.
	// +optional
	HighWatermark *resource.Quantity `json:"highWatermark,omitempty"`

	// LowWatermark is the target KV-cache utilization ratio after pressure mitigation.
	// +optional
	LowWatermark *resource.Quantity `json:"lowWatermark,omitempty"`

	// MaxBlockSize overrides the vLLM --block-size argument for KV-cache block allocation.
	// +optional
	MaxBlockSize *int `json:"maxBlockSize,omitempty"`

	// SwapSpace configures the vLLM --swap-space argument (GiB) for CPU-offloaded KV-cache.
	// +optional
	SwapSpace *resource.Quantity `json:"swapSpace,omitempty"`

	// ReconfigureCooldown is how long after a reconfigure action before
	// the controller considers restoring the original config.
	// Prevents thrashing between reduced and original settings.
	// Default: 5m.
	// +optional
	ReconfigureCooldown *metav1.Duration `json:"reconfigureCooldown,omitempty"`
}

// KVCacheStatus reports observed KV-cache metrics.
// +kubebuilder:object:generate=true
type KVCacheStatus struct {
	// Utilization is the current KV-cache usage ratio (0.0 to 1.0).
	// +optional
	Utilization string `json:"utilization,omitempty"`

	// Pressure indicates whether the model is under KV-cache pressure.
	// +optional
	Pressure bool `json:"pressure,omitempty"`

	// LastPressureTime is when cache pressure was last detected.
	// +optional
	LastPressureTime *metav1.Time `json:"lastPressureTime,omitempty"`

	// LastAction describes the most recent action taken in response to pressure.
	// +optional
	LastAction string `json:"lastAction,omitempty"`

	// Reconfigured indicates whether the controller has applied config overrides
	// to reduce KV-cache pressure.
	// +optional
	Reconfigured bool `json:"reconfigured,omitempty"`

	// ReconfiguredAt is when the reconfigure action was applied.
	// +optional
	ReconfiguredAt *metav1.Time `json:"reconfiguredAt,omitempty"`

	// OriginalMaxNumSeqs is the original maxNumSeqs value before reconfigure,
	// used to restore the config when pressure subsides.
	// +optional
	OriginalMaxNumSeqs *int32 `json:"originalMaxNumSeqs,omitempty"`

	// ReconfiguredMaxNumSeqs is the reduced maxNumSeqs value applied by reconfigure.
	// +optional
	ReconfiguredMaxNumSeqs *int32 `json:"reconfiguredMaxNumSeqs,omitempty"`

	// Evicted indicates the controller has scaled down replicas due to KV-cache pressure.
	// +optional
	Evicted bool `json:"evicted,omitempty"`

	// EvictedAt is when the eviction was triggered.
	// +optional
	EvictedAt *metav1.Time `json:"evictedAt,omitempty"`
}

// ModelCapabilities declares model capabilities for downstream consumers.
// Unset fields are auto-inferred from backend type and config.
// +kubebuilder:object:generate=true
type ModelCapabilities struct {
	// ToolCalling enables OpenAI-compatible function/tool calling.
	// Auto: true for vllm, ollama; true for llamacpp when jinja=true.
	// +optional
	ToolCalling *bool `json:"toolCalling,omitempty"`

	// Vision enables multimodal image input.
	// Auto: true for llamacpp when mmproj is configured.
	// +optional
	Vision *bool `json:"vision,omitempty"`

	// ImageGeneration marks this as an image generation model.
	// Auto: true for diffusers, comfyui, vllm-omni.
	// +optional
	ImageGeneration *bool `json:"imageGeneration,omitempty"`
}

// ModelSpec defines the desired state of Model
// This is the simplified v1alpha2 spec optimized for homelab users.
// +kubebuilder:object:generate=true
// +kubebuilder:validation:XValidation:rule="!self.source.startsWith('pvc://') || !has(self.cache) || !has(self.cache.strategy) || size(self.cache.strategy) == 0 || self.cache.strategy == 'SharedPVC' || self.cache.strategy == 'Local' || self.cache.strategy == 'None'",message="spec.cache.strategy must be SharedPVC, Local, or None when spec.source is pvc://... (SharedPVC copies to a cache PVC; Local stages onto node-local storage for unified runtimes; None mounts the source directly)"
// +kubebuilder:validation:XValidation:rule="!has(self.resources) || !has(self.resources.limits) || (!('nvidia.com/gpu' in self.resources.limits) && !('amd.com/gpu' in self.resources.limits) && !('gpu.intel.com/i915' in self.resources.limits))",message="Do not set GPU limits in spec.resources.limits; use spec.gpu.vendor/spec.gpu.count instead"
// +kubebuilder:validation:XValidation:rule="!has(self.resources) || !has(self.resources.requests) || (!('nvidia.com/gpu' in self.resources.requests) && !('amd.com/gpu' in self.resources.requests) && !('gpu.intel.com/i915' in self.resources.requests))",message="Do not set GPU requests in spec.resources.requests; use spec.gpu.vendor/spec.gpu.count instead"
// +kubebuilder:validation:XValidation:rule="!has(self.gpu) || self.gpu.vendor == 'auto' || !has(self.nodeSelector) || !('node.flexstack.io/gpu-vendor' in self.nodeSelector) || self.nodeSelector['node.flexstack.io/gpu-vendor'] == self.gpu.vendor",message="spec.nodeSelector['node.flexstack.io/gpu-vendor'] must match spec.gpu.vendor"
type ModelSpec struct {
	// Backend is the inference backend to use.
	// Supported: ollama, vllm, mlc-llm, llamacpp, diffusers, comfyui, vllm-omni
	// +kubebuilder:validation:Required
	Backend string `json:"backend"`

	// Source is the model source URI.
	// Formats:
	//   - HF://org/model        - HuggingFace model
	//   - ollama://model:tag   - Ollama model
	//   - file:///path/to/model - Local file path
	//   - pvc://name/path      - PVC-backed model
	// +kubebuilder:validation:Required
	Source string `json:"source"`

	// GPU configures GPU allocation and sharing.
	// +optional
	GPU *GPUSpec `json:"gpu,omitempty"`

	// Serverless configures scale-to-zero behavior.
	// Enabled by default for homelab use.
	// +optional
	Serverless *ServerlessSpec `json:"serverless,omitempty"`

	// Cache configures model caching behavior.
	// If not specified, cache strategy is inferred from gpu.shared.
	// +optional
	Cache *CacheSpec `json:"cache,omitempty"`

	// Config contains backend-specific configuration as JSON.
	// These are passed to the backend plugin for container configuration.
	// See backend documentation for available options.
	// Example: {"mode": "server", "maxNumSequence": 4}
	// +optional
	Config *apiextensionsv1.JSON `json:"config,omitempty"`

	// Resources defines compute resources for the model container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// NodeSelector for scheduling the model to specific nodes.
	// If not specified, auto-detects GPU nodes.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations are appended to the pod's tolerations.
	// The controller always adds a GPU-node toleration (dedicated=gpu:NoSchedule);
	// any tolerations specified here are merged with that default.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// LiteLLM configures proxy integration for unified API access.
	// +optional
	LiteLLM *LiteLLMSpec `json:"litellm,omitempty"`

	// ServiceLabels are semantic labels describing model capabilities.
	// Used for dynamic routing (e.g., ["textgen", "code", "fast"]).
	// +optional
	// +kubebuilder:validation:MaxItems=10
	ServiceLabels []string `json:"serviceLabels,omitempty"`

	// KVCache configures KV-cache pressure management.
	// Only effective with backends that support KV-cache tuning (vLLM).
	// +optional
	KVCache *KVCacheSpec `json:"kvCache,omitempty"`

	// Capabilities declares model capabilities for downstream consumers.
	// Unset fields are auto-inferred from backend type and config.
	// +optional
	Capabilities *ModelCapabilities `json:"capabilities,omitempty"`

	// Quantize configures post-download quantization of model weights.
	// When set, the controller downloads the source model, quantizes it,
	// and serves the quantized output.
	// +optional
	Quantize *QuantizationSpec `json:"quantize,omitempty"`
}

// GPUVendor selects which vendor GPU resource to request.
type GPUVendor string

const (
	GPUVendorAuto   GPUVendor = "auto"
	GPUVendorNVIDIA GPUVendor = "nvidia"
	GPUVendorAMD    GPUVendor = "amd"
	GPUVendorCPU    GPUVendor = "cpu"
)

// GPUSpec configures GPU allocation and sharing.
// +kubebuilder:object:generate=true
// +kubebuilder:validation:XValidation:rule="self.vendor != 'cpu' || !has(self.count)",message="gpu.count must be omitted when gpu.vendor is cpu"
// +kubebuilder:validation:XValidation:rule="self.vendor != 'cpu' || !has(self.vramEstimateMB)",message="gpu.vramEstimateMB must be omitted when gpu.vendor is cpu"
// +kubebuilder:validation:XValidation:rule="self.vendor != 'cpu' || !has(self.shared) || size(self.shared) == 0",message="gpu.shared must be empty when gpu.vendor is cpu"
type GPUSpec struct {
	// Vendor selects the GPU vendor to target.
	// Use "auto" (or omit) to auto-detect based on available nodes.
	// Use "cpu" for CPU-only inference (no GPU resource requests).
	// +kubebuilder:validation:Enum=auto;nvidia;amd;cpu
	// +kubebuilder:default=auto
	// +optional
	Vendor GPUVendor `json:"vendor,omitempty"`

	// Shared groups models together for time-sharing a GPU.
	// Models with the same shared value compete for the same GPU,
	// with higher priority models preempting lower priority ones.
	// If not set, model gets exclusive GPU access.
	// +optional
	Shared string `json:"shared,omitempty"`

	// Priority within a shared group. Higher = more important.
	// Models with higher priority preempt lower priority ones.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000
	// +kubebuilder:default=100
	// +optional
	Priority *int32 `json:"priority,omitempty"`

	// Count is the number of GPUs required.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=8
	// +optional
	Count *int32 `json:"count,omitempty"`

	// VRAMEstimateMB is the estimated VRAM usage in MB.
	// Used for scheduling decisions in shared groups.
	// +optional
	VRAMEstimateMB *int64 `json:"vramEstimateMB,omitempty"`

	// SwapCooldown overrides the default anti-thrashing cooldown for this
	// model's shared group. After a swap, the controller blocks further
	// demand-based swaps for this duration. Defaults to 5m if unset.
	// Set lower for small models that load quickly, higher for large ones.
	// +optional
	SwapCooldown *metav1.Duration `json:"swapCooldown,omitempty"`
}

// ServerlessSpec configures scale-to-zero behavior.
// +kubebuilder:object:generate=true
type ServerlessSpec struct {
	// Enabled controls scale-to-zero.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// MinReplicas is the minimum number of replicas to keep running.
	// Use 0 for true scale-to-zero. Use 1 for "warm start" behavior.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10
	// +optional
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// IdleTimeout is how long to wait before scaling to zero.
	// Default: 5m for LLMs, 10m for image generation backends.
	// +optional
	IdleTimeout *metav1.Duration `json:"idleTimeout,omitempty"`

	// ColdStartTimeout is max time to wait for model activation.
	// Requests exceeding this get 503 Service Unavailable.
	// +kubebuilder:default="60s"
	// +optional
	ColdStartTimeout *metav1.Duration `json:"coldStartTimeout,omitempty"`
}

// CacheSpec configures model caching behavior.
// +kubebuilder:object:generate=true
type CacheSpec struct {
	// Strategy for caching models.
	// - Memory: Keep model weights in RAM (fast reload, uses RAM)
	// - SharedPVC: Store on shared PVC (slower reload, saves RAM)
	// - Local: Use a hostPath directory (e.g., NVMe) for model storage
	// - None: No caching (downloads each time)
	// Default: Memory if gpu.shared is set, SharedPVC otherwise.
	// +kubebuilder:validation:Enum=Memory;SharedPVC;Local;None
	// +optional
	Strategy string `json:"strategy,omitempty"`

	// PVCName is the PVC to use for SharedPVC strategy.
	// Auto-created if not specified.
	// +optional
	PVCName string `json:"pvcName,omitempty"`

	// StorageClass for auto-created PVCs.
	// +kubebuilder:default="longhorn"
	// +optional
	StorageClass string `json:"storageClass,omitempty"`

	// Size is the storage size for auto-created PVCs.
	// +kubebuilder:default="50Gi"
	// +optional
	Size string `json:"size,omitempty"`

	// HostPath is the base directory on the node for Local strategy model storage.
	// A subdirectory per model is created automatically: <hostPath>/<namespace>/<model-name>/
	// +kubebuilder:default="/var/lib/flexinfer/models"
	// +optional
	HostPath string `json:"hostPath,omitempty"`

	// CompilationCache configures persistent GPU kernel compilation caching.
	// When enabled, MIOpen/PyTorch/Triton compilation artifacts are stored on
	// a hostPath volume that survives pod restarts, eliminating recompilation
	// on GPU swaps. Only effective for AMD ROCm backends.
	// +optional
	CompilationCache *CompilationCacheSpec `json:"compilationCache,omitempty"`

	// FlashLoader configures the flash-loader init container for multi-tier model loading.
	// When enabled, model files are parallel-copied from the source volume (PVC or hostPath)
	// to a tmpfs volume before the backend starts, reducing cold-start I/O latency.
	// +optional
	FlashLoader *FlashLoaderSpec `json:"flashLoader,omitempty"`
}

// CompilationCacheSpec configures host-persistent GPU compilation caching.
// +kubebuilder:object:generate=true
type CompilationCacheSpec struct {
	// Enabled controls whether compilation cache persistence is active.
	// Default: true when gpu.shared is set and gpu.vendor is amd.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// HostPath is the base directory on the node for compilation caches.
	// A subdirectory per model is created automatically: <hostPath>/<namespace>/<model-name>/
	// +kubebuilder:default="/var/lib/flexinfer/compile-cache"
	// +optional
	HostPath string `json:"hostPath,omitempty"`

	// SizeLimit is a soft limit for the compilation cache directory.
	// Not enforced by Kubernetes (hostPath has no built-in quota), but
	// used by the controller to set resource expectations.
	// +kubebuilder:default="2Gi"
	// +optional
	SizeLimit string `json:"sizeLimit,omitempty"`
}

// FlashLoaderSpec configures the flash-loader init container for multi-tier model loading.
// When enabled, model files are parallel-copied from the source volume (PVC or hostPath)
// to a tmpfs volume before the backend starts, reducing cold-start I/O latency.
// +kubebuilder:object:generate=true
type FlashLoaderSpec struct {
	// Enabled activates flash-loader. Default: auto (true when cache.strategy
	// is Local or SharedPVC with gpu.shared set).
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Concurrency is the number of parallel copy goroutines.
	// +kubebuilder:default=4
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=32
	// +optional
	Concurrency *int32 `json:"concurrency,omitempty"`

	// TmpfsSizeLimit caps the tmpfs destination volume.
	// Defaults to cache.size if not specified.
	// +optional
	TmpfsSizeLimit string `json:"tmpfsSizeLimit,omitempty"`

	// BufferSizeKB sets the per-worker I/O buffer in KB.
	// Larger buffers improve NVMe sequential throughput.
	// +kubebuilder:default=4096
	// +kubebuilder:validation:Minimum=32
	// +kubebuilder:validation:Maximum=16384
	// +optional
	BufferSizeKB *int32 `json:"bufferSizeKB,omitempty"`

	// VerifyIntegrity enables post-copy size verification.
	// +optional
	VerifyIntegrity *bool `json:"verifyIntegrity,omitempty"`

	// Image overrides the flash-loader init container image.
	// +optional
	Image string `json:"image,omitempty"`
}

// LiteLLMSpec configures LiteLLM proxy integration.
// +kubebuilder:object:generate=true
type LiteLLMSpec struct {
	// Enabled controls whether LiteLLM annotations are added.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// ServedModelName is the model name exposed to clients.
	// Defaults to Model resource name.
	// +optional
	ServedModelName string `json:"servedModelName,omitempty"`

	// Aliases are additional model names that route to this model.
	// +optional
	Aliases []string `json:"aliases,omitempty"`

	// CopilotAlias enables IDE/Copilot compatibility mode.
	// +optional
	CopilotAlias string `json:"copilotAlias,omitempty"`
}

// ModelStatus defines the observed state of Model
// +kubebuilder:object:generate=true
type ModelStatus struct {
	// Phase is the current model lifecycle phase.
	// +optional
	Phase ModelPhase `json:"phase,omitempty"`

	// Conditions represent the latest observations of the Model's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// GPU contains allocated GPU information.
	// +optional
	GPU *GPUStatus `json:"gpu,omitempty"`

	// Endpoint is the service endpoint for the model.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// LastActiveTime is when the model last served a request.
	// +optional
	LastActiveTime *metav1.Time `json:"lastActiveTime,omitempty"`

	// Metrics contains runtime performance metrics.
	// +optional
	Metrics *MetricsStatus `json:"metrics,omitempty"`

	// SharedGroup tracks state within the shared GPU group.
	// Only populated when gpu.shared is set.
	// +optional
	SharedGroup *SharedGroupStatus `json:"sharedGroup,omitempty"`

	// Cache tracks the cache state.
	// +optional
	Cache *CacheStatus `json:"cache,omitempty"`

	// KVCache tracks observed KV-cache metrics and pressure state.
	// +optional
	KVCache *KVCacheStatus `json:"kvCache,omitempty"`
}

// GPUStatus contains allocated GPU information.
// +kubebuilder:object:generate=true
type GPUStatus struct {
	// Node is where the GPU is allocated.
	Node string `json:"node,omitempty"`
	// Device is the GPU device index.
	Device string `json:"device,omitempty"`
	// Vendor is NVIDIA, AMD, or Intel.
	Vendor string `json:"vendor,omitempty"`
	// Architecture is the GPU arch (e.g., sm_89, gfx1100).
	Architecture string `json:"architecture,omitempty"`
	// MemoryMB is total GPU memory.
	MemoryMB int64 `json:"memoryMB,omitempty"`
}

// MetricsStatus contains runtime metrics.
// +kubebuilder:object:generate=true
type MetricsStatus struct {
	// TokensPerSecond is the measured generation speed.
	TokensPerSecond string `json:"tokensPerSecond,omitempty"`
	// LoadTimeSeconds is how long the model took to load.
	LoadTimeSeconds string `json:"loadTimeSeconds,omitempty"`
	// AvgLatencyMs is average request latency.
	AvgLatencyMs string `json:"avgLatencyMs,omitempty"`
}

// SharedGroupStatus tracks state within a shared GPU group.
// +kubebuilder:object:generate=true
type SharedGroupStatus struct {
	// GroupName is the shared group identifier.
	GroupName string `json:"groupName,omitempty"`
	// State within the group: Active, Queued, Preempted.
	State string `json:"state,omitempty"`
	// QueuePosition when waiting to be loaded.
	QueuePosition int32 `json:"queuePosition,omitempty"`
	// PreemptedBy is which model caused preemption.
	PreemptedBy string `json:"preemptedBy,omitempty"`
	// PreemptedAt is when preemption occurred.
	PreemptedAt *metav1.Time `json:"preemptedAt,omitempty"`
}

// CacheStatus tracks the cache state.
// +kubebuilder:object:generate=true
type CacheStatus struct {
	// Strategy being used.
	Strategy string `json:"strategy,omitempty"`
	// Ready indicates cache is populated.
	Ready bool `json:"ready,omitempty"`
	// PVCName is the PVC being used (if SharedPVC).
	PVCName string `json:"pvcName,omitempty"`
	// JobName is the cache job responsible for ensuring the artifact is present.
	// +optional
	JobName string `json:"jobName,omitempty"`
	// JobPhase is a coarse cache job phase: Pending, Running, Succeeded, Failed.
	// +optional
	JobPhase string `json:"jobPhase,omitempty"`
	// Message is an optional human-friendly cache status message.
	// +optional
	Message string `json:"message,omitempty"`
	// SizeBytes is the cached model size.
	SizeBytes int64 `json:"sizeBytes,omitempty"`
	// Quantization records the result of weight quantization.
	// +optional
	Quantization *QuantizationStatus `json:"quantization,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=mdl
//+kubebuilder:printcolumn:name="Backend",type="string",JSONPath=".spec.backend"
//+kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
//+kubebuilder:printcolumn:name="Shared",type="string",JSONPath=".spec.gpu.shared",priority=1
//+kubebuilder:printcolumn:name="Priority",type="integer",JSONPath=".spec.gpu.priority",priority=1
//+kubebuilder:printcolumn:name="TPS",type="string",JSONPath=".status.metrics.tokensPerSecond"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Model is the simplified resource for deploying ML models.
// It replaces the v1alpha1 ModelDeployment + GPUGroup workflow
// with a single, homelab-friendly resource.
type Model struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelSpec   `json:"spec,omitempty"`
	Status ModelStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ModelList contains a list of Model
type ModelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Model `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Model{}, &ModelList{})
}

// Helper methods for ModelSpec

// GetPriority returns the GPU priority, defaulting to 100.
func (s *ModelSpec) GetPriority() int32 {
	if s.GPU != nil && s.GPU.Priority != nil {
		return *s.GPU.Priority
	}
	return 100
}

// GetGPUCount returns the number of GPUs required, defaulting to 1.
func (s *ModelSpec) GetGPUCount() int32 {
	if s.GPU == nil {
		return 0
	}
	if strings.ToLower(string(s.GPU.Vendor)) == string(GPUVendorCPU) {
		return 0
	}
	if s.GPU.Count != nil {
		return *s.GPU.Count
	}
	return 1
}

// GetGPUVendor returns the configured GPU vendor selector.
// Defaults to "auto" when not specified.
func (s *ModelSpec) GetGPUVendor() GPUVendor {
	if s.GPU == nil {
		return GPUVendorCPU
	}
	v := strings.ToLower(strings.TrimSpace(string(s.GPU.Vendor)))
	if v == "" {
		return GPUVendorAuto
	}
	switch GPUVendor(v) {
	case GPUVendorAuto, GPUVendorNVIDIA, GPUVendorAMD, GPUVendorCPU:
		return GPUVendor(v)
	default:
		return GPUVendorAuto
	}
}

// GetMinReplicas returns the minimum number of replicas to keep running.
// Defaults to 0 when serverless is enabled.
func (s *ModelSpec) GetMinReplicas() int32 {
	if !s.IsServerless() {
		return 1
	}
	if s.Serverless != nil && s.Serverless.MinReplicas != nil {
		return *s.Serverless.MinReplicas
	}
	return 0
}

// IsShared returns true if this model participates in GPU sharing.
func (s *ModelSpec) IsShared() bool {
	return s.GPU != nil && s.GPU.Shared != ""
}

// IsServerless returns true if scale-to-zero is enabled.
func (s *ModelSpec) IsServerless() bool {
	if s.Serverless == nil || s.Serverless.Enabled == nil {
		return true // Default to serverless for homelab
	}
	return *s.Serverless.Enabled
}

// GetConfigMap parses the JSON config into a map.
// Returns nil if config is not set.
func (s *ModelSpec) GetConfigMap() map[string]any {
	if s.Config == nil || s.Config.Raw == nil {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(s.Config.Raw, &result); err != nil {
		return nil
	}
	return result
}

// ConfigString returns a config value as string with default.
func (s *ModelSpec) ConfigString(key, defaultVal string) string {
	cfg := s.GetConfigMap()
	if cfg == nil {
		return defaultVal
	}
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return defaultVal
}

// ConfigInt returns a config value as int with default.
func (s *ModelSpec) ConfigInt(key string, defaultVal int) int {
	cfg := s.GetConfigMap()
	if cfg == nil {
		return defaultVal
	}
	switch v := cfg[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return defaultVal
}

// GetKVCacheHighWatermark returns the high watermark as a float64, defaulting to 0.85.
func (s *ModelSpec) GetKVCacheHighWatermark() float64 {
	if s.KVCache != nil && s.KVCache.HighWatermark != nil {
		return s.KVCache.HighWatermark.AsApproximateFloat64()
	}
	return 0.85
}

// GetKVCacheLowWatermark returns the low watermark as a float64, defaulting to 0.60.
func (s *ModelSpec) GetKVCacheLowWatermark() float64 {
	if s.KVCache != nil && s.KVCache.LowWatermark != nil {
		return s.KVCache.LowWatermark.AsApproximateFloat64()
	}
	return 0.60
}

// GetKVCachePressurePolicy returns the pressure policy, defaulting to Observe.
func (s *ModelSpec) GetKVCachePressurePolicy() KVCachePressurePolicy {
	if s.KVCache != nil && s.KVCache.PressurePolicy != "" {
		return s.KVCache.PressurePolicy
	}
	return KVCachePressurePolicyObserve
}

// GetKVCacheReconfigureCooldown returns the reconfigure cooldown, defaulting to 5m.
func (s *ModelSpec) GetKVCacheReconfigureCooldown() time.Duration {
	if s.KVCache != nil && s.KVCache.ReconfigureCooldown != nil {
		return s.KVCache.ReconfigureCooldown.Duration
	}
	return 5 * time.Minute
}

// ConfigBool returns a config value as bool with default.
func (s *ModelSpec) ConfigBool(key string, defaultVal bool) bool {
	cfg := s.GetConfigMap()
	if cfg == nil {
		return defaultVal
	}
	if v, ok := cfg[key].(bool); ok {
		return v
	}
	return defaultVal
}
