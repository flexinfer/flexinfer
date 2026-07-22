package backend

import (
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// vllmOmniImageRules defines the image resolution precedence for vLLM-Omni.
//
// The gfx1100 arch entry is env-only (no built-in default) so it falls through
// to the AMD-generic image when no env override is set. The arch default now
// lives in deploy/gpuprofiles/gfx1100.yaml — callers that pass a GPUProfile
// through backend.ResolveBackendImage get the per-arch image from the profile,
// and only nodes without a profile fall back to this slice.
var vllmOmniImageRules = []ImageRule{
	// AMD gfx1100 arch-specific (env-only; profile owns the default)
	{Vendor: GPUVendorAMD, ArchPrefix: "gfx110", EnvVar: "DEFAULT_VLLM_OMNI_IMAGE_GFX1100"},
	// AMD generic
	{Vendor: GPUVendorAMD, EnvVar: "DEFAULT_VLLM_OMNI_IMAGE_AMD", Default: "registry.harbor.lan/flexinfer/vllm-omni:rocm-gfx1100"},
	// Global default
	{EnvVar: "DEFAULT_VLLM_OMNI_IMAGE", Default: "vllm/vllm-openai:latest"},
}

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
	return NameVLLMOmni
}

func (b *VLLMOmniBackend) Aliases() []string {
	return []string{"vllm-diffusion", "vllm_omni"}
}

func (b *VLLMOmniBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	return ResolveImage(vllmOmniImageRules, gpuVendor, gpuArch)
}

func (b *VLLMOmniBackend) Port() int32 {
	return PortVLLM
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

	if attentionBackend := spec.ConfigString("attentionBackend", ""); attentionBackend != "" {
		args = append(args, "--attention-backend", attentionBackend)
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

	// CPU offload removed in vLLM V1 (0.17.0+). Previously --cpu-offload-gb.

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

	// Prefix caching is ON by default in vLLM V1.
	// Newer argparse wiring uses BooleanOptionalAction, so the disable form is
	// --no-enable-prefix-caching rather than --no-prefix-caching.
	if spec.Config != nil {
		if _, ok := spec.Config["enablePrefixCaching"]; ok {
			if !spec.ConfigBool("enablePrefixCaching", true) {
				args = append(args, "--no-enable-prefix-caching")
			}
		}
		if _, ok := spec.Config["disableHybridKVCacheManager"]; ok {
			if spec.ConfigBool("disableHybridKVCacheManager", false) {
				args = append(args, "--disable-hybrid-kv-cache-manager")
			} else {
				args = append(args, "--no-disable-hybrid-kv-cache-manager")
			}
		}
	}

	// vLLM's OpenAI server omits usage.prompt_tokens_details.cached_tokens
	// unless launched with this flag (default false), independent of whether
	// prefix caching itself is on. See the matching block in vllm.go.
	args = appendVLLMBooleanOptionalArg(args, spec, "enablePromptTokensDetails", "--enable-prompt-tokens-details", "--no-enable-prompt-tokens-details")

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

	// Multimodal input limits (e.g., "image=4,audio=2")
	if mmLimit := spec.ConfigString("limitMmPerPrompt", ""); mmLimit != "" {
		args = append(args, "--limit-mm-per-prompt", mmLimit)
	}

	// Multimodal processor kwargs (JSON)
	if mmKwargs := spec.ConfigString("mmProcessorKwargs", ""); mmKwargs != "" {
		args = append(args, "--mm-processor-kwargs", mmKwargs)
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

		// vLLM 0.17.0+ is V1-only. VLLM_USE_V1 env var removed.
		// Only inject flash attention and AITER controls when explicitly configured.
		enableFA := spec.ConfigBool("enableFlashAttention", false)
		enableAiter := spec.ConfigBool("enableAiter", false)

		var vllmEnv []corev1.EnvVar
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
		{Name: "VLLM_CACHE_ROOT", Value: cacheMountPath + "/vllm"},
		{Name: "XDG_CACHE_HOME", Value: cacheMountPath + "/xdg"},
		{Name: "TORCHINDUCTOR_CACHE_DIR", Value: cacheMountPath + "/inductor"},
		{Name: "TRITON_CACHE_DIR", Value: cacheMountPath + "/triton"},
		{Name: "TORCH_HOME", Value: cacheMountPath + "/torch"},
	}
}
