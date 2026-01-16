// Package backend provides a plugin system for inference backends.
// Each backend implements the Backend interface to provide container
// configuration, health checks, and GPU compatibility information.
package backend

import (
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
	GPUVendorUnknown GPUVendor = "unknown"
)

// GPUResourceName returns the Kubernetes resource name for the GPU vendor.
func (v GPUVendor) ResourceName() corev1.ResourceName {
	switch v {
	case GPUVendorNVIDIA:
		return "nvidia.com/gpu"
	case GPUVendorAMD:
		return "amd.com/gpu"
	case GPUVendorIntel:
		return "gpu.intel.com/i915"
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
func (s *ModelSpec) ConfigBool(key string, defaultVal bool) bool {
	if s.Config == nil {
		return defaultVal
	}
	if v, ok := s.Config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return defaultVal
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

// StartupTimeout returns a default of 60 seconds.
func (b *BaseBackend) StartupTimeout() time.Duration {
	return 60 * time.Second
}

// NeedsVolume returns true by default (most backends need model volumes).
func (b *BaseBackend) NeedsVolume() bool {
	return true
}

// SupportsGPUVendor returns true for NVIDIA and AMD by default.
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

// ROCmEnvVars returns common environment variables for AMD ROCm GPUs.
// Use this helper in backend Env() implementations for AMD GPU support.
// Optimized for gfx1100 (RX 7900 XTX) and RDNA3 architecture.
func ROCmEnvVars() []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name:  "HSA_OVERRIDE_GFX_VERSION",
			Value: "11.0.0", // RDNA3 (RX 7900 series)
		},
		{
			Name:  "HIP_VISIBLE_DEVICES",
			Value: "0",
		},
		{
			Name:  "ROCR_VISIBLE_DEVICES",
			Value: "0",
		},
		{
			// Critical for gfx1100 stability - enables experimental AOTriton
			// flash attention which prevents SIGSEGV crashes on RDNA3.
			Name:  "TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL",
			Value: "1",
		},
		{
			Name:  "PYTORCH_ROCM_ARCH",
			Value: "gfx1100",
		},
	}
}

// HTTPReadinessProbe creates a standard HTTP readiness probe.
func HTTPReadinessProbe(path string, port int32, initialDelay, period, timeout int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: path,
				Port: intstr.FromInt32(port),
			},
		},
		InitialDelaySeconds: initialDelay,
		PeriodSeconds:       period,
		TimeoutSeconds:      timeout,
		FailureThreshold:    30, // Allow 5 minutes of failures (30 * 10s)
	}
}

// TCPReadinessProbe creates a TCP socket readiness probe.
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
		FailureThreshold:    90, // Allow 15 minutes (90 * 10s) for large models
	}
}
