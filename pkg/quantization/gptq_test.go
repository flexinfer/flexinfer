package quantization

import (
	"strings"
	"testing"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestGPTQJobBuilder_Validate_EdgeCases(t *testing.T) {
	builder := &GPTQJobBuilder{}

	tests := []struct {
		name    string
		spec    *aiv1alpha1.QuantizationSpec
		wantErr string
	}{
		{
			name: "wrong format",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatAWQ,
				UseGPU: true,
			},
			wantErr: "only handles GPTQ format",
		},
		{
			name: "GPU not enabled",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatGPTQ,
				UseGPU: false,
			},
			wantErr: "requires useGPU=true",
		},
		{
			name: "invalid bits (3)",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatGPTQ,
				UseGPU: true,
				Bits:   int32Ptr(3),
			},
			wantErr: "unsupported bit width",
		},
		{
			name: "valid bits 4",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatGPTQ,
				UseGPU: true,
				Bits:   int32Ptr(4),
			},
			wantErr: "",
		},
		{
			name: "valid bits 8",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatGPTQ,
				UseGPU: true,
				Bits:   int32Ptr(8),
			},
			wantErr: "",
		},
		{
			name: "zero group size",
			spec: &aiv1alpha1.QuantizationSpec{
				Format:    aiv1alpha1.QuantizationFormatGPTQ,
				UseGPU:    true,
				GroupSize: int32Ptr(0),
			},
			wantErr: "groupSize must be > 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := builder.Validate(tt.spec)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestGPTQJobBuilder_BuildScript_Content(t *testing.T) {
	builder := &GPTQJobBuilder{}

	t.Run("default values", func(t *testing.T) {
		script := builder.buildScript("qwen3-14b", 4, 128, true, false, 48, "0.80", "auto", nil)
		if !strings.Contains(script, `MODEL_DIR="/cache/qwen3-14b"`) {
			t.Error("script missing MODEL_DIR")
		}
		if !strings.Contains(script, "BITS=4") {
			t.Error("script missing BITS")
		}
		if !strings.Contains(script, "MAX_MEMORY_GB=48") {
			t.Error("script missing MAX_MEMORY_GB")
		}
		if !strings.Contains(script, "sym=True") {
			t.Error("script should have sym=True")
		}
		if !strings.Contains(script, "descAct=False") {
			t.Error("script should have descAct=False")
		}
		if !strings.Contains(script, "text_config") {
			t.Error("script missing VLM config extraction")
		}
		if !strings.Contains(script, "auto-detect hybrid") {
			t.Error("script should contain auto dynamic exclusion")
		}
	})

	t.Run("sym false descAct true", func(t *testing.T) {
		script := builder.buildScript("model", 4, 128, false, true, 48, "0.80", "auto", nil)
		if !strings.Contains(script, "sym=False") {
			t.Error("script should have sym=False")
		}
		if !strings.Contains(script, "descAct=True") {
			t.Error("script should have descAct=True")
		}
	})

	t.Run("dynamic exclusion none", func(t *testing.T) {
		script := builder.buildScript("model", 4, 128, true, false, 48, "0.80", "none", nil)
		if !strings.Contains(script, "Dynamic exclusion: disabled") {
			t.Error("script should disable dynamic exclusion")
		}
		if strings.Contains(script, "auto-detect hybrid") {
			t.Error("script should NOT contain auto dynamic exclusion when mode=none")
		}
	})

	t.Run("rocminfo gfx900 detection", func(t *testing.T) {
		script := builder.buildScript("model", 4, 128, true, false, 48, "0.80", "auto", nil)
		if !strings.Contains(script, "HSA_OVERRIDE_GFX_VERSION=9.0.6") {
			t.Error("script missing gfx900 ISA override")
		}
	})

	t.Run("GPTQModel writer.py patch", func(t *testing.T) {
		script := builder.buildScript("model", 4, 128, true, false, 48, "0.80", "auto", nil)
		if !strings.Contains(script, "Patched GPTQModel writer.py") {
			t.Error("script missing writer.py patch")
		}
	})

	t.Run("custom GPU memory fraction", func(t *testing.T) {
		script := builder.buildScript("model", 4, 128, true, false, 48, "0.95", "auto", nil)
		if !strings.Contains(script, "GPU memory fraction: 0.95") {
			t.Error("script should show custom GPU memory fraction")
		}
	})
}

func TestGPTQQuantizerImage(t *testing.T) {
	t.Run("default CUDA image", func(t *testing.T) {
		img := gptqQuantizerImage()
		if img != DefaultGPTQImage {
			t.Errorf("gptqQuantizerImage() = %q, want %q", img, DefaultGPTQImage)
		}
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv("FLEXINFER_QUANTIZER_GPTQ_IMAGE", "custom/gptq:v2")
		img := gptqQuantizerImage()
		if img != "custom/gptq:v2" {
			t.Errorf("gptqQuantizerImage() = %q, want custom", img)
		}
	})
}

func TestGPTQQuantizerROCmImage(t *testing.T) {
	tests := []struct {
		name    string
		gpuArch string
		envVars map[string]string
		wantImg string
	}{
		{
			name:    "gfx1100 default",
			gpuArch: "gfx1100",
			wantImg: DefaultGPTQROCmImage,
		},
		{
			name:    "gfx906 default",
			gpuArch: "gfx906",
			wantImg: DefaultGPTQROCmGFX906Image,
		},
		{
			name:    "empty arch falls to generic",
			gpuArch: "",
			wantImg: DefaultGPTQROCmImage,
		},
		{
			name:    "arch-specific env var override for gfx1100",
			gpuArch: "gfx1100",
			envVars: map[string]string{
				"FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX1100_IMAGE": "custom/gptq:gfx1100",
			},
			wantImg: "custom/gptq:gfx1100",
		},
		{
			name:    "arch-specific env var override for gfx906",
			gpuArch: "gfx906",
			envVars: map[string]string{
				"FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX906_IMAGE": "custom/gptq:gfx906",
			},
			wantImg: "custom/gptq:gfx906",
		},
		{
			name:    "generic ROCm env var override",
			gpuArch: "gfx1100",
			envVars: map[string]string{
				"FLEXINFER_QUANTIZER_GPTQ_ROCM_IMAGE": "custom/gptq:rocm-generic",
			},
			wantImg: "custom/gptq:rocm-generic",
		},
		{
			name:    "arch-specific takes priority over generic",
			gpuArch: "gfx1100",
			envVars: map[string]string{
				"FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX1100_IMAGE": "specific",
				"FLEXINFER_QUANTIZER_GPTQ_ROCM_IMAGE":         "generic",
			},
			wantImg: "specific",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}
			got := gptqQuantizerROCmImage(tt.gpuArch)
			if got != tt.wantImg {
				t.Errorf("gptqQuantizerROCmImage(%q) = %q, want %q", tt.gpuArch, got, tt.wantImg)
			}
		})
	}
}

func TestGPTQJobBuilder_BuildJob_AMDImage(t *testing.T) {
	builder := &GPTQJobBuilder{}
	params := JobParams{
		Name:      "test-model",
		Namespace: "default",
		PVCName:   "test-pvc",
		ModelPath: "test-model",
		Spec: &aiv1alpha1.QuantizationSpec{
			Format: aiv1alpha1.QuantizationFormatGPTQ,
			UseGPU: true,
		},
		GPUVendor: "amd",
		GPUArch:   "gfx1100",
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob error: %v", err)
	}

	image := job.Spec.Template.Spec.Containers[0].Image
	if image != DefaultGPTQROCmImage {
		t.Errorf("image = %q, want AMD ROCm image %q", image, DefaultGPTQROCmImage)
	}

	// Verify AMD HIP alloc conf env var is set
	found := false
	for _, env := range job.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "PYTORCH_HIP_ALLOC_CONF" {
			found = true
			if env.Value != "expandable_segments:True" {
				t.Errorf("PYTORCH_HIP_ALLOC_CONF = %q, want expandable_segments:True", env.Value)
			}
		}
	}
	if !found {
		t.Error("missing PYTORCH_HIP_ALLOC_CONF env var for AMD")
	}
}
