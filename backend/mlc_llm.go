package backend

import (
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// MLCLLMBackend implements the Backend interface for MLC-LLM.
// MLC-LLM provides high-performance LLM inference with pre-compiled models.
type MLCLLMBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&MLCLLMBackend{})
}

func (b *MLCLLMBackend) Name() string {
	return "mlc-llm"
}

func (b *MLCLLMBackend) Aliases() []string {
	return []string{"mlc", "mlc_llm"}
}

func (b *MLCLLMBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	switch gpuVendor {
	case GPUVendorAMD:
		if img := os.Getenv("DEFAULT_MLC_LLM_IMAGE_AMD"); img != "" {
			return img
		}
		return "ghcr.io/mlc-ai/mlc-llm:rocm"
	case GPUVendorNVIDIA:
		// Check for Maxwell architecture (sm_52)
		if strings.HasPrefix(gpuArch, "sm_5") {
			if img := os.Getenv("DEFAULT_MLC_LLM_IMAGE_MAXWELL"); img != "" {
				return img
			}
			// Maxwell-specific image built with CUDA 11.8
			return "registry.harbor.lan/flexinfer/mlc-llm:cuda-maxwell-v7"
		}
		if img := os.Getenv("DEFAULT_MLC_LLM_IMAGE"); img != "" {
			return img
		}
		return "ghcr.io/mlc-ai/mlc-llm:cuda"
	default:
		if img := os.Getenv("DEFAULT_MLC_LLM_IMAGE"); img != "" {
			return img
		}
		return "ghcr.io/mlc-ai/mlc-llm:cuda"
	}
}

func (b *MLCLLMBackend) Port() int32 {
	return 8000
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
	libPath := spec.ConfigString("modelLibPath", "")
	if libPath == "" && strings.EqualFold(spec.ConfigString("jitPolicy", ""), "READONLY") {
		// When JIT is disabled (READONLY), MLC requires a pre-compiled model library.
		// Default to the conventional on-disk name when the model path is a mounted directory.
		if spec.GPUVendor == GPUVendorAMD && spec.GPUArch != "" && strings.HasPrefix(modelPath, "/") {
			libPath = fmt.Sprintf("%s/lib_rocm_%s.so", strings.TrimRight(modelPath, "/"), spec.GPUArch)
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
		// Default to 23GB for RX 7900 XTX / similar
		env = append(env, corev1.EnvVar{
			Name:  "MLC_GPU_SIZE_BYTES",
			Value: "24696061952", // ~23GB
		})
	}

	// JIT policy
	if jitPolicy := spec.ConfigString("jitPolicy", ""); jitPolicy != "" {
		env = append(env, corev1.EnvVar{
			Name:  "MLC_JIT_POLICY",
			Value: jitPolicy,
		})
	}

	// Add ROCm environment for AMD GPUs
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars()...)

		// Some setups expose multiple AMD GPUs (e.g., iGPU + dGPU) to the container.
		// Allow pinning the runtime to a specific device via config. Prefer setting
		// both variables to avoid partial isolation depending on the stack.
		hipVisible := spec.ConfigString("hipVisibleDevices", "")
		rocrVisible := spec.ConfigString("rocrVisibleDevices", "")
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
		if ordinal := spec.ConfigString("gpuDeviceOrdinal", ""); ordinal != "" {
			env = append(env, corev1.EnvVar{Name: "GPU_DEVICE_ORDINAL", Value: ordinal})
		}
	}

	return env
}

func (b *MLCLLMBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/v1/models", 8000, 10, 10, 5)
}

func (b *MLCLLMBackend) StartupTimeout() time.Duration {
	return 90 * time.Second
}
