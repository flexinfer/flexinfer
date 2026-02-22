package backend

import (
	"testing"
)

func TestOllamaBackendImage_Maxwell(t *testing.T) {
	b := &OllamaBackend{}

	tests := []struct {
		name      string
		gpuVendor GPUVendor
		gpuArch   string
		envKey    string
		envVal    string
		wantImage string
	}{
		{
			name:      "NVIDIA sm_52 without env returns default",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_52",
			wantImage: "ollama/ollama:latest",
		},
		{
			name:      "NVIDIA sm_52 with Maxwell env returns Maxwell image",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_52",
			envKey:    "DEFAULT_BACKEND_IMAGE_MAXWELL",
			envVal:    "registry.harbor.lan/flexinfer/ollama:cuda-maxwell",
			wantImage: "registry.harbor.lan/flexinfer/ollama:cuda-maxwell",
		},
		{
			name:      "NVIDIA sm_50 with Maxwell env returns Maxwell image",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_50",
			envKey:    "DEFAULT_BACKEND_IMAGE_MAXWELL",
			envVal:    "registry.harbor.lan/flexinfer/ollama:cuda-maxwell",
			wantImage: "registry.harbor.lan/flexinfer/ollama:cuda-maxwell",
		},
		{
			name:      "NVIDIA sm_89 ignores Maxwell env",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_89",
			envKey:    "DEFAULT_BACKEND_IMAGE_MAXWELL",
			envVal:    "registry.harbor.lan/flexinfer/ollama:cuda-maxwell",
			wantImage: "ollama/ollama:latest",
		},
		{
			name:      "AMD gfx1100 without env returns generic rocm",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantImage: "ollama/ollama:rocm",
		},
		{
			name:      "AMD gfx1100 with env override",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			envKey:    "DEFAULT_OLLAMA_IMAGE_GFX1100",
			envVal:    "registry.harbor.lan/flexinfer/ollama:rocm-gfx1100",
			wantImage: "registry.harbor.lan/flexinfer/ollama:rocm-gfx1100",
		},
		{
			name:      "AMD gfx906 without env returns generic rocm",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			wantImage: "ollama/ollama:rocm",
		},
		{
			name:      "AMD gfx906 with env override",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			envKey:    "DEFAULT_OLLAMA_IMAGE_GFX906",
			envVal:    "registry.harbor.lan/flexinfer/ollama:rocm-gfx906",
			wantImage: "registry.harbor.lan/flexinfer/ollama:rocm-gfx906",
		},
		{
			name:      "AMD gfx900 generic returns rocm",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx900",
			wantImage: "ollama/ollama:rocm",
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
