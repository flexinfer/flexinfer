package backend

import (
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func whisperArg(args []string, flag string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1], true
		}
	}
	return "", false
}

func TestWhisperCppBackendArgs(t *testing.T) {
	tests := []struct {
		name    string
		spec    ModelSpec
		want    map[string]string
		present []string
		absent  []string
	}{
		{
			name: "runtime defaults",
			spec: ModelSpec{
				GPUVendor: GPUVendorAMD,
				ModelPath: "/models/flexinfer-system/whisper-large-v3-turbo-radeonvii/ggml-large-v3-turbo-q8_0.bin",
				Config: map[string]interface{}{
					"modelFile": "ggml-large-v3-turbo-q8_0.bin",
					"port":      float64(8123),
				},
			},
			want: map[string]string{
				"--host":           "0.0.0.0",
				"--port":           "8123",
				"--inference-path": "/v1/audio/transcriptions",
				"--language":       "auto",
				"--threads":        "4",
				"--model":          "/models/flexinfer-system/whisper-large-v3-turbo-radeonvii/ggml-large-v3-turbo-q8_0.bin",
			},
			present: []string{"--no-language-probabilities"},
			absent:  []string{"--no-gpu", "--no-flash-attn", "--beam-size", "--best-of"},
		},
		{
			name: "cache directory plus modelFile",
			spec: ModelSpec{
				GPUVendor: GPUVendorAMD,
				ModelPath: "/models/ns/asr",
				Config:    map[string]interface{}{"modelFile": "ggml-large-v3-turbo-q8_0.bin"},
			},
			want: map[string]string{"--model": "/models/ns/asr/ggml-large-v3-turbo-q8_0.bin", "--port": "8000"},
		},
		{
			name: "overrides",
			spec: ModelSpec{
				GPUVendor: GPUVendorAMD,
				Model:     "/weights/ggml.bin",
				Config: map[string]interface{}{
					"language":       "en",
					"threads":        float64(8),
					"flashAttention": false,
					"beamSize":       float64(5),
					"bestOf":         float64(5),
				},
			},
			want: map[string]string{
				"--language":  "en",
				"--threads":   "8",
				"--model":     "/weights/ggml.bin",
				"--beam-size": "5",
				"--best-of":   "5",
			},
			present: []string{"--no-flash-attn"},
		},
		{
			name:    "cpu",
			spec:    ModelSpec{GPUVendor: GPUVendorCPU, Model: "/weights/ggml.bin"},
			present: []string{"--no-gpu"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := (&WhisperCppBackend{}).Args(&tt.spec)
			for flag, want := range tt.want {
				if got, ok := whisperArg(args, flag); !ok || got != want {
					t.Errorf("%s = %q (present %v), want %q; args %v", flag, got, ok, want, args)
				}
			}
			for _, flag := range tt.present {
				if !slices.Contains(args, flag) {
					t.Errorf("missing %s in %v", flag, args)
				}
			}
			for _, flag := range tt.absent {
				if slices.Contains(args, flag) {
					t.Errorf("unexpected %s in %v", flag, args)
				}
			}
		})
	}
}

func TestWhisperCppBackendEnvLeavesGFX906Native(t *testing.T) {
	got := envMap((&WhisperCppBackend{}).Env(&ModelSpec{GPUVendor: GPUVendorAMD, GPUArch: "gfx906"}))
	if _, ok := got["HSA_OVERRIDE_GFX_VERSION"]; ok {
		t.Error("gfx906 must not set HSA_OVERRIDE_GFX_VERSION")
	}
	if got["HSA_ENABLE_SDMA"] != "0" || got["HSA_USE_SVM"] != "0" {
		t.Errorf("gfx906 env = %v, want SDMA and SVM disabled", got)
	}
	if env := (&WhisperCppBackend{}).Env(&ModelSpec{GPUVendor: GPUVendorCPU}); len(env) != 0 {
		t.Errorf("cpu Env() = %v, want none", env)
	}
}

func TestWhisperCppBackendRegistrationAndCapabilities(t *testing.T) {
	for _, name := range []string{NameWhisperCpp, "whisper.cpp", "whisper-cpp"} {
		b, ok := Get(name)
		if !ok {
			t.Fatalf("Get(%q) not registered", name)
		}
		if b.Name() != NameWhisperCpp {
			t.Errorf("Get(%q).Name() = %q, want %q", name, b.Name(), NameWhisperCpp)
		}
	}
	b, _ := Get(NameWhisperCpp)
	if b.Port() != PortWhisperCpp || PortWhisperCpp == 8080 {
		t.Errorf("Port() = %d; must not be 8080, the runtime API port", b.Port())
	}
	if b.Image(GPUVendorAMD, "gfx906") != "" {
		t.Error("Image() must be empty for the bundled runtime backend")
	}
	if cmd := b.Command(); len(cmd) != 1 || !strings.HasSuffix(cmd[0], "/whisper-server") {
		t.Errorf("Command() = %v, want the whisper-server binary", cmd)
	}
	for _, vendor := range []GPUVendor{GPUVendorAMD, GPUVendorNVIDIA, GPUVendorCPU} {
		if !b.SupportsGPUVendor(vendor) {
			t.Errorf("SupportsGPUVendor(%q) = false, want true", vendor)
		}
	}
	if !b.NeedsVolume() {
		t.Error("NeedsVolume() = false; the ggml weights come from the staged Local cache")
	}
	if IsImageGenBackend(NameWhisperCpp) {
		t.Error("whispercpp must not be classified as an image-generation backend")
	}
	if got := DefaultHWQueueEstimate(NameWhisperCpp, []corev1.EnvVar{{Name: EnvGPUMaxHWQueues, Value: "1"}}); got != 3 {
		t.Errorf("DefaultHWQueueEstimate = %d, want 3 (one HIP process at GPU_MAX_HW_QUEUES=1)", got)
	}
}
