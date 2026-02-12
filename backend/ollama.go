package backend

import (
	"os"
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
		if img := os.Getenv("DEFAULT_BACKEND_IMAGE_AMD"); img != "" {
			return img
		}
		return "ollama/ollama:rocm"
	case GPUVendorIntel:
		if img := os.Getenv("DEFAULT_BACKEND_IMAGE_INTEL"); img != "" {
			return img
		}
		return "ollama/ollama:latest"
	default:
		if img := os.Getenv("DEFAULT_BACKEND_IMAGE_NVIDIA"); img != "" {
			return img
		}
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
	}

	// Add ROCm environment for AMD GPUs
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars()...)
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
