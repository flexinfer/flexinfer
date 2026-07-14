package backend

import (
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// diffusersImageRules defines the image resolution precedence for Diffusers.
//
// Arch-specific rules are env-only (no built-in default) so they fall through
// to the AMD-generic image when no env override is set. The arch defaults now
// live in deploy/gpuprofiles/gfx1100.yaml and gfx906.yaml — callers that pass
// a GPUProfile through backend.ResolveBackendImage get the per-arch image
// from the profile, and only nodes without a profile fall back to this slice.
var diffusersImageRules = []ImageRule{
	// AMD arch-specific (env-only; profile owns the default)
	{Vendor: GPUVendorAMD, ArchPrefix: "gfx110", EnvVar: "DEFAULT_DIFFUSERS_IMAGE_GFX1100"},
	{Vendor: GPUVendorAMD, ArchPrefix: "gfx906", EnvVar: "DEFAULT_DIFFUSERS_IMAGE_GFX906"},
	// AMD generic
	{Vendor: GPUVendorAMD, EnvVar: "DEFAULT_DIFFUSERS_IMAGE_AMD", Default: "registry.harbor.lan/library/diffusers-api:rocm-latest"},
	// Global default
	{EnvVar: "DEFAULT_DIFFUSERS_IMAGE", Default: "registry.harbor.lan/library/diffusers-api:cuda"},
}

// DiffusersBackend implements the Backend interface for the Diffusers API server.
// Provides OpenAI-compatible image generation endpoints.
type DiffusersBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&DiffusersBackend{})
}

func (b *DiffusersBackend) Name() string {
	return NameDiffusers
}

func (b *DiffusersBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	return ResolveImage(diffusersImageRules, gpuVendor, gpuArch)
}

func (b *DiffusersBackend) Port() int32 {
	return PortDiffusers
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
	if vaeRepo := spec.ConfigString("vaeRepo", ""); vaeRepo != "" {
		env = append(env, corev1.EnvVar{
			Name:  "VAE_REPO",
			Value: vaeRepo,
		})
	}
	if singleFileConfig := spec.ConfigString("singleFileConfig", ""); singleFileConfig != "" {
		env = append(env, corev1.EnvVar{
			Name:  "SINGLE_FILE_CONFIG",
			Value: singleFileConfig,
		})
	}
	if singleFilePipeline := spec.ConfigString("singleFilePipeline", ""); singleFilePipeline != "" {
		env = append(env, corev1.EnvVar{
			Name:  "SINGLE_FILE_PIPELINE",
			Value: singleFilePipeline,
		})
	}
	if singleFileStrict := spec.ConfigString("singleFileStrict", ""); singleFileStrict != "" {
		env = append(env, corev1.EnvVar{
			Name:  "SINGLE_FILE_STRICT",
			Value: singleFileStrict,
		})
	}
	if modelFamily := spec.ConfigString("modelFamily", ""); modelFamily != "" {
		env = append(env, corev1.EnvVar{
			Name:  "MODEL_FAMILY",
			Value: modelFamily,
		})
	}
	if compileMode := spec.ConfigString("compileMode", ""); compileMode != "" {
		env = append(env, corev1.EnvVar{Name: "COMPILE_MODE", Value: compileMode})
	}
	if compileFullgraph := spec.ConfigString("compileFullgraph", ""); compileFullgraph != "" {
		env = append(env, corev1.EnvVar{Name: "COMPILE_FULLGRAPH", Value: compileFullgraph})
	}
	if compileDynamic := spec.ConfigString("compileDynamic", ""); compileDynamic != "" {
		env = append(env, corev1.EnvVar{Name: "COMPILE_DYNAMIC", Value: compileDynamic})
	}
	if compileRepeatedBlocks := spec.ConfigString("compileRepeatedBlocks", ""); compileRepeatedBlocks != "" {
		env = append(env, corev1.EnvVar{Name: "COMPILE_REPEATED_BLOCKS", Value: compileRepeatedBlocks})
	}
	if loraPath := spec.ConfigString("loraPath", ""); loraPath != "" {
		env = append(env, corev1.EnvVar{Name: "LORA_PATH", Value: loraPath})
	}
	if loraRepo := spec.ConfigString("loraRepo", ""); loraRepo != "" {
		env = append(env, corev1.EnvVar{Name: "LORA_REPO", Value: loraRepo})
	}
	if loraWeightName := spec.ConfigString("loraWeightName", ""); loraWeightName != "" {
		env = append(env, corev1.EnvVar{Name: "LORA_WEIGHT_NAME", Value: loraWeightName})
	}
	if loraAdapterName := spec.ConfigString("loraAdapterName", ""); loraAdapterName != "" {
		env = append(env, corev1.EnvVar{Name: "LORA_ADAPTER_NAME", Value: loraAdapterName})
	}
	if loraScale := spec.ConfigString("loraScale", ""); loraScale != "" {
		env = append(env, corev1.EnvVar{Name: "LORA_SCALE", Value: loraScale})
	}

	// Quantization mode (e.g. "nf4" for bitsandbytes NF4 on FLUX models)
	if quant := spec.ConfigString("quantization", ""); quant != "" {
		env = append(env, corev1.EnvVar{Name: "QUANTIZATION", Value: quant})
	}

	// BNB compute dtype override (e.g. "bfloat16" for 2x speedup on gfx1100).
	// Default is "float32" for numerical stability; bfloat16 needs empirical validation.
	if dtype := spec.ConfigString("computeDtype", ""); dtype != "" {
		env = append(env, corev1.EnvVar{Name: "BNB_COMPUTE_DTYPE", Value: dtype})
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
	if aiterRope := spec.ConfigString("useRocmAiterRopeBackend", ""); aiterRope != "" {
		env = append(env, corev1.EnvVar{
			Name:  "USE_ROCM_AITER_ROPE_BACKEND",
			Value: aiterRope,
		})
	}

	// ControlNet support
	if cnPath := spec.ConfigString("controlnetPath", ""); cnPath != "" {
		env = append(env, corev1.EnvVar{Name: "CONTROLNET_PATH", Value: cnPath})
	}
	if cnRepo := spec.ConfigString("controlnetRepo", ""); cnRepo != "" {
		env = append(env, corev1.EnvVar{Name: "CONTROLNET_REPO", Value: cnRepo})
	}
	if cnScale := spec.ConfigString("controlnetScale", ""); cnScale != "" {
		env = append(env, corev1.EnvVar{Name: "CONTROLNET_SCALE", Value: cnScale})
	}

	// Warmup inference control: skip the startup warmup pass if requested.
	if skip := spec.ConfigString("skipWarmup", ""); skip != "" {
		env = append(env, corev1.EnvVar{Name: "SKIP_WARMUP", Value: skip})
	}

	// Multi-resolution warmup: pre-compile MIOpen/Triton kernels at each resolution
	// so the first real request at any listed resolution is fast.
	// Priority: warmupResolutions > warmupWidth/warmupHeight > GPU-arch auto-default.
	if res := spec.ConfigString("warmupResolutions", ""); res != "" {
		env = append(env, corev1.EnvVar{Name: "WARMUP_RESOLUTIONS", Value: res})
	} else if warmupW := spec.ConfigString("warmupWidth", ""); warmupW != "" {
		// Legacy single-resolution config: pass through for backward compat.
		env = append(env, corev1.EnvVar{Name: "WARMUP_WIDTH", Value: warmupW})
		if warmupH := spec.ConfigString("warmupHeight", ""); warmupH != "" {
			env = append(env, corev1.EnvVar{Name: "WARMUP_HEIGHT", Value: warmupH})
		}
	} else if spec.GPUVendor == GPUVendorAMD {
		// Auto-default: AMD GPUs use MIOpen which compiles resolution-specific kernels.
		switch {
		case strings.HasPrefix(spec.GPUArch, "gfx110"):
			// gfx1100 (24GB VRAM): warm up both 512 and 1024
			env = append(env, corev1.EnvVar{Name: "WARMUP_RESOLUTIONS", Value: "512x512,1024x1024"})
		case strings.HasPrefix(spec.GPUArch, "gfx906"):
			// gfx906 (16GB VRAM): 1024x1024 risks OOM, stick with 512
			env = append(env, corev1.EnvVar{Name: "WARMUP_RESOLUTIONS", Value: "512x512"})
		}
		// Other AMD archs and NVIDIA: no auto-default (container default is 512x512)
	}

	// Add ROCm environment for AMD GPUs
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars(spec.GPUArch)...)
		env = append(env, DeviceIsolationEnvVars(spec)...)

		// Override HIPBLASLT for diffusers: ROCmEnvVars sets =1 for vLLM GEMM
		// perf on gfx110x, but hipBLASLt causes stability issues with diffusers
		// pipelines (matches Dockerfile ENV TORCH_BLAS_PREFER_HIPBLASLT=0).
		if strings.HasPrefix(spec.GPUArch, "gfx110") {
			env = append(env, corev1.EnvVar{Name: "TORCH_BLAS_PREFER_HIPBLASLT", Value: "0"})
		}
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

		// Workaround for ROCm/ROCm#4729: VAE decode crash on RDNA3 at
		// resolutions above 1024px. MIOPEN_FIND_MODE=2 (FAST) avoids the
		// kernel selection path that triggers the crash. ~10-15% slower but
		// stable. Configurable via CRD to allow override.
		if strings.HasPrefix(spec.GPUArch, "gfx110") {
			findMode := spec.ConfigString("miopenFindMode", "2")
			env = append(env, corev1.EnvVar{
				Name:  "MIOPEN_FIND_MODE",
				Value: findMode,
			})
			// expandable_segments prevents reserved-but-fragmented memory OOM
			// on consecutive generations (same fix used for quantization jobs).
			env = append(env, corev1.EnvVar{
				Name:  "PYTORCH_HIP_ALLOC_CONF",
				Value: "garbage_collection_threshold:0.9,max_split_size_mb:512,expandable_segments:True",
			})
		}

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
	// The HTTP server runs synchronous diffusion work in-process, so /health can
	// time out while the GPU is busy even though the server is healthy. Startup
	// retains the semantic HTTP check; after it succeeds, readiness only verifies
	// that the serving socket remains open and avoids removing a busy model from
	// EndpointSlices. Fail a genuinely closed socket after three probes (15s).
	probe := TCPReadinessProbe(8000, 0, 5, 3)
	probe.FailureThreshold = 3
	return probe
}

func (b *DiffusersBackend) StartupProbe() *corev1.Probe {
	return HTTPStartupProbe("/health", 8000, b.StartupTimeout())
}

func (b *DiffusersBackend) StartupTimeout() time.Duration {
	return 900 * time.Second // NF4+cpuOffload models need ~8-10 min to load + setup accelerate hooks
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
