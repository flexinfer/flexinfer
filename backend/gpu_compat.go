package backend

import "strings"

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
		"gfx906": {SupportFull, 16384},
		"sm_5":   {SupportUnsupported, 0},
	},
	"vllm-omni": {
		"gfx110": {SupportFull, 24576},
		"gfx906": {SupportFull, 16384},
		"sm_5":   {SupportUnsupported, 0},
	},
	"llamacpp": {
		"gfx110": {SupportFull, 24576},
		"gfx906": {SupportFull, 16384},
		"sm_5":   {SupportFull, 4096},
	},
	"diffusers": {
		"gfx110": {SupportFull, 24576},
		"gfx906": {SupportExperimental, 16384},
		"sm_5":   {SupportUnsupported, 0},
	},
	"comfyui": {
		"gfx110": {SupportFull, 24576},
		"gfx906": {SupportExperimental, 16384},
		"sm_5":   {SupportUnsupported, 0},
	},
	"ollama": {
		"gfx110": {SupportFull, 24576},
		"gfx906": {SupportFull, 16384},
		"sm_5":   {SupportFull, 4096},
	},
	"mlc-llm": {
		"gfx110": {SupportFull, 24576},
		"gfx906": {SupportExperimental, 16384},
		"sm_5":   {SupportFull, 4096},
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
