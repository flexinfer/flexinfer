package backend

import (
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// mlcLLMImageRules defines the image resolution precedence for MLC-LLM.
//
// AMD arch-specific rules are env-only (no built-in default) so they fall
// through to the AMD-generic image when no env override is set. The arch
// defaults now live in deploy/gpuprofiles/gfx1100.yaml and gfx906.yaml —
// callers that pass a GPUProfile through backend.ResolveBackendImage get the
// per-arch image from the profile, and only nodes without a profile fall back
// to this slice. The NVIDIA Maxwell entry keeps its hardcoded default until
// the sm-52 profile follow-up lands.
var mlcLLMImageRules = []ImageRule{
	// AMD arch-specific (env-only; profile owns the default)
	{Vendor: GPUVendorAMD, ArchPrefix: "gfx110", EnvVar: "DEFAULT_MLC_LLM_IMAGE_GFX1100"},
	{Vendor: GPUVendorAMD, ArchPrefix: "gfx906", EnvVar: "DEFAULT_MLC_LLM_IMAGE_GFX906"},
	// AMD generic
	{Vendor: GPUVendorAMD, EnvVar: "DEFAULT_MLC_LLM_IMAGE_AMD", Default: "ghcr.io/mlc-ai/mlc-llm:rocm"},
	// NVIDIA Maxwell sub-arch
	{Vendor: GPUVendorNVIDIA, ArchPrefix: "sm_5", EnvVar: "DEFAULT_MLC_LLM_IMAGE_MAXWELL", Default: "registry.harbor.lan/flexinfer/mlc-llm:cuda-maxwell-v7"},
	// NVIDIA generic
	{Vendor: GPUVendorNVIDIA, EnvVar: "DEFAULT_MLC_LLM_IMAGE", Default: "ghcr.io/mlc-ai/mlc-llm:cuda"},
	// Global default
	{EnvVar: "DEFAULT_MLC_LLM_IMAGE", Default: "ghcr.io/mlc-ai/mlc-llm:cuda"},
}

// MLCLLMBackend implements the Backend interface for MLC-LLM.
// MLC-LLM provides high-performance LLM inference with pre-compiled models.
type MLCLLMBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&MLCLLMBackend{})
}

func (b *MLCLLMBackend) Name() string {
	return NameMLCLLM
}

func (b *MLCLLMBackend) Aliases() []string {
	return []string{"mlc", "mlc_llm"}
}

func (b *MLCLLMBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	return ResolveImage(mlcLLMImageRules, gpuVendor, gpuArch)
}

func (b *MLCLLMBackend) Port() int32 {
	return PortMLCLLM
}

func (b *MLCLLMBackend) Command() []string {
	return []string{"python", "-m", "mlc_llm"}
}

func (b *MLCLLMBackend) Args(spec *ModelSpec) []string {
	// MLC-LLM CLI uses `serve` for the HTTP server subcommand.
	// Older configs may still specify `server`; accept it for compatibility.
	mode := spec.ConfigString("mode", "serve")
	if mode == "server" {
		mode = "serve"
	}

	args := []string{mode}

	// Model path
	modelPath := spec.ModelPath
	if modelPath == "" {
		modelPath = spec.Model
	}
	args = append(args, modelPath)

	// Host binding
	args = append(args, "--host", "0.0.0.0")

	// Model library path
	jitPolicy := spec.ConfigString("jitPolicy", "")
	// Maxwell (sm_5x) is a special case: JIT is often unavailable (no nvcc in
	// runtime images) and FP16 codegen is unsupported. Prefer pre-compiled libs.
	if jitPolicy == "" && spec.GPUVendor == GPUVendorNVIDIA && strings.HasPrefix(spec.GPUArch, "sm_5") {
		jitPolicy = "READONLY"
	}

	libPath := spec.ConfigString("modelLibPath", "")
	if libPath == "" && strings.EqualFold(jitPolicy, "READONLY") {
		// When JIT is disabled (READONLY), MLC requires a pre-compiled model library.
		// Default to the conventional on-disk name when the model path is a mounted directory.
		if spec.GPUVendor == GPUVendorAMD && spec.GPUArch != "" && strings.HasPrefix(modelPath, "/") {
			libPath = fmt.Sprintf("%s/lib_rocm_%s.so", strings.TrimRight(modelPath, "/"), spec.GPUArch)
		}
		if spec.GPUVendor == GPUVendorNVIDIA && strings.HasPrefix(spec.GPUArch, "sm_5") && strings.HasPrefix(modelPath, "/") {
			libPath = fmt.Sprintf("%s/maxwell-lib.so", strings.TrimRight(modelPath, "/"))
		}
	}
	if libPath != "" {
		args = append(args, "--model-lib", libPath)
	}

	// Build overrides string from config
	overrides := b.buildOverrides(spec)
	if overrides != "" {
		args = append(args, "--overrides", overrides)
	}

	return args
}

// buildOverrides constructs the MLC-LLM overrides string from config.
func (b *MLCLLMBackend) buildOverrides(spec *ModelSpec) string {
	var parts []string

	// Max number of sequences
	if maxSeq := spec.ConfigInt("maxNumSequence", 0); maxSeq > 0 {
		parts = append(parts, fmt.Sprintf("max_num_sequence=%d", maxSeq))
	}

	// Max total sequence length
	if maxLen := spec.ConfigInt("maxTotalSeqLength", 0); maxLen > 0 {
		parts = append(parts, fmt.Sprintf("max_total_seq_length=%d", maxLen))
	}

	// Prefill chunk size
	if chunkSize := spec.ConfigInt("prefillChunkSize", 0); chunkSize > 0 {
		parts = append(parts, fmt.Sprintf("prefill_chunk_size=%d", chunkSize))
	}

	// GPU memory utilization
	if memUtil := spec.ConfigString("gpuMemoryUtilization", ""); memUtil != "" {
		parts = append(parts, fmt.Sprintf("gpu_memory_utilization=%s", memUtil))
	}

	// Context window size
	if ctxSize := spec.ConfigInt("contextWindowSize", 0); ctxSize > 0 {
		parts = append(parts, fmt.Sprintf("context_window_size=%d", ctxSize))
	}

	// Sliding window size
	if slideSize := spec.ConfigInt("slidingWindowSize", 0); slideSize > 0 {
		parts = append(parts, fmt.Sprintf("sliding_window_size=%d", slideSize))
	}

	// Attention sink size
	if sinkSize := spec.ConfigInt("attentionSinkSize", 0); sinkSize > 0 {
		parts = append(parts, fmt.Sprintf("attention_sink_size=%d", sinkSize))
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, ";")
}

func (b *MLCLLMBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	var env []corev1.EnvVar

	// GPU memory size
	if spec.GPUMemoryBytes > 0 {
		env = append(env, corev1.EnvVar{
			Name:  "MLC_GPU_SIZE_BYTES",
			Value: fmt.Sprintf("%d", spec.GPUMemoryBytes),
		})
	} else {
		// Default to 5GB for Maxwell (6GB cards) and 23GB for RX 7900 XTX / similar.
		if spec.GPUVendor == GPUVendorNVIDIA && strings.HasPrefix(spec.GPUArch, "sm_5") {
			env = append(env, corev1.EnvVar{
				Name:  "MLC_GPU_SIZE_BYTES",
				Value: fmt.Sprintf("%d", DefaultMLCGPUBytesMaxwell),
			})
		} else {
			env = append(env, corev1.EnvVar{
				Name:  "MLC_GPU_SIZE_BYTES",
				Value: fmt.Sprintf("%d", DefaultMLCGPUBytesLarge),
			})
		}
	}

	// JIT policy
	jitPolicy := spec.ConfigString("jitPolicy", "")
	if jitPolicy == "" && spec.GPUVendor == GPUVendorNVIDIA && strings.HasPrefix(spec.GPUArch, "sm_5") {
		jitPolicy = "READONLY"
	}
	if jitPolicy != "" {
		env = append(env, corev1.EnvVar{
			Name:  "MLC_JIT_POLICY",
			Value: jitPolicy,
		})
	}

	// Add ROCm environment for AMD GPUs
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars(spec.GPUArch)...)

		env = append(env, DeviceIsolationEnvVars(spec)...)
	}

	return env
}

func (b *MLCLLMBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/v1/models", 8000, 10, 10, 5)
}

func (b *MLCLLMBackend) StartupTimeout() time.Duration {
	return 90 * time.Second
}
