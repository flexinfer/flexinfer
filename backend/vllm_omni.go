package backend

import (
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// VLLMOmniBackend implements the Backend interface for vLLM-Omni.
// vLLM-Omni provides diffusion model support with OpenAI-compatible API.
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
		if img := os.Getenv("DEFAULT_VLLM_OMNI_IMAGE_AMD"); img != "" {
			return img
		}
		// ROCm-enabled vLLM-Omni image for gfx1100 (RX 7900 XTX)
		return "registry.harbor.lan/library/vllm-api:rocm-navi"
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

	return args
}

func (b *VLLMOmniBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{
			Name:  "CUDA_DEVICE_ORDER",
			Value: "PCI_BUS_ID",
		},
	}

	// Add ROCm environment for AMD GPUs
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars()...)

		// Keep ROCm device visibility controls consistent with other AMD backends.
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

func (b *VLLMOmniBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/health", 8000, 30, 10, 5)
}

func (b *VLLMOmniBackend) StartupTimeout() time.Duration {
	return 180 * time.Second
}

// IsImageGeneration returns true for vLLM-Omni.
func (b *VLLMOmniBackend) IsImageGeneration() bool {
	return true
}

// DefaultIdleTimeout returns a longer timeout for image generation.
func (b *VLLMOmniBackend) DefaultIdleTimeout() time.Duration {
	return 10 * time.Minute
}
