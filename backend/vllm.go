package backend

import (
	"fmt"
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
	return "vllm"
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

	// CPU offload removed in vLLM V1 (0.17.0+). Previously --cpu-offload-gb.
	// For weight offloading, use model-level config or OffloadConfig in future.

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

	// Prefix caching is ON by default in vLLM V1 (0.17.0+).
	// Only emit --no-prefix-caching when explicitly disabled.
	if spec.Config != nil {
		if _, ok := spec.Config["enablePrefixCaching"]; ok {
			if !spec.ConfigBool("enablePrefixCaching", true) {
				args = append(args, "--no-prefix-caching")
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
		enableAiter := spec.ConfigBool("enableAiter", false)

		var vllmEnv []corev1.EnvVar
		if enableFA {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_USE_TRITON_FLASH_ATTN", Value: "1"})
		}

		// AITER: only applicable to gfx110x (RDNA3) and gfx942 (MI300X/CDNA3)
		supportsAiter := strings.HasPrefix(spec.GPUArch, "gfx110") ||
			strings.HasPrefix(spec.GPUArch, "gfx942")
		if enableAiter && supportsAiter {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_ROCM_USE_AITER", Value: "1"})
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

	return env
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
		{Name: "TORCHINDUCTOR_CACHE_DIR", Value: cacheMountPath + "/inductor"},
		{Name: "TRITON_CACHE_DIR", Value: cacheMountPath + "/triton"},
		{Name: "TORCH_HOME", Value: cacheMountPath + "/torch"},
	}
}
