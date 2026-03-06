// Package backend provides a plugin system for inference backends.
// Each backend implements the Backend interface to provide container
// configuration, health checks, and GPU compatibility information.
package backend

import (
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// GPUVendor represents the GPU manufacturer.
type GPUVendor string

const (
	GPUVendorNVIDIA  GPUVendor = "nvidia"
	GPUVendorAMD     GPUVendor = "amd"
	GPUVendorIntel   GPUVendor = "intel"
	GPUVendorCPU     GPUVendor = "cpu" // CPU-only inference (no GPU)
	GPUVendorUnknown GPUVendor = "unknown"
)

// GPUResourceName returns the Kubernetes resource name for the GPU vendor.
// Returns empty string for CPU-only vendor (no GPU resources needed).
func (v GPUVendor) ResourceName() corev1.ResourceName {
	switch v {
	case GPUVendorNVIDIA:
		return "nvidia.com/gpu"
	case GPUVendorAMD:
		return "amd.com/gpu"
	case GPUVendorIntel:
		return "gpu.intel.com/i915"
	case GPUVendorCPU:
		return "" // No GPU resource for CPU-only inference
	default:
		return "nvidia.com/gpu"
	}
}

// ModelSpec provides model configuration to backends.
// This is a simplified view of the model spec for backend use.
type ModelSpec struct {
	// Name is the model deployment name
	Name string

	// Model is the model identifier (HuggingFace ID, file path, etc.)
	Model string

	// ModelPath is the path where the model is mounted (for backends that need volumes)
	ModelPath string

	// Config contains backend-specific configuration as key-value pairs
	Config map[string]interface{}

	// GPUVendor indicates which GPU vendor is being used
	GPUVendor GPUVendor

	// GPUArch is the GPU architecture (e.g., "sm_52" for Maxwell, "gfx1100" for RDNA3)
	GPUArch string

	// GPUMemoryBytes is the available GPU memory
	GPUMemoryBytes int64
}

// ConfigString returns a string config value with a default.
func (s *ModelSpec) ConfigString(key, defaultVal string) string {
	if s.Config == nil {
		return defaultVal
	}
	if v, ok := s.Config[key]; ok {
		if str, ok := v.(string); ok {
			return str
		}
	}
	return defaultVal
}

// ConfigInt returns an int config value with a default.
func (s *ModelSpec) ConfigInt(key string, defaultVal int) int {
	if s.Config == nil {
		return defaultVal
	}
	if v, ok := s.Config[key]; ok {
		switch val := v.(type) {
		case int:
			return val
		case int64:
			return int(val)
		case float64:
			return int(val)
		}
	}
	return defaultVal
}

// ConfigBool returns a bool config value with a default.
// Handles both native bool values and string representations
// ("true", "false", "1", "0") since CRD config values arrive as
// JSON strings after json.Unmarshal into map[string]interface{}.
func (s *ModelSpec) ConfigBool(key string, defaultVal bool) bool {
	if s.Config == nil {
		return defaultVal
	}
	v, ok := s.Config[key]
	if !ok {
		return defaultVal
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return defaultVal
		}
		return b
	default:
		return defaultVal
	}
}

// Backend defines the interface that all inference backends must implement.
// This centralizes all backend-specific configuration in one place.
type Backend interface {
	// Identity

	// Name returns the canonical name of the backend (e.g., "mlc-llm", "vllm")
	Name() string

	// Aliases returns alternative names for the backend (e.g., "mlc" for "mlc-llm")
	Aliases() []string

	// Container Configuration

	// Image returns the container image for the given GPU vendor and architecture.
	// The gpuArch parameter is optional and may be empty.
	Image(gpuVendor GPUVendor, gpuArch string) string

	// Port returns the primary port the backend listens on.
	Port() int32

	// Command returns the container command. Returns nil to use image default.
	Command() []string

	// Args returns the container arguments for the given model spec.
	Args(spec *ModelSpec) []string

	// Env returns environment variables for the given model spec and GPU vendor.
	Env(spec *ModelSpec) []corev1.EnvVar

	// Health & Lifecycle

	// ReadinessProbe returns the readiness probe configuration.
	ReadinessProbe() *corev1.Probe

	// LivenessProbe returns the liveness probe configuration.
	// Returns nil to disable liveness probing.
	LivenessProbe() *corev1.Probe

	// StartupProbe returns the startup probe configuration.
	// Startup probes replace the readiness probe during cold start, allowing
	// aggressive polling (e.g., every 2s) without a large InitialDelaySeconds.
	// Returns nil to disable startup probing.
	StartupProbe() *corev1.Probe

	// StartupTimeout returns the maximum time to wait for the backend to start.
	// This is used for cold start timeout defaults.
	StartupTimeout() time.Duration

	// Model Source

	// NeedsVolume returns true if the backend requires a model volume mount.
	// Backends like Ollama that download models on-demand return false.
	NeedsVolume() bool

	// SupportsGPUVendor returns true if the backend supports the given GPU vendor.
	SupportsGPUVendor(vendor GPUVendor) bool

	// Metadata

	// IsImageGeneration returns true if this is an image generation backend.
	// Used for routing and service label defaults.
	IsImageGeneration() bool

	// DefaultIdleTimeout returns the default idle timeout for this backend.
	// Image generation backends may have longer timeouts.
	DefaultIdleTimeout() time.Duration
}

// KVCacheConfigurer is an optional interface that backends can implement
// to support KV-cache tuning arguments. Backends that manage their own
// KV-cache (like vLLM) implement this to translate CRD-level cache policies
// into CLI arguments.
type KVCacheConfigurer interface {
	// KVCacheArgs returns CLI arguments for KV-cache tuning based on the spec.
	// Returns nil if no tuning is needed.
	KVCacheArgs(maxBlockSize *int, swapSpaceGiB *float64) []string

	// SupportsSwapSpace returns true if the backend supports CPU-offloaded KV-cache.
	SupportsSwapSpace() bool
}

// LoRASupporter is an optional interface that backends can implement
// to support dynamic LoRA adapter loading and unloading at runtime.
type LoRASupporter interface {
	// SupportsLoRA returns true if the backend supports hot-loading LoRA adapters.
	SupportsLoRA() bool

	// LoRABaseArgs returns CLI arguments to enable LoRA support with a max adapter count.
	LoRABaseArgs(maxAdapters int) []string

	// LoadLoRAEndpoint returns the HTTP path for loading a LoRA adapter.
	LoadLoRAEndpoint() string

	// UnloadLoRAEndpoint returns the HTTP path for unloading a LoRA adapter.
	UnloadLoRAEndpoint() string
}

// QuantizationSupporter is an optional interface that backends can implement
// to declare which quantization formats they can consume. The controller uses
// this to validate that a quantized model is compatible with the target backend.
type QuantizationSupporter interface {
	// SupportedQuantFormats returns the quantization formats this backend accepts.
	SupportedQuantFormats() []string
}

// CompilationCacheConfigurer is an optional interface that backends can
// implement to declare which environment variables redirect GPU kernel
// compilation caches to a persistent mount path. The controller calls
// this when compilation cache is enabled to inject the correct env vars.
type CompilationCacheConfigurer interface {
	// CompilationCacheEnvVars returns env vars that redirect compilation
	// caches (MIOpen, TorchInductor, Triton, etc.) to the given mount path.
	CompilationCacheEnvVars(cacheMountPath string) []corev1.EnvVar
}

// BaseBackend provides common default implementations for Backend methods.
// Embed this in concrete backend implementations to inherit defaults.
type BaseBackend struct{}

// Aliases returns an empty slice (no aliases by default).
func (b *BaseBackend) Aliases() []string {
	return nil
}

// Command returns nil to use image default.
func (b *BaseBackend) Command() []string {
	return nil
}

// LivenessProbe returns nil (no liveness probe by default).
func (b *BaseBackend) LivenessProbe() *corev1.Probe {
	return nil
}

// StartupProbe returns nil (no startup probe by default).
func (b *BaseBackend) StartupProbe() *corev1.Probe {
	return nil
}

// StartupTimeout returns a default of 60 seconds.
func (b *BaseBackend) StartupTimeout() time.Duration {
	return 60 * time.Second
}

// NeedsVolume returns true by default (most backends need model volumes).
func (b *BaseBackend) NeedsVolume() bool {
	return true
}

// SupportsGPUVendor returns true for NVIDIA and AMD by default.
// Backends that support CPU-only inference should override this method.
func (b *BaseBackend) SupportsGPUVendor(vendor GPUVendor) bool {
	return vendor == GPUVendorNVIDIA || vendor == GPUVendorAMD
}

// IsImageGeneration returns false by default (most backends are LLM).
func (b *BaseBackend) IsImageGeneration() bool {
	return false
}

// DefaultIdleTimeout returns 5 minutes by default.
func (b *BaseBackend) DefaultIdleTimeout() time.Duration {
	return 5 * time.Minute
}

// ROCmEnvVars returns environment variables appropriate for the given AMD GPU
// architecture. Pass spec.GPUArch (e.g., "gfx1100", "gfx906").
//
// Architecture-specific behavior:
//   - gfx110x (RDNA3, RX 7900 series): HSA override 11.0.0, AOTriton, PYTORCH_ROCM_ARCH
//   - gfx90a (MI250, CDNA2): PYTORCH_ROCM_ARCH only (natively supported)
//   - gfx942 (MI300X, CDNA3): PYTORCH_ROCM_ARCH only (natively supported)
//   - gfx906 (Vega20, Radeon VII): disable SDMA for stability, PYTORCH_ROCM_ARCH
//   - unknown/empty: only PYTORCH_ROCM_ARCH if arch is known, no HSA override
func ROCmEnvVars(arch string) []corev1.EnvVar {
	var env []corev1.EnvVar

	switch {
	case strings.HasPrefix(arch, "gfx110"):
		// RDNA3 (RX 7900 series)
		env = append(env,
			corev1.EnvVar{Name: "HSA_OVERRIDE_GFX_VERSION", Value: "11.0.0"},
			corev1.EnvVar{
				// Critical for gfx1100 stability — enables experimental AOTriton
				// flash attention which prevents SIGSEGV crashes on RDNA3.
				Name:  "TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL",
				Value: "1",
			},
			// Faster kernel dispatch on ROCm — reduces launch overhead.
			corev1.EnvVar{Name: "HIP_FORCE_DEV_KERNARG", Value: "1"},
			// Prefer hipBLASLt over hipBLAS for GEMM — better linear layer perf on RDNA3.
			corev1.EnvVar{Name: "TORCH_BLAS_PREFER_HIPBLASLT", Value: "1"},
			corev1.EnvVar{Name: "PYTORCH_ROCM_ARCH", Value: "gfx1100"},
		)
	case strings.HasPrefix(arch, "gfx90a"):
		// MI250 (CDNA2): natively supported by ROCm, no HSA override needed.
		env = append(env,
			corev1.EnvVar{Name: "PYTORCH_ROCM_ARCH", Value: "gfx90a"},
		)
	case strings.HasPrefix(arch, "gfx942"):
		// MI300X (CDNA3): natively supported by ROCm, no HSA override needed.
		env = append(env,
			corev1.EnvVar{Name: "PYTORCH_ROCM_ARCH", Value: "gfx942"},
		)
	case strings.HasPrefix(arch, "gfx906"):
		// Vega20 (Radeon VII): hardware reports as gfx900 but images compile for
		// gfx906. HSA override ensures gfx906 ISA is used at runtime (required
		// by bitsandbytes NF4 HIP kernels which are compiled for gfx906).
		// Disable SDMA for stability on Vega20.
		// Disable SVM to work around hipMemGetInfo "invalid argument" crash in
		// ROCm 6.x containers where VMM is not supported (ROCm/ROCm#2433).
		env = append(env,
			corev1.EnvVar{Name: "HSA_OVERRIDE_GFX_VERSION", Value: "9.0.6"},
			corev1.EnvVar{Name: "HSA_ENABLE_SDMA", Value: "0"},
			corev1.EnvVar{Name: "HSA_USE_SVM", Value: "0"},
			corev1.EnvVar{Name: "PYTORCH_ROCM_ARCH", Value: "gfx906"},
		)
	default:
		// Unknown or unspecified arch — set PYTORCH_ROCM_ARCH if known,
		// but do not override HSA version.
		if arch != "" {
			env = append(env,
				corev1.EnvVar{Name: "PYTORCH_ROCM_ARCH", Value: arch},
			)
		}
	}

	return env
}

// DeviceIsolationEnvVars returns env vars to pin AMD GPU device selection.
// On systems with both iGPU and dGPU, the device plugin exposes both as
// amd.com/gpu resources. Without explicit device pinning, workloads may
// land on the iGPU which lacks dedicated VRAM and crashes on inference.
//
// Reads hipVisibleDevices and rocrVisibleDevices from model config.
// If only one is set, mirrors to both for consistent ROCm isolation.
func DeviceIsolationEnvVars(spec *ModelSpec) []corev1.EnvVar {
	hipVisible := spec.ConfigString("hipVisibleDevices", "")
	rocrVisible := spec.ConfigString("rocrVisibleDevices", "")
	ordinal := spec.ConfigString("gpuDeviceOrdinal", "")

	// gpuDeviceOrdinal acts as a fallback when neither HIP nor ROCR is set.
	if hipVisible == "" && rocrVisible == "" && ordinal != "" {
		hipVisible = ordinal
		rocrVisible = ordinal
	}
	if hipVisible != "" && rocrVisible == "" {
		rocrVisible = hipVisible
	}
	if rocrVisible != "" && hipVisible == "" {
		hipVisible = rocrVisible
	}

	var env []corev1.EnvVar
	if rocrVisible != "" {
		env = append(env, corev1.EnvVar{Name: "ROCR_VISIBLE_DEVICES", Value: rocrVisible})
	}
	if hipVisible != "" {
		env = append(env, corev1.EnvVar{Name: "HIP_VISIBLE_DEVICES", Value: hipVisible})
	}
	if ordinal != "" {
		env = append(env, corev1.EnvVar{Name: "GPU_DEVICE_ORDINAL", Value: ordinal})
	}
	return env
}

// HTTPReadinessProbe creates a standard HTTP readiness probe.
// K8s defaults (Scheme, SuccessThreshold) are set explicitly to prevent reconcile loops.
func HTTPReadinessProbe(path string, port int32, initialDelay, period, timeout int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path:   path,
				Port:   intstr.FromInt32(port),
				Scheme: corev1.URISchemeHTTP,
			},
		},
		InitialDelaySeconds: initialDelay,
		PeriodSeconds:       period,
		TimeoutSeconds:      timeout,
		SuccessThreshold:    1,
		FailureThreshold:    30, // Allow 5 minutes of failures (30 * 10s)
	}
}

// TCPReadinessProbe creates a TCP socket readiness probe.
// K8s defaults (SuccessThreshold) are set explicitly to prevent reconcile loops.
func TCPReadinessProbe(port int32, initialDelay, period, timeout int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{
				Port: intstr.FromInt32(port),
			},
		},
		InitialDelaySeconds: initialDelay,
		PeriodSeconds:       period,
		TimeoutSeconds:      timeout,
		SuccessThreshold:    1,
		FailureThreshold:    90, // Allow 15 minutes (90 * 10s) for large models
	}
}

// HTTPStartupProbe creates an HTTP startup probe that polls aggressively during
// cold start. Once the startup probe succeeds, K8s switches to the readiness
// probe. This eliminates the need for large InitialDelaySeconds on readiness.
// HTTPStartupProbe creates an HTTP startup probe with the given timeout.
// K8s defaults (Scheme, SuccessThreshold) are set explicitly to prevent reconcile loops.
func HTTPStartupProbe(path string, port int32, startupTimeout time.Duration) *corev1.Probe {
	period := int32(2)
	failureThreshold := int32(startupTimeout.Seconds()) / period
	if failureThreshold < 30 {
		failureThreshold = 30
	}
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path:   path,
				Port:   intstr.FromInt32(port),
				Scheme: corev1.URISchemeHTTP,
			},
		},
		InitialDelaySeconds: 5,
		PeriodSeconds:       period,
		TimeoutSeconds:      3,
		SuccessThreshold:    1,
		FailureThreshold:    failureThreshold,
	}
}
