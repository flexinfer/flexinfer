package backend

import (
	"fmt"
	"os"
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
		args = append(args, "--flash-attn")
	}

	// Multimodal projection file for vision models (e.g., LLaVA, Qwen-VL)
	if mmproj := spec.ConfigString("mmproj", ""); mmproj != "" {
		args = append(args, "--mmproj", mmproj)
	}

	// Chat template (e.g., chatml, llama2, etc.)
	if chatTemplate := spec.ConfigString("chatTemplate", ""); chatTemplate != "" {
		args = append(args, "--chat-template", chatTemplate)
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

	// Enable metrics endpoint
	if spec.ConfigBool("metrics", false) {
		args = append(args, "--metrics")
	}

	return args
}

func (b *LlamaCppBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	var env []corev1.EnvVar

	// Add ROCm environment for AMD GPUs
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars()...)
	}

	return env
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
