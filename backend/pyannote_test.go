package backend

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func pyannoteEnv(env []corev1.EnvVar) map[string]string {
	out := make(map[string]string, len(env))
	for _, e := range env {
		out[e.Name] = e.Value
	}
	return out
}

func TestPyannoteBackendRegistered(t *testing.T) {
	for _, name := range []string{"pyannote", "pyannote-audio", "pyannote.audio"} {
		b, ok := Get(name)
		if !ok || b.Name() != NamePyannote {
			t.Fatalf("Get(%q) = %v, %v; want the pyannote backend", name, b, ok)
		}
	}
	b, _ := Get(NamePyannote)
	if img := b.Image(GPUVendorAMD, "gfx906"); img != "" {
		t.Errorf("Image = %q; pyannote is a runtime subprocess with no image of its own", img)
	}
	if b.NeedsVolume() {
		t.Error("NeedsVolume = true; the server pulls the pipeline from the HF hub")
	}
	if b.Port() != 8000 || b.Args(&ModelSpec{}) != nil {
		t.Errorf("port %d args %v; want 8000 and no args (the server reads env)", b.Port(), b.Args(&ModelSpec{}))
	}
	if cmd := b.Command(); len(cmd) != 2 || cmd[0] != "/opt/pyannote/bin/python" || cmd[1] != "/opt/flexinfer/server-pyannote.py" {
		t.Errorf("Command = %v; want the pyannote layer's venv running the bundled server", cmd)
	}
	if p := b.ReadinessProbe(); p == nil || p.HTTPGet == nil || p.HTTPGet.Path != "/health" {
		t.Errorf("ReadinessProbe = %+v; want GET /health", p)
	}
	for _, v := range []GPUVendor{GPUVendorAMD, GPUVendorNVIDIA, GPUVendorCPU} {
		if !b.SupportsGPUVendor(v) {
			t.Errorf("SupportsGPUVendor(%s) = false", v)
		}
	}
}

func TestPyannoteBackendEnv(t *testing.T) {
	tests := []struct {
		name   string
		spec   ModelSpec
		want   map[string]string
		absent []string
	}{
		{
			name: "pipeline from source, token by file",
			spec: ModelSpec{
				Model:     "pyannote/speaker-diarization-3.1",
				GPUVendor: GPUVendorAMD,
				GPUArch:   "gfx906",
				Config:    map[string]any{"hfTokenSecret": "flexinfer-hf-token"},
			},
			want: map[string]string{
				"PYANNOTE_PIPELINE":      "pyannote/speaker-diarization-3.1",
				"PYANNOTE_DEVICE":        "cuda",
				"PORT":                   "8000",
				"PYANNOTE_HF_TOKEN_FILE": "/var/run/flexinfer/secrets/flexinfer-hf-token/HF_TOKEN",
				"PYTORCH_ROCM_ARCH":      "gfx906",
			},
			absent: []string{"HF_TOKEN"},
		},
		{
			name: "config overrides",
			spec: ModelSpec{
				Model:     "pyannote/speaker-diarization-3.1",
				GPUVendor: GPUVendorCPU,
				Config: map[string]any{
					"pipeline": "pyannote/speaker-diarization-community-1", "device": "cpu", "port": float64(8123),
					"hfTokenSecret": "hf", "hfTokenSecretKey": "token",
				},
			},
			want: map[string]string{
				"PYANNOTE_PIPELINE":      "pyannote/speaker-diarization-community-1",
				"PYANNOTE_DEVICE":        "cpu",
				"PORT":                   "8123",
				"PYANNOTE_HF_TOKEN_FILE": "/var/run/flexinfer/secrets/hf/token",
			},
			absent: []string{"PYTORCH_ROCM_ARCH"},
		},
		{
			name:   "no source, no secret",
			spec:   ModelSpec{GPUVendor: GPUVendorCPU},
			want:   map[string]string{"PYANNOTE_PIPELINE": DefaultPyannotePipeline},
			absent: []string{"PYANNOTE_HF_TOKEN_FILE"},
		},
		{
			name:   "a secret name that could escape the secrets directory is dropped",
			spec:   ModelSpec{GPUVendor: GPUVendorCPU, Config: map[string]any{"hfTokenSecret": "../../etc", "hfTokenSecretKey": "passwd"}},
			absent: []string{"PYANNOTE_HF_TOKEN_FILE"},
		},
	}
	b := &PyannoteBackend{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pyannoteEnv(b.Env(&tt.spec))
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("%s = %q; want %q", k, got[k], v)
				}
			}
			for _, k := range tt.absent {
				if _, ok := got[k]; ok {
					t.Errorf("%s set to %q; want it absent", k, got[k])
				}
			}
		})
	}
}

func TestPyannoteHFTokenSecret(t *testing.T) {
	tests := []struct {
		cfg       map[string]any
		name, key string
		wantErr   bool
	}{
		{cfg: nil},
		{cfg: map[string]any{"hfTokenSecret": "flexinfer-hf-token"}, name: "flexinfer-hf-token", key: "HF_TOKEN"},
		{cfg: map[string]any{"hfTokenSecret": "hf", "hfTokenSecretKey": "token.txt"}, name: "hf", key: "token.txt"},
		{cfg: map[string]any{"hfTokenSecret": "Not_A_Name"}, wantErr: true},
		{cfg: map[string]any{"hfTokenSecret": "hf", "hfTokenSecretKey": "a/b"}, wantErr: true},
		{cfg: map[string]any{"hfTokenSecret": "hf", "hfTokenSecretKey": ".."}, wantErr: true},
	}
	for _, tt := range tests {
		name, key, err := PyannoteHFTokenSecret(tt.cfg)
		if (err != nil) != tt.wantErr || name != tt.name || key != tt.key {
			t.Errorf("PyannoteHFTokenSecret(%v) = %q, %q, %v; want %q, %q, err=%v", tt.cfg, name, key, err, tt.name, tt.key, tt.wantErr)
		}
	}
}

// The D5-a Model declares hwQueueEstimate 3; the default estimate under the
// gfx906 profile env (GPU_MAX_HW_QUEUES=1) must agree, so a Model that omits
// it is still admitted against the same budget.
func TestPyannoteDefaultHWQueueEstimate(t *testing.T) {
	env := []corev1.EnvVar{{Name: EnvGPUMaxHWQueues, Value: "1"}}
	if got := DefaultHWQueueEstimate(NamePyannote, env); got != 3 {
		t.Fatalf("DefaultHWQueueEstimate(pyannote, GPU_MAX_HW_QUEUES=1) = %d; want 3", got)
	}
}
