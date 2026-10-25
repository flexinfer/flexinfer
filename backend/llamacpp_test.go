package backend

import (
	"slices"
	"strings"
	"testing"
)

func TestLlamaCppBackendArgs_ReasoningAndDevicePassThrough(t *testing.T) {
	b := &LlamaCppBackend{}
	spec := &ModelSpec{
		ModelPath: "/models/test/model.gguf",
		GPUVendor: GPUVendorAMD,
		Config: map[string]any{
			"gpuDeviceOrdinal": "0",
			"reasoningFormat":  "none",
			"reasoningBudget":  float64(0),
		},
	}

	args := b.Args(spec)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--device 0") {
		t.Fatalf("expected args to contain %q, got %#v", "--device 0", args)
	}
	if !strings.Contains(joined, "--reasoning-format none") {
		t.Fatalf("expected args to contain %q, got %#v", "--reasoning-format none", args)
	}
	if !strings.Contains(joined, "--reasoning-budget 0") {
		t.Fatalf("expected args to contain %q, got %#v", "--reasoning-budget 0", args)
	}
}

func TestLlamaCppBackendArgs_DeviceTakesPrecedenceOverOrdinal(t *testing.T) {
	b := &LlamaCppBackend{}
	spec := &ModelSpec{
		ModelPath: "/models/test/model.gguf",
		GPUVendor: GPUVendorAMD,
		Config: map[string]any{
			"device":           "2",
			"gpuDeviceOrdinal": "0",
		},
	}

	args := b.Args(spec)
	device := argValue(args, "--device")
	if device != "2" {
		t.Fatalf("expected --device 2, got %q in args %#v", device, args)
	}
}

func TestLlamaCppBackendArgs_PortOverride(t *testing.T) {
	b := &LlamaCppBackend{}
	spec := &ModelSpec{
		ModelPath: "/models/test/model.gguf",
		Config: map[string]any{
			"port": float64(8000),
		},
	}

	args := b.Args(spec)
	port := argValue(args, "--port")
	if port != "8000" {
		t.Fatalf("expected --port 8000, got %q in args %#v", port, args)
	}
}

func TestLlamaCppBackendArgs_StatefulSlotsAndLocalSpeculation(t *testing.T) {
	b := &LlamaCppBackend{}
	spec := &ModelSpec{
		ModelPath: "/models/test/model.gguf",
		GPUVendor: GPUVendorAMD,
		Config: map[string]any{
			"slotSavePath": "/models/.flexinfer/slots/test",
			"specType":     "ngram-simple",
			"draftMax":     float64(16),
			"draftMin":     float64(1),
		},
	}

	args := b.Args(spec)
	for key, want := range map[string]string{
		"--slot-save-path": "/models/.flexinfer/slots/test",
		"--spec-type":      "ngram-simple",
		"--draft-max":      "16",
		"--draft-min":      "1",
	} {
		if got := argValue(args, key); got != want {
			t.Errorf("%s = %q, want %q in args %#v", key, got, want, args)
		}
	}
}

// TestLlamaCppBackendArgs_DraftWindowSpelling pins the b8955 rename: draft-model
// spec types always use --spec-draft-n-*, n-gram types keep --draft-* only on
// pre-b8955 builds and get no draft flags after it, and an unset build keeps
// the legacy spelling of the certified gfx906 b8173 envelope.
func TestLlamaCppBackendArgs_DraftWindowSpelling(t *testing.T) {
	draftFlags := []string{"--draft-max", "--draft-min", "--spec-draft-n-max", "--spec-draft-n-min"}
	tests := []struct {
		name   string
		config map[string]any
		want   map[string]string
	}{
		{
			name:   "draft-mtp max only uses the renamed max flag",
			config: map[string]any{"specType": "draft-mtp", "draftMax": float64(2)},
			want:   map[string]string{"--spec-draft-n-max": "2"},
		},
		{
			name:   "draft-mtp window is renamed even without a declared build",
			config: map[string]any{"specType": "draft-mtp", "draftMax": float64(3), "draftMin": float64(1)},
			want:   map[string]string{"--spec-draft-n-max": "3", "--spec-draft-n-min": "1"},
		},
		{
			name:   "declared b8173 ngram-simple keeps the legacy pair",
			config: map[string]any{"specType": "ngram-simple", "draftMax": float64(16), "draftMin": float64(1), "llamacppBuild": float64(8173)},
			want:   map[string]string{"--draft-max": "16", "--draft-min": "1"},
		},
		{
			name:   "b8954 is the last legacy build",
			config: map[string]any{"draftMax": float64(4), "llamacppBuild": 8954},
			want:   map[string]string{"--draft-max": "4"},
		},
		{
			name:   "b8955 is the first renamed build",
			config: map[string]any{"draftMax": float64(4), "llamacppBuild": 8955},
			want:   map[string]string{"--spec-draft-n-max": "4"},
		},
		{
			name:   "b11335 ngram-simple renders no draft window",
			config: map[string]any{"specType": "ngram-simple", "draftMax": float64(16), "draftMin": float64(1), "llamacppBuild": float64(11335)},
			want:   map[string]string{},
		},
	}
	b := &LlamaCppBackend{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := b.Args(&ModelSpec{ModelPath: "/models/test/model.gguf", GPUVendor: GPUVendorAMD, Config: tt.config})
			for _, flag := range draftFlags {
				want, expected := tt.want[flag]
				got := argValue(args, flag)
				switch {
				case expected && got != want:
					t.Errorf("%s = %q, want %q in args %#v", flag, got, want, args)
				case !expected && slices.Contains(args, flag):
					t.Errorf("args unexpectedly contain %s: %#v", flag, args)
				}
			}
			if slices.Contains(args, "--llamacppBuild") || slices.Contains(args, "--llamacpp-build") {
				t.Errorf("llamacppBuild is controller metadata and must not reach llama-server: %#v", args)
			}
		})
	}
}

func TestLlamaCppBackendArgs_StatefulFeaturesRemainOptIn(t *testing.T) {
	b := &LlamaCppBackend{}
	args := b.Args(&ModelSpec{ModelPath: "/models/test/model.gguf", GPUVendor: GPUVendorAMD})
	joined := strings.Join(args, " ")
	for _, flag := range []string{"--slot-save-path", "--spec-type", "--draft-max", "--draft-min"} {
		if strings.Contains(joined, flag) {
			t.Errorf("default args unexpectedly contain %s: %#v", flag, args)
		}
	}
}

func TestLlamaCppBackendArgs_DefaultPort(t *testing.T) {
	b := &LlamaCppBackend{}
	spec := &ModelSpec{ModelPath: "/models/test/model.gguf"}

	args := b.Args(spec)
	port := argValue(args, "--port")
	if port != "8080" {
		t.Fatalf("expected --port 8080, got %q in args %#v", port, args)
	}
}

func TestLlamaCppBackendArgs_AppendsGGUFFileToModelDirectory(t *testing.T) {
	b := &LlamaCppBackend{}
	spec := &ModelSpec{
		ModelPath: "/models/flexinfer-system/qwen3-1p7b-tools-radeonvii",
		Config: map[string]any{
			"ggufFile": "qwen3-1.7b-q4_k_m.gguf",
		},
	}

	args := b.Args(spec)
	model := argValue(args, "--model")
	want := "/models/flexinfer-system/qwen3-1p7b-tools-radeonvii/qwen3-1.7b-q4_k_m.gguf"
	if model != want {
		t.Fatalf("expected --model %q, got %q in args %#v", want, model, args)
	}
}

func TestLlamaCppBackendArgs_KeepsExplicitGGUFModelPath(t *testing.T) {
	b := &LlamaCppBackend{}
	spec := &ModelSpec{
		ModelPath: "/models/flexinfer-system/qwen3-1p7b-tools-radeonvii/qwen3-1.7b-q4_k_m.gguf",
		Config: map[string]any{
			"ggufFile": "qwen3-1.7b-q4_k_m.gguf",
		},
	}

	args := b.Args(spec)
	model := argValue(args, "--model")
	if model != spec.ModelPath {
		t.Fatalf("expected --model %q, got %q in args %#v", spec.ModelPath, model, args)
	}
}

func TestLlamaCppBackendEnv_AMDDevicePinningFromOrdinal(t *testing.T) {
	b := &LlamaCppBackend{}
	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		Config: map[string]any{
			"gpuDeviceOrdinal": "1",
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v := envMap["HIP_VISIBLE_DEVICES"]; v != "1" {
		t.Fatalf("expected HIP_VISIBLE_DEVICES=1, got %q", v)
	}
	if v := envMap["ROCR_VISIBLE_DEVICES"]; v != "1" {
		t.Fatalf("expected ROCR_VISIBLE_DEVICES=1, got %q", v)
	}
	if v := envMap["GPU_DEVICE_ORDINAL"]; v != "1" {
		t.Fatalf("expected GPU_DEVICE_ORDINAL=1, got %q", v)
	}
}

func TestLlamaCppBackendArgs_JinjaFlag(t *testing.T) {
	b := &LlamaCppBackend{}

	t.Run("enabled", func(t *testing.T) {
		spec := &ModelSpec{
			ModelPath: "/models/test/model.gguf",
			Config: map[string]any{
				"jinja": true,
			},
		}
		args := b.Args(spec)
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--jinja") {
			t.Fatalf("expected args to contain --jinja, got %#v", args)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		spec := &ModelSpec{
			ModelPath: "/models/test/model.gguf",
			Config:    map[string]any{},
		}
		args := b.Args(spec)
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "--jinja") {
			t.Fatalf("expected args to NOT contain --jinja, got %#v", args)
		}
	})
}

func TestLlamaCppBackendArgs_EmbeddingFlag(t *testing.T) {
	b := &LlamaCppBackend{}

	t.Run("enabled", func(t *testing.T) {
		spec := &ModelSpec{
			ModelPath: "/models/bge/model.gguf",
			Config: map[string]any{
				"embedding": true,
			},
		}
		args := b.Args(spec)
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--embeddings") {
			t.Fatalf("expected args to contain --embeddings, got %#v", args)
		}
	})

	t.Run("not set by default", func(t *testing.T) {
		spec := &ModelSpec{
			ModelPath: "/models/test/model.gguf",
			Config:    map[string]any{},
		}
		args := b.Args(spec)
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "--embeddings") {
			t.Fatalf("expected args to NOT contain --embeddings, got %#v", args)
		}
	})
}

func TestLlamaCppBackendArgs_PoolingFlag(t *testing.T) {
	b := &LlamaCppBackend{}

	t.Run("set", func(t *testing.T) {
		spec := &ModelSpec{
			ModelPath: "/models/gte/model.gguf",
			Config: map[string]any{
				"embedding": true,
				"pooling":   "last",
			},
		}
		args := b.Args(spec)
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--pooling last") {
			t.Fatalf("expected args to contain --pooling last, got %#v", args)
		}
	})

	t.Run("not set by default", func(t *testing.T) {
		spec := &ModelSpec{
			ModelPath: "/models/bge/model.gguf",
			Config:    map[string]any{"embedding": true},
		}
		args := b.Args(spec)
		if strings.Contains(strings.Join(args, " "), "--pooling") {
			t.Fatalf("expected no --pooling without config, got %#v", args)
		}
	})
}

func TestLlamaCppBackendArgs_RerankingFlag(t *testing.T) {
	b := &LlamaCppBackend{}

	t.Run("enabled", func(t *testing.T) {
		spec := &ModelSpec{
			ModelPath: "/models/bge-reranker/model.gguf",
			Config: map[string]any{
				"reranking": true,
			},
		}
		args := b.Args(spec)
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--reranking") {
			t.Fatalf("expected args to contain --reranking, got %#v", args)
		}
	})

	t.Run("not set by default", func(t *testing.T) {
		spec := &ModelSpec{
			ModelPath: "/models/test/model.gguf",
			Config:    map[string]any{},
		}
		args := b.Args(spec)
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "--reranking") {
			t.Fatalf("expected args to NOT contain --reranking, got %#v", args)
		}
	})
}

func TestLlamaCppBackendArgs_FlashAttention(t *testing.T) {
	b := &LlamaCppBackend{}

	tests := []struct {
		name      string
		gpuVendor GPUVendor
		config    map[string]any
		want      string // "" means no --flash-attn flag
	}{
		{name: "true on AMD", gpuVendor: GPUVendorAMD, config: map[string]any{"flashAttention": true}, want: "on"},
		{name: "true on NVIDIA", gpuVendor: GPUVendorNVIDIA, config: map[string]any{"flashAttention": true}, want: "on"},
		{name: "false on AMD", gpuVendor: GPUVendorAMD, config: map[string]any{"flashAttention": false}, want: "off"},
		{name: "false on NVIDIA", gpuVendor: GPUVendorNVIDIA, config: map[string]any{"flashAttention": false}, want: "off"},
		{name: "string true", gpuVendor: GPUVendorAMD, config: map[string]any{"flashAttention": "true"}, want: "on"},
		{name: "string false", gpuVendor: GPUVendorAMD, config: map[string]any{"flashAttention": "false"}, want: "off"},
		{name: "unset keeps auto", gpuVendor: GPUVendorAMD, config: map[string]any{}, want: ""},
		{name: "nil config keeps auto", gpuVendor: GPUVendorAMD, config: nil, want: ""},
		{name: "unparseable keeps auto", gpuVendor: GPUVendorAMD, config: map[string]any{"flashAttention": "maybe"}, want: ""},
		{name: "true on CPU stays omitted", gpuVendor: GPUVendorCPU, config: map[string]any{"flashAttention": true}, want: ""},
		{name: "false on CPU turns it off", gpuVendor: GPUVendorCPU, config: map[string]any{"flashAttention": false}, want: "off"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := b.Args(&ModelSpec{
				ModelPath: "/models/test/model.gguf",
				GPUVendor: tt.gpuVendor,
				Config:    tt.config,
			})
			count := 0
			for _, arg := range args {
				if arg == "--flash-attn" {
					count++
				}
			}
			if tt.want == "" {
				if count != 0 {
					t.Fatalf("expected no --flash-attn, got %#v", args)
				}
				return
			}
			if count != 1 {
				t.Fatalf("expected exactly one --flash-attn, got %d in %#v", count, args)
			}
			if got := argValue(args, "--flash-attn"); got != tt.want {
				t.Fatalf("--flash-attn = %q, want %q in args %#v", got, tt.want, args)
			}
		})
	}
}

func TestLlamaCppBackendImage(t *testing.T) {
	b := &LlamaCppBackend{}

	tests := []struct {
		name      string
		gpuVendor GPUVendor
		gpuArch   string
		envKey    string
		envVal    string
		wantImage string
	}{
		{
			name:      "AMD gfx1100 falls through to generic AMD default",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantImage: "ghcr.io/ggml-org/llama.cpp:server-rocm",
		},
		{
			name:      "AMD gfx1101 falls through to generic AMD default",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1101",
			wantImage: "ghcr.io/ggml-org/llama.cpp:server-rocm",
		},
		{
			name:      "AMD gfx1100 with env override",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			envKey:    "DEFAULT_LLAMA_CPP_IMAGE_GFX1100",
			envVal:    "custom-registry/llamacpp:gfx1100-custom",
			wantImage: "custom-registry/llamacpp:gfx1100-custom",
		},
		{
			// Per-arch image now lives in deploy/gpuprofiles/gfx906.yaml; the
			// rule slice is env-only and falls through to the AMD-generic
			// default when neither env nor profile applies.
			name:      "AMD gfx906 falls through to AMD generic",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			wantImage: "ghcr.io/ggml-org/llama.cpp:server-rocm",
		},
		{
			name:      "AMD gfx906 with env override",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			envKey:    "DEFAULT_LLAMA_CPP_IMAGE_GFX906",
			envVal:    "custom-registry/llamacpp:gfx906-custom",
			wantImage: "custom-registry/llamacpp:gfx906-custom",
		},
		{
			name:      "AMD generic falls through to server-rocm",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx900",
			wantImage: "ghcr.io/ggml-org/llama.cpp:server-rocm",
		},
		{
			name:      "NVIDIA sm_52 Maxwell falls through to generic CUDA without profile",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_52",
			wantImage: "ghcr.io/ggml-org/llama.cpp:server-cuda",
		},
		{
			name:      "NVIDIA sm_50 Maxwell falls through to generic CUDA without profile",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_50",
			wantImage: "ghcr.io/ggml-org/llama.cpp:server-cuda",
		},
		{
			name:      "NVIDIA sm_52 with env override",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_52",
			envKey:    "DEFAULT_LLAMA_CPP_IMAGE_MAXWELL",
			envVal:    "custom-registry/llamacpp:maxwell-custom",
			wantImage: "custom-registry/llamacpp:maxwell-custom",
		},
		{
			name:      "NVIDIA sm_89 ignores Maxwell path",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_89",
			wantImage: "ghcr.io/ggml-org/llama.cpp:server-cuda",
		},
		{
			name:      "CPU returns server image",
			gpuVendor: GPUVendorCPU,
			gpuArch:   "",
			wantImage: "ghcr.io/ggml-org/llama.cpp:server",
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

func TestLlamaCppBackendCommandForImage(t *testing.T) {
	b := &LlamaCppBackend{}

	// LlamaCppBackend must satisfy the ImageAwareCommander contract so the
	// deployment builder calls CommandForImage instead of Command.
	var _ ImageAwareCommander = b

	custom := []string{"/opt/src/llama.cpp/build/bin/llama-server"}

	tests := []struct {
		name  string
		image string
		want  []string
	}{
		{
			name:  "upstream ggml-org CPU image uses entrypoint",
			image: "ghcr.io/ggml-org/llama.cpp:server",
			want:  nil,
		},
		{
			name:  "upstream ggml-org CUDA image uses entrypoint",
			image: "ghcr.io/ggml-org/llama.cpp:server-cuda",
			want:  nil,
		},
		{
			name:  "upstream ggml-org ROCm image uses entrypoint",
			image: "ghcr.io/ggml-org/llama.cpp:server-rocm",
			want:  nil,
		},
		{
			name:  "legacy ggerganov image uses entrypoint",
			image: "ghcr.io/ggerganov/llama.cpp:server",
			want:  nil,
		},
		{
			name:  "custom Harbor build keeps explicit binary path",
			image: "registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3",
			want:  custom,
		},
		{
			name:  "custom Harbor digest-pinned build keeps explicit binary path",
			image: "registry.harbor.lan/flexinfer/llamacpp@sha256:18e8cb0868346b273b1cd22b4c3ed0fb5c63d0bb74e72dd0dae10f8786d81cc5",
			want:  custom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := b.CommandForImage(tt.image)
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Errorf("CommandForImage(%q) = %#v, want %#v", tt.image, got, tt.want)
			}
		})
	}
}

func argValue(args []string, key string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key {
			return args[i+1]
		}
	}
	return ""
}
