package quantization

import (
	"strings"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
)

func TestAWQJobBuilder_Validate_EdgeCases(t *testing.T) {
	builder := &AWQJobBuilder{}

	tests := []struct {
		name    string
		spec    *aiv1alpha2.QuantizationSpec
		wantErr string
	}{
		{
			name: "wrong format",
			spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatGPTQ,
				UseGPU: true,
			},
			wantErr: "only handles AWQ format",
		},
		{
			name: "GPU not enabled",
			spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatAWQ,
				UseGPU: false,
			},
			wantErr: "requires useGPU=true",
		},
		{
			name: "invalid bits (8)",
			spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatAWQ,
				UseGPU: true,
				Bits:   int32Ptr(8),
			},
			wantErr: "unsupported bit width",
		},
		{
			name: "zero group size",
			spec: &aiv1alpha2.QuantizationSpec{
				Format:    aiv1alpha2.QuantizationFormatAWQ,
				UseGPU:    true,
				GroupSize: int32Ptr(0),
			},
			wantErr: "groupSize must be > 0",
		},
		{
			name: "negative group size",
			spec: &aiv1alpha2.QuantizationSpec{
				Format:    aiv1alpha2.QuantizationFormatAWQ,
				UseGPU:    true,
				GroupSize: int32Ptr(-1),
			},
			wantErr: "groupSize must be > 0",
		},
		{
			name: "valid defaults",
			spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatAWQ,
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

func TestAWQJobBuilder_BuildEnv_Content(t *testing.T) {
	builder := &AWQJobBuilder{}

	findEnv := func(env []corev1.EnvVar, name string) string {
		for _, e := range env {
			if e.Name == name {
				return e.Value
			}
		}
		return ""
	}

	t.Run("default calibration values", func(t *testing.T) {
		env := builder.buildEnv("my-model", 4, 128, nil)
		if v := findEnv(env, "MODEL_DIR"); v != "/cache/my-model" {
			t.Errorf("MODEL_DIR = %q, want /cache/my-model", v)
		}
		if v := findEnv(env, "BITS"); v != "4" {
			t.Errorf("BITS = %q, want 4", v)
		}
		if v := findEnv(env, "GROUP_SIZE"); v != "128" {
			t.Errorf("GROUP_SIZE = %q, want 128", v)
		}
		if v := findEnv(env, "DATASET"); v != "mit-han-lab/pile-val-backup" {
			t.Errorf("DATASET = %q, want default dataset", v)
		}
		if v := findEnv(env, "N_PARALLEL_CALIB_SAMPLES"); v != "" {
			t.Errorf("N_PARALLEL_CALIB_SAMPLES should not be set for nil calibration, got %q", v)
		}
	})

	t.Run("custom calibration", func(t *testing.T) {
		customDataset := "custom/dataset"
		env := builder.buildEnv("test-model", 4, 64, &aiv1alpha2.CalibrationSpec{
			MaxSeqLen:             int32Ptr(2048),
			MaxSamples:            int32Ptr(64),
			NParallelCalibSamples: int32Ptr(8),
			Dataset:               &customDataset,
		})
		if v := findEnv(env, "MAX_SEQ_LEN"); v != "2048" {
			t.Errorf("MAX_SEQ_LEN = %q, want 2048", v)
		}
		if v := findEnv(env, "MAX_SAMPLES"); v != "64" {
			t.Errorf("MAX_SAMPLES = %q, want 64", v)
		}
		if v := findEnv(env, "N_PARALLEL_CALIB_SAMPLES"); v != "8" {
			t.Errorf("N_PARALLEL_CALIB_SAMPLES = %q, want 8", v)
		}
		if v := findEnv(env, "DATASET"); v != "custom/dataset" {
			t.Errorf("DATASET = %q, want custom/dataset", v)
		}
	})

	t.Run("cleanup trap present in wrapper", func(t *testing.T) {
		script := builder.awqWrapperScript()
		if !strings.Contains(script, "trap cleanup EXIT") {
			t.Error("script missing cleanup trap")
		}
		if !strings.Contains(script, "trap - EXIT") {
			t.Error("script missing trap disarm after success")
		}
	})
}

func TestResolveImage_AWQ(t *testing.T) {
	t.Run("default image", func(t *testing.T) {
		img := ResolveImage(ImageFormatAWQ, "", "", "")
		if img != DefaultAWQImage {
			t.Errorf("ResolveImage(AWQ) = %q, want %q", img, DefaultAWQImage)
		}
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv("FLEXINFER_QUANTIZER_AWQ_IMAGE", "custom/awq:latest")
		img := ResolveImage(ImageFormatAWQ, "", "", "")
		if img != "custom/awq:latest" {
			t.Errorf("ResolveImage(AWQ) = %q, want %q", img, "custom/awq:latest")
		}
	})

	t.Run("runtime override (was missing in old AWQ)", func(t *testing.T) {
		t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "true")
		t.Setenv("FLEXINFER_RUNTIME_IMAGE", "registry.harbor.lan/flexinfer/runtime:unified")
		img := ResolveImage(ImageFormatAWQ, "", "", "")
		if img != "registry.harbor.lan/flexinfer/runtime:unified" {
			t.Errorf("ResolveImage(AWQ) = %q, want runtime override", img)
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
		Spec: &aiv1alpha2.QuantizationSpec{
			Format: aiv1alpha2.QuantizationFormatAWQ,
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
