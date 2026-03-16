package quantization

import (
	"strings"
	"testing"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestAWQJobBuilder_Validate_EdgeCases(t *testing.T) {
	builder := &AWQJobBuilder{}

	tests := []struct {
		name    string
		spec    *aiv1alpha1.QuantizationSpec
		wantErr string
	}{
		{
			name: "wrong format",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatGPTQ,
				UseGPU: true,
			},
			wantErr: "only handles AWQ format",
		},
		{
			name: "GPU not enabled",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatAWQ,
				UseGPU: false,
			},
			wantErr: "requires useGPU=true",
		},
		{
			name: "invalid bits (8)",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatAWQ,
				UseGPU: true,
				Bits:   int32Ptr(8),
			},
			wantErr: "unsupported bit width",
		},
		{
			name: "zero group size",
			spec: &aiv1alpha1.QuantizationSpec{
				Format:    aiv1alpha1.QuantizationFormatAWQ,
				UseGPU:    true,
				GroupSize: int32Ptr(0),
			},
			wantErr: "groupSize must be > 0",
		},
		{
			name: "negative group size",
			spec: &aiv1alpha1.QuantizationSpec{
				Format:    aiv1alpha1.QuantizationFormatAWQ,
				UseGPU:    true,
				GroupSize: int32Ptr(-1),
			},
			wantErr: "groupSize must be > 0",
		},
		{
			name: "valid defaults",
			spec: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatAWQ,
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

func TestAWQJobBuilder_BuildScript_Content(t *testing.T) {
	builder := &AWQJobBuilder{}

	t.Run("default calibration values", func(t *testing.T) {
		script := builder.buildScript("my-model", 4, 128, nil)
		if !strings.Contains(script, `MODEL_DIR="/cache/my-model"`) {
			t.Error("script missing MODEL_DIR")
		}
		if !strings.Contains(script, "BITS=4") {
			t.Error("script missing BITS=4")
		}
		if !strings.Contains(script, "GROUP_SIZE=128") {
			t.Error("script missing GROUP_SIZE=128")
		}
		if !strings.Contains(script, "nParallel=None") {
			t.Error("script should show nParallel=None for nil calibration")
		}
		if !strings.Contains(script, "mit-han-lab/pile-val-backup") {
			t.Error("script missing default calibration dataset")
		}
	})

	t.Run("custom calibration", func(t *testing.T) {
		customDataset := "custom/dataset"
		script := builder.buildScript("test-model", 4, 64, &aiv1alpha1.CalibrationSpec{
			MaxSeqLen:             int32Ptr(2048),
			MaxSamples:            int32Ptr(64),
			NParallelCalibSamples: int32Ptr(8),
			Dataset:               &customDataset,
		})
		if !strings.Contains(script, "maxSeqLen=2048") {
			t.Error("script missing custom maxSeqLen")
		}
		if !strings.Contains(script, "maxSamples=64") {
			t.Error("script missing custom maxSamples")
		}
		if !strings.Contains(script, "nParallel=8") {
			t.Error("script missing custom nParallel")
		}
		if !strings.Contains(script, "custom/dataset") {
			t.Error("script missing custom dataset")
		}
		if !strings.Contains(script, "n_parallel_calib_samples=8") {
			t.Error("script missing n_parallel_calib_samples kwarg")
		}
	})

	t.Run("cleanup trap present", func(t *testing.T) {
		script := builder.buildScript("model", 4, 128, nil)
		if !strings.Contains(script, "trap cleanup EXIT") {
			t.Error("script missing cleanup trap")
		}
		if !strings.Contains(script, "trap - EXIT") {
			t.Error("script missing trap disarm after success")
		}
	})
}

func TestAWQQuantizerImage(t *testing.T) {
	t.Run("default image", func(t *testing.T) {
		img := awqQuantizerImage()
		if img != DefaultAWQImage {
			t.Errorf("awqQuantizerImage() = %q, want %q", img, DefaultAWQImage)
		}
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv("FLEXINFER_QUANTIZER_AWQ_IMAGE", "custom/awq:latest")
		img := awqQuantizerImage()
		if img != "custom/awq:latest" {
			t.Errorf("awqQuantizerImage() = %q, want %q", img, "custom/awq:latest")
		}
	})
}

func TestAWQJobBuilder_BuildJob_ProfileImageOverride(t *testing.T) {
	builder := &AWQJobBuilder{}
	params := JobParams{
		Name:      "test-model",
		Namespace: "default",
		PVCName:   "test-pvc",
		ModelPath: "test-model",
		Spec: &aiv1alpha1.QuantizationSpec{
			Format: aiv1alpha1.QuantizationFormatAWQ,
			UseGPU: true,
		},
		ProfileQuantizerImage: "custom-profile/quantizer:v1",
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob error: %v", err)
	}

	image := job.Spec.Template.Spec.Containers[0].Image
	if image != "custom-profile/quantizer:v1" {
		t.Errorf("image = %q, want profile override %q", image, "custom-profile/quantizer:v1")
	}
}
