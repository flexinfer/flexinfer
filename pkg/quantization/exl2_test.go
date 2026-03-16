package quantization

import (
	"strings"
	"testing"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestEXL2JobBuilder_Validate_EdgeCases(t *testing.T) {
	builder := &EXL2JobBuilder{}

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
			wantErr: "only handles EXL2 format",
		},
		{
			name: "GPU not enabled",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatEXL2,
				UseGPU: false,
			},
			wantErr: "requires useGPU=true",
		},
		{
			name: "bits below minimum (1)",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatEXL2,
				UseGPU: true,
				Bits:   int32Ptr(1),
			},
			wantErr: "unsupported bit width",
		},
		{
			name: "bits above maximum (7)",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatEXL2,
				UseGPU: true,
				Bits:   int32Ptr(7),
			},
			wantErr: "unsupported bit width",
		},
		{
			name: "valid bits at minimum (2)",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatEXL2,
				UseGPU: true,
				Bits:   int32Ptr(2),
			},
			wantErr: "",
		},
		{
			name: "valid bits at maximum (6)",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatEXL2,
				UseGPU: true,
				Bits:   int32Ptr(6),
			},
			wantErr: "",
		},
		{
			name: "valid with defaults",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatEXL2,
				UseGPU: true,
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

func TestEXL2JobBuilder_BuildScript_Content(t *testing.T) {
	builder := &EXL2JobBuilder{}

	t.Run("script structure", func(t *testing.T) {
		script := builder.buildScript("test-model", 4)
		if !strings.Contains(script, `MODEL_DIR="/cache/test-model"`) {
			t.Error("script missing MODEL_DIR")
		}
		if !strings.Contains(script, "BITS=4") {
			t.Error("script missing BITS")
		}
		if !strings.Contains(script, "exllamav2") {
			t.Error("script should reference exllamav2")
		}
		if !strings.Contains(script, "trap cleanup EXIT") {
			t.Error("script missing cleanup trap")
		}
		if !strings.Contains(script, "termination-log") {
			t.Error("script missing termination-log output")
		}
	})
}

func TestEXL2QuantizerImage(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		img := exl2QuantizerImage()
		if img != DefaultEXL2Image {
			t.Errorf("exl2QuantizerImage() = %q, want %q", img, DefaultEXL2Image)
		}
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv("FLEXINFER_QUANTIZER_EXL2_IMAGE", "custom/exl2:v1")
		img := exl2QuantizerImage()
		if img != "custom/exl2:v1" {
			t.Errorf("exl2QuantizerImage() = %q, want custom", img)
		}
	})
}

func TestEXL2JobBuilder_BuildJob_Defaults(t *testing.T) {
	builder := &EXL2JobBuilder{}
	params := JobParams{
		Name:      "test-model",
		Namespace: "default",
		PVCName:   "test-pvc",
		ModelPath: "test-model",
		Spec: &aiv1alpha1.QuantizationSpec{
			Format: aiv1alpha1.QuantizationFormatEXL2,
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
	if container.Image != DefaultEXL2Image {
		t.Errorf("image = %q, want %q", container.Image, DefaultEXL2Image)
	}

	// Verify GPU resource request
	gpuQty := container.Resources.Requests["nvidia.com/gpu"]
	if gpuQty.String() != "1" {
		t.Errorf("GPU request = %q, want 1", gpuQty.String())
	}
}
