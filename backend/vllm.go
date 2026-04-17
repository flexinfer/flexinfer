package backend

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// vllmImageRules defines the image resolution precedence for vLLM.
// Order: arch-specific (env → default) → vendor (env → default) → global.
var vllmImageRules = []ImageRule{
	// AMD arch-specific
	{Vendor: GPUVendorAMD, ArchPrefix: "gfx110", EnvVar: "DEFAULT_VLLM_IMAGE_GFX1100", Default: "registry.harbor.lan/flexinfer/vllm:rocm-gfx1100-fa"},
	{Vendor: GPUVendorAMD, ArchPrefix: "gfx906", EnvVar: "DEFAULT_VLLM_IMAGE_GFX906", Default: "registry.harbor.lan/flexinfer/vllm:rocm-gfx906"},
	// AMD generic
	{Vendor: GPUVendorAMD, EnvVar: "DEFAULT_VLLM_IMAGE_AMD", Default: "rocm/vllm:latest"},
	// Global default (NVIDIA, unknown, etc.)
	{EnvVar: "DEFAULT_VLLM_IMAGE", Default: "vllm/vllm-openai:latest"},
}

// VLLMBackend implements the Backend interface for vLLM.
// vLLM provides an OpenAI-compatible API for LLM inference.
type VLLMBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&VLLMBackend{})
}

func (b *VLLMBackend) Name() string {
	return NameVLLM
}

func (b *VLLMBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	return ResolveImage(vllmImageRules, gpuVendor, gpuArch)
}

func (b *VLLMBackend) Port() int32 {
	return PortVLLM
}

func (b *VLLMBackend) Args(spec *ModelSpec) []string {
	args := []string{
		"--host", "0.0.0.0",
		"--port", fmt.Sprintf("%d", b.Port()),
	}

	// Model path
	if spec.ModelPath != "" {
		args = append(args, "--model", spec.ModelPath)
	} else if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}

	// Dtype
	if dtype := spec.ConfigString("dtype", ""); dtype != "" {
		args = append(args, "--dtype", dtype)
	}

	// Max model length
	if maxLen := spec.ConfigInt("maxModelLen", 0); maxLen > 0 {
		args = append(args, "--max-model-len", fmt.Sprintf("%d", maxLen))
	}

	// GPU memory utilization
	if memUtil := spec.ConfigString("gpuMemoryUtilization", ""); memUtil != "" {
		args = append(args, "--gpu-memory-utilization", memUtil)
	}

	// Attention backend override. kvCacheCodec=turboquant promotes the managed
	// intent into the vLLM CUSTOM path unless the manifest explicitly overrides it.
	if attentionBackend := resolveVLLMAttentionBackend(spec); attentionBackend != "" {
		args = append(args, "--attention-backend", attentionBackend)
	}

	// Trust remote code
	if spec.ConfigBool("trustRemoteCode", false) {
		args = append(args, "--trust-remote-code")
	}

	// Max concurrent sequences
	if maxSeqs := spec.ConfigInt("maxNumSeqs", 0); maxSeqs > 0 {
		args = append(args, "--max-num-seqs", fmt.Sprintf("%d", maxSeqs))
	}

	// Max batched tokens for chunked prefill
	if maxBatched := spec.ConfigInt("maxNumBatchedTokens", 0); maxBatched > 0 {
		args = append(args, "--max-num-batched-tokens", fmt.Sprintf("%d", maxBatched))
	}

	// Enforce eager mode (disable torch.compile and HIPGraph/CUDAGraph)
	if spec.ConfigBool("enforceEager", false) {
		args = append(args, "--enforce-eager")
	}

	// CPU weight offload — moves part of model weights to CPU-pinned memory.
	// Required when model weights exceed single-GPU VRAM (e.g. 27B GPTQ on 24 GB).
	if offloadGB := spec.ConfigString("cpuOffloadGb", ""); offloadGB != "" {
		args = append(args, "--cpu-offload-gb", offloadGB)
	}
	if offloadBackend := spec.ConfigString("offloadBackend", ""); offloadBackend != "" {
		args = append(args, "--offload-backend", offloadBackend)
	}

	// Override KV cache blocks — bypasses the profiling step that measures
	// available GPU memory. Useful when model weights consume nearly all VRAM
	// and the profiling allocation itself causes OOM.
	if blocks := spec.ConfigInt("numGpuBlocksOverride", 0); blocks > 0 {
		args = append(args, "--num-gpu-blocks-override", fmt.Sprintf("%d", blocks))
	}

	// Quantization method (awq, gptq, fp8, gguf, etc.)
	if quant := spec.ConfigString("quantization", ""); quant != "" {
		args = append(args, "--quantization", quant)
	}

	// Attention backend override (e.g. FLASH_ATTN, XFORMERS, ROCM_AITER_FA, CUSTOM).
	// Experimental runtimes such as TurboQuant rely on CUSTOM to activate their plugin backend.
	if attentionBackend := spec.ConfigString("attentionBackend", ""); attentionBackend != "" {
		args = append(args, "--attention-backend", attentionBackend)
	}

	// Tokenizer override — required for GGUF models which lack HF tokenizer files.
	// Points to the base HF repo (e.g., "Qwen/Qwen3.5-35B-A3B").
	if tok := spec.ConfigString("tokenizer", ""); tok != "" {
		args = append(args, "--tokenizer", tok)
	}

	// Served model name — controls the model name in /v1/models and routing
	if name := spec.ConfigString("servedModelName", ""); name != "" {
		args = append(args, "--served-model-name", name)
	}

	// KV cache dtype (auto, fp8_e5m2). FP8 requires MI300X+ hardware.
	if kvDtype := spec.ConfigString("kvCacheDtype", ""); kvDtype != "" {
		args = append(args, "--kv-cache-dtype", kvDtype)
	}

	// Prefix caching is ON by default in vLLM V1.
	// Newer argparse wiring uses BooleanOptionalAction, so the disable form is
	// --no-enable-prefix-caching rather than --no-prefix-caching.
	if spec.Config != nil {
		if _, ok := spec.Config["enablePrefixCaching"]; ok {
			if !spec.ConfigBool("enablePrefixCaching", true) {
				args = append(args, "--no-enable-prefix-caching")
			}
		}
		if _, ok := spec.Config["disableHybridKVCacheManager"]; ok {
			if spec.ConfigBool("disableHybridKVCacheManager", false) {
				args = append(args, "--disable-hybrid-kv-cache-manager")
			} else {
				args = append(args, "--no-disable-hybrid-kv-cache-manager")
			}
		}
	}

	// Tool calling support
	if spec.ConfigBool("enableToolCalling", false) {
		args = append(args, "--enable-auto-tool-choice")
		parser := spec.ConfigString("toolCallParser", "hermes")
		args = append(args, "--tool-call-parser", parser)
	}

	// Reasoning parser for models with <think> blocks (e.g., DeepSeek-R1)
	if rp := spec.ConfigString("reasoningParser", ""); rp != "" {
		args = append(args, "--reasoning-parser", rp)
	}

	// Disable stats logging to reduce log noise in production
	if spec.ConfigBool("disableLogStats", false) {
		args = append(args, "--disable-log-stats")
	}

	// Speculative decoding — passed as opaque JSON to --speculative-config.
	// Supports draft_model, eagle, eagle3, mtp, ngram, suffix, mlp_speculator.
	if specConfig := spec.ConfigString("speculativeConfig", ""); specConfig != "" {
		args = append(args, "--speculative-config", specConfig)
	}

	// Multimodal input limits (e.g., "image=4,audio=2")
	if mmLimit := spec.ConfigString("limitMmPerPrompt", ""); mmLimit != "" {
		args = append(args, "--limit-mm-per-prompt", mmLimit)
	}

	// Multimodal processor kwargs (JSON)
	if mmKwargs := spec.ConfigString("mmProcessorKwargs", ""); mmKwargs != "" {
		args = append(args, "--mm-processor-kwargs", mmKwargs)
	}

	return args
}

func (b *VLLMBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	var env []corev1.EnvVar

	// CUDA device ordering
	env = append(env, corev1.EnvVar{
		Name:  "CUDA_DEVICE_ORDER",
		Value: "PCI_BUS_ID",
	})

	// Add ROCm environment for AMD GPUs
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars(spec.GPUArch)...)

		// vLLM 0.17.0+ is V1-only. VLLM_USE_V1 env var removed.
		// Only inject flash attention and AITER controls when explicitly configured.
		enableFA := spec.ConfigBool("enableFlashAttention", false)

		var vllmEnv []corev1.EnvVar
		if enableFA {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_USE_TRITON_FLASH_ATTN", Value: "1"})
		}

		// ROCm kernel overrides are presence-aware so manifests can force
		// stability fallbacks instead of relying on vLLM's moving defaults.
		if value, ok := rocmBoolOverride(spec, "rocmUseAiter"); ok {
			if value == "0" {
				vllmEnv = append(vllmEnv, rocmAiterDisabledEnvVars()...)
			} else {
				vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_ROCM_USE_AITER", Value: "1"})
			}
		} else if value, ok := rocmBoolOverride(spec, "disableAiter"); ok {
			if value == "1" {
				vllmEnv = append(vllmEnv, rocmAiterDisabledEnvVars()...)
			} else {
				vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_ROCM_USE_AITER", Value: "1"})
			}
		} else {
			// AITER: only applicable to gfx110x (RDNA3) and gfx942 (MI300X/CDNA3)
			supportsAiter := strings.HasPrefix(spec.GPUArch, "gfx110") ||
				strings.HasPrefix(spec.GPUArch, "gfx942")
			if spec.ConfigBool("enableAiter", false) && supportsAiter {
				vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_ROCM_USE_AITER", Value: "1"})
			}
		}

		if value, ok := rocmBoolOverride(spec, "rocmCustomPagedAttn"); ok {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_ROCM_USE_AITER_PAGED_ATTN", Value: value})
		}

		env = append(env, vllmEnv...)

		env = append(env, DeviceIsolationEnvVars(spec)...)
	}

	// VLLM_DISABLED_KERNELS — comma-separated list of quantization kernels to skip.
	// Forces vLLM to fall back to alternative implementations (e.g., Triton).
	// Common use: disable ExllamaLinearKernel to avoid its fixed 288 MiB scratch
	// buffer on VRAM-constrained GPUs running compressed-tensors WNA16 models.
	if dk := spec.ConfigString("disabledKernels", ""); dk != "" {
		env = append(env, corev1.EnvVar{
			Name:  "VLLM_DISABLED_KERNELS",
			Value: dk,
		})
	}

	// PYTORCH_CUDA_ALLOC_CONF — PyTorch CUDA memory allocator configuration.
	// Controls memory fragmentation strategy. Key setting: expandable_segments:True
	// reduces fragmentation on VRAM-constrained GPUs (e.g., 24 GB running 22+ GiB
	// models). Without this, PyTorch may fail to allocate KV cache blocks even when
	// enough total free memory exists.
	if allocConf := spec.ConfigString("pytorchCudaAllocConf", ""); allocConf != "" {
		env = append(env, corev1.EnvVar{
			Name:  "PYTORCH_CUDA_ALLOC_CONF",
			Value: allocConf,
		})
	}

	if strings.EqualFold(spec.ConfigString("kvCacheCodec", ""), "turboquant") {
		env = append(env,
			corev1.EnvVar{Name: "FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC", Value: "turboquant"},
			corev1.EnvVar{Name: "FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC_STATUS", Value: "plugin"},
		)
	}

	return env
}

func resolveVLLMAttentionBackend(spec *ModelSpec) string {
	if spec == nil {
		return ""
	}
	if attentionBackend := spec.ConfigString("attentionBackend", ""); attentionBackend != "" {
		return attentionBackend
	}
	if strings.EqualFold(spec.ConfigString("kvCacheCodec", ""), "turboquant") {
		return "CUSTOM"
	}
	return ""
}

func (b *VLLMBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/health", 8000, 10, 10, 5)
}

func (b *VLLMBackend) StartupTimeout() time.Duration {
	return 300 * time.Second
}

// KVCacheArgs returns CLI arguments for KV-cache tuning.
func (b *VLLMBackend) KVCacheArgs(maxBlockSize *int, swapSpaceGiB *float64) []string {
	var args []string
	if maxBlockSize != nil {
		args = append(args, "--block-size", fmt.Sprintf("%d", *maxBlockSize))
	}
	if swapSpaceGiB != nil {
		args = append(args, "--swap-space", fmt.Sprintf("%.1f", *swapSpaceGiB))
	}
	return args
}

// SupportsSwapSpace returns false — vLLM V1 (0.17.0+) removed CPU↔GPU KV cache swapping.
func (b *VLLMBackend) SupportsSwapSpace() bool {
	return false
}

// SupportsLoRA returns true — vLLM supports hot-loading LoRA adapters.
func (b *VLLMBackend) SupportsLoRA() bool {
	return true
}

// LoRABaseArgs returns CLI arguments to enable LoRA support with a max adapter count.
func (b *VLLMBackend) LoRABaseArgs(maxAdapters int) []string {
	return []string{"--enable-lora", "--max-loras", fmt.Sprintf("%d", maxAdapters)}
}

// LoadLoRAEndpoint returns the vLLM API path for loading a LoRA adapter.
func (b *VLLMBackend) LoadLoRAEndpoint() string {
	return "/v1/load_lora_adapter"
}

// UnloadLoRAEndpoint returns the vLLM API path for unloading a LoRA adapter.
func (b *VLLMBackend) UnloadLoRAEndpoint() string {
	return "/v1/unload_lora_adapter"
}

// SupportedQuantFormats returns AWQ, GPTQ, FP8, and GGUF — the formats vLLM natively loads.
func (b *VLLMBackend) SupportedQuantFormats() []string {
	return []string{"AWQ", "GPTQ", "FP8", "GGUF"}
}

// CompilationCacheEnvVars implements CompilationCacheConfigurer.
// Redirects MIOpen, TorchInductor, and Triton caches for vLLM on ROCm.
func (b *VLLMBackend) CompilationCacheEnvVars(cacheMountPath string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "MIOPEN_CUSTOM_CACHE_DIR", Value: cacheMountPath + "/miopen"},
		{Name: "MIOPEN_USER_DB_PATH", Value: cacheMountPath + "/miopen/user.db"},
		{Name: "VLLM_CACHE_ROOT", Value: cacheMountPath + "/vllm"},
		{Name: "XDG_CACHE_HOME", Value: cacheMountPath + "/xdg"},
		{Name: "TORCHINDUCTOR_CACHE_DIR", Value: cacheMountPath + "/inductor"},
		{Name: "TRITON_CACHE_DIR", Value: cacheMountPath + "/triton"},
		{Name: "TORCH_HOME", Value: cacheMountPath + "/torch"},
	}
}

func rocmBoolOverride(spec *ModelSpec, key string) (string, bool) {
	if spec == nil || spec.Config == nil {
		return "", false
	}
	raw, ok := spec.Config[key]
	if !ok {
		return "", false
	}

	switch value := raw.(type) {
	case bool:
		if value {
			return "1", true
		}
		return "0", true
	case string:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return "", false
		}
		if parsed {
			return "1", true
		}
		return "0", true
	default:
		return "", false
	}
}

func rocmAiterDisabledEnvVars() []corev1.EnvVar {
	names := []string{
		"VLLM_ROCM_USE_AITER",
		"VLLM_ROCM_USE_AITER_PAGED_ATTN",
		"VLLM_ROCM_USE_AITER_LINEAR",
		"VLLM_ROCM_USE_AITER_MOE",
		"VLLM_ROCM_USE_AITER_RMSNORM",
		"VLLM_ROCM_USE_AITER_MLA",
		"VLLM_ROCM_USE_AITER_MHA",
		"VLLM_ROCM_USE_AITER_FP4_ASM_GEMM",
		"VLLM_ROCM_USE_AITER_TRITON_ROPE",
		"VLLM_ROCM_USE_AITER_FP8BMM",
		"VLLM_ROCM_USE_AITER_FP4BMM",
		"VLLM_ROCM_USE_AITER_UNIFIED_ATTENTION",
		"VLLM_ROCM_USE_AITER_FUSION_SHARED_EXPERTS",
		"VLLM_ROCM_USE_AITER_TRITON_GEMM",
	}
	env := make([]corev1.EnvVar, 0, len(names))
	for _, name := range names {
		env = append(env, corev1.EnvVar{Name: name, Value: "0"})
	}
	return env
}
