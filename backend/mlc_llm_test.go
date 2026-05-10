package backend

import (
	"strings"
	"testing"
)

func TestMLCLLMBackendArgs_ModeServerMapsToServe(t *testing.T) {
	b := &MLCLLMBackend{}

	spec := &ModelSpec{
		Model: "my-model",
		Config: map[string]any{
			"mode": "server",
		},
	}

	args := b.Args(spec)
	if len(args) == 0 || args[0] != "serve" {
		t.Fatalf("expected args[0] to be %q, got %#v", "serve", args)
	}
}

func TestMLCLLMBackendArgs_DefaultModeIsServe(t *testing.T) {
	b := &MLCLLMBackend{}

	spec := &ModelSpec{
		Model: "my-model",
	}

	args := b.Args(spec)
	if len(args) == 0 || args[0] != "serve" {
		t.Fatalf("expected args[0] to be %q, got %#v", "serve", args)
	}
}

func TestMLCLLMBackendArgs_UsesMaxTotalSeqLengthOverrideKey(t *testing.T) {
	b := &MLCLLMBackend{}

	spec := &ModelSpec{
		Model: "my-model",
		Config: map[string]any{
			"maxNumSequence":    2,
			"maxTotalSeqLength": 32768,
		},
	}

	args := b.Args(spec)
	joined := strings.Join(args, " ")
	if want := "max_total_seq_length=32768"; !strings.Contains(joined, want) {
		t.Fatalf("expected args to contain %q, got %#v", want, args)
	}
	if bad := "max_total_sequence_length="; strings.Contains(joined, bad) {
		t.Fatalf("expected args to not contain legacy key %q, got %#v", bad, args)
	}
}

func TestMLCLLMBackendImage_GFX1100(t *testing.T) {
	b := &MLCLLMBackend{}

	tests := []struct {
		name      string
		gpuVendor GPUVendor
		gpuArch   string
		wantImage string
	}{
		{
			// Per-arch image now lives in deploy/gpuprofiles/gfx1100.yaml; the
			// rule slice is env-only and falls through to the AMD-generic
			// default when neither env nor profile applies.
			name:      "AMD gfx1100 falls through to AMD generic ROCm image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantImage: "ghcr.io/mlc-ai/mlc-llm:rocm",
		},
		{
			name:      "AMD gfx1101 falls through to AMD generic ROCm image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1101",
			wantImage: "ghcr.io/mlc-ai/mlc-llm:rocm",
		},
		{
			name:      "AMD gfx1102 falls through to AMD generic ROCm image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1102",
			wantImage: "ghcr.io/mlc-ai/mlc-llm:rocm",
		},
		{
			name:      "AMD gfx942 returns generic ROCm image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx942",
			wantImage: "ghcr.io/mlc-ai/mlc-llm:rocm",
		},
		{
			name:      "NVIDIA sm_52 falls through to generic CUDA without profile",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_52",
			wantImage: "ghcr.io/mlc-ai/mlc-llm:cuda",
		},
		{
			name:      "NVIDIA sm_89 returns generic CUDA image",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_89",
			wantImage: "ghcr.io/mlc-ai/mlc-llm:cuda",
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

func TestMLCLLMBackend_MaxwellDefaults(t *testing.T) {
	b := &MLCLLMBackend{}

	spec := &ModelSpec{
		ModelPath: "/models/qwen3-0.6b",
		GPUVendor: GPUVendorNVIDIA,
		GPUArch:   "sm_52",
	}

	// Args should default to READONLY + conventional library path.
	args := b.Args(spec)
	joined := strings.Join(args, " ")
	if want := "--model-lib /models/qwen3-0.6b/maxwell-lib.so"; !strings.Contains(joined, want) {
		t.Fatalf("expected args to contain %q, got %#v", want, args)
	}

	// Env should default GPU memory size to ~5GB and set READONLY.
	env := b.Env(spec)
	var gotGPUSize, gotJIT string
	for _, e := range env {
		switch e.Name {
		case "MLC_GPU_SIZE_BYTES":
			gotGPUSize = e.Value
		case "MLC_JIT_POLICY":
			gotJIT = e.Value
		}
	}
	if gotGPUSize != "5000000000" {
		t.Fatalf("expected MLC_GPU_SIZE_BYTES=5000000000, got %q", gotGPUSize)
	}
	if gotJIT != "READONLY" {
		t.Fatalf("expected MLC_JIT_POLICY=READONLY, got %q", gotJIT)
	}
}
