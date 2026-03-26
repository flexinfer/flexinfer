package generator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const geminiDoctorConfig = `# Generated MCP configuration for gemini
[mcp_servers.loom]
command = "loom"
args = ["proxy"]
`

func TestDoctorCheckNotConfigured(t *testing.T) {
	tmpDir := t.TempDir()
	health := DoctorCheck(nil, "claude", filepath.Join(tmpDir, "nonexistent"))
	if health.Status != "not_configured" {
		t.Errorf("expected not_configured, got %s", health.Status)
	}
	if health.Platform != "claude" {
		t.Errorf("platform = %s, want claude", health.Platform)
	}
}

func TestDoctorCheckClaudeHealthy(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a settings.json with the expected hooks and permissions.
	claudeProfile, _ := GetPlatformProfile("claude")
	expectedHooks := claudeHooks(nil, claudeProfile, "")
	expectedPerms := claudePermissions(nil) // nil registry → minimal fallback
	settings := map[string]any{
		"hooks":       expectedHooks,
		"permissions": expectedPerms,
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	health := DoctorCheck(nil, "claude", tmpDir)
	if health.Hooks != "ok" {
		t.Errorf("hooks = %s, want ok; details: %v", health.Hooks, health.Details)
	}
	if health.Perms != "ok" {
		t.Errorf("perms = %s, want ok; details: %v", health.Perms, health.Details)
	}
	if health.Status != "healthy" {
		t.Errorf("status = %s, want healthy; details: %v", health.Status, health.Details)
	}
}

func TestDoctorCheckClaudeStaleHooks(t *testing.T) {
	tmpDir := t.TempDir()

	// Write settings.json with different hooks than expected.
	settings := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []map[string]any{
				{"hooks": []map[string]any{{"type": "command", "command": "echo old"}}},
			},
		},
		"permissions": claudePermissions(nil),
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	health := DoctorCheck(nil, "claude", tmpDir)
	if health.Hooks != "stale" {
		t.Errorf("hooks = %s, want stale", health.Hooks)
	}
	if health.Status != "stale" {
		t.Errorf("status = %s, want stale", health.Status)
	}
}

func TestDoctorCheckClaudeMissingSettings(t *testing.T) {
	tmpDir := t.TempDir()
	// No settings.json in tmpDir.

	health := DoctorCheck(nil, "claude", tmpDir)
	if health.Hooks != "missing" {
		t.Errorf("hooks = %s, want missing", health.Hooks)
	}
	if health.Perms != "missing" {
		t.Errorf("perms = %s, want missing", health.Perms)
	}
}

func TestDoctorCheckGeminiHealthy(t *testing.T) {
	tmpDir := t.TempDir()

	// Write settings.json with expected gemini hooks and policy.
	expected := geminiHooksConfig()
	data, err := json.MarshalIndent(expected, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(geminiDoctorConfig), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	health := DoctorCheck(nil, "gemini", tmpDir)
	if health.Hooks != "ok" {
		t.Errorf("hooks = %s, want ok; details: %v", health.Hooks, health.Details)
	}
	if health.Perms != "ok" {
		t.Errorf("perms = %s, want ok; details: %v", health.Perms, health.Details)
	}
	if health.Status != "healthy" {
		t.Errorf("status = %s, want healthy; details: %v", health.Status, health.Details)
	}
}

func TestDoctorCheckGeminiStaleHooks(t *testing.T) {
	tmpDir := t.TempDir()

	settings := geminiHooksConfig()
	settings["hooks"] = map[string]any{
		"SessionStart": []map[string]any{
			{"hooks": []map[string]any{{"type": "command", "command": "echo old"}}},
		},
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(geminiDoctorConfig), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	health := DoctorCheck(nil, "gemini", tmpDir)
	if health.Hooks != "stale" {
		t.Errorf("hooks = %s, want stale", health.Hooks)
	}
	if health.Perms != "ok" {
		t.Errorf("perms = %s, want ok; details: %v", health.Perms, health.Details)
	}
	if health.Status != "stale" {
		t.Errorf("status = %s, want stale", health.Status)
	}
}

func TestDoctorCheckGeminiMissingConfig(t *testing.T) {
	tmpDir := t.TempDir()

	expected := geminiHooksConfig()
	data, err := json.MarshalIndent(expected, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	health := DoctorCheck(nil, "gemini", tmpDir)
	if health.Hooks != "ok" {
		t.Errorf("hooks = %s, want ok; details: %v", health.Hooks, health.Details)
	}
	if health.Perms != "missing" {
		t.Errorf("perms = %s, want missing; details: %v", health.Perms, health.Details)
	}
	if health.Status != "stale" {
		t.Errorf("status = %s, want stale; details: %v", health.Status, health.Details)
	}
}

func TestDoctorCheckGeminiPolicyDrift(t *testing.T) {
	tmpDir := t.TempDir()

	settings := geminiHooksConfig()
	settings["general"] = map[string]any{
		"defaultApprovalMode": "manual",
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(geminiDoctorConfig), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "settings.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	health := DoctorCheck(nil, "gemini", tmpDir)
	if health.Hooks != "ok" {
		t.Errorf("hooks = %s, want ok; details: %v", health.Hooks, health.Details)
	}
	if health.Perms != "drift" {
		t.Errorf("perms = %s, want drift; details: %v", health.Perms, health.Details)
	}
	if health.Status != "stale" {
		t.Errorf("status = %s, want stale; details: %v", health.Status, health.Details)
	}
}

func TestDoctorCheckCodexHealthy(t *testing.T) {
	tmpDir := t.TempDir()

	// Write config.toml with a notify hook.
	content := `notify = ["loom", "agent", "heartbeat", "--agent-id", "codex"]

[mcp_servers.loom]
command = "loom"
args = ["proxy"]
`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	health := DoctorCheck(nil, "codex", tmpDir)
	if health.Hooks != "ok" {
		t.Errorf("hooks = %s, want ok; details: %v", health.Hooks, health.Details)
	}
	if health.Status != "healthy" {
		t.Errorf("status = %s, want healthy", health.Status)
	}
}

func TestDoctorCheckCodexHealthyWithoutHeartbeatString(t *testing.T) {
	tmpDir := t.TempDir()

	// Use a non-heartbeat notify command to ensure the doctor checks the
	// notify hook structure rather than a single hard-coded subcommand name.
	content := `approval_policy = "never"
sandbox_mode = "workspace-write"
notify = ["sh", "-c", "exec loom agent keepalive --agent-id codex --quiet"]

[mcp_servers.loom]
command = "loom"
args = ["proxy"]
`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	health := DoctorCheck(nil, "codex", tmpDir)
	if health.Hooks != "ok" {
		t.Errorf("hooks = %s, want ok; details: %v", health.Hooks, health.Details)
	}
	if health.Status != "healthy" {
		t.Errorf("status = %s, want healthy", health.Status)
	}
}

func TestDoctorCheckCodexMissingNotify(t *testing.T) {
	tmpDir := t.TempDir()

	// config.toml without notify line.
	content := `[mcp_servers.loom]
command = "loom"
args = ["proxy"]
`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	health := DoctorCheck(nil, "codex", tmpDir)
	if health.Hooks != "missing" {
		t.Errorf("hooks = %s, want missing", health.Hooks)
	}
}

func TestDoctorCheckAllReturnsAllPlatforms(t *testing.T) {
	tmpDir := t.TempDir()
	report := DoctorCheckAll(nil, tmpDir, tmpDir)
	if len(report.Platforms) != len(DoctorPlatforms) {
		t.Errorf("platforms count = %d, want %d", len(report.Platforms), len(DoctorPlatforms))
	}

	// All should be not_configured since tmpDir has no platform dirs.
	for _, h := range report.Platforms {
		if h.Status != "not_configured" {
			t.Errorf("[%s] status = %s, want not_configured", h.Platform, h.Status)
		}
	}
}

func TestJsonFingerprint(t *testing.T) {
	// Same data should produce same fingerprint regardless of key order.
	a := map[string]any{"z": 1, "a": 2, "m": 3}
	b := map[string]any{"a": 2, "m": 3, "z": 1}
	if jsonFingerprint(a) != jsonFingerprint(b) {
		t.Error("fingerprints should match for same data with different key order")
	}

	// Different data should produce different fingerprints.
	c := map[string]any{"a": 1, "b": 2}
	if jsonFingerprint(a) == jsonFingerprint(c) {
		t.Error("fingerprints should differ for different data")
	}
}

func TestDeriveStatus(t *testing.T) {
	tests := []struct {
		name     string
		health   PlatformHealth
		expected string
	}{
		{
			name:     "all ok",
			health:   PlatformHealth{Platform: "claude", Hooks: "ok", Perms: "ok", Schema: "ok"},
			expected: "healthy",
		},
		{
			name:     "stale hooks",
			health:   PlatformHealth{Platform: "claude", Hooks: "stale", Perms: "ok", Schema: "ok"},
			expected: "stale",
		},
		{
			name:     "drifted perms",
			health:   PlatformHealth{Platform: "claude", Hooks: "ok", Perms: "drift", Schema: "ok"},
			expected: "stale",
		},
		{
			name:     "schema errors",
			health:   PlatformHealth{Platform: "claude", Hooks: "ok", Perms: "ok", Schema: "errors"},
			expected: "errors",
		},
		{
			name:     "missing hooks on claude",
			health:   PlatformHealth{Platform: "claude", Hooks: "missing", Perms: "n/a", Schema: "n/a"},
			expected: "stale",
		},
		{
			name:     "missing perms on gemini",
			health:   PlatformHealth{Platform: "gemini", Hooks: "ok", Perms: "missing", Schema: "ok"},
			expected: "stale",
		},
		{
			name:     "missing hooks on kilocode (no hooks support)",
			health:   PlatformHealth{Platform: "kilocode", Hooks: "n/a", Perms: "n/a", Schema: "n/a"},
			expected: "healthy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveStatus(&tt.health)
			if got != tt.expected {
				t.Errorf("deriveStatus() = %s, want %s", got, tt.expected)
			}
		})
	}
}

func TestResolveConfigDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create workspace .claude dir.
	claudeDir := filepath.Join(tmpDir, ".claude")
	os.MkdirAll(claudeDir, 0755)

	// Should prefer workspace-local.
	got := resolveConfigDir("claude", tmpDir, "/nonexistent")
	if got != claudeDir {
		t.Errorf("resolveConfigDir = %s, want %s", got, claudeDir)
	}

	// Without workspace dir, fall back to home.
	homeDir := t.TempDir()
	homeClaudeDir := filepath.Join(homeDir, ".claude")
	os.MkdirAll(homeClaudeDir, 0755)

	got = resolveConfigDir("claude", filepath.Join(tmpDir, "nonexistent_workspace"), homeDir)
	if got != homeClaudeDir {
		t.Errorf("resolveConfigDir = %s, want %s", got, homeClaudeDir)
	}
}
