package backend

import (
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// ComfyUIBackend implements the Backend interface for ComfyUI.
// ComfyUI provides workflow-based image generation with a web UI.
type ComfyUIBackend struct {
	BaseBackend
}

func init() {
	MustRegister(&ComfyUIBackend{})
}

func (b *ComfyUIBackend) Name() string {
	return "comfyui"
}

func (b *ComfyUIBackend) Aliases() []string {
	return []string{"comfy", "comfy-ui", "comfy_ui"}
}

func (b *ComfyUIBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	switch gpuVendor {
	case GPUVendorAMD:
		// Check for arch-specific overrides before generic AMD fallback
		if strings.HasPrefix(gpuArch, "gfx110") {
			if img := os.Getenv("DEFAULT_COMFYUI_IMAGE_GFX1100"); img != "" {
				return img
			}
		}
		if strings.HasPrefix(gpuArch, "gfx906") {
			if img := os.Getenv("DEFAULT_COMFYUI_IMAGE_GFX906"); img != "" {
				return img
			}
		}
		if img := os.Getenv("DEFAULT_COMFYUI_IMAGE_AMD"); img != "" {
			return img
		}
		return "registry.harbor.lan/library/comfyui:rocm6.2.3-v8"
	default:
		if img := os.Getenv("DEFAULT_COMFYUI_IMAGE"); img != "" {
			return img
		}
		return "comfyanonymous/comfyui:latest"
	}
}

func (b *ComfyUIBackend) Port() int32 {
	return 8188
}

func (b *ComfyUIBackend) Command() []string {
	return []string{"python", "main.py"}
}

func (b *ComfyUIBackend) Args(spec *ModelSpec) []string {
	args := []string{
		"--listen", "0.0.0.0",
		"--port", "8188",
	}

	// Enable CORS
	if spec.ConfigBool("enableCORS", true) {
		args = append(args, "--enable-cors-header")
	}

	// Custom models path
	if modelsPath := spec.ConfigString("modelsPath", ""); modelsPath != "" {
		args = append(args, "--extra-model-paths-config", modelsPath)
	}

	// Custom nodes path
	if nodesPath := spec.ConfigString("customNodesPath", ""); nodesPath != "" {
		args = append(args, "--custom-path", nodesPath)
	}

	// Output directory
	if outputDir := spec.ConfigString("outputDirectory", ""); outputDir != "" {
		args = append(args, "--output-directory", outputDir)
	}

	return args
}

func (b *ComfyUIBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{
			Name:  "COMFYUI_OUTPUT_DIRECTORY",
			Value: "/output",
		},
		{
			Name:  "CUDA_DEVICE_ORDER",
			Value: "PCI_BUS_ID",
		},
	}

	// Add ROCm environment for AMD GPUs
	if spec.GPUVendor == GPUVendorAMD {
		env = append(env, ROCmEnvVars(spec.GPUArch)...)
		env = append(env, DeviceIsolationEnvVars(spec)...)
	}

	return env
}

func (b *ComfyUIBackend) ReadinessProbe() *corev1.Probe {
	// ComfyUI uses WebSocket, so we use TCP socket probe
	return TCPReadinessProbe(8188, 10, 10, 5)
}

func (b *ComfyUIBackend) StartupTimeout() time.Duration {
	return 180 * time.Second // Image gen models can take longer
}

// IsImageGeneration returns true for ComfyUI.
func (b *ComfyUIBackend) IsImageGeneration() bool {
	return true
}

// DefaultIdleTimeout returns a longer timeout for image generation.
func (b *ComfyUIBackend) DefaultIdleTimeout() time.Duration {
	return 10 * time.Minute
}

// CompilationCacheEnvVars implements CompilationCacheConfigurer.
// Redirects MIOpen, TorchInductor, and Triton caches for ComfyUI on ROCm.
func (b *ComfyUIBackend) CompilationCacheEnvVars(cacheMountPath string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "MIOPEN_CUSTOM_CACHE_DIR", Value: cacheMountPath + "/miopen"},
		{Name: "MIOPEN_USER_DB_PATH", Value: cacheMountPath + "/miopen/user.db"},
		{Name: "TORCHINDUCTOR_CACHE_DIR", Value: cacheMountPath + "/inductor"},
		{Name: "TRITON_CACHE_DIR", Value: cacheMountPath + "/triton"},
		{Name: "TORCH_HOME", Value: cacheMountPath + "/torch"},
	}
}
