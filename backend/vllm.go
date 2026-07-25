package backend

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// vllmImageRules defines the image resolution precedence for vLLM.
//
// Arch-specific rules are env-only (no built-in default) so they fall through
// to the AMD-generic image when no env override is set. The arch defaults now
// live in deploy/gpuprofiles/gfx1100.yaml and gfx906.yaml — callers that pass
// a GPUProfile through backend.ResolveBackendImage get the per-arch image
// from the profile, and only nodes without a profile fall back to this slice.
//
// Order: arch-specific (env-only) → vendor (env → default) → global.
var vllmImageRules = []ImageRule{
	// AMD arch-specific (env-only; profile owns the default)
	{Vendor: GPUVendorAMD, ArchPrefix: "gfx110", EnvVar: "DEFAULT_VLLM_IMAGE_GFX1100"},
	{Vendor: GPUVendorAMD, ArchPrefix: "gfx906", EnvVar: "DEFAULT_VLLM_IMAGE_GFX906"},
	// AMD generic
	{Vendor: GPUVendorAMD, EnvVar: "DEFAULT_VLLM_IMAGE_AMD", Default: "rocm/vllm:latest"},
	// Global default (NVIDIA, unknown, etc.)
	{EnvVar: "DEFAULT_VLLM_IMAGE", Default: "vllm/vllm-openai:latest"},
}

// VLLMBackend implements the Backend interface for vLLM.
// vLLM provides an OpenAI-compatible API for LLM inference.
type VLLMBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&VLLMBackend{})
}

func (b *VLLMBackend) Name() string {
	return NameVLLM
}

func (b *VLLMBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	return ResolveImage(vllmImageRules, gpuVendor, gpuArch)
}

func (b *VLLMBackend) Port() int32 {
	return PortVLLM
}

func (b *VLLMBackend) Args(spec *ModelSpec) []string {
	args := []string{
		"--host", "0.0.0.0",
		"--port", fmt.Sprintf("%d", b.Port()),
	}

	// Fail closed when vLLM detects a runtime environment mismatch. Keep this
	// optional because older images do not expose the BooleanOptionalAction.
	args = appendVLLMBooleanOptionalArg(args, spec, "failOnEnvironValidation", "--fail-on-environ-validation", "--no-fail-on-environ-validation")

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

	// Attention backend override. kvCacheCodec=turboquant promotes the managed
	// intent into the vLLM CUSTOM path unless the manifest explicitly overrides it.
	if attentionBackend := resolveVLLMAttentionBackend(spec); attentionBackend != "" {
		args = append(args, "--attention-backend", attentionBackend)
	}
	if gdnBackend := spec.ConfigString("gdnPrefillBackend", ""); gdnBackend != "" {
		args = append(args, "--gdn-prefill-backend", gdnBackend)
	}
	// Hybrid (mamba/GDN) prefix caching: linear-attention layers checkpoint
	// recurrent state at block boundaries. GDN models require "align" mode —
	// "all" is unsupported for GDN, and without a mode APC on hybrids stays
	// inert. Pairs with enablePrefixCaching on Qwen3.5/3.6-class lanes.
	if mambaCacheMode := spec.ConfigString("mambaCacheMode", ""); mambaCacheMode != "" {
		args = append(args, "--mamba-cache-mode", mambaCacheMode)
	}
	// Quantize the mamba/GDN SSM state cache (e.g. "float32" -> "bfloat16").
	// Smaller recurrent-state checkpoints mean more align-mode blocks stay
	// resident, which lifts hybrid prefix-cache hit rates.
	if ssmCacheDtype := spec.ConfigString("mambaSsmCacheDtype", ""); ssmCacheDtype != "" {
		args = append(args, "--mamba-ssm-cache-dtype", ssmCacheDtype)
	}

	// Trust remote code
	if spec.ConfigBool("trustRemoteCode", false) {
		args = append(args, "--trust-remote-code")
	}

	// Skip a multimodal model's vision encoder and serve only its language
	// model. This is particularly useful for Qwen3.5 on memory-constrained
	// cards: the official checkpoint stays intact on disk while vLLM avoids
	// loading and profiling the unused vision tower, leaving more VRAM for KV.
	if spec.ConfigBool("languageModelOnly", false) {
		args = append(args, "--language-model-only")
	}

	// Level-1 sleep mode: parks model weights in CPU RAM so a subsequent wake
	// skips the multi-minute cold reload. Inert on its own — nothing sleeps or
	// wakes the runtime without orchestration; drive it manually via the
	// runtime's /sleep and /wake_up endpoints. Groundwork for issue #68
	// (sleep-mode election integration).
	if spec.ConfigBool("enableSleepMode", false) {
		args = append(args, "--enable-sleep-mode")
	}

	// Max concurrent sequences
	if maxSeqs := spec.ConfigInt("maxNumSeqs", 0); maxSeqs > 0 {
		args = append(args, "--max-num-seqs", fmt.Sprintf("%d", maxSeqs))
	}

	// Max batched tokens for chunked prefill
	if maxBatched := spec.ConfigInt("maxNumBatchedTokens", 0); maxBatched > 0 {
		args = append(args, "--max-num-batched-tokens", fmt.Sprintf("%d", maxBatched))
	}
	args = appendVLLMBooleanOptionalArg(args, spec, "enableChunkedPrefill", "--enable-chunked-prefill", "--no-enable-chunked-prefill")
	if maxPartial := spec.ConfigInt("maxNumPartialPrefills", 0); maxPartial > 0 {
		args = append(args, "--max-num-partial-prefills", strconv.Itoa(maxPartial))
	}
	if maxLongPartial := spec.ConfigInt("maxLongPartialPrefills", 0); maxLongPartial > 0 {
		args = append(args, "--max-long-partial-prefills", strconv.Itoa(maxLongPartial))
	}
	if longThreshold := spec.ConfigInt("longPrefillTokenThreshold", 0); longThreshold > 0 {
		args = append(args, "--long-prefill-token-threshold", strconv.Itoa(longThreshold))
	}
	args = appendVLLMBooleanOptionalArg(args, spec, "schedulerReserveFullISL", "--scheduler-reserve-full-isl", "--no-scheduler-reserve-full-isl")
	// Scheduler policy ("fcfs" or "priority"). "priority" honors per-request
	// priority values, letting background traffic (e.g. dream cycles) yield
	// to live turns on the same lane.
	if schedulingPolicy := spec.ConfigString("schedulingPolicy", ""); schedulingPolicy != "" {
		args = append(args, "--scheduling-policy", schedulingPolicy)
	}

	// Disable model sliding-window attention. This is useful for ROCm backends
	// where the selected attention kernel cannot serve SWA-capable models.
	if spec.ConfigBool("disableSlidingWindow", false) {
		args = append(args, "--disable-sliding-window")
	}

	// Enforce eager mode (disable torch.compile and HIPGraph/CUDAGraph)
	if spec.ConfigBool("enforceEager", false) {
		args = append(args, "--enforce-eager")
	}

	// CUDA/HIP graph and torch.compile controls. These are useful on slower
	// startup nodes where limiting capture sizes can reduce compile pressure.
	if sizes := configValuesAsArgs(spec, "cudagraphCaptureSizes"); len(sizes) > 0 {
		args = append(args, "--cudagraph-capture-sizes")
		args = append(args, sizes...)
	}
	if maxSize := configValueAsArg(spec, "maxCudagraphCaptureSize"); maxSize != "" {
		args = append(args, "--max-cudagraph-capture-size", maxSize)
	}
	if compilation := configValueAsArg(spec, "compilationConfig"); compilation != "" {
		args = append(args, "--compilation-config", compilation)
	}
	args = appendVLLMBooleanOptionalArg(args, spec, "cudagraphMetrics", "--cudagraph-metrics", "--no-cudagraph-metrics")

	// CPU weight offload — moves part of model weights to CPU-pinned memory.
	// Required when model weights exceed single-GPU VRAM (e.g. 27B GPTQ on 24 GB).
	if offloadGB := spec.ConfigString("cpuOffloadGb", ""); offloadGB != "" {
		args = append(args, "--cpu-offload-gb", offloadGB)
	}
	if offloadBackend := spec.ConfigString("offloadBackend", ""); offloadBackend != "" {
		args = append(args, "--offload-backend", offloadBackend)
	}

	// Override KV cache blocks — bypasses the profiling step that measures
	// available GPU memory. Useful when model weights consume nearly all VRAM
	// and the profiling allocation itself causes OOM.
	if blocks := spec.ConfigInt("numGpuBlocksOverride", 0); blocks > 0 {
		args = append(args, "--num-gpu-blocks-override", fmt.Sprintf("%d", blocks))
	}

	// Quantization method (awq, gptq, fp8, gguf, etc.)
	if quant := spec.ConfigString("quantization", ""); quant != "" {
		args = append(args, "--quantization", quant)
	}

	// Tokenizer override — required for GGUF models which lack HF tokenizer files.
	// Points to the base HF repo (e.g., "Qwen/Qwen3.5-35B-A3B").
	if tok := spec.ConfigString("tokenizer", ""); tok != "" {
		args = append(args, "--tokenizer", tok)
	}

	// Served model name — controls the model name in /v1/models and routing
	if name := spec.ConfigString("servedModelName", ""); name != "" {
		args = append(args, "--served-model-name", name)
	}

	// Task is intentionally NOT emitted as a CLI arg.
	// vLLM 0.17.0+rocm700's api_server.py removed the top-level --task flag;
	// it auto-resolves from the model's config.json architectures (e.g.
	// WhisperForConditionalGeneration → transcription). The spec field is
	// kept for backward compatibility but ignored here; if a future vLLM
	// build needs explicit task selection, route it via
	// --override-model-config '{"task": "..."}'.

	// KV cache dtype (auto, fp8_e5m2). FP8 requires MI300X+ hardware.
	if kvDtype := spec.ConfigString("kvCacheDtype", ""); kvDtype != "" {
		args = append(args, "--kv-cache-dtype", kvDtype)
	}
	args = appendVLLMBooleanOptionalArg(args, spec, "calculateKvScales", "--calculate-kv-scales", "--no-calculate-kv-scales")
	args = appendVLLMBooleanOptionalArg(args, spec, "kvCacheMetrics", "--kv-cache-metrics", "--no-kv-cache-metrics")
	if sample := configValueAsArg(spec, "kvCacheMetricsSample"); sample != "" {
		args = append(args, "--kv-cache-metrics-sample", sample)
	}

	// HF config overrides — passed through verbatim to vLLM as --hf-overrides
	// (a JSON object). Lets a Model remap fields the served artifact's
	// config.json declares incorrectly without rewriting the artifact. Primary
	// use: remap an architectures name that a newer vLLM registers under a
	// different key — e.g. Qwen3.5/3.6-MoE GPTQ artifacts declare
	// "Qwen3_5MoeForCausalLM", which vLLM 0.19.x does not register, but it does
	// register "Qwen3NextForCausalLM" and "Qwen3_5MoeForConditionalGeneration".
	if hfOverrides := spec.ConfigString("hfOverrides", ""); hfOverrides != "" {
		args = append(args, "--hf-overrides", hfOverrides)
	}

	// Prefix caching: emit the explicit form for BOTH values. "V1 defaults ON"
	// is false for hybrid-GDN lanes — vLLM 0.23 boots them with
	// enable_prefix_caching=False and then silently resets mamba-cache-mode to
	// "none" (config.py:390), so relying on the default made
	// enablePrefixCaching:true a no-op on exactly the lanes that set it.
	args = appendVLLMBooleanOptionalArg(args, spec, "enablePrefixCaching", "--enable-prefix-caching", "--no-enable-prefix-caching")
	if spec.Config != nil {
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
	// prefix caching itself is on. The proxy's X-Flexinfer-Cached-Tokens
	// response header reads that usage field, so a lane that wants the header
	// must set enablePromptTokensDetails: true alongside prefix caching.
	args = appendVLLMBooleanOptionalArg(args, spec, "enablePromptTokensDetails", "--enable-prompt-tokens-details", "--no-enable-prompt-tokens-details")

	// Tool calling support
	if spec.ConfigBool("enableToolCalling", false) {
		args = append(args, "--enable-auto-tool-choice")
		parser := spec.ConfigString("toolCallParser", "hermes")
		args = append(args, "--tool-call-parser", parser)
	}

	// Reasoning parser for models with <think> blocks (e.g., DeepSeek-R1)
	if rp := spec.ConfigString("reasoningParser", ""); rp != "" {
		args = append(args, "--reasoning-parser", rp)
	}

	// Default keyword arguments for the model's chat-template renderer. vLLM
	// merges these with request-level chat_template_kwargs, with the request
	// taking precedence. Accept either an already-encoded string or a native
	// YAML/JSON map so manifests can express model-family defaults cleanly.
	if kwargs := configValueAsArg(spec, "defaultChatTemplateKwargs"); kwargs != "" {
		args = append(args, "--default-chat-template-kwargs", kwargs)
	}

	// Disable stats logging to reduce log noise in production
	if spec.ConfigBool("disableLogStats", false) {
		args = append(args, "--disable-log-stats")
	}

	// Speculative decoding — passed as opaque JSON to --speculative-config.
	// Supports draft_model, eagle, eagle3, mtp, ngram, suffix, mlp_speculator.
	if specConfig := spec.ConfigString("speculativeConfig", ""); specConfig != "" {
		args = append(args, "--speculative-config", specConfig)
	}

	// Multimodal input limits (e.g., "image=4,audio=2")
	if mmLimit := spec.ConfigString("limitMmPerPrompt", ""); mmLimit != "" {
		args = append(args, "--limit-mm-per-prompt", mmLimit)
	}

	// Multimodal processor kwargs (JSON)
	if mmKwargs := spec.ConfigString("mmProcessorKwargs", ""); mmKwargs != "" {
		args = append(args, "--mm-processor-kwargs", mmKwargs)
	}

	// LoRA: the controller sets config.enableLora (plus maxLoras/maxLoraRank)
	// when the model has LoRAAdapter CRs. Emitting the flags here means both the
	// dedicated Deployment path and the runtime-managed load path (both call
	// Args) launch vLLM with identical LoRA support. maxLoraRank must cover the
	// largest adapter rank or a higher-rank adapter is rejected at load time.
	if spec.ConfigBool("enableLora", false) {
		args = append(args, "--enable-lora")
		maxLoras := spec.ConfigInt("maxLoras", 1)
		if maxLoras < 1 {
			maxLoras = 1
		}
		args = append(args, "--max-loras", fmt.Sprintf("%d", maxLoras))
		if r := normalizeLoRARank(spec.ConfigInt("maxLoraRank", 0)); r > 0 {
			args = append(args, "--max-lora-rank", fmt.Sprintf("%d", r))
		}
	}

	return args
}

func appendVLLMBooleanOptionalArg(args []string, spec *ModelSpec, key, enabledArg, disabledArg string) []string {
	if spec == nil || spec.Config == nil {
		return args
	}
	if _, ok := spec.Config[key]; !ok {
		return args
	}
	if spec.ConfigBool(key, false) {
		return append(args, enabledArg)
	}
	return append(args, disabledArg)
}

func (b *VLLMBackend) Env(spec *ModelSpec) []corev1.EnvVar {
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

		var vllmEnv []corev1.EnvVar
		if enableFA {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_USE_TRITON_FLASH_ATTN", Value: "1"})
		}

		aiterDisabled := false

		// ROCm kernel overrides are presence-aware so manifests can force
		// stability fallbacks instead of relying on vLLM's moving defaults.
		if value, ok := rocmBoolOverride(spec, "rocmUseAiter"); ok {
			if value == "0" {
				vllmEnv = append(vllmEnv, rocmAiterDisabledEnvVars()...)
				aiterDisabled = true
			} else {
				vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_ROCM_USE_AITER", Value: "1"})
			}
		} else if value, ok := rocmBoolOverride(spec, "disableAiter"); ok {
			if value == "1" {
				vllmEnv = append(vllmEnv, rocmAiterDisabledEnvVars()...)
				aiterDisabled = true
			} else {
				vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_ROCM_USE_AITER", Value: "1"})
			}
		} else {
			// AITER: only applicable to gfx110x (RDNA3) and gfx942 (MI300X/CDNA3)
			supportsAiter := strings.HasPrefix(spec.GPUArch, "gfx110") ||
				strings.HasPrefix(spec.GPUArch, "gfx942")
			if spec.ConfigBool("enableAiter", false) && supportsAiter {
				vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_ROCM_USE_AITER", Value: "1"})
			}
		}

		if value, ok := rocmBoolOverride(spec, "rocmCustomPagedAttn"); ok && !aiterDisabled {
			vllmEnv = append(vllmEnv, corev1.EnvVar{Name: "VLLM_ROCM_USE_AITER_PAGED_ATTN", Value: value})
		}

		env = append(env, vllmEnv...)

		env = append(env, DeviceIsolationEnvVars(spec)...)
	}

	// VLLM_DISABLED_KERNELS — comma-separated list of quantization kernels to skip.
	// Forces vLLM to fall back to alternative implementations (e.g., Triton).
	// Common use: disable ExllamaLinearKernel to avoid its fixed 288 MiB scratch
	// buffer on VRAM-constrained GPUs running compressed-tensors WNA16 models.
	if dk := spec.ConfigString("disabledKernels", ""); dk != "" {
		env = append(env, corev1.EnvVar{
			Name:  "VLLM_DISABLED_KERNELS",
			Value: dk,
		})
	}

	// PYTORCH_CUDA_ALLOC_CONF — PyTorch CUDA memory allocator configuration.
	// Controls memory fragmentation strategy. Key setting: expandable_segments:True
	// reduces fragmentation on VRAM-constrained GPUs (e.g., 24 GB running 22+ GiB
	// models). Without this, PyTorch may fail to allocate KV cache blocks even when
	// enough total free memory exists.
	if allocConf := spec.ConfigString("pytorchCudaAllocConf", ""); allocConf != "" {
		env = append(env, corev1.EnvVar{
			Name:  "PYTORCH_CUDA_ALLOC_CONF",
			Value: allocConf,
		})
	}

	if strings.EqualFold(spec.ConfigString("kvCacheCodec", ""), "turboquant") {
		env = append(env,
			corev1.EnvVar{Name: "FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC", Value: "turboquant"},
			corev1.EnvVar{Name: "FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC_STATUS", Value: "plugin"},
		)
	}

	// LoRA hot-load: vLLM only exposes /v1/load_lora_adapter (the endpoint the
	// LoRA controller POSTs to) when this is set. The controller flips
	// config.enableLora when the model has LoRAAdapter CRs; both serving paths
	// call Env, so this covers the Deployment and runtime-managed paths alike.
	if spec.ConfigBool("enableLora", false) {
		env = append(env, corev1.EnvVar{Name: "VLLM_ALLOW_RUNTIME_LORA_UPDATING", Value: "True"})
	}

	return env
}

func resolveVLLMAttentionBackend(spec *ModelSpec) string {
	if spec == nil {
		return ""
	}
	if attentionBackend := spec.ConfigString("attentionBackend", ""); attentionBackend != "" {
		return attentionBackend
	}
	if strings.EqualFold(spec.ConfigString("kvCacheCodec", ""), "turboquant") {
		return "CUSTOM"
	}
	return ""
}

func (b *VLLMBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/health", 8000, 10, 10, 5)
}

// StartupProbe gives vLLM cold-load up to b.StartupTimeout() before kubelet
// starts running the readiness probe. Added 2026-05-15 after the V1 promotion
// rollback (!373): vLLM 0.19+ on ROCm gfx1100 has a longer cold-load than V0
// (AITER JIT, Triton kernel compile, vLLM custom fusions, model weight load)
// and Flux-driven Deployment recreates can put pods through that full cold
// path. The startup probe absorbs that variance; once it passes, the
// readiness probe handles steady-state health.
//
// See .loom/slice3-v1-sandbox-rms-norm-falsified-2026-05-15.md (Z1 option).
func (b *VLLMBackend) StartupProbe() *corev1.Probe {
	return HTTPStartupProbe("/health", 8000, b.StartupTimeout())
}

func (b *VLLMBackend) StartupProbeForSpec(spec *ModelSpec) *corev1.Probe {
	return HTTPStartupProbe("/health", 8000, b.startupTimeoutForSpec(spec))
}

func (b *VLLMBackend) StartupTimeout() time.Duration {
	return 300 * time.Second
}

func (b *VLLMBackend) startupTimeoutForSpec(spec *ModelSpec) time.Duration {
	timeout := b.StartupTimeout()
	if spec == nil {
		return timeout
	}
	if configured := spec.ConfigDuration("startupTimeout", 0); configured > 0 {
		return configured
	}
	if configured := spec.ConfigDuration("startupTimeoutSeconds", 0); configured > 0 {
		return configured
	}
	if spec.StartupTimeout > timeout {
		return spec.StartupTimeout
	}
	return timeout
}

// KVCacheArgs returns CLI arguments for KV-cache tuning.
func (b *VLLMBackend) KVCacheArgs(maxBlockSize *int, swapSpaceGiB *float64) []string {
	var args []string
	if maxBlockSize != nil {
		args = append(args, "--block-size", fmt.Sprintf("%d", *maxBlockSize))
	}
	if swapSpaceGiB != nil {
		args = append(args, "--swap-space", fmt.Sprintf("%.1f", *swapSpaceGiB))
	}
	return args
}

// SupportsSwapSpace returns false — vLLM V1 (0.17.0+) removed CPU↔GPU KV cache swapping.
func (b *VLLMBackend) SupportsSwapSpace() bool {
	return false
}

// SupportsLoRA returns true — vLLM supports hot-loading LoRA adapters.
func (b *VLLMBackend) SupportsLoRA() bool {
	return true
}

// normalizeLoRARank rounds a requested LoRA rank up to the nearest tier vLLM
// accepts for --max-lora-rank. vLLM only allows {8,16,32,64,128,256}; a rank at
// or below the default (16) returns 0 so the flag is omitted and vLLM keeps its
// default. Larger ranks round up to the next allowed tier.
func normalizeLoRARank(rank int) int {
	if rank <= 16 {
		return 0
	}
	for _, allowed := range []int{32, 64, 128, 256} {
		if rank <= allowed {
			return allowed
		}
	}
	return 256
}

// LoadLoRAEndpoint returns the vLLM API path for loading a LoRA adapter.
func (b *VLLMBackend) LoadLoRAEndpoint() string {
	return "/v1/load_lora_adapter"
}

// UnloadLoRAEndpoint returns the vLLM API path for unloading a LoRA adapter.
func (b *VLLMBackend) UnloadLoRAEndpoint() string {
	return "/v1/unload_lora_adapter"
}

// SupportedQuantFormats returns AWQ, GPTQ, FP8, and GGUF — the formats vLLM natively loads.
func (b *VLLMBackend) SupportedQuantFormats() []string {
	return []string{"AWQ", "GPTQ", "FP8", "GGUF"}
}

// CompilationCacheEnvVars implements CompilationCacheConfigurer.
// Redirects MIOpen, TorchInductor, and Triton caches for vLLM on ROCm.
func (b *VLLMBackend) CompilationCacheEnvVars(cacheMountPath string) []corev1.EnvVar {
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

func rocmBoolOverride(spec *ModelSpec, key string) (string, bool) {
	if spec == nil || spec.Config == nil {
		return "", false
	}
	raw, ok := spec.Config[key]
	if !ok {
		return "", false
	}

	switch value := raw.(type) {
	case bool:
		if value {
			return "1", true
		}
		return "0", true
	case string:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return "", false
		}
		if parsed {
			return "1", true
		}
		return "0", true
	default:
		return "", false
	}
}

func configValueAsArg(spec *ModelSpec, key string) string {
	if spec == nil || spec.Config == nil {
		return ""
	}
	raw, ok := spec.Config[key]
	if !ok || raw == nil {
		return ""
	}
	if value, ok := raw.(string); ok {
		return strings.TrimSpace(value)
	}
	switch raw.(type) {
	case map[string]any, []any, []int, []int32, []int64, []float64, []string:
		encoded, err := json.Marshal(raw)
		if err != nil {
			return ""
		}
		return string(encoded)
	default:
		return fmt.Sprint(raw)
	}
}

// configValuesAsArgs preserves list values as separate CLI tokens. This is
// required for argparse options that use nargs, such as vLLM's
// --cudagraph-capture-sizes. Scalar values remain a single token for backward
// compatibility with existing one-size model configurations.
func configValuesAsArgs(spec *ModelSpec, key string) []string {
	if spec == nil || spec.Config == nil {
		return nil
	}
	raw, ok := spec.Config[key]
	if !ok || raw == nil {
		return nil
	}

	value := reflect.ValueOf(raw)
	if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
		if arg := configValueAsArg(spec, key); arg != "" {
			return []string{arg}
		}
		return nil
	}

	args := make([]string, 0, value.Len())
	for i := 0; i < value.Len(); i++ {
		arg := strings.TrimSpace(fmt.Sprint(value.Index(i).Interface()))
		if arg != "" {
			args = append(args, arg)
		}
	}
	return args
}

func rocmAiterDisabledEnvVars() []corev1.EnvVar {
	names := []string{
		"VLLM_ROCM_USE_AITER",
		"VLLM_ROCM_USE_AITER_PAGED_ATTN",
		"VLLM_ROCM_USE_AITER_LINEAR",
		"VLLM_ROCM_USE_AITER_MOE",
		"VLLM_ROCM_USE_AITER_RMSNORM",
		"VLLM_ROCM_USE_AITER_MLA",
		"VLLM_ROCM_USE_AITER_MHA",
		"VLLM_ROCM_USE_AITER_FP4_ASM_GEMM",
		"VLLM_ROCM_USE_AITER_TRITON_ROPE",
		"VLLM_ROCM_USE_AITER_FP8BMM",
		"VLLM_ROCM_USE_AITER_FP4BMM",
		"VLLM_ROCM_USE_AITER_UNIFIED_ATTENTION",
		"VLLM_ROCM_USE_AITER_FUSION_SHARED_EXPERTS",
		"VLLM_ROCM_USE_AITER_TRITON_GEMM",
	}
	env := make([]corev1.EnvVar, 0, len(names))
	for _, name := range names {
		env = append(env, corev1.EnvVar{Name: name, Value: "0"})
	}
	return env
}
