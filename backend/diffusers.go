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
		// Check for arch-specific overrides before built-in defaults
		if strings.HasPrefix(gpuArch, "gfx110") {
			if img := os.Getenv("DEFAULT_DIFFUSERS_IMAGE_GFX1100"); img != "" {
				return img
			}
			return "registry.harbor.lan/flexinfer/diffusers:rocm-gfx1100"
		}
		if strings.HasPrefix(gpuArch, "gfx906") {
			if img := os.Getenv("DEFAULT_DIFFUSERS_IMAGE_GFX906"); img != "" {
				return img
			}
			return "registry.harbor.lan/flexinfer/diffusers:rocm-gfx906"
		}
		if img := os.Getenv("DEFAULT_DIFFUSERS_IMAGE_AMD"); img != "" {
			return img
		}
		// Fallback: use generic rocm-latest tag for other AMD GPUs
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

	// Warmup inference control: skip the startup warmup pass if requested.
	if skip := spec.ConfigString("skipWarmup", ""); skip != "" {
		env = append(env, corev1.EnvVar{Name: "SKIP_WARMUP", Value: skip})
	}
	// Warmup resolution: compile GPU kernels at the target resolution so MIOpen/Triton
	// produce the right kernel instantiations. Default 512x512 in the container.
	if warmupW := spec.ConfigString("warmupWidth", ""); warmupW != "" {
		env = append(env, corev1.EnvVar{Name: "WARMUP_WIDTH", Value: warmupW})
	}
	if warmupH := spec.ConfigString("warmupHeight", ""); warmupH != "" {
		env = append(env, corev1.EnvVar{Name: "WARMUP_HEIGHT", Value: warmupH})
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

		// gfx906 (16GB VRAM): tighter memory allocation and attention slicing
		if strings.HasPrefix(spec.GPUArch, "gfx906") {
			env = append(env,
				corev1.EnvVar{
					Name:  "PYTORCH_HIP_ALLOC_CONF",
					Value: "garbage_collection_threshold:0.8,max_split_size_mb:256",
				},
				corev1.EnvVar{
					Name:  "ENABLE_ATTENTION_SLICING",
					Value: "1",
				},
			)
		}
	}

	return env
}

func (b *DiffusersBackend) ReadinessProbe() *corev1.Probe {
	// InitialDelay=0: startup probe handles cold start; readiness only runs after startup succeeds.
	return HTTPReadinessProbe("/health", 8000, 0, 5, 3)
}

func (b *DiffusersBackend) StartupProbe() *corev1.Probe {
	return HTTPStartupProbe("/health", 8000, b.StartupTimeout())
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

// CompilationCacheEnvVars implements CompilationCacheConfigurer.
// Redirects MIOpen, TorchInductor, and Triton caches for diffusers pipelines.
func (b *DiffusersBackend) CompilationCacheEnvVars(cacheMountPath string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "MIOPEN_CUSTOM_CACHE_DIR", Value: cacheMountPath + "/miopen"},
		{Name: "MIOPEN_USER_DB_PATH", Value: cacheMountPath + "/miopen/user.db"},
		{Name: "TORCHINDUCTOR_CACHE_DIR", Value: cacheMountPath + "/inductor"},
		{Name: "TRITON_CACHE_DIR", Value: cacheMountPath + "/triton"},
		{Name: "TORCH_HOME", Value: cacheMountPath + "/torch"},
	}
}
