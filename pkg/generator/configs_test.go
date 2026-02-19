package generator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/validator"
)

// testRegistry returns a registry with platform_permissions populated for testing.
func testRegistry() *registry.Registry {
	return &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"agents": {
				Settings: map[string]any{
					"dirty_worktree_mode":                   "continue_scoped_commits",
					"dirty_worktree_nudge_on_session_start": true,
					"dirty_worktree_nudge_message":          "Dirty worktree detected. Continue on current branch with scoped commits.",
				},
			},
			"claude": {
				AdditionalDirectories: []string{"~/workspace"},
				Allow: []string{
					"mcp__loom",
					"Task", "Read", "Edit", "Write", "Glob", "Grep",
					"Bash(go *)", "Bash(git *)", "Bash(gh *)", "Bash(glab *)",
					"Bash(make *)", "Bash(kubectl *)", "Bash(loom *)",
					"WebFetch", "WebSearch",
				},
				Deny: []string{
					"Bash(kubectl edit *)",
					"Bash(kubectl set env *)",
				},
			},
			"codex": {
				Settings: map[string]any{
					"approval_policy":                    "never",
					"suppress_unstable_features_warning": true,
					"sandbox_mode":                       "workspace-write",
					"features": map[string]any{
						"include_apply_patch_tool": true,
						"apply_patch_freeform":     true,
						"unified_exec":             true,
					},
				},
			},
		},
	}
}

func TestGeneratedClaudeSettingsValid(t *testing.T) {
	config := claudeHooksConfig(testRegistry())

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal Claude settings: %v", err)
	}

	result := validator.ValidateClaudeSettings("settings.json", data)
	if result.HasErrors() {
		t.Errorf("Claude settings has validation errors: %v", result.Errors)
	}

	// Log warnings for visibility but don't fail
	for _, e := range result.Errors {
		if e.Severity == validator.SeverityWarning {
			t.Logf("upstream schema warning: %s - %s", e.Field, e.Message)
		}
	}
}

func TestGeneratedClaudeSettingsNilRegistry(t *testing.T) {
	// Ensure nil registry produces valid settings with fallback permissions.
	config := claudeHooksConfig(nil)

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal Claude settings: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("generated settings.json is not valid JSON: %v", err)
	}

	perms, ok := parsed["permissions"].(map[string]any)
	if !ok {
		t.Fatal("expected permissions key in settings")
	}
	allow, ok := perms["allow"].([]any)
	if !ok || len(allow) == 0 {
		t.Error("expected non-empty allow list in fallback permissions")
	}
}

func TestClaudePermissionsFromRegistry(t *testing.T) {
	reg := testRegistry()
	perms := claudePermissions(reg)

	allow, ok := perms["allow"].([]string)
	if !ok {
		t.Fatal("expected allow to be []string")
	}

	// Verify registry entries are reflected.
	found := map[string]bool{}
	for _, a := range allow {
		found[a] = true
	}
	for _, want := range []string{"Bash(glab *)", "Bash(gh *)", "mcp__loom"} {
		if !found[want] {
			t.Errorf("expected %q in allow list", want)
		}
	}

	deny, ok := perms["deny"].([]string)
	if !ok {
		t.Fatal("expected deny to be []string")
	}
	if len(deny) != 2 {
		t.Errorf("expected 2 deny entries, got %d", len(deny))
	}
}

func TestGeneratedGeminiSettingsValid(t *testing.T) {
	config := geminiHooksConfig()

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal Gemini settings: %v", err)
	}

	result := validator.ValidateGeminiSettings("settings.json", data)
	if result.HasErrors() {
		t.Errorf("Gemini settings has validation errors: %v", result.Errors)
	}

	for _, e := range result.Errors {
		if e.Severity == validator.SeverityWarning {
			t.Logf("upstream schema warning: %s - %s", e.Field, e.Message)
		}
	}
}

func TestGeneratedCodexConfigValid(t *testing.T) {
	// Generate a minimal Codex TOML config similar to what generateTomlConfig produces
	tomlContent := `# Generated MCP configuration for codex
# Source: mcp/context/registry.yaml

approval_policy = "never"

suppress_unstable_features_warning = true
features = { apply_patch_freeform = true, include_apply_patch_tool = true, unified_exec = true }

sandbox_mode = "workspace-write"
sandbox_workspace_write = { network_access = true, writable_roots = ["/tmp"] }

notify = ["loom", "agent", "heartbeat", "--agent-id", "codex", "--status", "active", "--ensure-session", "--infer-namespace", "--agent-type", "codex", "--quiet"]

[mcp_servers.loom]
command = "loom"
args = ["proxy"]
tool_timeout_sec = 300
`

	result := validator.ValidateCodexConfig("config.toml", []byte(tomlContent))
	if result.HasErrors() {
		t.Errorf("Codex config has validation errors: %v", result.Errors)
	}

	for _, e := range result.Errors {
		if e.Severity == validator.SeverityWarning {
			t.Logf("upstream schema warning: %s - %s", e.Field, e.Message)
		}
	}
}

func TestEmitCodexPreamble(t *testing.T) {
	reg := testRegistry()
	var sb strings.Builder
	emitCodexPreamble(&sb, reg, "/tmp/workspace")
	content := sb.String()

	for _, want := range []string{
		`approval_policy = "never"`,
		`suppress_unstable_features_warning = true`,
		`sandbox_mode = "workspace-write"`,
		`writable_roots = ["/tmp/workspace"]`,
		`web_search = "live"`,
		`Git safety policy: treat pre-existing dirty worktrees as baseline context.`,
		"notify =",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("expected Codex preamble to contain %q", want)
		}
	}
}

func TestEmitCodexPreambleNilRegistry(t *testing.T) {
	// Ensure nil registry still produces valid defaults.
	var sb strings.Builder
	emitCodexPreamble(&sb, nil, "/tmp")
	content := sb.String()

	if !strings.Contains(content, `approval_policy = "never"`) {
		t.Error("expected default approval_policy in nil-registry Codex preamble")
	}
}

func TestGenerateHooksConfig_WritesAndValidates(t *testing.T) {
	tmpDir := t.TempDir()
	reg := testRegistry()

	// Generate Claude hooks config
	if err := generateHooksConfig(reg, tmpDir, "claude"); err != nil {
		t.Fatalf("generateHooksConfig(claude) failed: %v", err)
	}

	// Verify file exists
	settingsPath := filepath.Join(tmpDir, "claude", "settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not found: %v", err)
	}

	// Validate against upstream schema
	result := validator.ValidateClaudeSettings(settingsPath, content)
	if result.HasErrors() {
		t.Errorf("generated Claude settings.json has validation errors: %v", result.Errors)
	}

	// Verify JSON structure
	var parsed map[string]any
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("generated settings.json is not valid JSON: %v", err)
	}

	if _, ok := parsed["permissions"]; !ok {
		t.Error("generated settings.json missing permissions key")
	}
	if _, ok := parsed["hooks"]; !ok {
		t.Error("generated settings.json missing hooks key")
	}
}

func TestGenerateHooksConfig_Gemini(t *testing.T) {
	tmpDir := t.TempDir()

	if err := generateHooksConfig(nil, tmpDir, "gemini"); err != nil {
		t.Fatalf("generateHooksConfig(gemini) failed: %v", err)
	}

	settingsPath := filepath.Join(tmpDir, "gemini", "settings.json")
	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not found: %v", err)
	}

	result := validator.ValidateGeminiSettings(settingsPath, content)
	if result.HasErrors() {
		t.Errorf("generated Gemini settings.json has validation errors: %v", result.Errors)
	}
}

func TestGenerateHooksConfig_NoHooksPlatform(t *testing.T) {
	tmpDir := t.TempDir()

	// Platforms without hooks should return nil and not write any file
	if err := generateHooksConfig(nil, tmpDir, "codex"); err != nil {
		t.Fatalf("generateHooksConfig(codex) failed: %v", err)
	}

	settingsPath := filepath.Join(tmpDir, "codex", "settings.json")
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Error("codex should not have a settings.json")
	}
}

// --- coerceStringSlice tests ---

func TestCoerceStringSlice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want []string
	}{
		{"nil", nil, nil},
		{"[]string", []string{"a", "b"}, []string{"a", "b"}},
		{"[]any strings", []any{"x", "y"}, []string{"x", "y"}},
		{"[]any mixed types", []any{"ok", 42, "", true}, []string{"ok"}},
		{"[]any all empty", []any{"", ""}, []string{}},
		{"wrong type int", 42, nil},
		{"wrong type string", "not-a-slice", nil},
		{"empty []string", []string{}, []string{}},
		{"empty []any", []any{}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := coerceStringSlice(tt.in)
			if tt.want == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("length mismatch: got %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// --- filterClaudePermissionRules tests ---

func TestFilterClaudePermissionRules(t *testing.T) {
	tests := []struct {
		name        string
		rules       []string
		wantKept    []string
		wantDropped []string
	}{
		{
			name:        "valid bare tool names",
			rules:       []string{"Bash", "Read", "Write", "Edit", "Glob", "Grep"},
			wantKept:    []string{"Bash", "Read", "Write", "Edit", "Glob", "Grep"},
			wantDropped: nil,
		},
		{
			name:        "valid tool with args",
			rules:       []string{"Bash(go *)", "Bash(git *)", "Read(src/*)"},
			wantKept:    []string{"Bash(go *)", "Bash(git *)", "Read(src/*)"},
			wantDropped: nil,
		},
		{
			name:        "valid mcp prefix",
			rules:       []string{"mcp__loom", "mcp__loom__git__git_status", "mcp__custom__tool"},
			wantKept:    []string{"mcp__loom", "mcp__loom__git__git_status", "mcp__custom__tool"},
			wantDropped: nil,
		},
		{
			name:        "invalid entries dropped",
			rules:       []string{"NotARealTool", "some random string", "kubectl edit"},
			wantKept:    nil,
			wantDropped: []string{"NotARealTool", "some random string", "kubectl edit"},
		},
		{
			name:        "mixed valid and invalid",
			rules:       []string{"Bash(go *)", "invalid_entry", "mcp__loom", "also bad"},
			wantKept:    []string{"Bash(go *)", "mcp__loom"},
			wantDropped: []string{"invalid_entry", "also bad"},
		},
		{
			name:        "empty strings skipped",
			rules:       []string{"", "Bash", "", "Read", ""},
			wantKept:    []string{"Bash", "Read"},
			wantDropped: nil,
		},
		{
			name:        "nil input",
			rules:       nil,
			wantKept:    nil,
			wantDropped: nil,
		},
		{
			name:        "all known tool names",
			rules:       []string{"ExitPlanMode", "KillShell", "LS", "LSP", "MultiEdit", "NotebookEdit", "NotebookRead", "Skill", "Task", "TaskCreate", "TaskGet", "TaskList", "TaskOutput", "TaskStop", "TaskUpdate", "TodoWrite", "ToolSearch", "WebFetch", "WebSearch"},
			wantKept:    []string{"ExitPlanMode", "KillShell", "LS", "LSP", "MultiEdit", "NotebookEdit", "NotebookRead", "Skill", "Task", "TaskCreate", "TaskGet", "TaskList", "TaskOutput", "TaskStop", "TaskUpdate", "TodoWrite", "ToolSearch", "WebFetch", "WebSearch"},
			wantDropped: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kept, dropped := filterClaudePermissionRules(tt.rules)
			assertStringSlice(t, "kept", kept, tt.wantKept)
			assertStringSlice(t, "dropped", dropped, tt.wantDropped)
		})
	}
}

func assertStringSlice(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: length %d, want %d (got=%v, want=%v)", label, len(got), len(want), got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s[%d]: got %q, want %q", label, i, got[i], want[i])
		}
	}
}

// --- claudePermissionRuleRegexp tests ---

func TestClaudePermissionRuleRegexp(t *testing.T) {
	re := claudePermissionRuleRegexp()
	if re == nil {
		t.Fatal("claudePermissionRuleRegexp() returned nil")
	}

	valid := []string{
		"Bash", "Bash(go test ./...)", "Read", "Read(src/*.go)",
		"Write", "Edit", "Glob", "Grep", "Task", "ToolSearch",
		"mcp__loom", "mcp__loom__git__git_status",
		"MultiEdit", "WebFetch", "WebSearch",
	}
	for _, v := range valid {
		if !re.MatchString(v) {
			t.Errorf("expected regex to match %q", v)
		}
	}

	invalid := []string{
		"NotARealTool", "bash", "read", "kubectl edit",
		"some random string", "", "mcp_single_underscore",
	}
	for _, v := range invalid {
		if v != "" && re.MatchString(v) {
			t.Errorf("expected regex NOT to match %q", v)
		}
	}
}

// --- claudePermissions with ask/disableBypass tests ---

func TestClaudePermissions_AskAndDisableBypass(t *testing.T) {
	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"claude": {
				Allow: []string{"Bash(go *)", "mcp__loom"},
				Deny:  []string{"Bash(rm -rf *)"},
				Settings: map[string]any{
					"ask":                             []any{"Bash(curl *)", "WebFetch"},
					"disable_bypass_permissions_mode": "true",
				},
			},
		},
	}

	perms := claudePermissions(reg)

	// Check ask was coerced and included.
	ask, ok := perms["ask"].([]string)
	if !ok {
		t.Fatal("expected ask to be []string")
	}
	if len(ask) != 2 || ask[0] != "Bash(curl *)" || ask[1] != "WebFetch" {
		t.Errorf("unexpected ask: %v", ask)
	}

	// Check disableBypassPermissionsMode.
	bypass, ok := perms["disableBypassPermissionsMode"].(string)
	if !ok || bypass != "true" {
		t.Errorf("expected disableBypassPermissionsMode=%q, got %v", "true", perms["disableBypassPermissionsMode"])
	}
}

func TestClaudePermissions_FiltersInvalidRules(t *testing.T) {
	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"claude": {
				Allow: []string{"Bash(go *)", "invalid_tool", "mcp__loom"},
				Deny:  []string{"Bash(rm *)", "also_invalid"},
				Settings: map[string]any{
					"ask": []any{"WebFetch", "not_valid_either"},
				},
			},
		},
	}

	perms := claudePermissions(reg)

	allow, ok := perms["allow"].([]string)
	if !ok {
		t.Fatal("expected allow to be []string")
	}
	if len(allow) != 2 {
		t.Errorf("expected 2 valid allow entries, got %d: %v", len(allow), allow)
	}

	deny, ok := perms["deny"].([]string)
	if !ok {
		t.Fatal("expected deny to be []string")
	}
	if len(deny) != 1 {
		t.Errorf("expected 1 valid deny entry, got %d: %v", len(deny), deny)
	}

	ask, ok := perms["ask"].([]string)
	if !ok {
		t.Fatal("expected ask to be []string")
	}
	if len(ask) != 1 || ask[0] != "WebFetch" {
		t.Errorf("expected ask=[WebFetch], got %v", ask)
	}
}

func TestClaudePermissions_AllAskInvalidRemovesKey(t *testing.T) {
	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"claude": {
				Allow: []string{"Bash"},
				Settings: map[string]any{
					"ask": []any{"invalid_one", "invalid_two"},
				},
			},
		},
	}

	perms := claudePermissions(reg)

	if _, exists := perms["ask"]; exists {
		t.Error("expected ask key to be deleted when all entries are invalid")
	}
}

func TestClaudePermissions_EmptyAllowAfterFilter(t *testing.T) {
	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"claude": {
				Allow: []string{"invalid_one", "invalid_two"},
			},
		},
	}

	perms := claudePermissions(reg)

	if _, exists := perms["allow"]; exists {
		t.Error("expected allow key to be absent when all entries are invalid")
	}
}

func TestClaudePermissions_SettingsDefaultMode(t *testing.T) {
	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"claude": {
				Allow: []string{"Read"},
				Settings: map[string]any{
					"default_mode": "bypassPermissions",
				},
			},
		},
	}

	perms := claudePermissions(reg)

	mode, ok := perms["defaultMode"].(string)
	if !ok || mode != "bypassPermissions" {
		t.Errorf("expected defaultMode=bypassPermissions, got %v", perms["defaultMode"])
	}
}

// --- Integration: filtered settings still validate against upstream schema ---

func TestGeneratedClaudeSettings_WithInvalidRulesStillValid(t *testing.T) {
	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"claude": {
				AdditionalDirectories: []string{"~/workspace"},
				Allow: []string{
					"mcp__loom", "Read", "Write", "Edit",
					"invalid_should_be_dropped",
				},
				Deny: []string{
					"Bash(kubectl edit *)",
					"another_invalid",
				},
			},
		},
	}

	config := claudeHooksConfig(reg)

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	result := validator.ValidateClaudeSettings("settings.json", data)
	if result.HasErrors() {
		t.Errorf("settings with filtered invalid rules should still validate: %v", result.Errors)
	}
}

func TestClaudeHooksConfig_IncludesDirtyWorktreeNudge(t *testing.T) {
	config := claudeHooksConfig(testRegistry())
	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected hooks map in claude config")
	}
	sessionStart, ok := hooks["SessionStart"].([]map[string]any)
	if !ok || len(sessionStart) == 0 {
		t.Fatal("expected SessionStart hooks")
	}
	entries, ok := sessionStart[0]["hooks"].([]map[string]any)
	if !ok || len(entries) < 2 {
		t.Fatalf("expected at least two SessionStart hooks (session-start + nudge), got %d", len(entries))
	}

	found := false
	for _, h := range entries {
		cmd, _ := h["command"].(string)
		if strings.Contains(cmd, "git ls-files --others --exclude-standard") &&
			strings.Contains(cmd, "Dirty worktree detected") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected dirty-worktree nudge command in SessionStart hooks: %#v", entries)
	}
}

func TestGeminiHooksConfig_IncludesDirtyWorktreeNudge(t *testing.T) {
	config := geminiHooksConfigFromRegistry(testRegistry())
	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected hooks map in gemini config")
	}
	sessionStart, ok := hooks["SessionStart"].([]map[string]any)
	if !ok || len(sessionStart) == 0 {
		t.Fatal("expected SessionStart hooks")
	}
	entries, ok := sessionStart[0]["hooks"].([]map[string]any)
	if !ok || len(entries) < 2 {
		t.Fatalf("expected at least two SessionStart hooks (session-start + nudge), got %d", len(entries))
	}

	found := false
	for _, h := range entries {
		cmd, _ := h["command"].(string)
		if strings.Contains(cmd, "git ls-files --others --exclude-standard") &&
			strings.Contains(cmd, "Dirty worktree detected") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected dirty-worktree nudge command in SessionStart hooks: %#v", entries)
	}
}

// --- Codex web_search tests ---

func TestEmitCodexPreamble_WebSearchOverride(t *testing.T) {
	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"codex": {
				Settings: map[string]any{
					"web_search": "cached",
				},
			},
		},
	}

	var sb strings.Builder
	emitCodexPreamble(&sb, reg, "/tmp")
	content := sb.String()

	if !strings.Contains(content, `web_search = "cached"`) {
		t.Error("expected web_search to be overridden to 'cached'")
	}
	if strings.Contains(content, `web_search = "live"`) {
		t.Error("default 'live' should not appear when overridden")
	}
}

func TestEmitCodexPreamble_WebSearchDisabled(t *testing.T) {
	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"codex": {
				Settings: map[string]any{
					"web_search": "disabled",
				},
			},
		},
	}

	var sb strings.Builder
	emitCodexPreamble(&sb, reg, "/tmp")
	content := sb.String()

	if !strings.Contains(content, `web_search = "disabled"`) {
		t.Error("expected web_search = 'disabled'")
	}
}
