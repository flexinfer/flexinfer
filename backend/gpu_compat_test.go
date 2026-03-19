package backend

import (
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestLookupGPUArchSupport_PrefixMatching(t *testing.T) {
	tests := []struct {
		name      string
		backend   string
		arch      string
		wantLevel GPUArchSupportLevel
		wantVRAM  int
		wantFound bool
	}{
		{
			name:      "vllm gfx1100 matches gfx110 prefix",
			backend:   "vllm",
			arch:      "gfx1100",
			wantLevel: SupportFull,
			wantVRAM:  24576,
			wantFound: true,
		},
		{
			name:      "vllm gfx1101 matches gfx110 prefix",
			backend:   "vllm",
			arch:      "gfx1101",
			wantLevel: SupportFull,
			wantVRAM:  24576,
			wantFound: true,
		},
		{
			name:      "vllm gfx1102 matches gfx110 prefix",
			backend:   "vllm",
			arch:      "gfx1102",
			wantLevel: SupportFull,
			wantVRAM:  24576,
			wantFound: true,
		},
		{
			name:      "vllm gfx906 full support",
			backend:   "vllm",
			arch:      "gfx906",
			wantLevel: SupportFull,
			wantVRAM:  16384,
			wantFound: true,
		},
		{
			name:      "vllm Maxwell sm_52 unsupported",
			backend:   "vllm",
			arch:      "sm_52",
			wantLevel: SupportUnsupported,
			wantVRAM:  0,
			wantFound: true,
		},
		{
			name:      "diffusers gfx906 experimental",
			backend:   "diffusers",
			arch:      "gfx906",
			wantLevel: SupportExperimental,
			wantVRAM:  16384,
			wantFound: true,
		},
		{
			name:      "comfyui gfx906 experimental",
			backend:   "comfyui",
			arch:      "gfx906",
			wantLevel: SupportExperimental,
			wantVRAM:  16384,
			wantFound: true,
		},
		{
			name:      "llamacpp Maxwell supported",
			backend:   "llamacpp",
			arch:      "sm_52",
			wantLevel: SupportFull,
			wantVRAM:  6144,
			wantFound: true,
		},
		{
			name:      "vllm gfx90a MI250 full support",
			backend:   "vllm",
			arch:      "gfx90a",
			wantLevel: SupportFull,
			wantVRAM:  65536,
			wantFound: true,
		},
		{
			name:      "vllm gfx942 MI300X full support",
			backend:   "vllm",
			arch:      "gfx942",
			wantLevel: SupportFull,
			wantVRAM:  196608,
			wantFound: true,
		},
		{
			name:      "llamacpp gfx90a MI250 full support",
			backend:   "llamacpp",
			arch:      "gfx90a",
			wantLevel: SupportFull,
			wantVRAM:  65536,
			wantFound: true,
		},
		{
			name:      "diffusers gfx942 MI300X full support",
			backend:   "diffusers",
			arch:      "gfx942",
			wantLevel: SupportFull,
			wantVRAM:  196608,
			wantFound: true,
		},
		{
			name:      "gfx90a does not match gfx906 entry",
			backend:   "vllm",
			arch:      "gfx90a",
			wantLevel: SupportFull,
			wantVRAM:  65536,
			wantFound: true,
		},
		{
			name:      "unknown arch returns not found",
			backend:   "vllm",
			arch:      "gfx1036",
			wantFound: false,
		},
		{
			name:      "unknown backend returns not found",
			backend:   "nonexistent",
			arch:      "gfx1100",
			wantFound: false,
		},
		{
			name:      "empty arch returns not found",
			backend:   "vllm",
			arch:      "",
			wantFound: false,
		},
		{
			name:      "ollama gfx1100 full",
			backend:   "ollama",
			arch:      "gfx1100",
			wantLevel: SupportFull,
			wantVRAM:  24576,
			wantFound: true,
		},
		{
			name:      "mlc-llm gfx906 experimental",
			backend:   "mlc-llm",
			arch:      "gfx906",
			wantLevel: SupportExperimental,
			wantVRAM:  16384,
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := LookupGPUArchSupport(tt.backend, tt.arch)
			if found != tt.wantFound {
				t.Fatalf("LookupGPUArchSupport(%q, %q) found = %v, want %v", tt.backend, tt.arch, found, tt.wantFound)
			}
			if !found {
				return
			}
			if got.Level != tt.wantLevel {
				t.Errorf("Level = %v, want %v", got.Level, tt.wantLevel)
			}
			if got.MaxVRAMMB != tt.wantVRAM {
				t.Errorf("MaxVRAMMB = %d, want %d", got.MaxVRAMMB, tt.wantVRAM)
			}
		})
	}
}

func TestQuantizerImageFromProfile_NormalizesFormatCase(t *testing.T) {
	profile := &aiv1alpha2.GPUProfileSpec{
		Quantization: &aiv1alpha2.QuantizationProfile{
			Images: map[string]string{
				"gptq": "registry.harbor.lan/flexinfer/runtime@sha256:test",
			},
		},
	}

	got, ok := QuantizerImageFromProfile(profile, "GPTQ")
	if !ok {
		t.Fatal("QuantizerImageFromProfile() ok = false, want true")
	}
	if got != "registry.harbor.lan/flexinfer/runtime@sha256:test" {
		t.Fatalf("QuantizerImageFromProfile() = %q, want %q", got, "registry.harbor.lan/flexinfer/runtime@sha256:test")
	}
}

func TestGPUArchSupportLevel_String(t *testing.T) {
	tests := []struct {
		level GPUArchSupportLevel
		want  string
	}{
		{SupportFull, "full"},
		{SupportExperimental, "experimental"},
		{SupportUnsupported, "unsupported"},
		{GPUArchSupportLevel(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("GPUArchSupportLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}
