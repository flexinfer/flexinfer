package backend

import (
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// LlamaCppBackend implements the Backend interface for llama.cpp.
// llama.cpp provides efficient LLM inference for GGUF models.
type LlamaCppBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&LlamaCppBackend{})
}

func (b *LlamaCppBackend) Name() string {
	return "llamacpp"
}

func (b *LlamaCppBackend) Aliases() []string {
	return []string{"llama.cpp", "llama-cpp", "llama_cpp"}
}

func (b *LlamaCppBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	switch gpuVendor {
	case GPUVendorAMD:
		// Check for gfx1100 (RX 7900 series, RDNA3)
		if strings.HasPrefix(gpuArch, "gfx110") {
			if img := os.Getenv("DEFAULT_LLAMA_CPP_IMAGE_GFX1100"); img != "" {
				return img
			}
		}
		// Check for gfx906 (Radeon VII, Vega20)
		if strings.HasPrefix(gpuArch, "gfx906") {
			if img := os.Getenv("DEFAULT_LLAMA_CPP_IMAGE_GFX906"); img != "" {
				return img
			}
		}
		if img := os.Getenv("DEFAULT_LLAMA_CPP_IMAGE_AMD"); img != "" {
			return img
		}
		return "ghcr.io/ggerganov/llama.cpp:server-rocm"
	case GPUVendorCPU:
		// CPU-only image without GPU dependencies
		if img := os.Getenv("DEFAULT_LLAMA_CPP_IMAGE_CPU"); img != "" {
			return img
		}
		return "ghcr.io/ggerganov/llama.cpp:server"
	default:
		if img := os.Getenv("DEFAULT_LLAMA_CPP_IMAGE"); img != "" {
			return img
		}
		return "ghcr.io/ggerganov/llama.cpp:server-cuda"
	}
}

func (b *LlamaCppBackend) Port() int32 {
	return 8080
}

// Command returns the llama-server binary path.
// Required for custom images that use tini as entrypoint (e.g., Harbor ROCm builds).
func (b *LlamaCppBackend) Command() []string {
	return []string{"/opt/src/llama.cpp/build/bin/llama-server"}
}

func (b *LlamaCppBackend) Args(spec *ModelSpec) []string {
	args := []string{
		"--host", "0.0.0.0",
		"--port", "8080",
	}

	// Model path
	if spec.ModelPath != "" {
		args = append(args, "--model", spec.ModelPath)
	} else if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}

	// Context size
	if ctxSize := spec.ConfigInt("contextSize", 0); ctxSize > 0 {
		args = append(args, "--ctx-size", fmt.Sprintf("%d", ctxSize))
	}

	// Number of GPU layers - handle CPU-only mode
	if spec.GPUVendor == GPUVendorCPU {
		// CPU-only inference: no GPU layers
		args = append(args, "--n-gpu-layers", "0")
	} else if nGPU := spec.ConfigInt("nGPULayers", 0); nGPU > 0 {
		args = append(args, "--n-gpu-layers", fmt.Sprintf("%d", nGPU))
	} else {
		// Default to using all layers on GPU
		args = append(args, "--n-gpu-layers", "999")
	}

	// Batch size
	if batchSize := spec.ConfigInt("batchSize", 0); batchSize > 0 {
		args = append(args, "--batch-size", fmt.Sprintf("%d", batchSize))
	}

	// Number of threads - important for CPU inference
	if threads := spec.ConfigInt("threads", 0); threads > 0 {
		args = append(args, "--threads", fmt.Sprintf("%d", threads))
	}

	// Flash attention (GPU only, skip for CPU)
	if spec.GPUVendor != GPUVendorCPU && spec.ConfigBool("flashAttention", false) {
		args = append(args, "--flash-attn", "on")
	}

	// Multimodal projection file for vision models (e.g., LLaVA, Qwen-VL)
	if mmproj := spec.ConfigString("mmproj", ""); mmproj != "" {
		args = append(args, "--mmproj", mmproj)
	}

	// Chat template (e.g., chatml, llama2, etc.)
	if chatTemplate := spec.ConfigString("chatTemplate", ""); chatTemplate != "" {
		args = append(args, "--chat-template", chatTemplate)
	}

	// Explicit device selection (useful on multi-GPU nodes).
	// If "device" is unset, fall back to gpuDeviceOrdinal for compatibility.
	if device := spec.ConfigString("device", ""); device != "" {
		args = append(args, "--device", device)
	} else if ordinal := spec.ConfigString("gpuDeviceOrdinal", ""); ordinal != "" {
		args = append(args, "--device", ordinal)
	}

	// Parallel requests
	if parallel := spec.ConfigInt("parallel", 0); parallel > 0 {
		args = append(args, "--parallel", fmt.Sprintf("%d", parallel))
	}

	// KV cache quantization (for memory efficiency)
	if cacheTypeK := spec.ConfigString("cacheTypeK", ""); cacheTypeK != "" {
		args = append(args, "--cache-type-k", cacheTypeK)
	}
	if cacheTypeV := spec.ConfigString("cacheTypeV", ""); cacheTypeV != "" {
		args = append(args, "--cache-type-v", cacheTypeV)
	}

	// Ubatch size for better throughput
	if ubatchSize := spec.ConfigInt("ubatchSize", 0); ubatchSize > 0 {
		args = append(args, "--ubatch-size", fmt.Sprintf("%d", ubatchSize))
	}

	// Thinking/reasoning output format for OpenAI-compatible responses.
	if reasoningFormat := spec.ConfigString("reasoningFormat", ""); reasoningFormat != "" {
		args = append(args, "--reasoning-format", reasoningFormat)
	}
	if reasoningBudget, ok := configOptionalInt(spec, "reasoningBudget"); ok {
		args = append(args, "--reasoning-budget", fmt.Sprintf("%d", reasoningBudget))
	}

	// Enable metrics endpoint
	if spec.ConfigBool("metrics", false) {
		args = append(args, "--metrics")
	}

	return args
}

func (b *LlamaCppBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	var env []corev1.EnvVar

	// Add ROCm environment for AMD GPUs.
	// llama.cpp uses HIP directly (not PyTorch), so PYTORCH_* vars are
	// irrelevant but harmless. HSA_OVERRIDE_GFX_VERSION, however, must
	// match the actual GPU — gfx906 is natively supported and must NOT
	// be overridden to 11.0.0.
	if spec.GPUVendor == GPUVendorAMD {
		if strings.HasPrefix(spec.GPUArch, "gfx906") {
			// Radeon VII (Vega20): natively supported by ROCm.
			// Disable SDMA for stability on Vega20.
			env = append(env, corev1.EnvVar{Name: "HSA_ENABLE_SDMA", Value: "0"})
		} else {
			env = append(env, ROCmEnvVars()...)
		}

		// Optional device pinning for systems exposing multiple AMD GPUs.
		// Keep behavior aligned with MLC-LLM config keys for easier migration.
		hipVisible := spec.ConfigString("hipVisibleDevices", "")
		rocrVisible := spec.ConfigString("rocrVisibleDevices", "")
		ordinal := spec.ConfigString("gpuDeviceOrdinal", "")
		if hipVisible == "" && rocrVisible == "" && ordinal != "" {
			hipVisible = ordinal
			rocrVisible = ordinal
		}
		if hipVisible != "" && rocrVisible == "" {
			rocrVisible = hipVisible
		}
		if rocrVisible != "" && hipVisible == "" {
			hipVisible = rocrVisible
		}
		if rocrVisible != "" {
			env = append(env, corev1.EnvVar{Name: "ROCR_VISIBLE_DEVICES", Value: rocrVisible})
		}
		if hipVisible != "" {
			env = append(env, corev1.EnvVar{Name: "HIP_VISIBLE_DEVICES", Value: hipVisible})
		}
		if ordinal != "" {
			env = append(env, corev1.EnvVar{Name: "GPU_DEVICE_ORDINAL", Value: ordinal})
		}
	}

	return env
}

func configOptionalInt(spec *ModelSpec, key string) (int, bool) {
	if spec.Config == nil {
		return 0, false
	}
	raw, ok := spec.Config[key]
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

func (b *LlamaCppBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/health", 8080, 10, 10, 5)
}

func (b *LlamaCppBackend) StartupTimeout() time.Duration {
	return 60 * time.Second
}

// SupportsGPUVendor returns true for NVIDIA, AMD, and CPU-only inference.
// llama.cpp is unique in supporting efficient CPU-only inference.
func (b *LlamaCppBackend) SupportsGPUVendor(vendor GPUVendor) bool {
	return vendor == GPUVendorNVIDIA || vendor == GPUVendorAMD || vendor == GPUVendorCPU
}

// SupportedQuantFormats returns GGUF — the native format for llama.cpp.
func (b *LlamaCppBackend) SupportedQuantFormats() []string {
	return []string{"GGUF"}
}
