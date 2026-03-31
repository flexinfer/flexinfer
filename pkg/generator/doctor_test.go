package generator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

	// Write config.toml with the current throttled Loom notify hook.
	content := `notify = ["sh", "-c", "HEARTBEAT_STAMP_FILE=\"${HOME}/.cache/loom/notify-heartbeat-codex-${WS_HASH}.stamp\"; NOW=\"$(date +%s)\"; LAST=\"$(cat \"$HEARTBEAT_STAMP_FILE\" 2>/dev/null || true)\"; case \"$LAST\" in ''|*[!0-9]*) ;; *) if [ $((NOW - LAST)) -lt 15 ]; then exit 0; fi ;; esac; exec loom agent heartbeat --agent-id codex --quiet", "--"]

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

func TestDoctorCheckCodexStaleUnthrottledNotify(t *testing.T) {
	tmpDir := t.TempDir()

	content := `approval_policy = "never"
sandbox_mode = "workspace-write"
notify = ["sh", "-c", "exec loom agent heartbeat --agent-id codex --quiet"]

[mcp_servers.loom]
command = "loom"
args = ["proxy"]
`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.toml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	health := DoctorCheck(nil, "codex", tmpDir)
	if health.Hooks != "stale" {
		t.Errorf("hooks = %s, want stale; details: %v", health.Hooks, health.Details)
	}
	if health.Status != "stale" {
		t.Errorf("status = %s, want stale", health.Status)
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

	// Create workspace .claude dir with an actual config file.
	claudeDir := filepath.Join(tmpDir, ".claude")
	os.MkdirAll(claudeDir, 0755)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"hooks":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Should prefer workspace-local.
	got := resolveConfigDir("claude", tmpDir, "/nonexistent")
	if got != claudeDir {
		t.Errorf("resolveConfigDir = %s, want %s", got, claudeDir)
	}

	// Without workspace dir, fall back to home.
	homeDir := t.TempDir()
	homeClaudeDir := filepath.Join(homeDir, ".claude")
	os.MkdirAll(homeClaudeDir, 0755)
	if err := os.WriteFile(filepath.Join(homeClaudeDir, "settings.json"), []byte(`{"hooks":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	got = resolveConfigDir("claude", filepath.Join(tmpDir, "nonexistent_workspace"), homeDir)
	if got != homeClaudeDir {
		t.Errorf("resolveConfigDir = %s, want %s", got, homeClaudeDir)
	}
}

func TestResolveConfigDir_IgnoresEmptyWorkspaceDir(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspaceCodexDir := filepath.Join(workspaceRoot, ".codex")
	if err := os.MkdirAll(workspaceCodexDir, 0755); err != nil {
		t.Fatal(err)
	}

	homeDir := t.TempDir()
	homeCodexDir := filepath.Join(homeDir, ".codex")
	if err := os.MkdirAll(homeCodexDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeCodexDir, "config.toml"), []byte(`notify = ["loom"]`), 0644); err != nil {
		t.Fatal(err)
	}

	got := resolveConfigDir("codex", workspaceRoot, homeDir)
	if got != homeCodexDir {
		t.Errorf("resolveConfigDir = %s, want %s", got, homeCodexDir)
	}
}

// =============================================================================
// Policy Health Tests
// =============================================================================

func TestCheckPolicyHealth_HooksPlatformWithHash(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a policy hash file to simulate a synced platform.
	if err := os.WriteFile(filepath.Join(tmpDir, PolicyHashFilename), []byte("abc123\n"), 0644); err != nil {
		t.Fatal(err)
	}

	health := &PlatformHealth{
		Platform: "claude",
		Policy:   "n/a",
	}
	checkPolicyHealth(health, "claude", tmpDir)

	if health.Policy != "ok" {
		t.Errorf("policy = %s, want ok", health.Policy)
	}
}

func TestCheckPolicyHealth_HooksPlatformWithoutHash(t *testing.T) {
	tmpDir := t.TempDir()
	// No policy hash file.

	health := &PlatformHealth{
		Platform: "claude",
		Policy:   "n/a",
	}
	checkPolicyHealth(health, "claude", tmpDir)

	if health.Policy != "not-configured" {
		t.Errorf("policy = %s, want not-configured", health.Policy)
	}

	// Should have a detail message suggesting regen.
	found := false
	for _, d := range health.Details {
		if strings.Contains(d, "policy hash not found") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected detail about missing policy hash, got: %v", health.Details)
	}
}

func TestCheckPolicyHealth_HooklessPlatform(t *testing.T) {
	tmpDir := t.TempDir()

	health := &PlatformHealth{
		Platform: "antigravity",
		Policy:   "n/a",
	}
	checkPolicyHealth(health, "antigravity", tmpDir)

	if health.Policy != "n/a" {
		t.Errorf("policy = %s, want n/a for hookless platform", health.Policy)
	}

	// Should have a detail about proxy-level enforcement.
	found := false
	for _, d := range health.Details {
		if strings.Contains(d, "proxy-level") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected proxy-level detail for hookless platform, got: %v", health.Details)
	}
}

func TestCheckPolicyHealth_GeminiHooksEnabled(t *testing.T) {
	tmpDir := t.TempDir()

	// Gemini has hooks.enabled: true in the profile.
	health := &PlatformHealth{
		Platform: "gemini",
		Policy:   "n/a",
	}
	checkPolicyHealth(health, "gemini", tmpDir)

	if health.Policy != "not-configured" {
		t.Errorf("policy = %s, want not-configured (no hash file)", health.Policy)
	}
}

func TestCheckPolicyHealth_ZedHookless(t *testing.T) {
	tmpDir := t.TempDir()

	health := &PlatformHealth{
		Platform: "zed",
		Policy:   "n/a",
	}
	checkPolicyHealth(health, "zed", tmpDir)

	if health.Policy != "n/a" {
		t.Errorf("policy = %s, want n/a for zed (no native hooks)", health.Policy)
	}
}

func TestCheckPolicyHealth_CodexHooksEnabled(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a hash file.
	if err := os.WriteFile(filepath.Join(tmpDir, PolicyHashFilename), []byte("hash123\n"), 0644); err != nil {
		t.Fatal(err)
	}

	health := &PlatformHealth{
		Platform: "codex",
		Policy:   "n/a",
	}
	checkPolicyHealth(health, "codex", tmpDir)

	if health.Policy != "ok" {
		t.Errorf("policy = %s, want ok", health.Policy)
	}
}

func TestDeriveStatus_StalePolicyMakesStale(t *testing.T) {
	h := &PlatformHealth{
		Platform: "claude",
		Hooks:    "ok",
		Perms:    "ok",
		Schema:   "ok",
		Policy:   "stale",
	}
	got := deriveStatus(h)
	if got != "stale" {
		t.Errorf("deriveStatus() = %s, want stale when policy is stale", got)
	}
}

func TestDeriveStatus_PolicyOkDoesNotAffectStatus(t *testing.T) {
	h := &PlatformHealth{
		Platform: "claude",
		Hooks:    "ok",
		Perms:    "ok",
		Schema:   "ok",
		Policy:   "ok",
	}
	got := deriveStatus(h)
	if got != "healthy" {
		t.Errorf("deriveStatus() = %s, want healthy", got)
	}
}

func TestDeriveStatus_PolicyNADoesNotAffectStatus(t *testing.T) {
	h := &PlatformHealth{
		Platform: "antigravity",
		Hooks:    "n/a",
		Perms:    "n/a",
		Schema:   "n/a",
		Policy:   "n/a",
	}
	got := deriveStatus(h)
	if got != "healthy" {
		t.Errorf("deriveStatus() = %s, want healthy for hookless platform", got)
	}
}

func TestDoctorCheck_IncludesPolicyField(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a Claude config dir with settings.json.
	claudeProfile, _ := GetPlatformProfile("claude")
	expectedHooks := claudeHooks(nil, claudeProfile, "")
	expectedPerms := claudePermissions(nil)
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

	// Policy should be "not-configured" because no hash file exists.
	if health.Policy != "not-configured" {
		t.Errorf("policy = %s, want not-configured", health.Policy)
	}

	// Now write a policy hash and check again.
	if err := os.WriteFile(filepath.Join(tmpDir, PolicyHashFilename), []byte("somehash\n"), 0644); err != nil {
		t.Fatal(err)
	}
	health2 := DoctorCheck(nil, "claude", tmpDir)
	if health2.Policy != "ok" {
		t.Errorf("policy = %s, want ok after hash file created", health2.Policy)
	}
}

func TestDoctorCheckAll_ReportsPolicyStatus(t *testing.T) {
	tmpDir := t.TempDir()
	report := DoctorCheckAll(nil, tmpDir, tmpDir)

	for _, h := range report.Platforms {
		// All should be not_configured since tmpDir has no platform dirs.
		// Policy field should be set (either "n/a" for unconfigured platforms).
		if h.Policy == "" {
			t.Errorf("[%s] policy field should not be empty", h.Platform)
		}
	}
}
