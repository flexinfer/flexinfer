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

// TestResolveBackendImage_RealBackendsArchEnvOnly asserts the slice-3 contract:
// per-arch defaults moved to deploy/gpuprofiles/*.yaml, so each backend's rule
// slice is env-only on gfx110/gfx906 and falls through to the vendor-generic
// default. A GPUProfile.Image override still wins, matching ResolveBackendImage
// precedence.
func TestResolveBackendImage_RealBackendsArchEnvOnly(t *testing.T) {
	tests := []struct {
		name              string
		backend           Backend
		arch              string
		envKey            string
		envVal            string
		profile           *aiv1alpha2.GPUProfileSpec
		wantImage         string
		wantContractCheck string // human-readable assertion of which precedence rule is exercised
	}{
		// vLLM ----------------------------------------------------------------
		{
			name:              "vllm gfx1100 no profile no env -> AMD generic fallback",
			backend:           &VLLMBackend{},
			arch:              "gfx1100",
			wantImage:         "rocm/vllm:latest",
			wantContractCheck: "arch rule env-only; falls through to vendor generic",
		},
		{
			name:    "vllm gfx1100 profile image wins",
			backend: &VLLMBackend{},
			arch:    "gfx1100",
			profile: &aiv1alpha2.GPUProfileSpec{
				Architecture: "gfx1100",
				Vendor:       "amd",
				Backends: map[string]aiv1alpha2.BackendProfile{
					"vllm": {Support: "full", Image: "registry.example.com/vllm:profile"},
				},
			},
			wantImage:         "registry.example.com/vllm:profile",
			wantContractCheck: "profile.Image takes precedence over rule slice",
		},
		{
			name:              "vllm gfx906 no profile no env -> AMD generic fallback",
			backend:           &VLLMBackend{},
			arch:              "gfx906",
			wantImage:         "rocm/vllm:latest",
			wantContractCheck: "arch rule env-only; falls through to vendor generic",
		},
		{
			name:              "vllm gfx1100 env override still wired",
			backend:           &VLLMBackend{},
			arch:              "gfx1100",
			envKey:            "DEFAULT_VLLM_IMAGE_GFX1100",
			envVal:            "registry.example.com/vllm:env-override",
			wantImage:         "registry.example.com/vllm:env-override",
			wantContractCheck: "arch env override fires when profile is nil",
		},
		// Diffusers ----------------------------------------------------------
		{
			name:              "diffusers gfx1100 no profile no env -> AMD generic",
			backend:           &DiffusersBackend{},
			arch:              "gfx1100",
			wantImage:         "registry.harbor.lan/library/diffusers-api:rocm-latest",
			wantContractCheck: "arch rule env-only; falls through to vendor generic",
		},
		{
			name:              "diffusers gfx906 no profile no env -> AMD generic",
			backend:           &DiffusersBackend{},
			arch:              "gfx906",
			wantImage:         "registry.harbor.lan/library/diffusers-api:rocm-latest",
			wantContractCheck: "arch rule env-only; falls through to vendor generic",
		},
		// ComfyUI ------------------------------------------------------------
		{
			name:              "comfyui gfx1100 no profile no env -> AMD generic",
			backend:           &ComfyUIBackend{},
			arch:              "gfx1100",
			wantImage:         "registry.harbor.lan/library/comfyui:rocm6.2.3-v8",
			wantContractCheck: "arch rule env-only; falls through to vendor generic",
		},
		{
			name:              "comfyui gfx906 no profile no env -> AMD generic",
			backend:           &ComfyUIBackend{},
			arch:              "gfx906",
			wantImage:         "registry.harbor.lan/library/comfyui:rocm6.2.3-v8",
			wantContractCheck: "arch rule env-only; falls through to vendor generic",
		},
		// llama.cpp ----------------------------------------------------------
		{
			name:              "llamacpp gfx906 no profile no env -> AMD generic (was hardcoded)",
			backend:           &LlamaCppBackend{},
			arch:              "gfx906",
			wantImage:         "ghcr.io/ggerganov/llama.cpp:server-rocm",
			wantContractCheck: "previously hardcoded gfx906 default removed; now profile-owned",
		},
		{
			name:    "llamacpp gfx906 profile image wins",
			backend: &LlamaCppBackend{},
			arch:    "gfx906",
			profile: &aiv1alpha2.GPUProfileSpec{
				Architecture: "gfx906",
				Vendor:       "amd",
				Backends: map[string]aiv1alpha2.BackendProfile{
					"llamacpp": {Support: "full", Image: "registry.example.com/llamacpp:gfx906-profile"},
				},
			},
			wantImage:         "registry.example.com/llamacpp:gfx906-profile",
			wantContractCheck: "profile.Image restores per-arch override",
		},
		// MLC-LLM ------------------------------------------------------------
		{
			name:              "mlc-llm gfx1100 no profile no env -> AMD generic",
			backend:           &MLCLLMBackend{},
			arch:              "gfx1100",
			wantImage:         "ghcr.io/mlc-ai/mlc-llm:rocm",
			wantContractCheck: "arch rule env-only; falls through to vendor generic",
		},
		{
			name:              "mlc-llm gfx906 no profile no env -> AMD generic",
			backend:           &MLCLLMBackend{},
			arch:              "gfx906",
			wantImage:         "ghcr.io/mlc-ai/mlc-llm:rocm",
			wantContractCheck: "arch rule env-only; falls through to vendor generic",
		},
		// vLLM-Omni ----------------------------------------------------------
		{
			name:              "vllm-omni gfx1100 no profile no env -> AMD generic (which is also gfx1100)",
			backend:           &VLLMOmniBackend{},
			arch:              "gfx1100",
			wantImage:         "registry.harbor.lan/flexinfer/vllm-omni:rocm-gfx1100",
			wantContractCheck: "arch rule env-only; vendor generic happens to ship the gfx1100 image",
		},
		// Profile-without-backend-entry fallback -----------------------------
		{
			name:    "vllm gfx1100 profile lacks vllm entry -> falls through to backend rules",
			backend: &VLLMBackend{},
			arch:    "gfx1100",
			profile: &aiv1alpha2.GPUProfileSpec{
				Architecture: "gfx1100",
				Vendor:       "amd",
				Backends: map[string]aiv1alpha2.BackendProfile{
					"diffusers": {Support: "full", Image: "registry.example.com/diffusers:profile"},
				},
			},
			wantImage:         "rocm/vllm:latest",
			wantContractCheck: "profile-without-backend-entry falls through to vendor generic",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envKey != "" {
				t.Setenv(tt.envKey, tt.envVal)
			}
			got := ResolveBackendImage(tt.backend, tt.profile, GPUVendorAMD, tt.arch)
			if got != tt.wantImage {
				t.Errorf("ResolveBackendImage(%s, %s) = %q, want %q (%s)",
					tt.backend.Name(), tt.arch, got, tt.wantImage, tt.wantContractCheck)
			}
		})
	}
}

// envVarSliceEqual reports whether two []corev1.EnvVar slices contain the same
// (Name, Value) pairs in the same order.
func envVarSliceEqual(a, b []corev1.EnvVar) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Value != b[i].Value {
			return false
		}
	}
	return true
}

func TestResolveBackendROCmEnv_GPUProfileWins(t *testing.T) {
	profileEnv := []corev1.EnvVar{
		{Name: "HSA_OVERRIDE_GFX_VERSION", Value: "12.0.0"},
		{Name: "PYTORCH_ROCM_ARCH", Value: "gfx1200"},
	}
	profile := &aiv1alpha2.GPUProfileSpec{
		Architecture: "gfx1100",
		Vendor:       "amd",
		Env:          profileEnv,
	}

	got := ResolveBackendROCmEnv(profile, GPUVendorAMD, "gfx1100")
	if !envVarSliceEqual(got, profileEnv) {
		t.Fatalf("ResolveBackendROCmEnv = %+v, want profile env %+v", got, profileEnv)
	}

	// Confirm we did NOT call into ROCmEnvVars by checking we got the
	// profile-declared "12.0.0" rather than the gfx1100 hardcoded "11.0.0".
	for _, e := range got {
		if e.Name == "HSA_OVERRIDE_GFX_VERSION" && e.Value == "11.0.0" {
			t.Fatalf("ResolveBackendROCmEnv used ROCmEnvVars fallback when profile.Env was set")
		}
	}
}

func TestResolveBackendROCmEnv_ExplicitNilFallsThroughToROCmEnvVars(t *testing.T) {
	got := ResolveBackendROCmEnv(nil, GPUVendorAMD, "gfx1100")
	want := ROCmEnvVars("gfx1100")
	if !envVarSliceEqual(got, want) {
		t.Fatalf("ResolveBackendROCmEnv(nil) = %+v, want ROCmEnvVars(gfx1100) = %+v", got, want)
	}
	// Sanity: gfx1100 fallback must include the HSA override at 11.0.0.
	foundOverride := false
	for _, e := range got {
		if e.Name == "HSA_OVERRIDE_GFX_VERSION" && e.Value == "11.0.0" {
			foundOverride = true
			break
		}
	}
	if !foundOverride {
		t.Fatalf("expected gfx1100 fallback to include HSA_OVERRIDE_GFX_VERSION=11.0.0; got %+v", got)
	}
}

func TestResolveBackendROCmEnv_ProfileWithoutEnvFallsThrough(t *testing.T) {
	// Profile is non-nil but declares no env entries — fall through to the
	// in-code ROCmEnvVars switch.
	profile := &aiv1alpha2.GPUProfileSpec{
		Architecture: "gfx906",
		Vendor:       "amd",
		// no Env field
	}

	got := ResolveBackendROCmEnv(profile, GPUVendorAMD, "gfx906")
	want := ROCmEnvVars("gfx906")
	if !envVarSliceEqual(got, want) {
		t.Fatalf("ResolveBackendROCmEnv(profile w/o env) = %+v, want fallback %+v", got, want)
	}
	// Sanity: gfx906 fallback must include the HSA override at 9.0.6.
	foundOverride := false
	for _, e := range got {
		if e.Name == "HSA_OVERRIDE_GFX_VERSION" && e.Value == "9.0.6" {
			foundOverride = true
			break
		}
	}
	if !foundOverride {
		t.Fatalf("expected gfx906 fallback to include HSA_OVERRIDE_GFX_VERSION=9.0.6; got %+v", got)
	}
}

func TestResolveBackendROCmEnv_ProfileWithEmptyEnvFallsThrough(t *testing.T) {
	// Profile declares Env explicitly as an empty slice — equivalent to no
	// override, so fall through to ROCmEnvVars.
	profile := &aiv1alpha2.GPUProfileSpec{
		Architecture: "gfx1100",
		Vendor:       "amd",
		Env:          []corev1.EnvVar{},
	}

	got := ResolveBackendROCmEnv(profile, GPUVendorAMD, "gfx1100")
	want := ROCmEnvVars("gfx1100")
	if !envVarSliceEqual(got, want) {
		t.Fatalf("ResolveBackendROCmEnv(profile w/ empty env) = %+v, want fallback %+v", got, want)
	}
}

func TestResolveBackendROCmEnv_NonAMDWithoutProfileReturnsNil(t *testing.T) {
	// For non-AMD vendors with no profile env, the helper returns an empty
	// slice — ROCmEnvVars is ROCm-specific and must not leak into NVIDIA pods.
	got := ResolveBackendROCmEnv(nil, GPUVendorNVIDIA, "sm_89")
	if len(got) != 0 {
		t.Fatalf("ResolveBackendROCmEnv(nil, nvidia, sm_89) = %+v, want nil", got)
	}
}

func TestEnvFromProfile_NilProfile(t *testing.T) {
	got, ok := EnvFromProfile(nil)
	if ok {
		t.Fatalf("EnvFromProfile(nil) ok = true, want false")
	}
	if got != nil {
		t.Fatalf("EnvFromProfile(nil) = %+v, want nil", got)
	}
}

func TestEnvFromProfile_EmptyEnv(t *testing.T) {
	profile := &aiv1alpha2.GPUProfileSpec{Architecture: "gfx1100"}
	got, ok := EnvFromProfile(profile)
	if ok {
		t.Fatalf("EnvFromProfile(profile w/o env) ok = true, want false")
	}
	if got != nil {
		t.Fatalf("EnvFromProfile(profile w/o env) = %+v, want nil", got)
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

// TestResolveBackendGPUSupport asserts the slice-5 contract: profile-declared
// support wins, no entry on the profile falls through to the legacy
// BackendGPUCompatibility table, and explicit nil profile uses the legacy table.
func TestResolveBackendGPUSupport(t *testing.T) {
	tests := []struct {
		name        string
		profile     *aiv1alpha2.GPUProfileSpec
		backendName string
		gpuArch     string
		wantLevel   GPUArchSupportLevel
		wantVRAM    int
		wantFound   bool
		desc        string
	}{
		{
			name:        "profile declares full beats legacy map",
			profile:     &aiv1alpha2.GPUProfileSpec{VRAMMB: 24576, Backends: map[string]aiv1alpha2.BackendProfile{"vllm": {Support: "full"}}},
			backendName: "vllm",
			gpuArch:     "gfx1100",
			wantLevel:   SupportFull,
			wantVRAM:    24576,
			wantFound:   true,
			desc:        "profile.Support=full -> SupportFull",
		},
		{
			name:        "profile declares experimental wins",
			profile:     &aiv1alpha2.GPUProfileSpec{VRAMMB: 16384, Backends: map[string]aiv1alpha2.BackendProfile{"comfyui": {Support: "experimental"}}},
			backendName: "comfyui",
			gpuArch:     "gfx906",
			wantLevel:   SupportExperimental,
			wantVRAM:    16384,
			wantFound:   true,
			desc:        "profile.Support=experimental -> SupportExperimental",
		},
		{
			name:        "profile declares unsupported overrides full in legacy map",
			profile:     &aiv1alpha2.GPUProfileSpec{VRAMMB: 24576, Backends: map[string]aiv1alpha2.BackendProfile{"vllm": {Support: "unsupported"}}},
			backendName: "vllm",
			gpuArch:     "gfx1100",
			wantLevel:   SupportUnsupported,
			wantVRAM:    24576,
			wantFound:   true,
			desc:        "profile can downgrade a backend the legacy map marks as full",
		},
		{
			name:        "profile lacks backend entry falls through to legacy map",
			profile:     &aiv1alpha2.GPUProfileSpec{VRAMMB: 24576, Backends: map[string]aiv1alpha2.BackendProfile{"diffusers": {Support: "full"}}},
			backendName: "vllm",
			gpuArch:     "gfx1100",
			wantLevel:   SupportFull,
			wantVRAM:    24576,
			wantFound:   true,
			desc:        "no vllm in profile -> legacy map entry for gfx1100",
		},
		{
			name:        "profile with unknown support level falls through to legacy",
			profile:     &aiv1alpha2.GPUProfileSpec{Backends: map[string]aiv1alpha2.BackendProfile{"vllm": {Support: "canary"}}},
			backendName: "vllm",
			gpuArch:     "gfx1100",
			wantLevel:   SupportFull,
			wantVRAM:    24576,
			wantFound:   true,
			desc:        "unknown support strings are not recognized; helper falls through",
		},
		{
			name:        "nil profile uses legacy map",
			profile:     nil,
			backendName: "vllm",
			gpuArch:     "gfx906",
			wantLevel:   SupportFull,
			wantVRAM:    16384,
			wantFound:   true,
			desc:        "nil profile -> legacy map (backstop for nodes without a profile)",
		},
		{
			name:        "nil profile and unknown backend returns not found",
			profile:     nil,
			backendName: "nonexistent",
			gpuArch:     "gfx1100",
			wantFound:   false,
			desc:        "no profile and no legacy entry -> not found",
		},
		{
			name:        "profile entry without support falls through to legacy",
			profile:     &aiv1alpha2.GPUProfileSpec{Backends: map[string]aiv1alpha2.BackendProfile{"vllm": {Image: "registry.example.com/vllm:profile"}}},
			backendName: "vllm",
			gpuArch:     "gfx1100",
			wantLevel:   SupportFull,
			wantVRAM:    24576,
			wantFound:   true,
			desc:        "profile.Image without Support -> not a support entry, fall through",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := ResolveBackendGPUSupport(tt.profile, tt.backendName, tt.gpuArch)
			if found != tt.wantFound {
				t.Fatalf("ResolveBackendGPUSupport found = %v, want %v (%s)", found, tt.wantFound, tt.desc)
			}
			if !found {
				return
			}
			if got.Level != tt.wantLevel {
				t.Errorf("Level = %v, want %v (%s)", got.Level, tt.wantLevel, tt.desc)
			}
			if got.MaxVRAMMB != tt.wantVRAM {
				t.Errorf("MaxVRAMMB = %d, want %d (%s)", got.MaxVRAMMB, tt.wantVRAM, tt.desc)
			}
		})
	}
}

// TestIsBackendSupported documents the policy mapping from support levels to
// the boolean "may run here?" decision. SupportExperimental returns true (the
// controller emits a warning event separately); SupportUnsupported and
// "not found" return false.
func TestIsBackendSupported(t *testing.T) {
	tests := []struct {
		name        string
		profile     *aiv1alpha2.GPUProfileSpec
		backendName string
		gpuArch     string
		want        bool
		desc        string
	}{
		{
			name:        "profile full -> true",
			profile:     &aiv1alpha2.GPUProfileSpec{Backends: map[string]aiv1alpha2.BackendProfile{"vllm": {Support: "full"}}},
			backendName: "vllm",
			gpuArch:     "gfx1100",
			want:        true,
			desc:        "production-ready backends are supported",
		},
		{
			name:        "profile experimental -> true (allow with caveats)",
			profile:     &aiv1alpha2.GPUProfileSpec{Backends: map[string]aiv1alpha2.BackendProfile{"comfyui": {Support: "experimental"}}},
			backendName: "comfyui",
			gpuArch:     "gfx906",
			want:        true,
			desc:        "experimental returns true; the controller warns separately",
		},
		{
			name:        "profile unsupported -> false",
			profile:     &aiv1alpha2.GPUProfileSpec{Backends: map[string]aiv1alpha2.BackendProfile{"vllm": {Support: "unsupported"}}},
			backendName: "vllm",
			gpuArch:     "gfx1100",
			want:        false,
			desc:        "unsupported blocks deployment",
		},
		{
			name:        "nil profile uses legacy map (vllm gfx1100 full)",
			profile:     nil,
			backendName: "vllm",
			gpuArch:     "gfx1100",
			want:        true,
			desc:        "nil profile -> legacy map -> full -> true",
		},
		{
			name:        "nil profile uses legacy map (vllm sm_52 unsupported)",
			profile:     nil,
			backendName: "vllm",
			gpuArch:     "sm_52",
			want:        false,
			desc:        "Maxwell vllm is unsupported in the legacy map",
		},
		{
			name:        "no profile and unknown backend -> false",
			profile:     nil,
			backendName: "nonexistent",
			gpuArch:     "gfx1100",
			want:        false,
			desc:        "absent entry is treated as unsupported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBackendSupported(tt.profile, tt.backendName, tt.gpuArch); got != tt.want {
				t.Errorf("IsBackendSupported = %v, want %v (%s)", got, tt.want, tt.desc)
			}
		})
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
