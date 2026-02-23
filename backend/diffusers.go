package backend

import (
	"os"
	"strings"
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
		// Check for arch-specific overrides before generic AMD fallback
		if strings.HasPrefix(gpuArch, "gfx110") {
			if img := os.Getenv("DEFAULT_DIFFUSERS_IMAGE_GFX1100"); img != "" {
				return img
			}
		}
		if strings.HasPrefix(gpuArch, "gfx906") {
			if img := os.Getenv("DEFAULT_DIFFUSERS_IMAGE_GFX906"); img != "" {
				return img
			}
		}
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

	// Inference defaults (passed to the server.py auto-detect logic)
	if steps := spec.ConfigString("numInferenceSteps", ""); steps != "" {
		env = append(env, corev1.EnvVar{
			Name:  "DEFAULT_NUM_INFERENCE_STEPS",
			Value: steps,
		})
	}
	if scale := spec.ConfigString("guidanceScale", ""); scale != "" {
		env = append(env, corev1.EnvVar{
			Name:  "DEFAULT_GUIDANCE_SCALE",
			Value: scale,
		})
	}
	if sched := spec.ConfigString("scheduler", ""); sched != "" {
		env = append(env, corev1.EnvVar{
			Name:  "DEFAULT_SCHEDULER",
			Value: sched,
		})
	}
	if neg := spec.ConfigString("negativePrompt", ""); neg != "" {
		env = append(env, corev1.EnvVar{
			Name:  "DEFAULT_NEGATIVE_PROMPT",
			Value: neg,
		})
	}
	if fp16 := spec.ConfigString("useFp16", ""); fp16 != "" {
		env = append(env, corev1.EnvVar{
			Name:  "USE_FP16",
			Value: fp16,
		})
	}
	if vaePath := spec.ConfigString("vaePath", ""); vaePath != "" {
		env = append(env, corev1.EnvVar{
			Name:  "VAE_PATH",
			Value: vaePath,
		})
	}

	// Image editing pipeline mode (inpainting, instruct, or default text2image)
	if mode := spec.ConfigString("pipelineMode", ""); mode != "" {
		env = append(env, corev1.EnvVar{Name: "PIPELINE_MODE", Value: mode})
	}
	if strength := spec.ConfigString("strength", ""); strength != "" {
		env = append(env, corev1.EnvVar{Name: "DEFAULT_STRENGTH", Value: strength})
	}
	if imgScale := spec.ConfigString("imageGuidanceScale", ""); imgScale != "" {
		env = append(env, corev1.EnvVar{Name: "DEFAULT_IMAGE_GUIDANCE_SCALE", Value: imgScale})
	}

	// Add ROCm environment for AMD GPUs
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars(spec.GPUArch)...)
		env = append(env, DeviceIsolationEnvVars(spec)...)
		// CPU offload: moves pipeline components to GPU one at a time instead
		// of bulk .to("cuda"). Avoids ROCm memory access faults with large
		// models (e.g. full SDXL) on gfx1100. ~20-30% slower but stable.
		cpuOffload := "0"
		if v := spec.ConfigString("cpuOffload", ""); v == "true" || v == "1" {
			cpuOffload = "1"
		}
		env = append(env, corev1.EnvVar{
			Name:  "USE_CPU_OFFLOAD",
			Value: cpuOffload,
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
