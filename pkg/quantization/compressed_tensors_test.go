package quantization

import (
	"strings"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestCompressedTensorsJobBuilder_Validate(t *testing.T) {
	builder := &CompressedTensorsJobBuilder{}

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
			wantErr: "only handles COMPRESSED_TENSORS format",
		},
		{
			name: "gpu required",
			spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatCompressedTensors,
			},
			wantErr: "requires useGPU=true",
		},
		{
			name: "invalid bits",
			spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatCompressedTensors,
				Bits:   int32Ptr(8),
				UseGPU: true,
			},
			wantErr: "unsupported bit width",
		},
		{
			name: "invalid group size",
			spec: &aiv1alpha2.QuantizationSpec{
				Format:    aiv1alpha2.QuantizationFormatCompressedTensors,
				Bits:      int32Ptr(4),
				GroupSize: int32Ptr(64),
				UseGPU:    true,
			},
			wantErr: "groupSize must be 128",
		},
		{
			name: "valid w4a16",
			spec: &aiv1alpha2.QuantizationSpec{
				Format:    aiv1alpha2.QuantizationFormatCompressedTensors,
				Bits:      int32Ptr(4),
				GroupSize: int32Ptr(128),
				UseGPU:    true,
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := builder.Validate(tt.spec)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCompressedTensorsJobBuilder_BuildJob_GuardedByConfig(t *testing.T) {
	builder := &CompressedTensorsJobBuilder{}

	params := JobParams{
		Name:      "ct-model",
		Namespace: "default",
		PVCName:   "ct-model-pvc",
		ModelPath: "ct-model",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatCompressedTensors,
			Bits:      int32Ptr(4),
			GroupSize: int32Ptr(128),
			UseGPU:    true,
		},
	}

	if _, err := builder.BuildJob(params); err == nil {
		t.Fatal("BuildJob() should fail when image/command are not configured")
	} else if !strings.Contains(err.Error(), "not configured for job execution") {
		t.Fatalf("BuildJob() error = %v, want not-configured error", err)
	}
}

func TestCompressedTensorsJobBuilder_BuildJob_Configured(t *testing.T) {
	t.Setenv("FLEXINFER_QUANTIZER_COMPRESSED_TENSORS_IMAGE", "registry.example/quantizer:ct")
	t.Setenv("FLEXINFER_COMPRESSED_TENSORS_COMMAND", "python3 /opt/flexinfer/scripts/run_llmcompressor.py")

	builder := &CompressedTensorsJobBuilder{}
	params := JobParams{
		Name:      "ct-model",
		Namespace: "default",
		PVCName:   "ct-model-pvc",
		ModelPath: "ct-model",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatCompressedTensors,
			Bits:      int32Ptr(4),
			GroupSize: int32Ptr(128),
			UseGPU:    true,
		},
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob() error = %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	if got := container.Image; got != "registry.example/quantizer:ct" {
		t.Fatalf("image = %q, want configured image", got)
	}

	env := containerEnvMap(container.Env)
	if got := env["OUT_DIR"]; got != "/cache/ct-model/compressed-tensors-w4-g128" {
		t.Fatalf("OUT_DIR = %q, want compressed-tensors-w4-g128 path", got)
	}
	if got := env["TYPE"]; got != "W4A16_G128" {
		t.Fatalf("TYPE = %q, want W4A16_G128", got)
	}
	if got := env["COMPRESSED_TENSORS_COMMAND"]; got != "python3 /opt/flexinfer/scripts/run_llmcompressor.py" {
		t.Fatalf("COMPRESSED_TENSORS_COMMAND = %q, want configured command", got)
	}
}

func TestCompressedTensorsNamingHelpers(t *testing.T) {
	if got := CompressedTensorsType(4, 128); got != "W4A16_G128" {
		t.Fatalf("CompressedTensorsType() = %q, want W4A16_G128", got)
	}
	if got := CompressedTensorsOutputSubdir(4, 128); got != "compressed-tensors-w4-g128" {
		t.Fatalf("CompressedTensorsOutputSubdir() = %q, want compressed-tensors-w4-g128", got)
	}
}
