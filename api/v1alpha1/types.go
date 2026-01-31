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

	// ColdStartTimeoutSeconds is the maximum time to wait for model activation during cold start.
	// Requests exceeding this timeout receive 503 Service Unavailable.
	// Large models on NFS may need 900-1800s (15-30 min) to load.
	// +kubebuilder:default=60
	// +kubebuilder:validation:Minimum=10
	// +kubebuilder:validation:Maximum=1800
	// +optional
	ColdStartTimeoutSeconds *int32 `json:"coldStartTimeoutSeconds,omitempty"`

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

	// MLCLLM contains MLC-LLM backend-specific configuration.
	// Only applies when Backend is "mlc-llm" or "mlc".
	// +optional
	MLCLLM *MLCLLMSpec `json:"mlcllm,omitempty"`

	// VLLM contains vLLM backend-specific configuration.
	// Only applies when Backend is "vllm".
	// +optional
	VLLM *VLLMSpec `json:"vllm,omitempty"`

	// LlamaCpp contains llama.cpp backend-specific configuration.
	// Only applies when Backend is "llamacpp" or "llama.cpp".
	// +optional
	LlamaCpp *LlamaCppSpec `json:"llamacpp,omitempty"`

	// ComfyUI contains ComfyUI backend-specific configuration.
	// Only applies when Backend is "comfyui".
	// +optional
	ComfyUI *ComfyUISpec `json:"comfyui,omitempty"`

	// VLLMOmni contains vLLM-Omni backend-specific configuration for image generation.
	// Only applies when Backend is "vllm-omni".
	// +optional
	VLLMOmni *VLLMOmniSpec `json:"vllmOmni,omitempty"`

	// GPUGroupRef references a GPUGroup this deployment belongs to.
	// When set, this deployment participates in shared GPU scheduling.
	// The GPUGroup controller handles scaling decisions instead of individual idle timeouts.
	// +optional
	GPUGroupRef *string `json:"gpuGroupRef,omitempty"`

	// Priority within a GPUGroup. Higher values = higher priority for preemption.
	// Can be overridden by GPUGroup.models[].priority.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000
	// +kubebuilder:default=100
	// +optional
	Priority *int32 `json:"priority,omitempty"`

	// VRAMEstimateMB is the estimated VRAM usage for this model in megabytes.
	// Used by GPUGroup for bin-packing and swap decisions.
	// +optional
	VRAMEstimateMB *int64 `json:"vramEstimateMB,omitempty"`

	// ServiceLabels are semantic labels that describe the model's capabilities.
	// When this model becomes active in a GPUGroup, these labels are added as
	// annotations on the model's Service for dynamic routing by the proxy.
	// Example: ["textgen", "fast-text", "code"]
	// +optional
	// +kubebuilder:validation:MaxItems=10
	ServiceLabels []string `json:"serviceLabels,omitempty"`
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

// MLCLLMSpec configures MLC-LLM backend-specific settings.
// Only applies when Backend is "mlc-llm" or "mlc".
// +kubebuilder:object:generate=true
type MLCLLMSpec struct {
	// Mode specifies the MLC-LLM serving mode.
	// - "local": Single-user mode with lower memory footprint (default)
	// - "server": Multi-user mode with larger KV cache pre-allocation for high throughput
	// - "interactive": Interactive mode optimized for chat applications
	// +kubebuilder:validation:Enum=local;server;interactive
	// +optional
	Mode string `json:"mode,omitempty"`

	// ModelLibPath is the path to a pre-compiled model library (.so file).
	// When set, MLC-LLM will use this library instead of JIT compilation.
	// Required for Maxwell GPUs and recommended for production to skip JIT compilation.
	// Example: /models/Qwen3-0.6B-q0f32-MLC/maxwell-lib.so
	// +optional
	ModelLibPath string `json:"modelLibPath,omitempty"`

	// GPUMemoryBytes specifies the GPU memory limit in bytes for MLC-LLM.
	// MLC-LLM uses this to calculate KV cache size and memory allocation.
	// If not set, defaults to 23GB (23068672000) for modern GPUs.
	// For Maxwell GPUs (GTX 980 Ti), recommend setting to 5000000000 (~5GB).
	// +optional
	GPUMemoryBytes *int64 `json:"gpuMemoryBytes,omitempty"`

	// JITPolicy controls Just-In-Time compilation behavior.
	// - "ON": Enable JIT compilation (default)
	// - "OFF": Disable JIT, requires pre-compiled model library via ModelLibPath
	// - "REDO": Force recompilation even if cached
	// - "READONLY": Use cached compilations only, fail if not found
	// +kubebuilder:validation:Enum=ON;OFF;REDO;READONLY
	// +optional
	JITPolicy string `json:"jitPolicy,omitempty"`

	// Overrides specifies MLC-LLM model parameter overrides.
	// These are passed via the --overrides flag as semicolon-separated key=value pairs.
	// +optional
	Overrides *MLCOverrides `json:"overrides,omitempty"`

	// CompileOptions specifies TVM/CUDA compile options for JIT compilation.
	// These control which acceleration backends are enabled.
	// Auto-configured for Maxwell GPUs if not specified.
	// +optional
	CompileOptions *MLCCompileOptions `json:"compileOptions,omitempty"`
}

// MLCOverrides configures MLC-LLM model parameter overrides.
// These parameters are passed to MLC-LLM via the --overrides flag.
// +kubebuilder:object:generate=true
type MLCOverrides struct {
	// PrefillChunkSize controls the prefill chunk size for attention computation.
	// Lower values reduce temporary buffer memory usage.
	// Default: 512 (uses ~1GB temp buffer vs ~3.6GB at 2048)
	// +kubebuilder:validation:Minimum=64
	// +kubebuilder:validation:Maximum=8192
	// +optional
	PrefillChunkSize *int32 `json:"prefillChunkSize,omitempty"`

	// MaxTotalSeqLength limits the total sequence length (context + generation).
	// Affects KV cache memory allocation. Higher values use more GPU memory.
	// Default: 16384
	// +kubebuilder:validation:Minimum=256
	// +kubebuilder:validation:Maximum=131072
	// +optional
	MaxTotalSeqLength *int32 `json:"maxTotalSeqLength,omitempty"`

	// MaxNumSequence controls the maximum number of concurrent sequences (batch size).
	// In server mode, this limits concurrent requests. Lower values reduce KV cache memory.
	// Default: 1 for local/interactive, 128 for server mode.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=256
	// +optional
	MaxNumSequence *int32 `json:"maxNumSequence,omitempty"`

	// GPUMemoryUtilization sets the fraction of GPU memory to use (0.0-1.0).
	// MLC-LLM uses this to calculate KV cache allocation in server mode.
	// Example: "0.85" for 85% of GPU memory.
	// +optional
	GPUMemoryUtilization string `json:"gpuMemoryUtilization,omitempty"`

	// ContextWindowSize sets the context window size for the model.
	// Should not exceed MaxTotalSeqLength.
	// +kubebuilder:validation:Minimum=256
	// +kubebuilder:validation:Maximum=131072
	// +optional
	ContextWindowSize *int32 `json:"contextWindowSize,omitempty"`

	// Raw allows specifying arbitrary override parameters as a semicolon-separated string.
	// These are appended to the generated overrides.
	// Example: "temperature=0.7;top_p=0.9"
	// +optional
	Raw string `json:"raw,omitempty"`
}

// MLCCompileOptions configures TVM compile options for MLC-LLM JIT compilation.
// These options control which GPU acceleration backends are enabled.
// For Maxwell GPUs, UseCutlass and UseFlashInfer should be disabled.
// +kubebuilder:object:generate=true
type MLCCompileOptions struct {
	// UseCutlass enables CUTLASS kernels for GEMM operations.
	// Requires FP16 support (sm_53+). Must be disabled for Maxwell GPUs (sm_52).
	// Default: true for modern GPUs, false for Maxwell
	// +optional
	UseCutlass *bool `json:"useCutlass,omitempty"`

	// UseFlashInfer enables FlashInfer attention kernels.
	// Requires sm_80+ (Ampere or newer). Must be disabled for Maxwell/Pascal GPUs.
	// Default: true for Ampere+, false for older architectures
	// +optional
	UseFlashInfer *bool `json:"useFlashInfer,omitempty"`

	// UseCublasGemm enables cuBLAS fallback for GEMM operations.
	// Recommended for older GPUs that don't support CUTLASS.
	// Default: false for modern GPUs, true for Maxwell
	// +optional
	UseCublasGemm *bool `json:"useCublasGemm,omitempty"`

	// UseCudaGraph enables CUDA graph capture for kernel fusion.
	// Can improve performance but may cause issues on older GPUs.
	// Default: true for modern GPUs, false for Maxwell
	// +optional
	UseCudaGraph *bool `json:"useCudaGraph,omitempty"`
}

// VLLMSpec configures vLLM backend-specific settings.
// Only applies when Backend is "vllm".
// +kubebuilder:object:generate=true
type VLLMSpec struct {
	// TensorParallelSize is the number of GPUs to use for tensor parallelism.
	// Must be a power of 2. Requires multiple GPUs on the same node.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=8
	// +optional
	TensorParallelSize *int32 `json:"tensorParallelSize,omitempty"`

	// Dtype specifies the data type for model weights.
	// Options: auto, float16, bfloat16, float32
	// Default: auto (vLLM chooses based on model config)
	// +kubebuilder:validation:Enum=auto;float16;bfloat16;float32
	// +optional
	Dtype string `json:"dtype,omitempty"`

	// Quantization specifies the quantization method.
	// Options: awq, squeezellm, gptq, fp8, None
	// +kubebuilder:validation:Enum=awq;squeezellm;gptq;fp8;None;""
	// +optional
	Quantization string `json:"quantization,omitempty"`

	// MaxModelLen is the maximum sequence length (context + generation).
	// If not set, vLLM uses the model's max_position_embeddings.
	// +kubebuilder:validation:Minimum=256
	// +kubebuilder:validation:Maximum=131072
	// +optional
	MaxModelLen *int32 `json:"maxModelLen,omitempty"`

	// GPUMemoryUtilization is the fraction of GPU memory to use (0.0-1.0).
	// Default: 0.9 (90% of available GPU memory)
	// +optional
	GPUMemoryUtilization *string `json:"gpuMemoryUtilization,omitempty"`

	// EnforceEager disables CUDA graph and runs in eager mode.
	// Useful for debugging or when CUDA graphs cause issues.
	// +optional
	EnforceEager *bool `json:"enforceEager,omitempty"`

	// MaxNumSeqs is the maximum number of sequences per iteration.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxNumSeqs *int32 `json:"maxNumSeqs,omitempty"`

	// SwapSpace is the CPU swap space size (GiB) per GPU.
	// +kubebuilder:validation:Minimum=0
	// +optional
	SwapSpace *int32 `json:"swapSpace,omitempty"`

	// TrustRemoteCode allows execution of code from HuggingFace repos.
	// Required for some models with custom architectures.
	// +optional
	TrustRemoteCode *bool `json:"trustRemoteCode,omitempty"`

	// === AMD GFX1100 (RDNA3) Optimizations ===

	// HIPVisibleDevices specifies which AMD GPUs to use via HIP_VISIBLE_DEVICES.
	// On systems with both iGPU and discrete GPU, the device plugin's besteffort
	// policy always picks the iGPU (lower PCI address). Set to "1" to use the
	// discrete GPU instead of the iGPU.
	// Format: comma-separated device indices (e.g., "0", "1", "0,1")
	// +optional
	HIPVisibleDevices string `json:"hipVisibleDevices,omitempty"`

	// EnablePrefixCaching enables automatic prefix caching for improved
	// performance on repeated prompt prefixes. Reduces KV cache memory by
	// reusing cached prefixes across requests.
	// +optional
	EnablePrefixCaching *bool `json:"enablePrefixCaching,omitempty"`

	// KVCacheDtype specifies the data type for the KV cache.
	// Options: auto, fp8, fp8_e5m2, fp8_e4m3, fp8_inc, int8
	// Notes:
	// - ROCm (AMD) FP8 KV-cache support depends on the vLLM build and GPU arch.
	// - int8 is kept for backwards compatibility but is treated as an alias
	//   for fp8 on modern vLLM builds.
	// Default: auto (vLLM chooses based on model config)
	// +kubebuilder:validation:Enum=auto;fp8;fp8_e5m2;fp8_e4m3;fp8_inc;int8
	// +optional
	KVCacheDtype string `json:"kvCacheDtype,omitempty"`

	// CalculateKVScales enables dynamic calculation of k_scale and v_scale when
	// KVCacheDtype is fp8 (or fp8_e4m3/fp8_e5m2). If false, vLLM will attempt to
	// load KV scales from the checkpoint when available, otherwise defaults to 1.
	// On ROCm, enabling this is commonly required when using fp8 KV cache with
	// non-precalibrated checkpoints.
	// +optional
	CalculateKVScales *bool `json:"calculateKvScales,omitempty"`

	// AttentionBackend specifies the attention computation backend.
	// Options: FLASH_ATTN, XFORMERS, ROCM_FLASH, FLASHINFER, TORCH_SDPA
	// For AMD ROCm GPUs (gfx1100), TORCH_SDPA is recommended for stability.
	// When unset on AMD GPUs, defaults to TORCH_SDPA automatically.
	// FLASH_ATTN may cause SIGSEGV on ROCm - use with caution.
	// +kubebuilder:validation:Enum=FLASH_ATTN;XFORMERS;ROCM_FLASH;FLASHINFER;TORCH_SDPA;flash_attn;xformers;rocm_flash;flashinfer;torch_sdpa
	// +optional
	AttentionBackend string `json:"attentionBackend,omitempty"`

	// CPUOffloadGB specifies the amount of CPU RAM (in GB) to use for
	// offloading model weights or KV cache when GPU memory is insufficient.
	// Useful for AMD GPUs with 24GB VRAM to cache more models.
	// Note: Not compatible with tensor parallelism.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=512
	// +optional
	CPUOffloadGB *int32 `json:"cpuOffloadGB,omitempty"`

	// EnableChunkedPrefill enables chunked prefill for better memory efficiency
	// during prompt processing. Can reduce peak memory usage and improve
	// throughput for long prompts.
	// +optional
	EnableChunkedPrefill *bool `json:"enableChunkedPrefill,omitempty"`

	// BlockSize specifies the token block size for KV cache management.
	// Smaller values may reduce memory fragmentation but increase overhead.
	// Options: 8, 16, 32 (default: 16)
	// +kubebuilder:validation:Enum=8;16;32
	// +optional
	BlockSize *int32 `json:"blockSize,omitempty"`

	// RopeScaling configures RoPE (Rotary Position Embedding) scaling
	// for extended context length support beyond the model's training length.
	// +optional
	RopeScaling *VLLMRopeScaling `json:"ropeScaling,omitempty"`
}

// VLLMRopeScaling configures RoPE scaling parameters for extended context.
// +kubebuilder:object:generate=true
type VLLMRopeScaling struct {
	// Type specifies the RoPE scaling method.
	// Options: linear, dynamic, yarn, longrope
	// - linear: Simple linear interpolation
	// - dynamic: Dynamic NTK-aware scaling
	// - yarn: YaRN (Yet another RoPE extensioN)
	// - longrope: LongRoPE for very long contexts
	// +kubebuilder:validation:Enum=linear;dynamic;yarn;longrope
	// +optional
	Type string `json:"type,omitempty"`

	// Factor specifies the scaling factor for context extension.
	// For linear scaling, this is the context extension multiplier.
	// Example: "2.0" doubles the context length.
	// Specified as a string to avoid floating-point precision issues.
	// +optional
	Factor string `json:"factor,omitempty"`
}

// LlamaCppSpec configures llama.cpp backend-specific settings.
// Only applies when Backend is "llamacpp" or "llama.cpp".
// +kubebuilder:object:generate=true
type LlamaCppSpec struct {
	// ContextSize is the context size (number of tokens).
	// Default: 2048
	// +kubebuilder:validation:Minimum=128
	// +kubebuilder:validation:Maximum=131072
	// +optional
	ContextSize *int32 `json:"contextSize,omitempty"`

	// NGPULayers is the number of layers to offload to GPU.
	// Set to a high number (e.g., 999) to offload all layers.
	// Default: 0 (CPU only)
	// +kubebuilder:validation:Minimum=0
	// +optional
	NGPULayers *int32 `json:"nGPULayers,omitempty"`

	// BatchSize is the batch size for prompt processing.
	// +kubebuilder:validation:Minimum=1
	// +optional
	BatchSize *int32 `json:"batchSize,omitempty"`

	// Threads is the number of threads to use for generation.
	// Default: number of CPU cores
	// +kubebuilder:validation:Minimum=1
	// +optional
	Threads *int32 `json:"threads,omitempty"`

	// FlashAttention enables Flash Attention for faster inference.
	// Requires compatible GPU (NVIDIA with CUDA).
	// +optional
	FlashAttention *bool `json:"flashAttention,omitempty"`

	// MainGPU specifies the GPU to use for the main model.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MainGPU *int32 `json:"mainGPU,omitempty"`

	// RopeFreqBase overrides the RoPE frequency base.
	// Specified as a string to avoid floating-point precision issues (e.g., "10000.0").
	// +optional
	RopeFreqBase string `json:"ropeFreqBase,omitempty"`

	// RopeFreqScale overrides the RoPE frequency scale.
	// Specified as a string to avoid floating-point precision issues (e.g., "1.0").
	// +optional
	RopeFreqScale string `json:"ropeFreqScale,omitempty"`
}

// ComfyUISpec configures ComfyUI backend-specific settings for image generation.
// Only applies when Backend is "comfyui".
// +kubebuilder:object:generate=true
type ComfyUISpec struct {
	// WorkflowsPath is the path to custom workflow JSON files.
	// +optional
	WorkflowsPath string `json:"workflowsPath,omitempty"`

	// ModelsPath is the path where models are mounted.
	// FlexInfer mounts ModelCache volumes at /models (including RAM caches copied into /dev/shm).
	// +kubebuilder:default="/models"
	// +optional
	ModelsPath string `json:"modelsPath,omitempty"`

	// CustomNodesPath is the path to custom nodes.
	// +kubebuilder:default="/app/ComfyUI/custom_nodes"
	// +optional
	CustomNodesPath string `json:"customNodesPath,omitempty"`

	// PreloadModels is a list of models to preload on startup.
	// Format: "category/filename" (e.g., "checkpoints/sdxl.safetensors")
	// +optional
	PreloadModels []string `json:"preloadModels,omitempty"`

	// EnableCORS enables CORS headers for API access.
	// +kubebuilder:default=true
	// +optional
	EnableCORS *bool `json:"enableCORS,omitempty"`

	// ExtraArgs is additional command line arguments to pass to ComfyUI.
	// +optional
	ExtraArgs []string `json:"extraArgs,omitempty"`
}

// VLLMOmniSpec configures vLLM-Omni backend-specific settings for image generation.
// vLLM-Omni provides OpenAI DALL-E compatible API for image generation.
// Only applies when Backend is "vllm-omni".
// +kubebuilder:object:generate=true
type VLLMOmniSpec struct {
	// DiffusionModel is the HuggingFace model ID to use for image generation.
	// Examples: "Qwen/Qwen-Image", "Tongyi-MAI/Z-Image-Turbo"
	// If not specified, uses the model field from ModelDeploymentSpec.
	// +optional
	DiffusionModel string `json:"diffusionModel,omitempty"`

	// CacheAcceleration enables cache-based speedup (TeaCache/Cache-DiT).
	// - "none": No cache acceleration
	// - "teacache": TeaCache for 1.5-2x speedup (default)
	// - "cachedit": Cache-DiT for faster inference
	// +kubebuilder:default="teacache"
	// +kubebuilder:validation:Enum=none;teacache;cachedit
	// +optional
	CacheAcceleration string `json:"cacheAcceleration,omitempty"`

	// DefaultSize is the default image output size.
	// +kubebuilder:default="1024x1024"
	// +optional
	DefaultSize string `json:"defaultSize,omitempty"`

	// GPUMemoryUtilization is the fraction of GPU memory to use (0.0-1.0).
	// Default: 0.9 (90% of available GPU memory)
	// +optional
	GPUMemoryUtilization *string `json:"gpuMemoryUtilization,omitempty"`

	// MaxNumSeqs is the maximum number of sequences per iteration.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxNumSeqs *int32 `json:"maxNumSeqs,omitempty"`
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

	// GPUGroupState tracks this deployment's state within its GPUGroup (if any).
	// Only populated when GPUGroupRef is set.
	// +optional
	GPUGroupState *ModelDeploymentGPUGroupState `json:"gpuGroupState,omitempty"`
}

// ModelDeploymentGPUGroupState tracks a model's state within a GPUGroup
// +kubebuilder:object:generate=true
type ModelDeploymentGPUGroupState struct {
	// GroupName is the GPUGroup this deployment belongs to.
	GroupName string `json:"groupName,omitempty"`

	// State is the current state within the group: Active, Preempted, Queued, Idle.
	State string `json:"state,omitempty"`

	// PreemptedAt is when this model was last preempted.
	// +optional
	PreemptedAt *metav1.Time `json:"preemptedAt,omitempty"`

	// PreemptedBy is the model that caused preemption.
	// +optional
	PreemptedBy string `json:"preemptedBy,omitempty"`

	// QueuedRequests is the number of requests waiting for this model.
	// +optional
	QueuedRequests int32 `json:"queuedRequests,omitempty"`
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
	// ModelDeploymentPhaseIdle indicates the ModelDeployment is scaled to zero (serverless)
	ModelDeploymentPhaseIdle ModelDeploymentPhase = "Idle"
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

	// Architecture is the GPU architecture (e.g., sm_52 for Maxwell, gfx1100 for RDNA3)
	// +optional
	Architecture string `json:"architecture,omitempty"`

	// Vendor is the GPU vendor (NVIDIA, AMD, Intel)
	// +optional
	Vendor string `json:"vendor,omitempty"`
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

// ModelDeployment is the Schema for the modeldeployments API.
//
// Deprecated: ModelDeployment is deprecated in favor of the v1alpha2 Model resource.
// ModelDeployment will be removed in a future release. Please migrate to v1alpha2 Model.
// See docs/migration/v1alpha1-to-v1alpha2.md for migration guide.
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
