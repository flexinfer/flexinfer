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

// vendorMatrix maps platform names to their known capabilities.
var vendorMatrix = map[string]*VendorCapabilities{
	"claude": {
		DisplayName:      "Claude Code",
		SessionStartHook: true,
		SessionEndHook:   true,
		PostToolUseHook:  true,
		PreToolUseHook:   true,
		NotifyHook:       false,
		MCPServers:       true,
		Permissions:      true,
		SandboxMode:      false,
		WebSearch:        true,
		CustomModels:     true,
		PluginSystem:     false,
	},
	"gemini": {
		DisplayName:      "Gemini CLI",
		SessionStartHook: true,
		SessionEndHook:   true,
		PostToolUseHook:  true,
		PreToolUseHook:   false,
		NotifyHook:       false,
		MCPServers:       true,
		Permissions:      false,
		SandboxMode:      false,
		WebSearch:        true,
		CustomModels:     true,
		PluginSystem:     false,
	},
	"codex": {
		DisplayName:      "Codex CLI",
		SessionStartHook: false,
		SessionEndHook:   false,
		PostToolUseHook:  false,
		PreToolUseHook:   false,
		NotifyHook:       true,
		MCPServers:       true,
		Permissions:      true,
		SandboxMode:      true,
		WebSearch:        true,
		CustomModels:     true,
		PluginSystem:     false,
	},
	"opencode": {
		DisplayName:      "OpenCode",
		SessionStartHook: false,
		SessionEndHook:   false,
		PostToolUseHook:  false,
		PreToolUseHook:   false,
		NotifyHook:       false,
		MCPServers:       true,
		Permissions:      false,
		SandboxMode:      false,
		WebSearch:        false,
		CustomModels:     true,
		PluginSystem:     true,
	},
	"kilocode": {
		DisplayName:      "Kilo Code",
		SessionStartHook: false,
		SessionEndHook:   false,
		PostToolUseHook:  false,
		PreToolUseHook:   false,
		NotifyHook:       false,
		MCPServers:       true,
		Permissions:      false,
		SandboxMode:      false,
		WebSearch:        false,
		CustomModels:     false,
		PluginSystem:     false,
	},
	"antigravity": {
		DisplayName:      "Antigravity",
		SessionStartHook: false,
		SessionEndHook:   false,
		PostToolUseHook:  false,
		PreToolUseHook:   false,
		NotifyHook:       false,
		MCPServers:       true,
		Permissions:      false,
		SandboxMode:      false,
		WebSearch:        false,
		CustomModels:     false,
		PluginSystem:     false,
	},
	"vscode": {
		DisplayName:      "VS Code",
		SessionStartHook: false,
		SessionEndHook:   false,
		PostToolUseHook:  false,
		PreToolUseHook:   false,
		NotifyHook:       false,
		MCPServers:       true,
		Permissions:      false,
		SandboxMode:      false,
		WebSearch:        false,
		CustomModels:     false,
		PluginSystem:     false,
	},
	"zed": {
		DisplayName:      "Zed",
		SessionStartHook: false,
		SessionEndHook:   false,
		PostToolUseHook:  false,
		PreToolUseHook:   false,
		NotifyHook:       false,
		MCPServers:       true,
		Permissions:      false,
		SandboxMode:      false,
		WebSearch:        false,
		CustomModels:     false,
		PluginSystem:     false,
	},
}

// GetVendorCapabilities returns the capabilities for a platform, or nil if unknown.
func GetVendorCapabilities(platform string) *VendorCapabilities {
	return vendorMatrix[platform]
}

// AllVendors returns all known platform names.
func AllVendors() []string {
	names := make([]string, 0, len(vendorMatrix))
	for name := range vendorMatrix {
		names = append(names, name)
	}
	return names
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
	caps := vendorMatrix[platform]
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
