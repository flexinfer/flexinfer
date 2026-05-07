package backend

import (
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
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
