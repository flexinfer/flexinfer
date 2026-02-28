package backend

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestComfyUIBackendEnv_GFX906MemoryTuning(t *testing.T) {
	b := &ComfyUIBackend{}

	findEnv := func(envs []corev1.EnvVar, name string) (string, bool) {
		for _, e := range envs {
			if e.Name == name {
				return e.Value, true
			}
		}
		return "", false
	}

	spec := &ModelSpec{
		Model:     "test-model",
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx906",
		Config:    map[string]interface{}{},
	}
	envs := b.Env(spec)

	if v, ok := findEnv(envs, "PYTORCH_HIP_ALLOC_CONF"); !ok {
		t.Error("expected PYTORCH_HIP_ALLOC_CONF for gfx906")
	} else if v != "garbage_collection_threshold:0.8,max_split_size_mb:256" {
		t.Errorf("PYTORCH_HIP_ALLOC_CONF = %q, want tighter gfx906 config", v)
	}

	if v, ok := findEnv(envs, "ENABLE_ATTENTION_SLICING"); !ok {
		t.Error("expected ENABLE_ATTENTION_SLICING for gfx906")
	} else if v != "1" {
		t.Errorf("ENABLE_ATTENTION_SLICING = %q, want 1", v)
	}

	// gfx1100 should NOT get gfx906-specific tuning
	spec1100 := &ModelSpec{
		Model:     "test-model",
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config:    map[string]interface{}{},
	}
	envs1100 := b.Env(spec1100)
	if _, ok := findEnv(envs1100, "ENABLE_ATTENTION_SLICING"); ok {
		t.Error("expected ENABLE_ATTENTION_SLICING to be absent for gfx1100")
	}

	// gfx1100 should get MIOPEN_FIND_MODE=2 (VAE decode crash workaround)
	if v, ok := findEnv(envs1100, "MIOPEN_FIND_MODE"); !ok {
		t.Error("expected MIOPEN_FIND_MODE for gfx1100 (ROCm/ROCm#4729 workaround)")
	} else if v != "2" {
		t.Errorf("MIOPEN_FIND_MODE = %q, want 2", v)
	}

	// gfx906 should NOT get MIOPEN_FIND_MODE
	if _, ok := findEnv(envs, "MIOPEN_FIND_MODE"); ok {
		t.Error("expected MIOPEN_FIND_MODE to be absent for gfx906")
	}
}

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
			name:      "AMD gfx1100 without env returns arch-specific image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantImage: "registry.harbor.lan/flexinfer/comfyui:rocm-gfx1100",
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
			name:      "AMD gfx906 without env returns arch-specific image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			wantImage: "registry.harbor.lan/flexinfer/comfyui:rocm-gfx906",
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
