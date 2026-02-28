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

		env = append(env, vllmEnv...)

		env = append(env, DeviceIsolationEnvVars(spec)...)
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

// SupportedQuantFormats returns AWQ, GPTQ, and FP8 — the formats vLLM natively loads.
func (b *VLLMBackend) SupportedQuantFormats() []string {
	return []string{"AWQ", "GPTQ", "FP8"}
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
