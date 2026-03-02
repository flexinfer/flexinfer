package generator

// VendorCapabilities tracks which features each AI coding agent platform supports.
// Used by loom doctor to warn when a configured feature isn't supported by the platform,
// and by the generator to guard against emitting unsupported settings.
type VendorCapabilities struct {
	// Lifecycle hooks
	SessionStartHook bool
	SessionEndHook   bool
	PostToolUseHook  bool
	PreToolUseHook   bool
	NotifyHook       bool // Codex-style "run on every turn" hook

	// Configuration features
	MCPServers   bool
	Permissions  bool
	SandboxMode  bool
	WebSearch    bool
	CustomModels bool
	PluginSystem bool // OpenCode-style plugin hooks

	// Labels for reporting
	DisplayName string
}

// GetVendorCapabilities returns the capabilities for a platform, derived from
// the YAML platform profile. Returns nil if the platform is unknown.
func GetVendorCapabilities(platform string) *VendorCapabilities {
	p, err := GetPlatformProfile(platform)
	if err != nil {
		return nil
	}
	return p.ToVendorCapabilities()
}

// AllVendors returns all known platform names from the YAML profiles.
func AllVendors() []string {
	return AllPlatformNames()
}

// VendorWarning represents a feature mismatch between config and vendor capabilities.
type VendorWarning struct {
	Platform string
	Feature  string
	Message  string
}

// CheckVendorFeatures compares what the generator would emit for a platform
// against the vendor capability matrix and returns any warnings.
func CheckVendorFeatures(platform string) []VendorWarning {
	caps := GetVendorCapabilities(platform)
	if caps == nil {
		return nil
	}

	var warnings []VendorWarning

	// Check hook-related features.
	if !caps.SessionStartHook && !caps.NotifyHook && !caps.PluginSystem {
		warnings = append(warnings, VendorWarning{
			Platform: platform,
			Feature:  "hooks",
			Message:  caps.DisplayName + " has no hook support; agent presence requires proxy --agent-hint fallback",
		})
	}

	// caps.Permissions and caps.SandboxMode are informational;
	// actual checks are deferred to the caller with registry access.

	return warnings
}
