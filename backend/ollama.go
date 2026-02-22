package backend

import (
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// OllamaBackend implements the Backend interface for Ollama.
// Ollama downloads models on-demand, so it doesn't require a volume mount.
type OllamaBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&OllamaBackend{})
}

func (b *OllamaBackend) Name() string {
	return "ollama"
}

func (b *OllamaBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	// Check for environment variable overrides
	switch gpuVendor {
	case GPUVendorAMD:
		// Check for arch-specific overrides before generic AMD fallback
		if strings.HasPrefix(gpuArch, "gfx110") {
			if img := os.Getenv("DEFAULT_OLLAMA_IMAGE_GFX1100"); img != "" {
				return img
			}
		}
		if strings.HasPrefix(gpuArch, "gfx906") {
			if img := os.Getenv("DEFAULT_OLLAMA_IMAGE_GFX906"); img != "" {
				return img
			}
		}
		if img := os.Getenv("DEFAULT_BACKEND_IMAGE_AMD"); img != "" {
			return img
		}
		return "ollama/ollama:rocm"
	case GPUVendorIntel:
		if img := os.Getenv("DEFAULT_BACKEND_IMAGE_INTEL"); img != "" {
			return img
		}
		return "ollama/ollama:latest"
	case GPUVendorNVIDIA:
		// Check for Maxwell architecture (sm_52) — pre-built ollama CUDA 12.x
		// binaries do not include sm_52 support.
		if strings.HasPrefix(gpuArch, "sm_5") {
			if img := os.Getenv("DEFAULT_BACKEND_IMAGE_MAXWELL"); img != "" {
				return img
			}
		}
		if img := os.Getenv("DEFAULT_BACKEND_IMAGE_NVIDIA"); img != "" {
			return img
		}
		if img := os.Getenv("DEFAULT_BACKEND_IMAGE"); img != "" {
			return img
		}
		return "ollama/ollama:latest"
	default:
		if img := os.Getenv("DEFAULT_BACKEND_IMAGE"); img != "" {
			return img
		}
		return "ollama/ollama:latest"
	}
}

func (b *OllamaBackend) Port() int32 {
	return 11434
}

func (b *OllamaBackend) Args(spec *ModelSpec) []string {
	return []string{"serve"}
}

func (b *OllamaBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{
			Name:  "OLLAMA_HOST",
			Value: "0.0.0.0",
		},
		{
			// Prevent Ollama from unloading models after idle timeout.
			// Without this, the first request after ~5min idle pays a
			// multi-second cold-start penalty to reload weights into GPU.
			Name:  "OLLAMA_KEEP_ALIVE",
			Value: "-1",
		},
	}

	// Add ROCm environment for AMD GPUs
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars(spec.GPUArch)...)
		env = append(env, DeviceIsolationEnvVars(spec)...)
	}

	return env
}

func (b *OllamaBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/", 11434, 10, 10, 5)
}

func (b *OllamaBackend) StartupTimeout() time.Duration {
	return 60 * time.Second
}

// NeedsVolume returns false because Ollama downloads models on-demand.
func (b *OllamaBackend) NeedsVolume() bool {
	return false
}

// SupportedQuantFormats returns GGUF — Ollama natively consumes GGUF models.
func (b *OllamaBackend) SupportedQuantFormats() []string {
	return []string{"GGUF"}
}
