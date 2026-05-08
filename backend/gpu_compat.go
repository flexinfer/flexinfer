package backend

import (
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
)

// GPUArchSupportLevel indicates how well a backend supports a GPU architecture.
type GPUArchSupportLevel int

const (
	// SupportFull means the backend is production-ready on this architecture.
	SupportFull GPUArchSupportLevel = iota
	// SupportExperimental means the backend works but may have stability issues.
	SupportExperimental
	// SupportUnsupported means the backend cannot run on this architecture.
	SupportUnsupported
)

func (l GPUArchSupportLevel) String() string {
	switch l {
	case SupportFull:
		return "full"
	case SupportExperimental:
		return "experimental"
	case SupportUnsupported:
		return "unsupported"
	default:
		return "unknown"
	}
}

// GPUArchSupport describes the support level and VRAM capacity for a backend
// on a specific GPU architecture prefix.
type GPUArchSupport struct {
	Level     GPUArchSupportLevel
	MaxVRAMMB int // Typical max VRAM in MB for this architecture class (0 = unknown)
}

// BackendGPUCompatibility maps (backend name, architecture prefix) to support info.
// Architecture prefixes use shortest-unique matching (e.g., "gfx110" matches gfx1100/1101/1102).
//
// Deprecated: this hardcoded table is the legacy fallback for code paths without
// a GPUProfile in scope. Prefer ResolveBackendGPUSupport / IsBackendSupported,
// which consult `deploy/gpuprofiles/*.yaml::backends.<name>.support` first and
// only consult this map when the profile is nil. The map remains for the
// scheduler extender and any other path that has no access to the
// GPUProfileReconciler cache.
var BackendGPUCompatibility = map[string]map[string]GPUArchSupport{
	"vllm": {
		"gfx110": {SupportFull, 24576},
		"gfx90a": {SupportFull, 65536},
		"gfx942": {SupportFull, 196608},
		"gfx906": {SupportFull, 16384},
		"sm_5":   {SupportUnsupported, 0},
	},
	"vllm-omni": {
		"gfx110": {SupportFull, 24576},
		"gfx90a": {SupportFull, 65536},
		"gfx942": {SupportFull, 196608},
		"gfx906": {SupportFull, 16384},
		"sm_5":   {SupportUnsupported, 0},
	},
	"llamacpp": {
		"gfx110": {SupportFull, 24576},
		"gfx90a": {SupportFull, 65536},
		"gfx942": {SupportFull, 196608},
		"gfx906": {SupportFull, 16384},
		"sm_5":   {SupportFull, 6144},
	},
	"diffusers": {
		"gfx110": {SupportFull, 24576},
		"gfx90a": {SupportFull, 65536},
		"gfx942": {SupportFull, 196608},
		"gfx906": {SupportFull, 16384},
		"sm_5":   {SupportUnsupported, 0},
	},
	"comfyui": {
		"gfx110": {SupportFull, 24576},
		"gfx90a": {SupportFull, 65536},
		"gfx942": {SupportFull, 196608},
		"gfx906": {SupportExperimental, 16384},
		"sm_5":   {SupportUnsupported, 0},
	},
	"ollama": {
		"gfx110": {SupportFull, 24576},
		"gfx90a": {SupportFull, 65536},
		"gfx942": {SupportFull, 196608},
		"gfx906": {SupportFull, 16384},
		"sm_5":   {SupportFull, 6144},
	},
	"mlc-llm": {
		"gfx110": {SupportFull, 24576},
		"gfx90a": {SupportFull, 65536},
		"gfx942": {SupportFull, 196608},
		"gfx906": {SupportExperimental, 16384},
		"sm_5":   {SupportFull, 6144},
	},
}

// LookupGPUArchSupport returns the support info for a backend and GPU architecture.
// It uses prefix matching: "gfx1100" matches "gfx110", "sm_52" matches "sm_5".
// Returns (support, true) on match, or (zero, false) if no entry exists.
//
// Deprecated: prefer ResolveBackendGPUSupport, which honors GPUProfile-declared
// support levels first. This function is retained for code paths that do not
// have access to a GPUProfileReconciler cache (e.g. scheduler/scheduler.go).
func LookupGPUArchSupport(backendName, gpuArch string) (GPUArchSupport, bool) {
	archMap, ok := BackendGPUCompatibility[backendName]
	if !ok {
		return GPUArchSupport{}, false
	}

	// Try longest prefix match first for specificity.
	var bestMatch GPUArchSupport
	var bestLen int
	var found bool

	for prefix, support := range archMap {
		if strings.HasPrefix(gpuArch, prefix) && len(prefix) > bestLen {
			bestMatch = support
			bestLen = len(prefix)
			found = true
		}
	}

	return bestMatch, found
}

// ResolveBackendGPUSupport returns the support level for a backend on a GPU
// architecture, preferring a GPUProfile-declared entry before falling back to
// the in-code BackendGPUCompatibility table.
//
// Precedence (highest to lowest):
//  1. profile.Backends[backendName].Support (when profile is non-nil and has
//     the entry). VRAMMB is taken from profile.VRAMMB.
//  2. BackendGPUCompatibility[backendName][archPrefix] (legacy table, prefix
//     matched against gpuArch).
//
// The fallback chain is only consulted when the profile is explicitly nil or
// does not declare an entry for this backend. This matches the GPUProfile
// contract that an architecture's CR is the source of truth for backend
// support, and that the in-code table is a backstop for nodes that have not
// yet been onboarded to a profile.
func ResolveBackendGPUSupport(profile *aiv1alpha2.GPUProfileSpec, backendName, gpuArch string) (GPUArchSupport, bool) {
	if profile != nil {
		if support, ok := LookupGPUArchSupportFromProfile(profile, backendName); ok {
			return support, true
		}
	}
	return LookupGPUArchSupport(backendName, gpuArch)
}

// IsBackendSupported reports whether a backend may run on a GPU architecture.
// It consults ResolveBackendGPUSupport for the precedence cascade and applies
// the FlexInfer policy:
//
//   - SupportFull         -> true  (production-ready)
//   - SupportExperimental -> true  (allow with caveats; the controller is
//     expected to emit an ExperimentalGPUSupport
//     event so operators see the warning)
//   - SupportUnsupported  -> false (block deployment)
//   - no entry found      -> false (unknown combinations are rejected by the
//     scheduler defense-in-depth path; the
//     controller's validateBackendGPUCompatibility
//     treats "no entry" as "skip" — callers that need
//     that softer behavior should use
//     ResolveBackendGPUSupport directly and inspect
//     the boolean themselves)
//
// Note: support level is informational. Whether to actually deploy is a
// separate gate (annotations, runtime-paused, canary status). This helper only
// answers the static "can the backend run here?" question.
func IsBackendSupported(profile *aiv1alpha2.GPUProfileSpec, backendName, gpuArch string) bool {
	support, found := ResolveBackendGPUSupport(profile, backendName, gpuArch)
	if !found {
		return false
	}
	switch support.Level {
	case SupportFull, SupportExperimental:
		return true
	default:
		return false
	}
}

// LookupGPUArchSupportFromProfile returns the support level for a backend from a GPUProfile.
// Returns (support, true) if the profile has an entry for the backend, or (zero, false) otherwise.
func LookupGPUArchSupportFromProfile(profile *aiv1alpha2.GPUProfileSpec, backendName string) (GPUArchSupport, bool) {
	if profile == nil || profile.Backends == nil {
		return GPUArchSupport{}, false
	}
	bp, ok := profile.Backends[backendName]
	if !ok {
		return GPUArchSupport{}, false
	}
	var level GPUArchSupportLevel
	switch bp.Support {
	case "full":
		level = SupportFull
	case "experimental":
		level = SupportExperimental
	case "unsupported":
		level = SupportUnsupported
	default:
		return GPUArchSupport{}, false
	}
	return GPUArchSupport{Level: level, MaxVRAMMB: int(profile.VRAMMB)}, true
}

// ImageFromProfile returns the container image override for a backend from a GPUProfile.
// Returns ("", false) if no override is configured.
func ImageFromProfile(profile *aiv1alpha2.GPUProfileSpec, backendName string) (string, bool) {
	if profile == nil || profile.Backends == nil {
		return "", false
	}
	bp, ok := profile.Backends[backendName]
	if !ok || bp.Image == "" {
		return "", false
	}
	return bp.Image, true
}

// ResolveBackendImage returns the container image for a backend, preferring a
// GPUProfile-declared override before falling back to the backend's hardcoded
// arch rules.
//
// Precedence (highest to lowest):
//  1. profile.Backends[backendName].Image (when profile is non-nil and has the entry)
//  2. b.Image(vendor, arch) (existing arch-prefix rule chain)
//
// The fallback chain is only consulted when the profile is explicitly nil or
// does not declare an override for this backend. This matches the GPUProfile
// contract that an architecture's CR is the source of truth for image
// selection, and that the in-code arch tables are a backstop for nodes that
// have not yet been onboarded to a profile.
func ResolveBackendImage(b Backend, profile *aiv1alpha2.GPUProfileSpec, vendor GPUVendor, arch string) string {
	if profile != nil {
		if img, ok := ImageFromProfile(profile, b.Name()); ok {
			return img
		}
	}
	return b.Image(vendor, arch)
}

// EnvFromProfile returns the env vars declared by a GPUProfile, or nil if the
// profile is nil or has no env entries. Callers use the boolean to decide
// whether to fall back to a hardcoded source.
func EnvFromProfile(profile *aiv1alpha2.GPUProfileSpec) ([]corev1.EnvVar, bool) {
	if profile == nil || len(profile.Env) == 0 {
		return nil, false
	}
	return profile.Env, true
}

// ResolveBackendROCmEnv returns the AMD GPU environment variables for the
// runtime/Job paths, preferring a GPUProfile-declared env list before falling
// back to the in-code ROCmEnvVars switch.
//
// Precedence (highest to lowest):
//  1. profile.Env (when profile is non-nil and has at least one entry)
//  2. ROCmEnvVars(arch) (existing gfx110*/gfx90a/gfx942/gfx906 switch)
//
// The vendor argument is accepted for symmetry with ResolveBackendImage and to
// allow future per-vendor branching (e.g., NVIDIA-specific defaults). Today the
// fallback only fires for AMD vendors because ROCmEnvVars is ROCm-specific; for
// non-AMD vendors with no profile env, the helper returns an empty slice.
//
// The fallback chain is only consulted when the profile is explicitly nil or
// declares an empty env list. This matches the GPUProfile contract that an
// architecture's CR is the source of truth for env injection, and that the
// in-code arch tables are a backstop for nodes that have not yet been
// onboarded to a profile.
func ResolveBackendROCmEnv(profile *aiv1alpha2.GPUProfileSpec, vendor GPUVendor, arch string) []corev1.EnvVar {
	if env, ok := EnvFromProfile(profile); ok {
		return env
	}
	if vendor != GPUVendorAMD {
		return nil
	}
	return ROCmEnvVars(arch)
}

// QuantizerImageFromProfile returns the quantizer container image for a format from a GPUProfile.
// Returns ("", false) if no image is configured.
func QuantizerImageFromProfile(profile *aiv1alpha2.GPUProfileSpec, format string) (string, bool) {
	if profile == nil || profile.Quantization == nil || profile.Quantization.Images == nil {
		return "", false
	}
	if format == "" {
		return "", false
	}
	img, ok := profile.Quantization.Images[format]
	if !ok {
		img, ok = profile.Quantization.Images[strings.ToLower(format)]
	}
	if !ok || img == "" {
		return "", false
	}
	return img, true
}
