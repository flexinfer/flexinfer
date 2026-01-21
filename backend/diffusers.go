package backend

import (
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// DiffusersBackend implements the Backend interface for the Diffusers API server.
// Provides OpenAI-compatible image generation endpoints.
type DiffusersBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&DiffusersBackend{})
}

func (b *DiffusersBackend) Name() string {
	return "diffusers"
}

func (b *DiffusersBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	switch gpuVendor {
	case GPUVendorAMD:
		if img := os.Getenv("DEFAULT_DIFFUSERS_IMAGE_AMD"); img != "" {
			return img
		}
		// Use rocm-latest tag which is built with gfx1100/RDNA3 optimizations
		return "registry.harbor.lan/library/diffusers-api:rocm-latest"
	default:
		if img := os.Getenv("DEFAULT_DIFFUSERS_IMAGE"); img != "" {
			return img
		}
		return "registry.harbor.lan/library/diffusers-api:cuda"
	}
}

func (b *DiffusersBackend) Port() int32 {
	return 8000
}

func (b *DiffusersBackend) Args(spec *ModelSpec) []string {
	// Diffusers API server uses environment variables for configuration
	return nil
}

func (b *DiffusersBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{
			Name:  "MODEL_ID",
			Value: spec.Model,
		},
		{
			Name:  "MODEL",
			Value: spec.Model,
		},
		{
			Name:  "PORT",
			Value: "8000",
		},
	}

	// Add ROCm environment for AMD GPUs
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars()...)
		// Disable CPU offload - gfx1100 is fast enough without it and
		// CPU offload causes ~10x slowdown on modern RDNA3 GPUs
		env = append(env, corev1.EnvVar{
			Name:  "USE_CPU_OFFLOAD",
			Value: "0",
		})
	}

	return env
}

func (b *DiffusersBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/health", 8000, 30, 10, 5)
}

func (b *DiffusersBackend) StartupTimeout() time.Duration {
	return 180 * time.Second // Image gen models can take longer to load
}

// NeedsVolume returns true so HuggingFace artifacts can be cached on a SharedPVC.
func (b *DiffusersBackend) NeedsVolume() bool {
	return true
}

// IsImageGeneration returns true for diffusers.
func (b *DiffusersBackend) IsImageGeneration() bool {
	return true
}

// DefaultIdleTimeout returns a longer timeout for image generation.
func (b *DiffusersBackend) DefaultIdleTimeout() time.Duration {
	return 10 * time.Minute
}
