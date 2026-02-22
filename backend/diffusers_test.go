package backend

import (
	"testing"
)

func TestDiffusersBackendImage(t *testing.T) {
	b := &DiffusersBackend{}

	tests := []struct {
		name      string
		gpuVendor GPUVendor
		gpuArch   string
		envKey    string
		envVal    string
		wantImage string
	}{
		{
			name:      "AMD gfx1100 without env returns generic rocm-latest",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantImage: "registry.harbor.lan/library/diffusers-api:rocm-latest",
		},
		{
			name:      "AMD gfx1100 with env override",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			envKey:    "DEFAULT_DIFFUSERS_IMAGE_GFX1100",
			envVal:    "registry.harbor.lan/flexinfer/diffusers:rocm-gfx1100",
			wantImage: "registry.harbor.lan/flexinfer/diffusers:rocm-gfx1100",
		},
		{
			name:      "AMD gfx906 without env returns generic rocm-latest",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			wantImage: "registry.harbor.lan/library/diffusers-api:rocm-latest",
		},
		{
			name:      "AMD gfx906 with env override",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			envKey:    "DEFAULT_DIFFUSERS_IMAGE_GFX906",
			envVal:    "registry.harbor.lan/flexinfer/diffusers:rocm-gfx906",
			wantImage: "registry.harbor.lan/flexinfer/diffusers:rocm-gfx906",
		},
		{
			name:      "AMD generic returns rocm-latest",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx900",
			wantImage: "registry.harbor.lan/library/diffusers-api:rocm-latest",
		},
		{
			name:      "NVIDIA returns cuda image",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_89",
			wantImage: "registry.harbor.lan/library/diffusers-api:cuda",
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
