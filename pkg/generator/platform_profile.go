package generator

import (
	_ "embed"
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed platform_profiles.yaml
var embeddedProfiles []byte

// PlatformProfile describes a platform's config format, hooks, capabilities,
// and serialization features. Loaded from platform_profiles.yaml at startup.
type PlatformProfile struct {
	DisplayName  string            `yaml:"display_name"`
	ConfigFormat string            `yaml:"config_format"` // "json" | "toml"
	ConfigFile   string            `yaml:"config_file"`   // e.g. "mcp.json", "config.toml"
	ConfigRoot   string            `yaml:"config_root"`   // JSON root key (e.g. "mcpServers")
	Hooks        HookProfile       `yaml:"hooks"`
	LoomProxy    LoomProxyProfile  `yaml:"loom_proxy"`
	Capabilities CapabilityProfile `yaml:"capabilities"`
	Features     FeatureProfile    `yaml:"features"`
}

// HookProfile describes lifecycle hook support for a platform.
type HookProfile struct {
	Enabled          bool     `yaml:"enabled"`
	File             string   `yaml:"file"`
	Type             string   `yaml:"type"` // "json" (default) or "typescript"
	Events           []string `yaml:"events"`
	AgentID          string   `yaml:"agent_id"`
	AgentType        string   `yaml:"agent_type"`
	Description      string   `yaml:"description"`
	SessionEndEvent  string   `yaml:"session_end_event"`
	HeartbeatEvent   string   `yaml:"heartbeat_event"`
	HeartbeatMatcher string   `yaml:"heartbeat_matcher"`
	Extras           []string `yaml:"extras"` // e.g. ["preToolUse_guardrails", "postToolUse_formatters"]
}

// LoomProxyProfile configures the loom proxy command arguments for a platform.
type LoomProxyProfile struct {
	AgentHint   string `yaml:"agent_hint,omitempty"`
	ToolProfile string `yaml:"tool_profile,omitempty"`
	MaxTools    int    `yaml:"max_tools,omitempty"`
}

// CapabilityProfile captures what a platform supports.
type CapabilityProfile struct {
	MCPServers   bool `yaml:"mcp_servers"`
	Permissions  bool `yaml:"permissions"`
	Sandbox      bool `yaml:"sandbox"`
	WebSearch    bool `yaml:"web_search"`
	CustomModels bool `yaml:"custom_models"`
	PluginSystem bool `yaml:"plugin_system"`
}

// FeatureProfile describes serialization-level behaviors.
type FeatureProfile struct {
	CommandFormat       string `yaml:"command_format"` // "separate" | "array"
	EnvKey              string `yaml:"env_key"`        // "env" | "environment"
	TimeoutUnit         string `yaml:"timeout_unit"`   // "seconds" | "milliseconds"
	TimeoutField        string `yaml:"timeout_field"`  // field name for timeout
	SupportsTimeout     bool   `yaml:"supports_timeout"`
	SupportsDescription bool   `yaml:"supports_description"`
	RequiresPreamble    bool   `yaml:"requires_preamble"`
}

// profilesFile is the top-level YAML structure.
type profilesFile struct {
	Platforms map[string]*PlatformProfile `yaml:"platforms"`
}

var (
	profileRegistry map[string]*PlatformProfile
	profileOnce     sync.Once
	profileErr      error
)

func loadProfiles() (map[string]*PlatformProfile, error) {
	profileOnce.Do(func() {
		var pf profilesFile
		if err := yaml.Unmarshal(embeddedProfiles, &pf); err != nil {
			profileErr = fmt.Errorf("parse platform profiles: %w", err)
			return
		}
		profileRegistry = pf.Platforms
	})
	return profileRegistry, profileErr
}

// GetPlatformProfile returns the profile for a platform, or an error if unknown.
func GetPlatformProfile(platform string) (*PlatformProfile, error) {
	profiles, err := loadProfiles()
	if err != nil {
		return nil, err
	}
	p, ok := profiles[platform]
	if !ok {
		return nil, fmt.Errorf("unknown platform: %q", platform)
	}
	return p, nil
}

// AllPlatformNames returns the names of all defined platforms.
func AllPlatformNames() []string {
	profiles, err := loadProfiles()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	return names
}

// ToVendorCapabilities converts a PlatformProfile to the legacy
// VendorCapabilities struct for backward compatibility.
func (p *PlatformProfile) ToVendorCapabilities() *VendorCapabilities {
	vc := &VendorCapabilities{
		DisplayName:  p.DisplayName,
		MCPServers:   p.Capabilities.MCPServers,
		Permissions:  p.Capabilities.Permissions,
		SandboxMode:  p.Capabilities.Sandbox,
		WebSearch:    p.Capabilities.WebSearch,
		CustomModels: p.Capabilities.CustomModels,
		PluginSystem: p.Capabilities.PluginSystem,
	}
	for _, evt := range p.Hooks.Events {
		switch evt {
		case "sessionStart":
			vc.SessionStartHook = true
		case "sessionEnd":
			vc.SessionEndHook = true
		case "postToolUse", "toolExecuteAfter":
			vc.PostToolUseHook = true
		case "preToolUse":
			vc.PreToolUseHook = true
		case "notify":
			vc.NotifyHook = true
		}
	}
	return vc
}
