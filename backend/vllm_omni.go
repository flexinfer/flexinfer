package backend

import (
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// VLLMOmniBackend implements the Backend interface for vLLM-Omni.
// vLLM-Omni extends vLLM with multimodal generation (image, audio, video).
// Entrypoint: `vllm serve` — the Dockerfile sets this as ENTRYPOINT.
type VLLMOmniBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&VLLMOmniBackend{})
}

func (b *VLLMOmniBackend) Name() string {
	return "vllm-omni"
}

func (b *VLLMOmniBackend) Aliases() []string {
	return []string{"vllm-diffusion", "vllm_omni"}
}

func (b *VLLMOmniBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	switch gpuVendor {
	case GPUVendorAMD:
		if strings.HasPrefix(gpuArch, "gfx110") {
			if img := os.Getenv("DEFAULT_VLLM_OMNI_IMAGE_GFX1100"); img != "" {
				return img
			}
			return "registry.harbor.lan/flexinfer/vllm-omni:rocm-gfx1100"
		}
		if img := os.Getenv("DEFAULT_VLLM_OMNI_IMAGE_AMD"); img != "" {
			return img
		}
		return "registry.harbor.lan/flexinfer/vllm-omni:rocm-gfx1100"
	default:
		if img := os.Getenv("DEFAULT_VLLM_OMNI_IMAGE"); img != "" {
			return img
		}
		return "vllm/vllm-openai:latest"
	}
}

func (b *VLLMOmniBackend) Port() int32 {
	return 8000
}

func (b *VLLMOmniBackend) Args(spec *ModelSpec) []string {
	args := []string{
		"--host", "0.0.0.0",
		"--port", fmt.Sprintf("%d", b.Port()),
	}

	// Model path
	if spec.ModelPath != "" {
		args = append(args, "--model", spec.ModelPath)
	} else if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}

	// Dtype
	if dtype := spec.ConfigString("dtype", ""); dtype != "" {
		args = append(args, "--dtype", dtype)
	}

	// Max model length
	if maxLen := spec.ConfigInt("maxModelLen", 0); maxLen > 0 {
		args = append(args, "--max-model-len", fmt.Sprintf("%d", maxLen))
	}

	// GPU memory utilization
	if memUtil := spec.ConfigString("gpuMemoryUtilization", ""); memUtil != "" {
		args = append(args, "--gpu-memory-utilization", memUtil)
	}

	// Trust remote code (often needed for omni models)
	if spec.ConfigBool("trustRemoteCode", false) {
		args = append(args, "--trust-remote-code")
	}

	// Max concurrent sequences
	if maxSeqs := spec.ConfigInt("maxNumSeqs", 0); maxSeqs > 0 {
		args = append(args, "--max-num-seqs", fmt.Sprintf("%d", maxSeqs))
	}

	// Max batched tokens
	if maxBatched := spec.ConfigInt("maxNumBatchedTokens", 0); maxBatched > 0 {
		args = append(args, "--max-num-batched-tokens", fmt.Sprintf("%d", maxBatched))
	}

	// Enforce eager mode
	if spec.ConfigBool("enforceEager", false) {
		args = append(args, "--enforce-eager")
	}

	// CPU offload — move part of model weights to CPU to free VRAM for KV cache
	if cpuOffload := spec.ConfigInt("cpuOffloadGb", 0); cpuOffload > 0 {
		args = append(args, "--cpu-offload-gb", fmt.Sprintf("%d", cpuOffload))
	}

	// Override KV cache blocks — bypasses the profiling step
	if blocks := spec.ConfigInt("numGpuBlocksOverride", 0); blocks > 0 {
		args = append(args, "--num-gpu-blocks-override", fmt.Sprintf("%d", blocks))
	}

	// Quantization method
	if quant := spec.ConfigString("quantization", ""); quant != "" {
		args = append(args, "--quantization", quant)
	}

	// Tokenizer override
	if tok := spec.ConfigString("tokenizer", ""); tok != "" {
		args = append(args, "--tokenizer", tok)
	}

	// Served model name
	if name := spec.ConfigString("servedModelName", ""); name != "" {
		args = append(args, "--served-model-name", name)
	}

	// KV cache dtype
	if kvDtype := spec.ConfigString("kvCacheDtype", ""); kvDtype != "" {
		args = append(args, "--kv-cache-dtype", kvDtype)
	}

	// Prefix caching
	if spec.Config != nil {
		if _, ok := spec.Config["enablePrefixCaching"]; ok {
			if spec.ConfigBool("enablePrefixCaching", true) {
				args = append(args, "--enable-prefix-caching")
			} else {
				args = append(args, "--no-prefix-caching")
			}
		}
	}

	// Tool calling support
	if spec.ConfigBool("enableToolCalling", false) {
		args = append(args, "--enable-auto-tool-choice")
		parser := spec.ConfigString("toolCallParser", "hermes")
		args = append(args, "--tool-call-parser", parser)
	}

	// Reasoning parser
	if rp := spec.ConfigString("reasoningParser", ""); rp != "" {
		args = append(args, "--reasoning-parser", rp)
	}

	// Disable stats logging
	if spec.ConfigBool("disableLogStats", false) {
		args = append(args, "--disable-log-stats")
	}

	return args
}

func (b *VLLMOmniBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	var env []corev1.EnvVar

	// CUDA device ordering
	env = append(env, corev1.EnvVar{
		Name:  "CUDA_DEVICE_ORDER",
		Value: "PCI_BUS_ID",
	})

	// Add ROCm environment for AMD GPUs
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars(spec.GPUArch)...)

		engineVersion := spec.ConfigString("vllmEngineVersion", "")
		enableFA := spec.ConfigBool("enableFlashAttention", false)
		enableAiter := spec.ConfigBool("enableAiter", false)

		var vllmEnv []corev1.EnvVar
		if engineVersion == "v1" {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_USE_V1", Value: "1"})
		} else if engineVersion == "v0" {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_USE_V1", Value: "0"})
		}
		if enableFA {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_USE_TRITON_FLASH_ATTN", Value: "1"})
		}

		supportsAiter := strings.HasPrefix(spec.GPUArch, "gfx110") ||
			strings.HasPrefix(spec.GPUArch, "gfx942")
		if enableAiter && supportsAiter {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_ROCM_USE_AITER", Value: "1"})
		}

		env = append(env, vllmEnv...)
		env = append(env, DeviceIsolationEnvVars(spec)...)
	}

	// VLLM_DISABLED_KERNELS
	if dk := spec.ConfigString("disabledKernels", ""); dk != "" {
		env = append(env, corev1.EnvVar{
			Name:  "VLLM_DISABLED_KERNELS",
			Value: dk,
		})
	}

	// PYTORCH_CUDA_ALLOC_CONF
	if allocConf := spec.ConfigString("pytorchCudaAllocConf", ""); allocConf != "" {
		env = append(env, corev1.EnvVar{
			Name:  "PYTORCH_CUDA_ALLOC_CONF",
			Value: allocConf,
		})
	}

	return env
}

func (b *VLLMOmniBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/health", 8000, 0, 5, 3)
}

func (b *VLLMOmniBackend) StartupProbe() *corev1.Probe {
	return HTTPStartupProbe("/health", 8000, b.StartupTimeout())
}

func (b *VLLMOmniBackend) StartupTimeout() time.Duration {
	return 300 * time.Second
}

// IsImageGeneration returns true for vLLM-Omni.
func (b *VLLMOmniBackend) IsImageGeneration() bool {
	return true
}

// DefaultIdleTimeout returns a longer timeout for multimodal generation.
func (b *VLLMOmniBackend) DefaultIdleTimeout() time.Duration {
	return 10 * time.Minute
}

// SupportedQuantFormats returns AWQ, GPTQ, and FP8 — inherited from vLLM.
func (b *VLLMOmniBackend) SupportedQuantFormats() []string {
	return []string{"AWQ", "GPTQ", "FP8"}
}

// CompilationCacheEnvVars implements CompilationCacheConfigurer.
func (b *VLLMOmniBackend) CompilationCacheEnvVars(cacheMountPath string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "MIOPEN_CUSTOM_CACHE_DIR", Value: cacheMountPath + "/miopen"},
		{Name: "MIOPEN_USER_DB_PATH", Value: cacheMountPath + "/miopen/user.db"},
		{Name: "TORCHINDUCTOR_CACHE_DIR", Value: cacheMountPath + "/inductor"},
		{Name: "TRITON_CACHE_DIR", Value: cacheMountPath + "/triton"},
		{Name: "TORCH_HOME", Value: cacheMountPath + "/torch"},
	}
}
