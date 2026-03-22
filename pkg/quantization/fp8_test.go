package quantization

import (
	"strings"
	"testing"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestFP8JobBuilder_Validate_EdgeCases(t *testing.T) {
	builder := &FP8JobBuilder{}

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
			wantErr: "only handles FP8 format",
		},
		{
			name: "GPU not enabled",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatFP8,
				UseGPU: false,
			},
			wantErr: "requires useGPU=true",
		},
		{
			name: "invalid bits (4)",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatFP8,
				UseGPU: true,
				Bits:   int32Ptr(4),
			},
			wantErr: "unsupported bit width",
		},
		{
			name: "groupSize not allowed",
			spec: &aiv1alpha1.QuantizationSpec{
				Format:    aiv1alpha1.QuantizationFormatFP8,
				UseGPU:    true,
				GroupSize: int32Ptr(128),
			},
			wantErr: "does not use groupSize",
		},
		{
			name: "valid with defaults",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatFP8,
				UseGPU: true,
			},
			wantErr: "",
		},
		{
			name: "valid with explicit bits 8",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatFP8,
				UseGPU: true,
				Bits:   int32Ptr(8),
			},
			wantErr: "",
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

func TestFP8JobBuilder_BuildScript_Content(t *testing.T) {
	builder := &FP8JobBuilder{}

	t.Run("script structure", func(t *testing.T) {
		script := builder.buildScript("test-model", 8)
		if !strings.Contains(script, `MODEL_DIR="/cache/test-model"`) {
			t.Error("script missing MODEL_DIR")
		}
		if !strings.Contains(script, "BITS=8") {
			t.Error("script missing BITS")
		}
		if !strings.Contains(script, "fp8-b${BITS}") {
			t.Error("script missing output dir pattern")
		}
		if !strings.Contains(script, "trap cleanup EXIT") {
			t.Error("script missing cleanup trap")
		}
		if !strings.Contains(script, "termination-log") {
			t.Error("script missing termination-log output")
		}
		if !strings.Contains(script, "FP8_CONVERT_SCRIPT") {
			t.Error("script missing convert script env var")
		}
	})
}

func TestResolveImage_FP8(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		img := ResolveImage(ImageFormatFP8, "", "", "")
		if img != DefaultFP8Image {
			t.Errorf("ResolveImage(FP8) = %q, want %q", img, DefaultFP8Image)
		}
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv("FLEXINFER_QUANTIZER_FP8_IMAGE", "custom/fp8:v1")
		img := ResolveImage(ImageFormatFP8, "", "", "")
		if img != "custom/fp8:v1" {
			t.Errorf("ResolveImage(FP8) = %q, want custom", img)
		}
	})
}

func TestFP8JobBuilder_BuildJob_Defaults(t *testing.T) {
	builder := &FP8JobBuilder{}
	params := JobParams{
		Name:      "test-model",
		Namespace: "default",
		PVCName:   "test-pvc",
		ModelPath: "test-model",
		Spec: &aiv1alpha1.QuantizationSpec{
			Format: aiv1alpha1.QuantizationFormatFP8,
			UseGPU: true,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob error: %v", err)
	}

	if job.Name != "test-model-quantize" {
		t.Errorf("job name = %q, want test-model-quantize", job.Name)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != DefaultFP8Image {
		t.Errorf("image = %q, want %q", container.Image, DefaultFP8Image)
	}

	// Verify GPU resource request
	gpuQty := container.Resources.Requests["nvidia.com/gpu"]
	if gpuQty.String() != "1" {
		t.Errorf("GPU request = %q, want 1", gpuQty.String())
	}
}
