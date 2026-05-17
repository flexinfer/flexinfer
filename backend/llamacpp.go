package backend

import (
	"fmt"
	"path"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// llamaCppImageRules defines the image resolution precedence for llama.cpp.
//
// AMD arch-specific rules are env-only (no built-in default) so they fall
// through to the AMD-generic image when no env override is set. The arch
// defaults now live in deploy/gpuprofiles/gfx1100.yaml, gfx906.yaml, and
// sm_52.yaml —
// callers that pass a GPUProfile through backend.ResolveBackendImage get the
// per-arch image from the profile, and only nodes without a profile fall back
// to this slice.
var llamaCppImageRules = []ImageRule{
	// AMD arch-specific (env-only; profile owns the default)
	{Vendor: GPUVendorAMD, ArchPrefix: "gfx110", EnvVar: "DEFAULT_LLAMA_CPP_IMAGE_GFX1100"},
	{Vendor: GPUVendorAMD, ArchPrefix: "gfx906", EnvVar: "DEFAULT_LLAMA_CPP_IMAGE_GFX906"},
	// AMD generic
	{Vendor: GPUVendorAMD, EnvVar: "DEFAULT_LLAMA_CPP_IMAGE_AMD", Default: "ghcr.io/ggerganov/llama.cpp:server-rocm"},
	// CPU-only
	{Vendor: GPUVendorCPU, EnvVar: "DEFAULT_LLAMA_CPP_IMAGE_CPU", Default: "ghcr.io/ggerganov/llama.cpp:server"},
	// NVIDIA Maxwell sub-arch (env-only; profile owns the default)
	{Vendor: GPUVendorNVIDIA, ArchPrefix: "sm_5", EnvVar: "DEFAULT_LLAMA_CPP_IMAGE_MAXWELL"},
	// NVIDIA/global default
	{EnvVar: "DEFAULT_LLAMA_CPP_IMAGE", Default: "ghcr.io/ggerganov/llama.cpp:server-cuda"},
}

// LlamaCppBackend implements the Backend interface for llama.cpp.
// llama.cpp provides efficient LLM inference for GGUF models.
type LlamaCppBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&LlamaCppBackend{})
}

func (b *LlamaCppBackend) Name() string {
	return NameLlamaCpp
}

func (b *LlamaCppBackend) Aliases() []string {
	return []string{"llama.cpp", "llama-cpp", "llama_cpp"}
}

func (b *LlamaCppBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	return ResolveImage(llamaCppImageRules, gpuVendor, gpuArch)
}

func (b *LlamaCppBackend) Port() int32 {
	return PortLlamaCpp
}

// Command returns the llama-server binary path.
// Required for custom images that use tini as entrypoint (e.g., Harbor ROCm builds).
func (b *LlamaCppBackend) Command() []string {
	return []string{"/opt/src/llama.cpp/build/bin/llama-server"}
}

func (b *LlamaCppBackend) Args(spec *ModelSpec) []string {
	port := spec.ConfigInt("port", int(b.Port()))
	args := []string{
		"--host", "0.0.0.0",
		"--port", fmt.Sprintf("%d", port),
	}

	// Model path
	if modelPath := llamaCppModelPath(spec); modelPath != "" {
		args = append(args, "--model", modelPath)
	} else if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}

	// Context size
	if ctxSize := spec.ConfigInt("contextSize", 0); ctxSize > 0 {
		args = append(args, "--ctx-size", fmt.Sprintf("%d", ctxSize))
	}

	// Number of GPU layers - handle CPU-only mode
	if spec.GPUVendor == GPUVendorCPU {
		// CPU-only inference: no GPU layers
		args = append(args, "--n-gpu-layers", "0")
	} else if nGPU := spec.ConfigInt("nGPULayers", 0); nGPU > 0 {
		args = append(args, "--n-gpu-layers", fmt.Sprintf("%d", nGPU))
	} else {
		// Default to using all layers on GPU
		args = append(args, "--n-gpu-layers", fmt.Sprintf("%d", DefaultGPULayersAll))
	}

	// Batch size
	if batchSize := spec.ConfigInt("batchSize", 0); batchSize > 0 {
		args = append(args, "--batch-size", fmt.Sprintf("%d", batchSize))
	}

	// Number of threads - important for CPU inference
	if threads := spec.ConfigInt("threads", 0); threads > 0 {
		args = append(args, "--threads", fmt.Sprintf("%d", threads))
	}

	// Flash attention (GPU only, skip for CPU)
	if spec.GPUVendor != GPUVendorCPU && spec.ConfigBool("flashAttention", false) {
		args = append(args, "--flash-attn", "on")
	}

	// Multimodal projection file for vision models (e.g., LLaVA, Qwen-VL)
	if mmproj := spec.ConfigString("mmproj", ""); mmproj != "" {
		args = append(args, "--mmproj", mmproj)
	}

	// Chat template (e.g., chatml, llama2, etc.)
	if chatTemplate := spec.ConfigString("chatTemplate", ""); chatTemplate != "" {
		args = append(args, "--chat-template", chatTemplate)
	}

	// Jinja template engine (required for tool/function calling support)
	if spec.ConfigBool("jinja", false) {
		args = append(args, "--jinja")
	}

	// Explicit device selection (useful on multi-GPU nodes).
	// If "device" is unset, fall back to gpuDeviceOrdinal for compatibility.
	if device := spec.ConfigString("device", ""); device != "" {
		args = append(args, "--device", device)
	} else if ordinal := spec.ConfigString("gpuDeviceOrdinal", ""); ordinal != "" {
		args = append(args, "--device", ordinal)
	}

	// Parallel requests
	if parallel := spec.ConfigInt("parallel", 0); parallel > 0 {
		args = append(args, "--parallel", fmt.Sprintf("%d", parallel))
	}

	// KV cache quantization (for memory efficiency)
	if cacheTypeK := spec.ConfigString("cacheTypeK", ""); cacheTypeK != "" {
		args = append(args, "--cache-type-k", cacheTypeK)
	}
	if cacheTypeV := spec.ConfigString("cacheTypeV", ""); cacheTypeV != "" {
		args = append(args, "--cache-type-v", cacheTypeV)
	}

	// Ubatch size for better throughput
	if ubatchSize := spec.ConfigInt("ubatchSize", 0); ubatchSize > 0 {
		args = append(args, "--ubatch-size", fmt.Sprintf("%d", ubatchSize))
	}

	// Thinking/reasoning output format for OpenAI-compatible responses.
	if reasoningFormat := spec.ConfigString("reasoningFormat", ""); reasoningFormat != "" {
		args = append(args, "--reasoning-format", reasoningFormat)
	}
	if reasoningBudget, ok := configOptionalInt(spec, "reasoningBudget"); ok {
		args = append(args, "--reasoning-budget", fmt.Sprintf("%d", reasoningBudget))
	}

	// Embedding mode (required for /v1/embeddings endpoint)
	if spec.ConfigBool("embedding", false) {
		args = append(args, "--embeddings")
	}

	// Enable metrics endpoint
	if spec.ConfigBool("metrics", false) {
		args = append(args, "--metrics")
	}

	// Disable auto-fit when GPU doesn't support VMM (e.g., gfx906/Vega20).
	// hipMemGetInfo crashes on these GPUs; -fit off skips the memory query.
	if spec.ConfigBool("fitOff", false) {
		args = append(args, "-fit", "off")
	}

	return args
}

func llamaCppModelPath(spec *ModelSpec) string {
	if spec == nil || spec.ModelPath == "" {
		return ""
	}
	modelPath := spec.ModelPath
	ggufFile := strings.TrimLeft(strings.TrimSpace(spec.ConfigString("ggufFile", "")), "/")
	if ggufFile == "" {
		ggufFile = strings.TrimLeft(strings.TrimSpace(spec.ConfigString("modelFile", "")), "/")
	}
	if ggufFile == "" || strings.Contains(ggufFile, "..") || path.Base(modelPath) == path.Base(ggufFile) {
		return modelPath
	}
	if strings.HasSuffix(strings.ToLower(path.Base(modelPath)), ".gguf") {
		return modelPath
	}
	return path.Join(modelPath, ggufFile)
}

func (b *LlamaCppBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	var env []corev1.EnvVar

	// Add ROCm environment for AMD GPUs.
	// llama.cpp uses HIP directly (not PyTorch), so PYTORCH_* vars are
	// irrelevant but harmless. ROCmEnvVars handles per-arch differences
	// (gfx906 gets HSA_ENABLE_SDMA=0 instead of gfx1100 HSA override).
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars(spec.GPUArch)...)
		env = append(env, DeviceIsolationEnvVars(spec)...)
	}

	return env
}

func configOptionalInt(spec *ModelSpec, key string) (int, bool) {
	if spec.Config == nil {
		return 0, false
	}
	raw, ok := spec.Config[key]
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

func (b *LlamaCppBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/health", 8080, 10, 10, 5)
}

func (b *LlamaCppBackend) StartupTimeout() time.Duration {
	return 60 * time.Second
}

// SupportsGPUVendor returns true for NVIDIA, AMD, and CPU-only inference.
// llama.cpp is unique in supporting efficient CPU-only inference.
func (b *LlamaCppBackend) SupportsGPUVendor(vendor GPUVendor) bool {
	return vendor == GPUVendorNVIDIA || vendor == GPUVendorAMD || vendor == GPUVendorCPU
}

// SupportedQuantFormats returns GGUF — the native format for llama.cpp.
func (b *LlamaCppBackend) SupportedQuantFormats() []string {
	return []string{"GGUF"}
}
