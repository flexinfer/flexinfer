package backend

import (
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// VLLMBackend implements the Backend interface for vLLM.
// vLLM provides an OpenAI-compatible API for LLM inference.
type VLLMBackend struct {
	BaseBackend
}

func init() {
	Register(&VLLMBackend{})
}

func (b *VLLMBackend) Name() string {
	return "vllm"
}

func (b *VLLMBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	switch gpuVendor {
	case GPUVendorAMD:
		if img := os.Getenv("DEFAULT_VLLM_IMAGE_AMD"); img != "" {
			return img
		}
		// ROCm-enabled vLLM image for gfx1100 (RX 7900 XTX)
		return "registry.harbor.lan/library/vllm-api:rocm-navi"
	default:
		if img := os.Getenv("DEFAULT_VLLM_IMAGE"); img != "" {
			return img
		}
		return "vllm/vllm-openai:latest"
	}
}

func (b *VLLMBackend) Port() int32 {
	return 8000
}

func (b *VLLMBackend) Args(spec *ModelSpec) []string {
	args := []string{
		"--host", "0.0.0.0",
		"--port", "8000",
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

	// Trust remote code
	if spec.ConfigBool("trustRemoteCode", false) {
		args = append(args, "--trust-remote-code")
	}

	return args
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
		env = append(env, ROCmEnvVars()...)

		// vLLM-specific ROCm tuning for gfx1100 (RX 7900 XTX)
		// These settings prevent SIGSEGV crashes on RDNA3 architecture
		env = append(env,
			corev1.EnvVar{
				// Force V0 engine - V1 engine has compatibility issues with gfx1100
				Name:  "VLLM_USE_V1",
				Value: "0",
			},
			corev1.EnvVar{
				// Use Triton flash attention on gfx1100 - CK (Composable Kernel) crashes
				// with "invalid device function" because CK isn't compiled for RDNA3
				Name:  "VLLM_USE_TRITON_FLASH_ATTN",
				Value: "1",
			},
			corev1.EnvVar{
				// Disable AITER (Asynchronous Iteration) which can cause crashes on gfx1100
				Name:  "VLLM_ROCM_USE_AITER",
				Value: "0",
			},
		)

		// HIP_VISIBLE_DEVICES allows selecting specific GPUs on multi-GPU systems.
		// On systems with both iGPU and discrete GPU, set to "1" to use discrete.
		// Device indices: 0=first GPU (often iGPU), 1=second GPU (discrete), etc.
		if hipDevices := spec.ConfigString("hipVisibleDevices", ""); hipDevices != "" {
			env = append(env, corev1.EnvVar{
				Name:  "HIP_VISIBLE_DEVICES",
				Value: hipDevices,
			})
		}
	}

	return env
}

func (b *VLLMBackend) ReadinessProbe() *corev1.Probe {
	return HTTPReadinessProbe("/health", 8000, 10, 10, 5)
}

func (b *VLLMBackend) StartupTimeout() time.Duration {
	return 120 * time.Second
}
