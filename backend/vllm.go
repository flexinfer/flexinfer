package backend

import (
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

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
	switch gpuVendor {
	case GPUVendorAMD:
		// Check for gfx1100 (RX 7900 series, RDNA3) which needs specialized image
		if strings.HasPrefix(gpuArch, "gfx110") {
			if img := os.Getenv("DEFAULT_VLLM_IMAGE_GFX1100"); img != "" {
				return img
			}
			// GFX1100-specific image built with ROCm 6.4 and flash attention disabled
			return "registry.harbor.lan/flexinfer/vllm:rocm-gfx1100"
		}
		// Check for gfx906 (Radeon VII, Vega20) which needs specialized image
		if strings.HasPrefix(gpuArch, "gfx906") {
			if img := os.Getenv("DEFAULT_VLLM_IMAGE_GFX906"); img != "" {
				return img
			}
			// GFX906-specific image built without flash attention
			return "registry.harbor.lan/flexinfer/vllm:rocm-gfx906"
		}
		if img := os.Getenv("DEFAULT_VLLM_IMAGE_AMD"); img != "" {
			return img
		}
		// Generic ROCm image for other AMD GPUs (MI300X, etc.)
		return "rocm/vllm:latest"
	default:
		if img := os.Getenv("DEFAULT_VLLM_IMAGE"); img != "" {
			return img
		}
		return "vllm/vllm-openai:latest"
	}
}

func (b *VLLMBackend) Port() int32 {
	return 8000
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

	// CPU offload — move part of model weights to CPU to free VRAM for KV cache
	// Note: not supported in vLLM V1 engine (nightly 0.14.0+), only V0 (0.7.x)
	if cpuOffload := spec.ConfigInt("cpuOffloadGb", 0); cpuOffload > 0 {
		args = append(args, "--cpu-offload-gb", fmt.Sprintf("%d", cpuOffload))
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

	// Prefix caching — enabled by default in V1, allow explicit control
	if spec.Config != nil {
		if _, ok := spec.Config["enablePrefixCaching"]; ok {
			if spec.ConfigBool("enablePrefixCaching", true) {
				args = append(args, "--enable-prefix-caching")
			} else {
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

		// Only inject vLLM-specific env vars when explicitly configured.
		// - Legacy 0.7.3 images: Dockerfile bakes VLLM_USE_V1=0 as safe default
		// - 0.14.0+ images: VLLM_USE_V1 env var removed, V1 is the only engine
		// When empty (default), don't inject — let Dockerfile ENV win.
		engineVersion := spec.ConfigString("vllmEngineVersion", "")
		enableFA := spec.ConfigBool("enableFlashAttention", false)
		enableAiter := spec.ConfigBool("enableAiter", false)

		// Collect vLLM env vars that are explicitly opted into
		var vllmEnv []corev1.EnvVar
		if engineVersion == "v1" {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_USE_V1", Value: "1"})
		} else if engineVersion == "v0" {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_USE_V1", Value: "0"})
		}
		if enableFA {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_USE_TRITON_FLASH_ATTN", Value: "1"})
		}

		// AITER: only applicable to gfx110x (RDNA3) and gfx942 (MI300X/CDNA3)
		supportsAiter := strings.HasPrefix(spec.GPUArch, "gfx110") ||
			strings.HasPrefix(spec.GPUArch, "gfx942")
		if enableAiter && supportsAiter {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_ROCM_USE_AITER", Value: "1"})
		}

		// Prefill-Decode split attention: uses separate Triton kernels for prefill
		// and a custom ROCm paged-attention kernel for decode. Can improve decode
		// throughput on RDNA3 where AITER is not available.
		if spec.ConfigBool("enablePrefillDecodeAttention", false) {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_V1_USE_PREFILL_DECODE_ATTENTION", Value: "1"})
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
	return 120 * time.Second
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

// SupportsSwapSpace returns true — vLLM supports CPU-offloaded KV-cache via --swap-space.
func (b *VLLMBackend) SupportsSwapSpace() bool {
	return true
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
