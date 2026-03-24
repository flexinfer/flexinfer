package quantization

import (
	"encoding/json"
	"strings"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
)

func TestGPTQJobBuilder_Validate_EdgeCases(t *testing.T) {
	builder := &GPTQJobBuilder{}

	tests := []struct {
		name    string
		spec    *aiv1alpha2.QuantizationSpec
		wantErr string
	}{
		{
			name: "wrong format",
			spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatAWQ,
				UseGPU: true,
			},
			wantErr: "only handles GPTQ format",
		},
		{
			name: "GPU not enabled",
			spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatGPTQ,
				UseGPU: false,
			},
			wantErr: "requires useGPU=true",
		},
		{
			name: "invalid bits (3)",
			spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatGPTQ,
				UseGPU: true,
				Bits:   int32Ptr(3),
			},
			wantErr: "unsupported bit width",
		},
		{
			name: "valid bits 4",
			spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatGPTQ,
				UseGPU: true,
				Bits:   int32Ptr(4),
			},
			wantErr: "",
		},
		{
			name: "valid bits 8",
			spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatGPTQ,
				UseGPU: true,
				Bits:   int32Ptr(8),
			},
			wantErr: "",
		},
		{
			name: "zero group size",
			spec: &aiv1alpha2.QuantizationSpec{
				Format:    aiv1alpha2.QuantizationFormatGPTQ,
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

func TestGPTQJobBuilder_BuildEnv_Content(t *testing.T) {
	builder := &GPTQJobBuilder{}

	findEnv := func(env []corev1.EnvVar, name string) string {
		for _, e := range env {
			if e.Name == name {
				return e.Value
			}
		}
		return ""
	}

	t.Run("default values", func(t *testing.T) {
		env := builder.buildEnv("qwen3-14b", 4, 128, true, false, 48, "0.80", "auto", "", nil)
		if v := findEnv(env, "MODEL_DIR"); v != "/cache/qwen3-14b" {
			t.Errorf("MODEL_DIR = %q, want /cache/qwen3-14b", v)
		}
		if v := findEnv(env, "BITS"); v != "4" {
			t.Errorf("BITS = %q, want 4", v)
		}
		if v := findEnv(env, "MAX_MEMORY_GB"); v != "48" {
			t.Errorf("MAX_MEMORY_GB = %q, want 48", v)
		}
		if v := findEnv(env, "SYM"); v != "True" {
			t.Errorf("SYM = %q, want True", v)
		}
		if v := findEnv(env, "DESC_ACT"); v != "False" {
			t.Errorf("DESC_ACT = %q, want False", v)
		}
		if v := findEnv(env, "GPU_MEMORY_FRACTION"); v != "0.80" {
			t.Errorf("GPU_MEMORY_FRACTION = %q, want 0.80", v)
		}
		if v := findEnv(env, "DYNAMIC_EXCLUSION"); v != "auto" {
			t.Errorf("DYNAMIC_EXCLUSION = %q, want auto", v)
		}
		if v := findEnv(env, "QUANTIZE_MODEL_POLICIES"); !strings.Contains(v, "qwen3.5-text") {
			t.Errorf("QUANTIZE_MODEL_POLICIES = %q, want default qwen3.5 policy JSON", v)
		} else {
			var policies []map[string]any
			if err := json.Unmarshal([]byte(v), &policies); err != nil {
				t.Fatalf("unmarshal policy JSON: %v", err)
			}
			if len(policies) == 0 {
				t.Fatalf("expected at least one default policy")
			}
			overrides, ok := policies[0]["calibration_overrides"].(map[string]any)
			if !ok {
				t.Fatalf("expected calibration_overrides in default policy JSON")
			}
			if got := int(overrides["max_samples"].(float64)); got != 16 {
				t.Fatalf("default max_samples override = %d, want 16", got)
			}
			if got := int(overrides["max_seq_len"].(float64)); got != 512 {
				t.Fatalf("default max_seq_len override = %d, want 512", got)
			}
			if got := int(overrides["max_tokens"].(float64)); got != 8192 {
				t.Fatalf("default max_tokens override = %d, want 8192", got)
			}
			runtimeOverrides, ok := policies[0]["runtime_overrides"].(map[string]any)
			if !ok {
				t.Fatalf("expected runtime_overrides in default policy JSON")
			}
			if got := runtimeOverrides["attn_implementation"]; got != "eager" {
				t.Fatalf("default attn_implementation override = %v, want eager", got)
			}
			if got := runtimeOverrides["disable_qwen35_fla"]; got != true {
				t.Fatalf("default disable_qwen35_fla override = %v, want true", got)
			}
			if got := runtimeOverrides["fix_mistral_regex"]; got != true {
				t.Fatalf("default fix_mistral_regex override = %v, want true", got)
			}
		}
	})

	t.Run("sym false descAct true", func(t *testing.T) {
		env := builder.buildEnv("model", 4, 128, false, true, 48, "0.80", "auto", "", nil)
		if v := findEnv(env, "SYM"); v != "False" {
			t.Errorf("SYM = %q, want False", v)
		}
		if v := findEnv(env, "DESC_ACT"); v != "True" {
			t.Errorf("DESC_ACT = %q, want True", v)
		}
	})

	t.Run("dynamic exclusion none", func(t *testing.T) {
		env := builder.buildEnv("model", 4, 128, true, false, 48, "0.80", "none", "", nil)
		if v := findEnv(env, "DYNAMIC_EXCLUSION"); v != "none" {
			t.Errorf("DYNAMIC_EXCLUSION = %q, want none", v)
		}
	})

	t.Run("custom GPU memory fraction", func(t *testing.T) {
		env := builder.buildEnv("model", 4, 128, true, false, 48, "0.95", "auto", "", nil)
		if v := findEnv(env, "GPU_MEMORY_FRACTION"); v != "0.95" {
			t.Errorf("GPU_MEMORY_FRACTION = %q, want 0.95", v)
		}
	})

	t.Run("operator model policy override", func(t *testing.T) {
		t.Setenv("FLEXINFER_GPTQ_MODEL_POLICIES", `[{"name":"custom"}]`)
		env := builder.buildEnv("model", 4, 128, true, false, 48, "0.80", "auto", "", nil)
		if v := findEnv(env, "QUANTIZE_MODEL_POLICIES"); v != `[{"name":"custom"}]` {
			t.Errorf("QUANTIZE_MODEL_POLICIES = %q, want custom JSON", v)
		}
	})

	t.Run("resume defaults enabled", func(t *testing.T) {
		env := builder.buildEnv("model", 4, 128, true, false, 48, "0.80", "auto", "", nil)
		if v := findEnv(env, "GPTQ_RESUME"); v != "true" {
			t.Errorf("GPTQ_RESUME = %q, want true", v)
		}
		if v := findEnv(env, "GPTQ_CALIBRATION_CACHE"); v != "true" {
			t.Errorf("GPTQ_CALIBRATION_CACHE = %q, want true", v)
		}
	})

	t.Run("resume env overrides", func(t *testing.T) {
		t.Setenv("FLEXINFER_GPTQ_RESUME", "false")
		t.Setenv("FLEXINFER_GPTQ_CALIBRATION_CACHE", "false")
		env := builder.buildEnv("model", 4, 128, true, false, 48, "0.80", "auto", "", nil)
		if v := findEnv(env, "GPTQ_RESUME"); v != "false" {
			t.Errorf("GPTQ_RESUME = %q, want false", v)
		}
		if v := findEnv(env, "GPTQ_CALIBRATION_CACHE"); v != "false" {
			t.Errorf("GPTQ_CALIBRATION_CACHE = %q, want false", v)
		}
	})

	t.Run("wrapper script has ROCm detection", func(t *testing.T) {
		script := builder.gptqWrapperScript()
		if !strings.Contains(script, "HSA_OVERRIDE_GFX_VERSION=9.0.6") {
			t.Error("wrapper missing gfx900 ISA override")
		}
	})

	t.Run("wrapper script has GPTQModel patch", func(t *testing.T) {
		script := builder.gptqWrapperScript()
		if !strings.Contains(script, "Patched GPTQModel writer.py") {
			t.Error("wrapper missing writer.py patch")
		}
	})
}

func TestResolveImage_GPTQ(t *testing.T) {
	t.Run("default CUDA image", func(t *testing.T) {
		img := ResolveImage(ImageFormatGPTQ, "", "", "")
		if img != DefaultGPTQImage {
			t.Errorf("ResolveImage(GPTQ) = %q, want %q", img, DefaultGPTQImage)
		}
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv("FLEXINFER_QUANTIZER_GPTQ_IMAGE", "custom/gptq:v2")
		img := ResolveImage(ImageFormatGPTQ, "", "", "")
		if img != "custom/gptq:v2" {
			t.Errorf("ResolveImage(GPTQ) = %q, want custom", img)
		}
	})
}

func TestResolveImage_GPTQ_ROCm(t *testing.T) {
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
			got := ResolveImage(ImageFormatGPTQ, "", "amd", tt.gpuArch)
			if got != tt.wantImg {
				t.Errorf("ResolveImage(GPTQ, amd, %q) = %q, want %q", tt.gpuArch, got, tt.wantImg)
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
		Spec: &aiv1alpha2.QuantizationSpec{
			Format: aiv1alpha2.QuantizationFormatGPTQ,
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

	// Verify AMD allocator env var is set.
	found := false
	for _, env := range job.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "PYTORCH_ALLOC_CONF" {
			found = true
			if env.Value != rocmAllocatorConfig {
				t.Errorf("PYTORCH_ALLOC_CONF = %q, want %q", env.Value, rocmAllocatorConfig)
			}
		}
	}
	if !found {
		t.Error("missing PYTORCH_ALLOC_CONF env var for AMD")
	}
}
