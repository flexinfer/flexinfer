package backend

import (
	"testing"
)

func TestComfyUIBackendImage(t *testing.T) {
	b := &ComfyUIBackend{}

	tests := []struct {
		name      string
		gpuVendor GPUVendor
		gpuArch   string
		envKey    string
		envVal    string
		wantImage string
	}{
		{
			name:      "AMD gfx1100 without env returns generic rocm",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantImage: "registry.harbor.lan/library/comfyui:rocm6.2.3-v8",
		},
		{
			name:      "AMD gfx1100 with env override",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			envKey:    "DEFAULT_COMFYUI_IMAGE_GFX1100",
			envVal:    "registry.harbor.lan/flexinfer/comfyui:rocm-gfx1100",
			wantImage: "registry.harbor.lan/flexinfer/comfyui:rocm-gfx1100",
		},
		{
			name:      "AMD gfx906 without env returns generic rocm",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			wantImage: "registry.harbor.lan/library/comfyui:rocm6.2.3-v8",
		},
		{
			name:      "AMD gfx906 with env override",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			envKey:    "DEFAULT_COMFYUI_IMAGE_GFX906",
			envVal:    "registry.harbor.lan/flexinfer/comfyui:rocm-gfx906",
			wantImage: "registry.harbor.lan/flexinfer/comfyui:rocm-gfx906",
		},
		{
			name:      "AMD generic returns rocm",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx900",
			wantImage: "registry.harbor.lan/library/comfyui:rocm6.2.3-v8",
		},
		{
			name:      "NVIDIA returns default comfy image",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_89",
			wantImage: "comfyanonymous/comfyui:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envKey != "" {
				t.Setenv(tt.envKey, tt.envVal)
			}
			got := b.Image(tt.gpuVendor, tt.gpuArch)
			if got != tt.wantImage {
				t.Errorf("Image(%v, %q) = %q, want %q", tt.gpuVendor, tt.gpuArch, got, tt.wantImage)
			}
		})
	}
}
