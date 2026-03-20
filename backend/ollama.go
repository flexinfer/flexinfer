package backend

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

// ollamaImageRules defines the image resolution precedence for Ollama.
// Arch-specific rules have env-only overrides (no built-in default) so they
// fall through to the vendor-level default when unset.
var ollamaImageRules = []ImageRule{
	// AMD arch-specific (env-only, fall through to AMD generic)
	{Vendor: GPUVendorAMD, ArchPrefix: "gfx110", EnvVar: "DEFAULT_OLLAMA_IMAGE_GFX1100"},
	{Vendor: GPUVendorAMD, ArchPrefix: "gfx906", EnvVar: "DEFAULT_OLLAMA_IMAGE_GFX906"},
	// AMD generic
	{Vendor: GPUVendorAMD, EnvVar: "DEFAULT_BACKEND_IMAGE_AMD", Default: "ollama/ollama:rocm"},
	// Intel
	{Vendor: GPUVendorIntel, EnvVar: "DEFAULT_BACKEND_IMAGE_INTEL", Default: "ollama/ollama:latest"},
	// NVIDIA Maxwell sub-arch (env-only, fall through to NVIDIA generic)
	{Vendor: GPUVendorNVIDIA, ArchPrefix: "sm_5", EnvVar: "DEFAULT_BACKEND_IMAGE_MAXWELL"},
	// NVIDIA generic
	{Vendor: GPUVendorNVIDIA, EnvVar: "DEFAULT_BACKEND_IMAGE_NVIDIA"},
	{Vendor: GPUVendorNVIDIA, EnvVar: "DEFAULT_BACKEND_IMAGE", Default: "ollama/ollama:latest"},
	// Global default (CPU, unknown, etc.)
	{EnvVar: "DEFAULT_BACKEND_IMAGE", Default: "ollama/ollama:latest"},
}

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
	return ResolveImage(ollamaImageRules, gpuVendor, gpuArch)
}

func (b *OllamaBackend) Port() int32 {
	return PortOllama
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
