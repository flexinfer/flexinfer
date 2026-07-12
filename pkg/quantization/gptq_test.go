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
		{
			name: "invalid dense module policy",
			spec: &aiv1alpha2.QuantizationSpec{
				Format:            aiv1alpha2.QuantizationFormatGPTQ,
				UseGPU:            true,
				DenseModulePolicy: stringPtrGPTQ("always"),
			},
			wantErr: "denseModulePolicy",
		},
		{
			name: "invalid dense module cosine threshold",
			spec: &aiv1alpha2.QuantizationSpec{
				Format:                     aiv1alpha2.QuantizationFormatGPTQ,
				UseGPU:                     true,
				DenseModuleCosineThreshold: stringPtrGPTQ("1.25"),
			},
			wantErr: "denseModuleCosineThreshold",
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
		env := builder.buildEnv("qwen3-14b", "gptq-w4-g128", 4, 128, true, false, 48, "0.80", "auto", "", 0, nil)
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
		if v := findEnv(env, "QUANTIZE_MODEL_POLICIES"); !strings.Contains(v, "qwen3.5-text") || !strings.Contains(v, "qwen3.5-moe-text") || !strings.Contains(v, "qwen3.6-text") {
			t.Errorf("QUANTIZE_MODEL_POLICIES = %q, want default Qwen policy JSON", v)
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

			var qwen36Policy map[string]any
			for _, policy := range policies {
				if policy["name"] == "qwen3.6-text" {
					qwen36Policy = policy
					break
				}
			}
			if qwen36Policy == nil {
				t.Fatalf("expected qwen3.6-text default policy in %v", policies)
			}
			qwen36Overrides, ok := qwen36Policy["quantize_config_overrides"].(map[string]any)
			if !ok {
				t.Fatalf("expected quantize_config_overrides in qwen3.6-text policy JSON")
			}
			if _, ok := qwen36Overrides["lm_head"]; ok {
				t.Fatalf("qwen3.6 lm_head override should be absent to avoid ROCm/CPU LAPACK fallback path: %v", qwen36Overrides)
			}

			var gemmaPolicy map[string]any
			for _, policy := range policies {
				if policy["name"] == "gemma4-text" {
					gemmaPolicy = policy
					break
				}
			}
			if gemmaPolicy == nil {
				t.Fatalf("expected gemma4-text default policy in %v", policies)
			}
			artifactOverrides, ok := gemmaPolicy["artifact_overrides"].(map[string]any)
			if !ok {
				t.Fatalf("expected artifact_overrides in gemma4-text policy JSON")
			}
			if got := artifactOverrides["preserve_native_output"]; got != true {
				t.Fatalf("gemma4 preserve_native_output = %v, want true", got)
			}
			if got := artifactOverrides["refuse_moe_expert_tensors"]; got != true {
				t.Fatalf("gemma4 refuse_moe_expert_tensors = %v, want true", got)
			}
		}
	})

	t.Run("sym false descAct true", func(t *testing.T) {
		env := builder.buildEnv("model", "gptq-w4-g128", 4, 128, false, true, 48, "0.80", "auto", "", 0, nil)
		if v := findEnv(env, "SYM"); v != "False" {
			t.Errorf("SYM = %q, want False", v)
		}
		if v := findEnv(env, "DESC_ACT"); v != "True" {
			t.Errorf("DESC_ACT = %q, want True", v)
		}
	})

	t.Run("dynamic exclusion none", func(t *testing.T) {
		env := builder.buildEnv("model", "gptq-w4-g128", 4, 128, true, false, 48, "0.80", "none", "", 0, nil)
		if v := findEnv(env, "DYNAMIC_EXCLUSION"); v != "none" {
			t.Errorf("DYNAMIC_EXCLUSION = %q, want none", v)
		}
	})

	t.Run("custom GPU memory fraction", func(t *testing.T) {
		env := builder.buildEnv("model", "gptq-w4-g128", 4, 128, true, false, 48, "0.95", "auto", "", 0, nil)
		if v := findEnv(env, "GPU_MEMORY_FRACTION"); v != "0.95" {
			t.Errorf("GPU_MEMORY_FRACTION = %q, want 0.95", v)
		}
	})

	t.Run("operator model policy override", func(t *testing.T) {
		t.Setenv("FLEXINFER_GPTQ_MODEL_POLICIES", `[{"name":"custom"}]`)
		env := builder.buildEnv("model", "gptq-w4-g128", 4, 128, true, false, 48, "0.80", "auto", "", 0, nil)
		if v := findEnv(env, "QUANTIZE_MODEL_POLICIES"); v != `[{"name":"custom"}]` {
			t.Errorf("QUANTIZE_MODEL_POLICIES = %q, want custom JSON", v)
		}
	})

	t.Run("resume defaults enabled", func(t *testing.T) {
		env := builder.buildEnv("model", "gptq-w4-g128", 4, 128, true, false, 48, "0.80", "auto", "", 0, nil)
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
		env := builder.buildEnv("model", "gptq-w4-g128", 4, 128, true, false, 48, "0.80", "auto", "", 0, nil)
		if v := findEnv(env, "GPTQ_RESUME"); v != "false" {
			t.Errorf("GPTQ_RESUME = %q, want false", v)
		}
		if v := findEnv(env, "GPTQ_CALIBRATION_CACHE"); v != "false" {
			t.Errorf("GPTQ_CALIBRATION_CACHE = %q, want false", v)
		}
	})

	t.Run("resume_layers default on", func(t *testing.T) {
		env := builder.buildEnv("model", "gptq-w4-g128", 4, 128, true, false, 48, "0.80", "auto", "", 0, nil)
		if v := findEnv(env, "GPTQ_RESUME_LAYERS"); v != "true" {
			t.Errorf("GPTQ_RESUME_LAYERS = %q, want true (default-on since 2026-06-11)", v)
		}
	})

	t.Run("resume_layers env override", func(t *testing.T) {
		t.Setenv("FLEXINFER_GPTQ_RESUME_LAYERS", "false")
		env := builder.buildEnv("model", "gptq-w4-g128", 4, 128, true, false, 48, "0.80", "auto", "", 0, nil)
		if v := findEnv(env, "GPTQ_RESUME_LAYERS"); v != "false" {
			t.Errorf("GPTQ_RESUME_LAYERS = %q, want false", v)
		}
	})

	t.Run("gfx906 defaults to cpu device map", func(t *testing.T) {
		env := builder.buildEnv("model", "gptq-w4-g128", 4, 128, true, false, 48, "0.80", "auto", "gfx906", 16384, nil)
		if v := findEnv(env, "QUANTIZE_DEVICE_MAP"); v != "cpu" {
			t.Errorf("QUANTIZE_DEVICE_MAP = %q, want cpu for gfx906", v)
		}
	})

	t.Run("gfx900 defaults to cpu device map", func(t *testing.T) {
		env := builder.buildEnv("model", "gptq-w4-g128", 4, 128, true, false, 48, "0.80", "auto", "gfx900", 16384, nil)
		if v := findEnv(env, "QUANTIZE_DEVICE_MAP"); v != "cpu" {
			t.Errorf("QUANTIZE_DEVICE_MAP = %q, want cpu for gfx900", v)
		}
	})

	t.Run("gfx906 device map override", func(t *testing.T) {
		t.Setenv("FLEXINFER_GPTQ_DEVICE_MAP", "auto")
		env := builder.buildEnv("model", "gptq-w4-g128", 4, 128, true, false, 48, "0.80", "auto", "gfx906", 16384, nil)
		if v := findEnv(env, "QUANTIZE_DEVICE_MAP"); v != "auto" {
			t.Errorf("QUANTIZE_DEVICE_MAP = %q, want env override auto", v)
		}
	})

	t.Run("wrapper script has version check", func(t *testing.T) {
		script := builder.gptqWrapperScript()
		if !strings.Contains(script, "mkdir -p /workspace") {
			t.Error("wrapper missing workspace sentinel bootstrap")
		}
		if !strings.Contains(script, "/workspace/quantize_gptq.py") {
			t.Error("wrapper missing quantize sentinel bootstrap")
		}
		if !strings.Contains(script, "EXPECTED_VERSION=") {
			t.Error("wrapper missing script version check")
		}
		if !strings.Contains(script, GPTQScriptVersion) {
			t.Errorf("wrapper does not contain current GPTQScriptVersion %q", GPTQScriptVersion)
		}
		if !strings.Contains(script, "FLEXINFER_SCRIPT_VERSION") {
			t.Error("wrapper missing FLEXINFER_SCRIPT_VERSION reference")
		}
		if !strings.Contains(script, "Script version mismatch") {
			t.Error("wrapper missing version mismatch error message")
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
		if !strings.Contains(script, "Patched GPTQModel save path to skip meta-backed tensors") {
			t.Error("wrapper missing save-path meta tensor patch")
		}
		if !strings.Contains(script, "Synthesized pad_token_id from eos_token_id") {
			t.Error("wrapper missing extracted text-config pad token fallback")
		}
		if !strings.Contains(script, "Disabled GPTQModel VLM processor loading") {
			t.Error("wrapper missing Qwen text-only processor bypass")
		}
	})

	t.Run("wrapper script has save-complete short-circuit", func(t *testing.T) {
		script := builder.gptqWrapperScript()
		if !strings.Contains(script, ".save-complete") {
			t.Error("wrapper missing .save-complete marker reference")
		}
		if !strings.Contains(script, "SAVE_COMPLETE=") {
			t.Error("wrapper missing SAVE_COMPLETE path var")
		}
		if !strings.Contains(script, "VERIFY_SAVE_COMPLETE") {
			t.Error("wrapper missing save-complete verification heredoc")
		}
		if !strings.Contains(script, "verified via .save-complete") {
			t.Error("wrapper missing save-complete verified message")
		}
		if !strings.Contains(script, "heuristic: no .save-complete marker") {
			t.Error("wrapper missing heuristic-fallback message")
		}
	})

	t.Run("wrapper script version is v20", func(t *testing.T) {
		if GPTQScriptVersion != "v20" {
			t.Errorf("GPTQScriptVersion = %q, want v20 (Qwen text-only processor bypass)", GPTQScriptVersion)
		}
	})

	t.Run("wrapper disables native CPU pack extension by default", func(t *testing.T) {
		script := builder.gptqWrapperScript()
		if !strings.Contains(script, "GPTQMODEL_DISABLE_PACK_EXT") {
			t.Error("wrapper should default GPTQMODEL_DISABLE_PACK_EXT for older CPU nodes")
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
			// gfx906 no longer hardcodes a separate default — the GPUProfile CR
			// is the source of truth. Without a profile or env override, the
			// generic ROCm default is returned.
			name:    "gfx906 falls back to generic rocm default without profile",
			gpuArch: "gfx906",
			wantImg: DefaultGPTQROCmImage,
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

func TestGPTQJobBuilder_BuildJob_DenseModuleValidationEnv(t *testing.T) {
	builder := &GPTQJobBuilder{}
	params := JobParams{
		Name:      "gemma4-26b-a4b-gptq-candidate",
		Namespace: "default",
		PVCName:   "test-pvc",
		ModelPath: "gemma4-26b-a4b-gptq-candidate",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format:                     aiv1alpha2.QuantizationFormatGPTQ,
			UseGPU:                     true,
			DenseModulePolicy:          stringPtrGPTQ("validate"),
			DenseModuleCosineThreshold: stringPtrGPTQ("0.995"),
		},
		GPUVendor: "amd",
		GPUArch:   "gfx1100",
	}

	job, err := builder.BuildJob(params)
	if err != nil {
		t.Fatalf("BuildJob error: %v", err)
	}

	env := map[string]string{}
	for _, item := range job.Spec.Template.Spec.Containers[0].Env {
		env[item.Name] = item.Value
	}
	for _, name := range []string{"DENSE_GPTQ_POLICY", "GEMMA4_DENSE_GPTQ_POLICY"} {
		if env[name] != "validate" {
			t.Fatalf("%s = %q, want validate", name, env[name])
		}
	}
	for _, name := range []string{"DENSE_GPTQ_COSINE_THRESHOLD", "GEMMA4_DENSE_GPTQ_COSINE_THRESHOLD"} {
		if env[name] != "0.995" {
			t.Fatalf("%s = %q, want 0.995", name, env[name])
		}
	}
}

func TestGPTQJobBuilder_BuildJob_Gemma4MoEHybridOutput(t *testing.T) {
	builder := &GPTQJobBuilder{}
	params := JobParams{
		Name:      "gemma4-26b-a4b-gptq",
		Namespace: "default",
		PVCName:   "test-pvc",
		ModelPath: "gemma4-26b-a4b-gptq",
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

	for _, env := range job.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "OUT_DIR" {
			want := "/cache/gemma4-26b-a4b-gptq/gptq-w4-g128-hybrid-v12"
			if env.Value != want {
				t.Fatalf("OUT_DIR = %q, want %q", env.Value, want)
			}
			return
		}
	}
	t.Fatal("missing OUT_DIR env var")
}

func stringPtrGPTQ(v string) *string { return &v }
