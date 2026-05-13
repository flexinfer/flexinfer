package generator

import (
	"testing"
)

func TestGetVendorCapabilities(t *testing.T) {
	tests := []struct {
		platform string
		wantNil  bool
		wantName string
	}{
		{"claude", false, "Claude Code"},
		{"gemini", false, "Gemini CLI"},
		{"codex", false, "Codex CLI"},
		{"opencode", false, "OpenCode"},
		{"kilocode", false, "Kilo Code"},
		{"antigravity", false, "Antigravity"},
		{"vscode", false, "VS Code"},
		{"zed", false, "Zed"},
		{"nonexistent", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			caps := GetVendorCapabilities(tt.platform)
			if tt.wantNil {
				if caps != nil {
					t.Errorf("expected nil for %q, got %+v", tt.platform, caps)
				}
				return
			}
			if caps == nil {
				t.Fatalf("expected non-nil for %q", tt.platform)
			}
			if caps.DisplayName != tt.wantName {
				t.Errorf("DisplayName = %q, want %q", caps.DisplayName, tt.wantName)
			}
		})
	}
}

func TestAllVendors(t *testing.T) {
	vendors := AllVendors()
	if len(vendors) < 7 {
		t.Errorf("expected at least 7 vendors, got %d", len(vendors))
	}
	// Verify key platforms are present.
	found := map[string]bool{}
	for _, v := range vendors {
		found[v] = true
	}
	for _, name := range []string{"claude", "gemini", "codex", "opencode", "kilocode"} {
		if !found[name] {
			t.Errorf("missing expected vendor %q", name)
		}
	}
}

func TestCheckVendorFeatures_NoHooksWarning(t *testing.T) {
	// Platforms without any hook support should generate a warning.
	noHookPlatforms := []string{"kilocode", "antigravity", "vscode", "zed"}
	for _, p := range noHookPlatforms {
		warnings := CheckVendorFeatures(p)
		if len(warnings) == 0 {
			t.Errorf("expected hook warning for %q, got none", p)
		}
		found := false
		for _, w := range warnings {
			if w.Feature == "hooks" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected hooks warning for %q", p)
		}
	}

	// Platforms with hooks should NOT generate the warning.
	hookPlatforms := []string{"claude", "gemini", "codex"}
	for _, p := range hookPlatforms {
		warnings := CheckVendorFeatures(p)
		for _, w := range warnings {
			if w.Feature == "hooks" {
				t.Errorf("unexpected hooks warning for %q: %s", p, w.Message)
			}
		}
	}
}

func TestVendorCapabilities_ClaudeHasFullHooks(t *testing.T) {
	caps := GetVendorCapabilities("claude")
	if !caps.SessionStartHook || !caps.SessionEndHook || !caps.PostToolUseHook || !caps.PreToolUseHook {
		t.Error("Claude Code should have full hook support")
	}
	if !caps.MCPServers || !caps.Permissions {
		t.Error("Claude Code should support MCP servers and permissions")
	}
}

func TestVendorCapabilities_CodexHasNotifyAndHooks(t *testing.T) {
	caps := GetVendorCapabilities("codex")
	if !caps.NotifyHook {
		t.Error("Codex should have notify hook support (config.toml notify = […])")
	}
	if !caps.SandboxMode {
		t.Error("Codex should have sandbox mode support")
	}
	// Codex v0.129.0 (2026-05-07) shipped a Claude-shape [hooks] block. We
	// emit hooks.json alongside config.toml so SessionStart fires natively.
	// `notify` is retained for turn-end keepalive + as a session-end signal.
	if !caps.SessionStartHook {
		t.Error("Codex should have SessionStart hook (hooks.json, Codex v0.129.0+)")
	}
	// PostToolUse is intentionally not exposed for codex: heartbeat_matcher
	// has no useful narrowing equivalent, so a hook would fire on every
	// tool call and bounce the TUI. notify + keepalive-wrap cover
	// keepalive without per-tool-call shell overhead.
	if caps.PostToolUseHook {
		t.Error("Codex should NOT advertise PostToolUse hook (per-tool heartbeat causes TUI bounce; notify covers keepalive)")
	}
}

func TestVendorCapabilities_OpenCodeHasPlugins(t *testing.T) {
	caps := GetVendorCapabilities("opencode")
	if !caps.PluginSystem {
		t.Error("OpenCode should have plugin system support")
	}
	if caps.SessionStartHook || caps.NotifyHook {
		t.Error("OpenCode should not have native hooks or notify")
	}
}
