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
	// StorageStrategySharedPVC uses a shared PVC (RWO when nodeSelector is set, RWX otherwise)
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
	// ModelCachePhaseAbliterating means the model weights are being abliterated
	ModelCachePhaseAbliterating ModelCachePhase = "Abliterating"
	// ModelCachePhaseFinetuning means the model is being finetuned
	ModelCachePhaseFinetuning ModelCachePhase = "Finetuning"
	// ModelCachePhaseQuantizing means the model is being quantized
	ModelCachePhaseQuantizing ModelCachePhase = "Quantizing"
	// ModelCachePhasePublishing means the model is being published to an external registry
	ModelCachePhasePublishing ModelCachePhase = "Publishing"
	// ModelCachePhaseReady means the model is ready to be used
	ModelCachePhaseReady ModelCachePhase = "Ready"
	// ModelCachePhaseFailed means something went wrong
	ModelCachePhaseFailed ModelCachePhase = "Failed"
)

// QuantizationFormat identifies the quantization format to produce.
// +kubebuilder:validation:Enum=GGUF;AWQ;GPTQ;EXL2;FP8
type QuantizationFormat string

const (
	QuantizationFormatGGUF QuantizationFormat = "GGUF"
	QuantizationFormatAWQ  QuantizationFormat = "AWQ"
	QuantizationFormatGPTQ QuantizationFormat = "GPTQ"
	QuantizationFormatEXL2 QuantizationFormat = "EXL2"
	QuantizationFormatFP8  QuantizationFormat = "FP8"
)

// CalibrationSpec configures calibration parameters for AWQ/GPTQ quantization.
// +kubebuilder:object:generate=true
type CalibrationSpec struct {
	// MaxSeqLen is the maximum sequence length for calibration samples.
	// Defaults to 4096.
	// +kubebuilder:validation:Minimum=128
	// +kubebuilder:validation:Maximum=32768
	// +optional
	MaxSeqLen *int32 `json:"maxSeqLen,omitempty"`

	// MaxSamples is the number of calibration samples to use.
	// Defaults to 256.
	// +kubebuilder:validation:Minimum=8
	// +kubebuilder:validation:Maximum=2048
	// +optional
	MaxSamples *int32 `json:"maxSamples,omitempty"`

	// NParallelCalibSamples controls how many calibration samples are processed
	// in parallel during AWQ quantization. Lower values reduce peak VRAM usage.
	// Defaults to 16 for models >10B params on <=24GB VRAM.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=256
	// +optional
	NParallelCalibSamples *int32 `json:"nParallelCalibSamples,omitempty"`

	// Dataset is the HuggingFace dataset used for calibration samples.
	// Defaults to "mit-han-lab/pile-val-backup".
	// +optional
	Dataset *string `json:"dataset,omitempty"`
}

// QuantizationSpec configures post-download quantization of model weights.
// When set on a ModelCache, the controller creates a quantization Job after
// the download completes.
// +kubebuilder:object:generate=true
type QuantizationSpec struct {
	// Format is the target quantization format.
	// +kubebuilder:validation:Required
	Format QuantizationFormat `json:"format"`

	// GGUFType is the GGUF quantization level (e.g., Q4_K_M, Q5_K_M, Q8_0).
	// Only used when Format is GGUF.
	// +optional
	GGUFType string `json:"ggufType,omitempty"`

	// Bits is the quantization bit width for AWQ/GPTQ formats.
	// +optional
	Bits *int32 `json:"bits,omitempty"`

	// GroupSize is the quantization group size for AWQ/GPTQ formats.
	// +optional
	GroupSize *int32 `json:"groupSize,omitempty"`

	// UseGPU enables GPU-accelerated quantization (required for AWQ/GPTQ).
	// GGUF quantization runs on CPU only.
	// +optional
	UseGPU bool `json:"useGPU,omitempty"`

	// MaxMemoryGB limits the memory available to the quantization job.
	// Defaults to 32GB for GGUF, 48GB for AWQ/GPTQ.
	// +optional
	MaxMemoryGB *int32 `json:"maxMemoryGB,omitempty"`

	// TimeoutSeconds overrides the default 2-hour deadline for quantization jobs.
	// +kubebuilder:validation:Minimum=300
	// +kubebuilder:validation:Maximum=43200
	// +optional
	TimeoutSeconds *int64 `json:"timeoutSeconds,omitempty"`

	// Sym enables symmetric quantization for GPTQ (default true).
	// sym=true is required for ExLlama kernels (best ROCm performance).
	// Ignored for non-GPTQ formats.
	// +optional
	Sym *bool `json:"sym,omitempty"`

	// DescAct enables activation reordering (desc_act) for GPTQ (default false).
	// desc_act=false gives faster inference; true gives slightly better quality.
	// Ignored for non-GPTQ formats.
	// +optional
	DescAct *bool `json:"descAct,omitempty"`

	// Calibration configures calibration parameters for AWQ/GPTQ quantization.
	// Ignored for GGUF and EXL2 formats.
	// +optional
	Calibration *CalibrationSpec `json:"calibration,omitempty"`

	// GPUMemoryFraction caps the fraction of GPU VRAM available to the
	// quantization process (e.g. "0.80"). Defaults to "0.80".
	// Lower values leave more headroom for GPU driver overhead (important on ROCm
	// where HIP/GTT allocations bypass container cgroup limits).
	// Only used for GPTQ format.
	// +optional
	GPUMemoryFraction *string `json:"gpuMemoryFraction,omitempty"`

	// DynamicExclusion controls which module patterns are excluded from
	// quantization (kept at full precision). Defaults to "auto".
	//   - "auto": auto-detect hybrid architectures and exclude attention/expert/
	//     vision/MTP modules (matches official Qwen GPTQ-Int4 approach).
	//   - "none": quantize all modules to the target bit width (pure INT4).
	//     Produces smaller models that fit on smaller VRAM cards.
	// Only used for GPTQ format.
	// +kubebuilder:validation:Enum=auto;none
	// +optional
	DynamicExclusion *string `json:"dynamicExclusion,omitempty"`

	// NodeSelector overrides spec.nodeSelector for quantization jobs.
	// Useful when quantization needs a different node (e.g., more RAM).
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

// QuantizationStatus records the result of quantization.
// +kubebuilder:object:generate=true
type QuantizationStatus struct {
	// Format is the quantization format that was applied.
	Format string `json:"format,omitempty"`

	// Type is the specific quantization type (e.g., Q4_K_M for GGUF).
	Type string `json:"type,omitempty"`

	// OriginalSizeBytes is the size of the model before quantization.
	OriginalSizeBytes int64 `json:"originalSizeBytes,omitempty"`

	// CompressedSizeBytes is the size of the model after quantization.
	CompressedSizeBytes int64 `json:"compressedSizeBytes,omitempty"`

	// CompressionRatio is the ratio of original to compressed size (e.g., "3.75").
	CompressionRatio string `json:"compressionRatio,omitempty"`

	// QuantizationTime is the wall-clock duration of the quantization job.
	QuantizationTime string `json:"quantizationTime,omitempty"`

	// StartedAt is the timestamp when the quantization job started.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// CompletedAt is the timestamp when the quantization job completed.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// CalibrationParams records the calibration parameters that were actually used.
	// +optional
	CalibrationParams *CalibrationSpec `json:"calibrationParams,omitempty"`

	// Progress is the current job progress percentage (0-100), read from structured telemetry.
	// +optional
	Progress *int32 `json:"progress,omitempty"`

	// ProgressDetail is a human-readable description of current progress (e.g., "layer 152/336").
	// +optional
	ProgressDetail string `json:"progressDetail,omitempty"`

	// FailureMessage contains the last lines of pod logs on failure.
	// +optional
	FailureMessage string `json:"failureMessage,omitempty"`
}

// AbliterationSpec configures pre-quantization abliteration of model weights.
// Abliteration removes the "refusal direction" from model weights so the model
// responds without censorship. The pipeline becomes:
// Download (BF16) → Abliterate (modify weights in-place) → Quantize → Ready.
// +kubebuilder:object:generate=true
type AbliterationSpec struct {
	// TargetLayers selects which decoder layers to abliterate.
	// "auto" (default) = all decoder layers, "10-55" = range, "0,1,5,10" = explicit indices.
	// +kubebuilder:default="auto"
	// +optional
	TargetLayers *string `json:"targetLayers,omitempty"`

	// WeightMatrices to orthogonalize against the refusal direction.
	// Defaults to ["o_proj", "down_proj"] if empty.
	// +optional
	WeightMatrices []string `json:"weightMatrices,omitempty"`

	// NumSamples is the number of contrastive prompt pairs for activation collection.
	// +kubebuilder:validation:Minimum=16
	// +kubebuilder:validation:Maximum=512
	// +kubebuilder:default=128
	// +optional
	NumSamples *int32 `json:"numSamples,omitempty"`

	// MaxMemoryGB for the abliteration container. Default 56 (27B BF16 + overhead).
	// +optional
	MaxMemoryGB *int32 `json:"maxMemoryGB,omitempty"`

	// TimeoutSeconds overrides the default 2-hour deadline.
	// +kubebuilder:validation:Minimum=300
	// +kubebuilder:validation:Maximum=43200
	// +optional
	TimeoutSeconds *int64 `json:"timeoutSeconds,omitempty"`

	// UseGPU: when true, loads model with device_map="auto" (GPU+CPU offload).
	// +optional
	UseGPU bool `json:"useGPU,omitempty"`

	// SkipVisionLayers excludes vision encoder layers from abliteration (VLMs like Qwen3.5).
	// +kubebuilder:default=true
	// +optional
	SkipVisionLayers *bool `json:"skipVisionLayers,omitempty"`

	// SkipGDNLayers excludes GDN (Gated Delta Network) linear attention layers from
	// abliteration. Qwen3.5 uses a hybrid architecture where 48 of 64 layers are GDN
	// (linear attention) and 16 are standard self-attention. Abliterating GDN layers
	// destroys the recurrence mechanics (out_proj feedback into decay/gate computation).
	// When true (default), the script auto-detects GDN layers via decoder_sparse_step
	// from config.json and abliterates only the full-attention layers.
	// +kubebuilder:default=true
	// +optional
	SkipGDNLayers *bool `json:"skipGDNLayers,omitempty"`

	// NormThreshold is the max allowed L2 norm of the refusal direction.
	// Abliteration aborts if any layer's norm exceeds this value.
	// The controller also validates this after the job completes.
	// Format: numeric string (e.g., "100"). Default "100".
	// +kubebuilder:default="100"
	// +optional
	NormThreshold *string `json:"normThreshold,omitempty"`

	// AblitateLmHead controls whether the lm_head output projection is abliterated.
	// Set to false to skip lm_head modification (safer for hybrid architectures).
	// +kubebuilder:default=true
	// +optional
	AblitateLmHead *bool `json:"ablitateLmHead,omitempty"`

	// NodeSelector overrides spec.nodeSelector for abliteration jobs.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

// AbliterationStatus records the result of abliteration.
// +kubebuilder:object:generate=true
type AbliterationStatus struct {
	// LayersModified is the number of decoder layers that were abliterated.
	LayersModified int32 `json:"layersModified,omitempty"`

	// RefusalDirNorm is the L2 norm of the mean refusal direction vector (diagnostic).
	RefusalDirNorm string `json:"refusalDirNorm,omitempty"`

	// AbliterationTime is the wall-clock duration of the abliteration job.
	AbliterationTime string `json:"abliterationTime,omitempty"`

	// StartedAt is the timestamp when the abliteration job started.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// Progress is the current job progress percentage (0-100), read from structured telemetry.
	// +optional
	Progress *int32 `json:"progress,omitempty"`

	// ProgressDetail is a human-readable description of current progress.
	// +optional
	ProgressDetail string `json:"progressDetail,omitempty"`

	// FailureMessage contains the last lines of pod logs on failure.
	// +optional
	FailureMessage string `json:"failureMessage,omitempty"`
}

// FinetuneMode selects the finetuning approach.
// +kubebuilder:validation:Enum=lora;qlora;full
type FinetuneMode string

const (
	FinetuneModeLora  FinetuneMode = "lora"
	FinetuneModeQLora FinetuneMode = "qlora"
	FinetuneModeFull  FinetuneMode = "full"
)

// FinetuneDatasetSpec selects the training dataset.
// +kubebuilder:object:generate=true
type FinetuneDatasetSpec struct {
	// HuggingFace is a HF dataset ID (e.g., "tatsu-lab/alpaca").
	// +optional
	HuggingFace *string `json:"huggingFace,omitempty"`
	// PVCName references a PVC containing the dataset.
	// +optional
	PVCName *string `json:"pvcName,omitempty"`
	// PVCSubPath is the path within the dataset PVC.
	// +optional
	PVCSubPath *string `json:"pvcSubPath,omitempty"`
	// Split selects the dataset split (default "train").
	// +optional
	Split *string `json:"split,omitempty"`
	// MaxSamples limits the number of training samples (nil = all).
	// +optional
	MaxSamples *int32 `json:"maxSamples,omitempty"`
}

// FinetuneLoRAConfig configures LoRA/QLoRA adapter parameters.
// +kubebuilder:object:generate=true
type FinetuneLoRAConfig struct {
	// Rank (r) for LoRA decomposition. Default 16.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=256
	// +optional
	Rank *int32 `json:"rank,omitempty"`
	// Alpha scaling factor. Default 32.
	// +optional
	Alpha *int32 `json:"alpha,omitempty"`
	// Dropout probability. Default 0.05.
	// +optional
	Dropout *string `json:"dropout,omitempty"`
	// TargetModules overrides which modules get LoRA adapters.
	// Default: Unsloth auto-selection.
	// +optional
	TargetModules []string `json:"targetModules,omitempty"`
}

// FinetuneSpec configures post-abliteration finetuning of model weights.
// +kubebuilder:object:generate=true
type FinetuneSpec struct {
	// Mode selects LoRA, QLoRA, or full finetuning. Default "qlora".
	// +kubebuilder:default="qlora"
	// +optional
	Mode *FinetuneMode `json:"mode,omitempty"`

	// Dataset configures the training data source.
	// +kubebuilder:validation:Required
	Dataset FinetuneDatasetSpec `json:"dataset"`

	// LoRA configures LoRA/QLoRA adapter parameters.
	// Ignored when mode is "full".
	// +optional
	LoRA *FinetuneLoRAConfig `json:"lora,omitempty"`

	// Epochs is the number of training epochs. Default 3.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	Epochs *int32 `json:"epochs,omitempty"`

	// BatchSize is the per-device training batch size. Default 4.
	// +optional
	BatchSize *int32 `json:"batchSize,omitempty"`

	// LearningRate (e.g., "2e-4"). Default "2e-4".
	// +optional
	LearningRate *string `json:"learningRate,omitempty"`

	// MaxSeqLen for training. Default 2048.
	// +kubebuilder:validation:Minimum=128
	// +kubebuilder:validation:Maximum=32768
	// +optional
	MaxSeqLen *int32 `json:"maxSeqLen,omitempty"`

	// MergeAdapter merges the LoRA adapter back into the base model after training.
	// Required for quantization to see the finetuned weights. Default true.
	// +optional
	MergeAdapter *bool `json:"mergeAdapter,omitempty"`

	// UseGPU enables GPU-accelerated finetuning. Default true.
	// +optional
	UseGPU *bool `json:"useGPU,omitempty"`

	// MaxMemoryGB limits memory for the finetune job container.
	// Default 56.
	// +optional
	MaxMemoryGB *int32 `json:"maxMemoryGB,omitempty"`

	// TimeoutSeconds overrides the default 6-hour deadline.
	// +kubebuilder:validation:Minimum=300
	// +kubebuilder:validation:Maximum=86400
	// +optional
	TimeoutSeconds *int64 `json:"timeoutSeconds,omitempty"`

	// GradientCheckpointing enables gradient checkpointing to reduce VRAM.
	// Default true.
	// +optional
	GradientCheckpointing *bool `json:"gradientCheckpointing,omitempty"`
}

// FinetuneStatus records the result of finetuning.
// +kubebuilder:object:generate=true
type FinetuneStatus struct {
	// TrainLoss is the final training loss.
	TrainLoss string `json:"trainLoss,omitempty"`
	// SamplesPerSecond is the training throughput.
	SamplesPerSecond string `json:"samplesPerSecond,omitempty"`
	// EpochsCompleted is the number of epochs completed.
	EpochsCompleted int32 `json:"epochsCompleted,omitempty"`
	// TotalSteps is the total training steps completed.
	TotalSteps int32 `json:"totalSteps,omitempty"`
	// FinetuneTime is the wall-clock duration.
	FinetuneTime string `json:"finetuneTime,omitempty"`
	// StartedAt timestamp.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// Progress is the current job progress percentage (0-100), read from structured telemetry.
	// +optional
	Progress *int32 `json:"progress,omitempty"`
	// ProgressDetail is a human-readable description of current progress.
	// +optional
	ProgressDetail string `json:"progressDetail,omitempty"`
	// FailureMessage on error.
	// +optional
	FailureMessage string `json:"failureMessage,omitempty"`
}

// PublishTarget identifies where to publish the model.
// +kubebuilder:validation:Enum=oci;huggingface
type PublishTarget string

const (
	PublishTargetOCI         PublishTarget = "oci"
	PublishTargetHuggingFace PublishTarget = "huggingface"
)

// PublishSpec configures post-pipeline publishing of model artifacts.
// When set, the controller creates a publish Job after the last pipeline
// phase (quantize/finetune/abliterate/download) succeeds.
// Pipeline: Download → [Abliterate] → [Finetune] → [Quantize] → [Publish] → Ready.
// +kubebuilder:object:generate=true
type PublishSpec struct {
	// Targets lists the publish destinations.
	// +kubebuilder:validation:MinItems=1
	Targets []PublishTarget `json:"targets"`

	// OCIRef is the OCI artifact reference to push to (e.g. "registry.harbor.lan/models/qwen3:gptq-int4").
	// Required when targets includes "oci".
	// +optional
	OCIRef *string `json:"ociRef,omitempty"`

	// HuggingFaceRepo is the HuggingFace repository to upload to (e.g. "myorg/qwen3-gptq-int4").
	// Required when targets includes "huggingface".
	// +optional
	HuggingFaceRepo *string `json:"huggingFaceRepo,omitempty"`

	// SecretRef is the name of the K8s secret containing credentials.
	// Expected keys: OCI_USERNAME, OCI_PASSWORD (for OCI), HF_TOKEN (for HuggingFace).
	// +optional
	SecretRef *string `json:"secretRef,omitempty"`

	// MaxMemoryGB limits the memory for the publish job container.
	// Publish is CPU+network only. Default 8.
	// +optional
	MaxMemoryGB *int32 `json:"maxMemoryGB,omitempty"`

	// TimeoutSeconds overrides the default 2-hour deadline for publish jobs.
	// +kubebuilder:validation:Minimum=300
	// +kubebuilder:validation:Maximum=43200
	// +optional
	TimeoutSeconds *int64 `json:"timeoutSeconds,omitempty"`
}

// PublishStatus records the result of model publishing.
// +kubebuilder:object:generate=true
type PublishStatus struct {
	// OCIDigest is the digest of the published OCI artifact.
	// +optional
	OCIDigest string `json:"ociDigest,omitempty"`

	// HuggingFaceCommit is the commit hash from the HuggingFace upload.
	// +optional
	HuggingFaceCommit string `json:"huggingFaceCommit,omitempty"`

	// PublishedAt is the timestamp when publishing completed.
	// +optional
	PublishedAt *metav1.Time `json:"publishedAt,omitempty"`

	// StartedAt is the timestamp when the publish job started.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// Progress is the current job progress percentage (0-100).
	// +optional
	Progress *int32 `json:"progress,omitempty"`

	// ProgressDetail is a human-readable description of current progress.
	// +optional
	ProgressDetail string `json:"progressDetail,omitempty"`

	// FailureMessage contains the error message on failure.
	// +optional
	FailureMessage string `json:"failureMessage,omitempty"`
}

// DownloadSpec configures the model download job.
// When nil, sensible defaults are applied (16Gi memory, hf_transfer auto-enabled).
// +kubebuilder:object:generate=true
type DownloadSpec struct {
	// MaxMemoryGB limits the memory available to the download job container.
	// Defaults to 16. Set higher for hf_transfer with very large models.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=128
	// +optional
	MaxMemoryGB *int32 `json:"maxMemoryGB,omitempty"`

	// HFTransfer enables the hf_transfer Rust extension for faster parallel downloads.
	// nil = auto (enabled when MaxMemoryGB >= 16), true/false = explicit override.
	// hf_transfer uses ~4-8Gi for parallel connections on large models.
	// +optional
	HFTransfer *bool `json:"hfTransfer,omitempty"`

	// BackoffLimit is the number of retries before marking the download as failed.
	// Defaults to 3.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10
	// +optional
	BackoffLimit *int32 `json:"backoffLimit,omitempty"`

	// TimeoutSeconds overrides the default job deadline for downloads.
	// +kubebuilder:validation:Minimum=300
	// +kubebuilder:validation:Maximum=86400
	// +optional
	TimeoutSeconds *int64 `json:"timeoutSeconds,omitempty"`
}

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

	// FlashLoader configures the flash-loader init container for fast model loading.
	// When enabled, model files are parallel-copied from PVC to tmpfs before
	// the inference container starts, dramatically reducing cold start time.
	// +optional
	FlashLoader *FlashLoaderSpec `json:"flashLoader,omitempty"`

	// Quantization configures post-download quantization of model weights.
	// When set, the controller creates a quantization Job after the download
	// completes, converting the model to the specified format before marking Ready.
	// +optional
	Quantization *QuantizationSpec `json:"quantization,omitempty"`

	// Abliteration configures pre-quantization abliteration of model weights.
	// When set, the controller creates an abliteration Job after download completes
	// (and before quantization if both are set). Abliteration removes the "refusal
	// direction" from transformer weights via contrastive activation analysis.
	// +optional
	Abliteration *AbliterationSpec `json:"abliteration,omitempty"`

	// Finetune configures post-abliteration finetuning of model weights.
	// When set, the controller creates a finetune Job after abliteration (if set)
	// or download completes (and before quantization if set).
	// Pipeline: Download → [Abliterate] → [Finetune] → [Quantize] → Ready.
	// +optional
	Finetune *FinetuneSpec `json:"finetune,omitempty"`

	// Publish configures post-pipeline publishing of model artifacts to OCI or HuggingFace.
	// When set, the controller creates a publish Job after the last pipeline phase succeeds.
	// Pipeline: Download → [Abliterate] → [Finetune] → [Quantize] → [Publish] → Ready.
	// +optional
	Publish *PublishSpec `json:"publish,omitempty"`

	// Download configures the model download job (memory, hf_transfer, retries).
	// When nil, defaults are applied: 16Gi memory, hf_transfer auto-enabled, 3 retries.
	// +optional
	Download *DownloadSpec `json:"download,omitempty"`

	// MaxRetries is the maximum number of automatic retries before marking as permanently failed.
	// Applies to all pipeline phases (download, abliteration, finetune, quantization, publish).
	// Uses exponential backoff between retries: min(30s * 2^retryCount, 10m).
	// Default: 3.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10
	// +optional
	MaxRetries *int32 `json:"maxRetries,omitempty"`
}

// GetMaxRetries returns the configured maximum retries, or the default of 3.
func (s *ModelCacheSpec) GetMaxRetries() int32 {
	if s.MaxRetries != nil {
		return *s.MaxRetries
	}
	return 3
}

// FlashLoaderSpec configures the flash-loader init container for fast model loading.
// When enabled, an init container parallel-copies model files from PVC to tmpfs
// before the inference container starts, reducing cold start I/O latency.
// +kubebuilder:object:generate=true
type FlashLoaderSpec struct {
	// Enabled activates flash-loader for this cache.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Concurrency is the number of parallel copy goroutines.
	// +kubebuilder:default=4
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=32
	// +optional
	Concurrency int `json:"concurrency,omitempty"`

	// TmpfsSizeLimit is the maximum size of the tmpfs volume.
	// Defaults to 2x the model size if not specified.
	// +optional
	TmpfsSizeLimit *string `json:"tmpfsSizeLimit,omitempty"`

	// P2P enables peer-to-peer transfer between pods on the same node.
	// +optional
	P2P bool `json:"p2p,omitempty"`

	// P2PPort is the port used for peer-to-peer transfer.
	// +kubebuilder:default=9876
	// +optional
	P2PPort int `json:"p2pPort,omitempty"`

	// Image overrides the flash-loader init container image.
	// +optional
	Image string `json:"image,omitempty"`
}

// ModelCacheStatus defines the observed state of ModelCache
// +kubebuilder:object:generate=true
type ModelCacheStatus struct {
	// Phase represents the current lifecycle state
	// +optional
	Phase ModelCachePhase `json:"phase,omitempty"`

	// RetryCount tracks how many times the current phase has been retried
	// +optional
	RetryCount int32 `json:"retryCount,omitempty"`

	// LastFailureTime records when the last failure occurred
	// +optional
	LastFailureTime *metav1.Time `json:"lastFailureTime,omitempty"`

	// LastFailurePhase records which phase last failed
	// +optional
	LastFailurePhase string `json:"lastFailurePhase,omitempty"`

	// CurrentPhase tracks which pipeline phase is currently active.
	// Values: "download", "abliteration", "finetune", "quantization", "publish", "ready".
	// Used by phase guards to enforce strict pipeline ordering.
	// +optional
	CurrentPhase string `json:"currentPhase,omitempty"`

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

	// === Quantization Status ===

	// Quantization records the result of model quantization.
	// Only populated when spec.quantization is set and the job completes.
	// +optional
	Quantization *QuantizationStatus `json:"quantization,omitempty"`

	// Abliteration records the result of model abliteration.
	// Only populated when spec.abliteration is set and the job completes.
	// +optional
	Abliteration *AbliterationStatus `json:"abliteration,omitempty"`

	// Finetune records the result of model finetuning.
	// Only populated when spec.finetune is set and the job completes.
	// +optional
	Finetune *FinetuneStatus `json:"finetune,omitempty"`

	// Publish records the result of model publishing.
	// Only populated when spec.publish is set and the job completes.
	// +optional
	Publish *PublishStatus `json:"publish,omitempty"`

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
//+kubebuilder:printcolumn:name="Retries",type="integer",JSONPath=".status.retryCount",priority=1
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
