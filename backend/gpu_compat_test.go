package backend

import (
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
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
			name:      "diffusers gfx906 full",
			backend:   "diffusers",
			arch:      "gfx906",
			wantLevel: SupportFull,
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

// imageBackend is a minimal Backend used to assert ResolveBackendImage
// precedence without standing up the full registry.
type imageBackend struct {
	BaseBackend
	name string
	// image is what b.Image() returns regardless of vendor/arch.
	image string
	// imageCalls counts how many times Image() was invoked. Tests use this
	// to prove the GPUProfile-first path short-circuits the fallback.
	imageCalls int
}

func (b *imageBackend) Name() string                   { return b.name }
func (b *imageBackend) Image(GPUVendor, string) string { b.imageCalls++; return b.image }
func (b *imageBackend) Port() int32                    { return 8080 }
func (b *imageBackend) Args(*ModelSpec) []string       { return nil }
func (b *imageBackend) Env(*ModelSpec) []corev1.EnvVar { return nil }
func (b *imageBackend) ReadinessProbe() *corev1.Probe  { return nil }

func TestResolveBackendImage_GPUProfileWins(t *testing.T) {
	b := &imageBackend{name: "vllm", image: "registry.example.com/vllm:fallback"}
	profile := &aiv1alpha2.GPUProfileSpec{
		Architecture: "gfx1100",
		Vendor:       "amd",
		Backends: map[string]aiv1alpha2.BackendProfile{
			"vllm": {
				Support: "full",
				Image:   "registry.harbor.lan/flexinfer/runtime@sha256:profile",
			},
		},
	}

	got := ResolveBackendImage(b, profile, GPUVendorAMD, "gfx1100")
	if want := "registry.harbor.lan/flexinfer/runtime@sha256:profile"; got != want {
		t.Fatalf("ResolveBackendImage = %q, want profile image %q", got, want)
	}
	if b.imageCalls != 0 {
		t.Errorf("expected backend Image() to be skipped when profile declares override; got %d calls", b.imageCalls)
	}
}

func TestResolveBackendImage_ExplicitNilFallsThroughToBackend(t *testing.T) {
	b := &imageBackend{name: "vllm", image: "registry.example.com/vllm:fallback"}

	got := ResolveBackendImage(b, nil, GPUVendorAMD, "gfx1100")
	if want := "registry.example.com/vllm:fallback"; got != want {
		t.Fatalf("ResolveBackendImage(nil profile) = %q, want fallback %q", got, want)
	}
	if b.imageCalls != 1 {
		t.Errorf("expected backend Image() to be invoked exactly once for nil profile; got %d calls", b.imageCalls)
	}
}

func TestResolveBackendImage_ProfileWithoutBackendEntryFallsThrough(t *testing.T) {
	b := &imageBackend{name: "vllm", image: "registry.example.com/vllm:fallback"}
	profile := &aiv1alpha2.GPUProfileSpec{
		Architecture: "gfx1100",
		Vendor:       "amd",
		// Profile declares diffusers but not vllm — vllm must fall back.
		Backends: map[string]aiv1alpha2.BackendProfile{
			"diffusers": {Support: "full", Image: "registry.harbor.lan/flexinfer/diffusers:profile"},
		},
	}

	got := ResolveBackendImage(b, profile, GPUVendorAMD, "gfx1100")
	if want := "registry.example.com/vllm:fallback"; got != want {
		t.Fatalf("ResolveBackendImage = %q, want fallback %q (profile has no vllm entry)", got, want)
	}
	if b.imageCalls != 1 {
		t.Errorf("expected backend Image() fallback to fire when profile lacks entry; got %d calls", b.imageCalls)
	}
}

func TestResolveBackendImage_ProfileEntryWithEmptyImageFallsThrough(t *testing.T) {
	b := &imageBackend{name: "vllm", image: "registry.example.com/vllm:fallback"}
	profile := &aiv1alpha2.GPUProfileSpec{
		Architecture: "gfx1100",
		Vendor:       "amd",
		Backends: map[string]aiv1alpha2.BackendProfile{
			// Support declared but no image override — fall through to the
			// backend's arch-rule resolution.
			"vllm": {Support: "full"},
		},
	}

	got := ResolveBackendImage(b, profile, GPUVendorAMD, "gfx1100")
	if want := "registry.example.com/vllm:fallback"; got != want {
		t.Fatalf("ResolveBackendImage = %q, want fallback %q (profile entry has empty image)", got, want)
	}
	if b.imageCalls != 1 {
		t.Errorf("expected backend Image() fallback to fire when profile entry has empty image; got %d calls", b.imageCalls)
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
