package backend

import (
	"testing"
)

func TestVLLMBackendImage_GFX1100(t *testing.T) {
	b := &VLLMBackend{}

	tests := []struct {
		name      string
		gpuVendor GPUVendor
		gpuArch   string
		wantImage string
	}{
		{
			name:      "AMD gfx1100 returns gfx1100-specific image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantImage: "registry.harbor.lan/flexinfer/vllm:rocm-gfx1100",
		},
		{
			name:      "AMD gfx1101 returns gfx1100-specific image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1101",
			wantImage: "registry.harbor.lan/flexinfer/vllm:rocm-gfx1100",
		},
		{
			name:      "AMD gfx1102 returns gfx1100-specific image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1102",
			wantImage: "registry.harbor.lan/flexinfer/vllm:rocm-gfx1100",
		},
		{
			name:      "AMD gfx942 returns generic ROCm image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx942",
			wantImage: "rocm/vllm:latest",
		},
		{
			name:      "AMD empty arch returns generic ROCm image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "",
			wantImage: "rocm/vllm:latest",
		},
		{
			name:      "NVIDIA returns CUDA image",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_89",
			wantImage: "vllm/vllm-openai:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := b.Image(tt.gpuVendor, tt.gpuArch)
			if got != tt.wantImage {
				t.Errorf("Image(%v, %q) = %q, want %q", tt.gpuVendor, tt.gpuArch, got, tt.wantImage)
			}
		})
	}
}

func TestVLLMBackendEnv_GFX1100Settings(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
	}

	env := b.Env(spec)

	// Check that gfx1100-specific environment variables are set
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	// V1 engine should be disabled for gfx1100
	if v, ok := envMap["VLLM_USE_V1"]; !ok || v != "0" {
		t.Errorf("expected VLLM_USE_V1=0, got %q", v)
	}

	// Triton flash attention should be disabled for gfx1100
	if v, ok := envMap["VLLM_USE_TRITON_FLASH_ATTN"]; !ok || v != "0" {
		t.Errorf("expected VLLM_USE_TRITON_FLASH_ATTN=0, got %q", v)
	}

	// AITER should be disabled for gfx1100
	if v, ok := envMap["VLLM_ROCM_USE_AITER"]; !ok || v != "0" {
		t.Errorf("expected VLLM_ROCM_USE_AITER=0, got %q", v)
	}

	// HSA override should be set for RDNA3
	if v, ok := envMap["HSA_OVERRIDE_GFX_VERSION"]; !ok || v != "11.0.0" {
		t.Errorf("expected HSA_OVERRIDE_GFX_VERSION=11.0.0, got %q", v)
	}
}

func TestVLLMBackendEnv_HIPVisibleDevices(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		Config: map[string]interface{}{
			"hipVisibleDevices": "1",
		},
	}

	env := b.Env(spec)

	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v, ok := envMap["HIP_VISIBLE_DEVICES"]; !ok || v != "1" {
		t.Errorf("expected HIP_VISIBLE_DEVICES=1, got %q", v)
	}
}
