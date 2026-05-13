package generator

import (
	"testing"
)

func TestLoadProfiles_AllPlatformsPresent(t *testing.T) {
	t.Parallel()

	expected := []string{
		"claude", "claude_desktop", "gemini", "codex", "opencode",
		"vscode", "antigravity", "kilocode", "zed",
	}

	profiles, err := loadProfiles()
	if err != nil {
		t.Fatalf("loadProfiles: %v", err)
	}

	for _, name := range expected {
		if _, ok := profiles[name]; !ok {
			t.Errorf("expected platform %q in profiles", name)
		}
	}
}

func TestGetPlatformProfile_Claude(t *testing.T) {
	t.Parallel()

	p, err := GetPlatformProfile("claude")
	if err != nil {
		t.Fatalf("GetPlatformProfile: %v", err)
	}

	if p.ConfigFormat != "json" {
		t.Errorf("config_format = %q, want json", p.ConfigFormat)
	}
	if p.ConfigFile != "mcp.json" {
		t.Errorf("config_file = %q, want mcp.json", p.ConfigFile)
	}
	if p.ConfigRoot != "mcpServers" {
		t.Errorf("config_root = %q, want mcpServers", p.ConfigRoot)
	}
	if !p.Hooks.Enabled {
		t.Error("hooks.enabled = false, want true")
	}
	if p.Hooks.File != "settings.json" {
		t.Errorf("hooks.file = %q, want settings.json", p.Hooks.File)
	}
	if len(p.Hooks.PolicyRefs) != 1 || p.Hooks.PolicyRefs[0] != "gitops_flux" {
		t.Fatalf("hooks.policy_refs = %#v, want [gitops_flux]", p.Hooks.PolicyRefs)
	}
	if !p.Capabilities.Permissions {
		t.Error("capabilities.permissions = false, want true")
	}
	if p.LoomProxy.AgentHint != "claude-code" {
		t.Errorf("loom_proxy.agent_hint = %q, want claude-code", p.LoomProxy.AgentHint)
	}
	if p.LoomProxy.ToolProfile != "llm-core" {
		t.Errorf("loom_proxy.tool_profile = %q, want llm-core", p.LoomProxy.ToolProfile)
	}
	if p.LoomProxy.MaxTools != 140 {
		t.Errorf("loom_proxy.max_tools = %d, want 140", p.LoomProxy.MaxTools)
	}
}

func TestGetPlatformProfile_Codex(t *testing.T) {
	t.Parallel()

	p, err := GetPlatformProfile("codex")
	if err != nil {
		t.Fatalf("GetPlatformProfile: %v", err)
	}

	if p.ConfigFormat != "toml" {
		t.Errorf("config_format = %q, want toml", p.ConfigFormat)
	}
	if !p.Capabilities.Sandbox {
		t.Error("capabilities.sandbox = false, want true")
	}
	if !p.Features.RequiresPreamble {
		t.Error("features.requires_preamble = false, want true")
	}
	if p.LoomProxy.AgentHint != "codex" {
		t.Errorf("loom_proxy.agent_hint = %q, want codex", p.LoomProxy.AgentHint)
	}
	if p.LoomProxy.ToolProfile != "llm-core" {
		t.Errorf("loom_proxy.tool_profile = %q, want llm-core", p.LoomProxy.ToolProfile)
	}
	if p.LoomProxy.MaxTools != 140 {
		t.Errorf("loom_proxy.max_tools = %d, want 140", p.LoomProxy.MaxTools)
	}
}

func TestGetPlatformProfile_ClaudeDesktopProxy(t *testing.T) {
	t.Parallel()

	p, err := GetPlatformProfile("claude_desktop")
	if err != nil {
		t.Fatalf("GetPlatformProfile: %v", err)
	}

	if p.LoomProxy.AgentHint != "claude-desktop" {
		t.Errorf("loom_proxy.agent_hint = %q, want claude-desktop", p.LoomProxy.AgentHint)
	}
	if p.LoomProxy.ToolProfile != "llm-core" {
		t.Errorf("loom_proxy.tool_profile = %q, want llm-core", p.LoomProxy.ToolProfile)
	}
	if p.LoomProxy.MaxTools != 140 {
		t.Errorf("loom_proxy.max_tools = %d, want 140", p.LoomProxy.MaxTools)
	}
	if got := p.LoomProxy.Env["LOOM_PROXY_IDLE_EXIT_SECONDS"]; got != "0" {
		t.Errorf("loom_proxy.env[LOOM_PROXY_IDLE_EXIT_SECONDS] = %q, want 0", got)
	}
}

func TestGetPlatformProfile_OpenCode(t *testing.T) {
	t.Parallel()

	p, err := GetPlatformProfile("opencode")
	if err != nil {
		t.Fatalf("GetPlatformProfile: %v", err)
	}

	if p.ConfigRoot != "mcp" {
		t.Errorf("config_root = %q, want mcp", p.ConfigRoot)
	}
	if p.Features.CommandFormat != "array" {
		t.Errorf("features.command_format = %q, want array", p.Features.CommandFormat)
	}
	if p.Features.EnvKey != "environment" {
		t.Errorf("features.env_key = %q, want environment", p.Features.EnvKey)
	}
	if p.Features.TimeoutUnit != "milliseconds" {
		t.Errorf("features.timeout_unit = %q, want milliseconds", p.Features.TimeoutUnit)
	}
	if !p.Capabilities.PluginSystem {
		t.Error("capabilities.plugin_system = false, want true")
	}
}

func TestGetPlatformProfile_Unknown(t *testing.T) {
	t.Parallel()

	_, err := GetPlatformProfile("nonexistent")
	if err == nil {
		t.Error("expected error for unknown platform")
	}
}

func TestToVendorCapabilities_Claude(t *testing.T) {
	t.Parallel()

	p, _ := GetPlatformProfile("claude")
	vc := p.ToVendorCapabilities()

	if !vc.SessionStartHook {
		t.Error("SessionStartHook = false, want true")
	}
	if !vc.SessionEndHook {
		t.Error("SessionEndHook = false, want true")
	}
	if !vc.PostToolUseHook {
		t.Error("PostToolUseHook = false, want true")
	}
	if !vc.PreToolUseHook {
		t.Error("PreToolUseHook = false, want true")
	}
	if vc.NotifyHook {
		t.Error("NotifyHook = true, want false")
	}
	if !vc.Permissions {
		t.Error("Permissions = false, want true")
	}
}

func TestToVendorCapabilities_Codex(t *testing.T) {
	t.Parallel()

	p, _ := GetPlatformProfile("codex")
	vc := p.ToVendorCapabilities()

	if !vc.NotifyHook {
		t.Error("NotifyHook = false, want true (config.toml notify still emitted)")
	}
	// Codex v0.129.0 (2026-05-07) added a Claude-shape [hooks] block. We emit
	// hooks.json alongside config.toml, so SessionStart/Stop/PostToolUse are
	// now native lifecycle hooks for Codex too.
	if !vc.SessionStartHook {
		t.Error("SessionStartHook = false, want true (Codex hooks.json)")
	}
	if !vc.PostToolUseHook {
		t.Error("PostToolUseHook = false, want true (Codex hooks.json)")
	}
	if !vc.SandboxMode {
		t.Error("SandboxMode = false, want true")
	}
}

func TestAllPlatformNames_IncludesAll(t *testing.T) {
	t.Parallel()

	names := AllPlatformNames()
	if len(names) < 9 {
		t.Errorf("AllPlatformNames() returned %d, want >= 9", len(names))
	}

	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	for _, expected := range []string{"claude", "codex", "gemini", "opencode", "vscode"} {
		if !nameSet[expected] {
			t.Errorf("expected %q in AllPlatformNames()", expected)
		}
	}
}

func TestProfileConfigFormatCoverage(t *testing.T) {
	t.Parallel()

	profiles, _ := loadProfiles()
	jsonCount, tomlCount := 0, 0
	for _, p := range profiles {
		switch p.ConfigFormat {
		case "json":
			jsonCount++
		case "toml":
			tomlCount++
		default:
			t.Errorf("unexpected config_format %q for %s", p.ConfigFormat, p.DisplayName)
		}
	}
	if jsonCount == 0 {
		t.Error("no JSON-format platforms found")
	}
	if tomlCount == 0 {
		t.Error("no TOML-format platforms found")
	}
}
