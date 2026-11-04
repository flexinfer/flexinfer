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
	// From 0.33.0 the flag is a deprecated no-op (Mamba prefix caching follows
	// --enable-prefix-caching, vLLM #60043) and 0.34 removes it (#64838), so a
	// lane that declares vllmVersion 0.33 or later does not render it.
	if mambaCacheMode := spec.ConfigString("mambaCacheMode", ""); mambaCacheMode != "" && !vllmVersionAtLeast(spec, vllmMambaCacheModeIgnoredSince) {
		args = append(args, "--mamba-cache-mode", mambaCacheMode)
	}
	// Prefix-cache hit granularity in tokens (vLLM 0.32+). It may be finer
	// than a hybrid model's physical block as long as every KV cache group's
	// block size divides by it, so hits can land inside a block.
	if unit := spec.ConfigInt("prefixMatchUnit", 0); unit > 0 {
		args = append(args, "--prefix-match-unit", strconv.Itoa(unit))
	}
	// Also checkpoint Mamba state where an MTP/EAGLE sibling resumes, not only
	// at the prompt tail (vLLM 0.32+). Needs prefix caching (Mamba "align"
	// mode), MTP or EAGLE on the Mamba group and a prefixMatchUnit below the
	// Mamba block size.
	args = appendVLLMBooleanOptionalArg(args, spec, "enableMambaSharedPrefixCheckpoint", "--enable-mamba-shared-prefix-checkpoint", "--no-enable-mamba-shared-prefix-checkpoint")
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
	// Parameter-name globs a level-2 sleep keeps resident (vLLM 0.32+), e.g.
	// ["draft.*"] for an MTP head. The flag takes one token per glob.
	if names := configValuesAsArgs(spec, "sleepPreserveParameterNames"); len(names) > 0 {
		args = append(args, "--sleep-preserve-parameter-names")
		args = append(args, names...)
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
	// maxNumPartialPrefills and maxLongPartialPrefills are refused by
	// ValidateVLLMConfig: their flags exist in no vLLM release we serve.
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
	// Kernel selection and tuning (vLLM's KernelConfig), e.g.
	// {"enable_rocm_segmented_attn_autotune": true} for ROCM_SEGMENTED_ATTN.
	if kernel := configValueAsArg(spec, "kernelConfig"); kernel != "" {
		args = append(args, "--kernel-config", kernel)
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
	// calculateKvScales: emit only the enabling flag. vLLM 0.28 removed the
	// option (PR #49389; it was deprecated and inert for hybrid-GDN lanes since
	// 0.19, see issue #69), so `--no-calculate-kv-scales` fails argument
	// parsing on every runtime from 0.28 on while `false` is the default on
	// every runtime we have ever served. A lane that sets `true` still gets the
	// flag so the intent fails loudly on a runtime that no longer has it.
	if spec.ConfigBool("calculateKvScales", false) {
		args = append(args, "--calculate-kv-scales")
	}
	args = appendVLLMBooleanOptionalArg(args, spec, "kvCacheMetrics", "--kv-cache-metrics", "--no-kv-cache-metrics")
	if sample := configValueAsArg(spec, "kvCacheMetricsSample"); sample != "" {
		args = append(args, "--kv-cache-metrics-sample", sample)
	}
	// KV connector (e.g. offloading), passed through as a JSON object. Accept
	// an encoded string or a native YAML/JSON map. From 0.33 a connector also
	// keeps the legacy prefix-cache block hashes (no MTP lookahead hashes).
	if kvTransfer := configValueAsArg(spec, "kvTransferConfig"); kvTransfer != "" {
		args = append(args, "--kv-transfer-config", kvTransfer)
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

	// Certified structured outputs (D4). vLLM serves response_format and
	// structured_outputs on every lane with backend auto by default; pinning
	// the backend here binds the lane to the envelope its GPUProfile
	// certificate proved, so a runtime upgrade cannot swap the backend under
	// a certified lane. Set only by the controller (see ModelSpec).
	if soBackend := strings.TrimSpace(spec.StructuredOutputsBackend); soBackend != "" {
		raw, _ := json.Marshal(struct {
			Backend string `json:"backend"`
		}{Backend: soBackend})
		args = append(args, "--structured-outputs-config", string(raw))
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
			// vLLM uses AITER only on CDNA 3 or newer (gfx942/gfx950:
			// is_aiter_found_and_supported checks the CDNA version), so on
			// gfx110x every VLLM_ROCM_USE_AITER* variable is a no-op and
			// enableAiter, disableAiter and rocmUseAiter change nothing there.
			// gfx110 stays in this check only so existing gfx1100 lanes keep
			// rendering the env they render today.
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

	// Sleep mode control plane: --enable-sleep-mode (Args) only teaches the
	// engine to support sleeping. The HTTP verbs that actually drive it --
	// POST /sleep, POST /wake_up, GET /is_sleeping -- live in vLLM's dev API
	// router, which api_server.py registers only when VLLM_SERVER_DEV_MODE is
	// set (verified against vLLM 0.23.0: vllm/entrypoints/serve/dev/sleep/
	// api_router.py, reachable solely through register_vllm_dev_api_routers).
	// Without this the flag is unusable -- every sleep/wake call 404s. Same
	// shape as the enableLora -> VLLM_ALLOW_RUNTIME_LORA_UPDATING pairing
	// above. Scoped to lanes that opt into enableSleepMode so no other lane
	// gains the dev endpoints.
	if spec.ConfigBool("enableSleepMode", false) {
		env = append(env, corev1.EnvVar{Name: "VLLM_SERVER_DEV_MODE", Value: "1"})
	}

	return withoutRemovedVLLMEnv(spec, env)
}

// vllmRelease is a vLLM release number, major.minor.patch.
type vllmRelease struct{ major, minor, patch int }

func (v vllmRelease) atLeast(o vllmRelease) bool {
	if v.major != o.major {
		return v.major > o.major
	}
	if v.minor != o.minor {
		return v.minor > o.minor
	}
	return v.patch >= o.patch
}

func (v vllmRelease) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

// vllmMambaCacheModeIgnoredSince is the release from which --mamba-cache-mode
// is a deprecated no-op.
var vllmMambaCacheModeIgnoredSince = vllmRelease{0, 33, 0}

// vllmEnvRemovedSince maps VLLM_* variables the backend renders to the first
// release whose vllm/envs.py does not define them. With
// --fail-on-environ-validation, Platform.validate_environ refuses to start on
// any VLLM_* variable that envs.py does not define. The 0.32.0 entries are
// absent from 0.32.0 and every later release; when they went is not tracked,
// so a lane on an older declared release keeps them.
var vllmEnvRemovedSince = map[string]vllmRelease{
	"VLLM_ROCM_USE_AITER_PAGED_ATTN":   {0, 32, 0},
	"VLLM_USE_TRITON_FLASH_ATTN":       {0, 32, 0},
	"VLLM_ROCM_USE_AITER_FP4_ASM_GEMM": {0, 33, 0}, // vLLM #61278
}

// parseVLLMVersion reads a config.vllmVersion value: "0.33.0", optionally
// with a leading "v" or a "+local" suffix ("0.33.0+rocm72"). The patch
// number may be left out ("0.33").
func parseVLLMVersion(raw string) (vllmRelease, error) {
	s := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return vllmRelease{}, fmt.Errorf("spec.config.vllmVersion %q is not a vLLM release such as \"0.33.0\"", raw)
	}
	nums := make([]int, 3)
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || strings.Trim(part, "0123456789") != "" {
			return vllmRelease{}, fmt.Errorf("spec.config.vllmVersion %q is not a vLLM release such as \"0.33.0\"", raw)
		}
		nums[i] = n
	}
	return vllmRelease{nums[0], nums[1], nums[2]}, nil
}

// declaredVLLMVersion returns config.vllmVersion, the vLLM release the lane's
// image carries. Like config.llamacppBuild it is controller-side metadata
// that never reaches the server: it only selects which flags and VLLM_*
// variables to render. ok is false when the key is unset or unreadable
// (ValidateVLLMConfig refuses the latter), and then the backend renders what
// it rendered before the key existed.
func declaredVLLMVersion(spec *ModelSpec) (vllmRelease, bool) {
	raw := spec.ConfigString("vllmVersion", "")
	if raw == "" {
		return vllmRelease{}, false
	}
	v, err := parseVLLMVersion(raw)
	if err != nil {
		return vllmRelease{}, false
	}
	return v, true
}

func vllmVersionAtLeast(spec *ModelSpec, release vllmRelease) bool {
	v, ok := declaredVLLMVersion(spec)
	return ok && v.atLeast(release)
}

// withoutRemovedVLLMEnv drops the variables the lane's declared vLLM release
// no longer defines. Without a declared release env is returned unchanged.
func withoutRemovedVLLMEnv(spec *ModelSpec, env []corev1.EnvVar) []corev1.EnvVar {
	v, ok := declaredVLLMVersion(spec)
	if !ok {
		return env
	}
	kept := env[:0:0]
	for _, e := range env {
		if since, removed := vllmEnvRemovedSince[e.Name]; removed && v.atLeast(since) {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// ValidateVLLMConfig refuses spec.config keys the vLLM backend cannot render
// for any release, and an unreadable vllmVersion. The controller runs it
// before rendering, so a refused Model never reaches a Deployment or a
// runtime pod.
func ValidateVLLMConfig(cfg map[string]any) error {
	// The flags were never in a vLLM release we serve: argument parsing
	// rejects them in 0.32.0, 0.33.0 and main alike.
	for _, dead := range []struct{ key, flag string }{
		{"maxNumPartialPrefills", "--max-num-partial-prefills"},
		{"maxLongPartialPrefills", "--max-long-partial-prefills"},
	} {
		if _, ok := cfg[dead.key]; ok {
			return fmt.Errorf("spec.config.%s is not supported: vLLM has no %s flag (absent from 0.32.0, 0.33.0 and main), so the server would refuse to start; remove the key", dead.key, dead.flag)
		}
	}
	if raw, ok := cfg["vllmVersion"]; ok {
		s, isString := raw.(string)
		if !isString {
			return fmt.Errorf("spec.config.vllmVersion must be a quoted string such as \"0.33.0\" (got %v)", raw)
		}
		if _, err := parseVLLMVersion(s); err != nil {
			return err
		}
	}
	return nil
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

// ReadinessProbeForSpec checks API health and engine progress independently of startup.
// Three consecutive failures (90s) absorb a transient /health or /metrics blip, so
// the lane leaves Ready at most one minute after the stall window elapses.
func (b *VLLMBackend) ReadinessProbeForSpec(spec *ModelSpec) *corev1.Probe {
	script := fmt.Sprintf(vllmStallProbeScript, StallWindowForSpec(spec).Seconds())
	return &corev1.Probe{
		ProbeHandler:  corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"python3", "-c", script}}},
		PeriodSeconds: 30, TimeoutSeconds: 15, FailureThreshold: 3, SuccessThreshold: 1,
	}
}

// Monotonic time avoids wall-clock jumps; PID 1 start time protects persisted
// state across container restarts. Missing metrics fail readiness rather than
// silently treating an unsupported metrics format as engine progress.
const vllmStallProbeScript = `
import json, math, os, sys, time, urllib.request
base = "http://127.0.0.1:8000"
urllib.request.urlopen(base + "/health", timeout=5).close()
text = urllib.request.urlopen(base + "/metrics", timeout=5).read(4194304).decode()
names = ["vllm:num_requests_running", "vllm:num_requests_waiting", "vllm:prompt_tokens_total", "vllm:generation_tokens_total"]
values = [0.0] * 4
seen = set()
for line in text.splitlines():
    line = line.strip()
    if not line or line.startswith("#"):
        continue
    name = line.split("{", 1)[0].split()[0]
    if name not in names:
        continue
    rest = line.rsplit("}", 1)[-1] if "{" in line else line[len(name):]
    value = float(rest.split()[0])
    if not math.isfinite(value) or value < 0:
        sys.exit(1)
    i = names.index(name)
    values[i] += value
    seen.add(i)
if len(seen) != 4:
    sys.exit(1)
path = "/tmp/flexinfer-vllm-stall.json"
now = time.monotonic()
with open("/proc/1/stat") as f:
    process_start = f.read().rsplit(")", 1)[1].split()[19]
old = {}
try:
    with open(path) as f:
        old = json.load(f)
except (OSError, ValueError):
    pass
active = values[0] + values[1] > 0
since = old.get("since", now)
if old.get("process_start") != process_start or not active or not old.get("active") or old.get("tokens") != values[2:] or now - old.get("observed", now) > 90 or now < since:
    since = now
state = dict(process_start=process_start, since=since, observed=now, active=active, tokens=values[2:])
with open(path + ".new", "w") as f:
    json.dump(state, f)
os.replace(path + ".new", path)
sys.exit(1 if active and now - since >= %g else 0)
`

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

// rocmAiterDisabledEnvVars turns every AITER kernel family off. The list is
// the pre-0.32 surface; Env drops the names a lane's declared vllmVersion no
// longer defines (see vllmEnvRemovedSince).
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
