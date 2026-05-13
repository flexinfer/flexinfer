package generator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

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
					"dirty_worktree_nudge_message":          "Dirty worktree detected. Continue on current branch with scoped commits, inspect existing linked trees first, and use repo-local worktrees.",
					"guardrails": map[string]any{
						"gitops_flux": map[string]any{
							"blocked_commands": []any{"kubectl edit", "kubectl set env"},
							"message":          "GitOps policy: kubectl edit/set env bypasses git history. Edit manifests and use flux reconcile.",
						},
					},
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
				Deny: []string{},
			},
			"codex": {
				Settings: map[string]any{
					"approval_policy": map[string]any{
						"granular": map[string]any{
							"sandbox_approval":    false,
							"rules":               false,
							"mcp_elicitations":    false,
							"request_permissions": false,
							"skill_approval":      false,
						},
					},
					"suppress_unstable_features_warning": true,
					"sandbox_mode":                       "workspace-write",
					"features": map[string]any{
						"include_apply_patch_tool": true,
						"apply_patch_freeform":     true,
						"unified_exec":             true,
					},
				},
			},
			"gemini": {
				Settings: map[string]any{
					"approval_mode":                  "auto_edit",
					"checkpointing":                  true,
					"enable_permanent_tool_approval": true,
					"folder_trust_enabled":           true,
					"tools_allowed": []any{
						"run_shell_command(git)",
						"run_shell_command(echo)",
						"run_shell_command(printf)",
						"run_shell_command(rg)",
						"run_shell_command(tee)",
					},
					"tools_exclude": []any{
						"run_shell_command(rm)",
					},
				},
			},
		},
	}
}

func TestGeneratedClaudeSettingsValid(t *testing.T) {
	claudeProfile, _ := GetPlatformProfile("claude")
	config := claudeHooksConfig(testRegistry(), claudeProfile, "")

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
	claudeProfile, _ := GetPlatformProfile("claude")
	config := claudeHooksConfig(nil, claudeProfile, "")

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

func TestClaudeHooksConfig_UsesSharedGitOpsPolicy(t *testing.T) {
	claudeProfile, _ := GetPlatformProfile("claude")
	config := claudeHooksConfig(testRegistry(), claudeProfile, "")

	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected hooks map in claude config")
	}

	preToolUse, ok := hooks["PreToolUse"].([]map[string]any)
	if !ok || len(preToolUse) == 0 {
		t.Fatal("expected PreToolUse hooks from shared policy")
	}

	foundPolicyMessage := false
	foundGitCommitReminder := false
	for _, block := range preToolUse {
		entries, _ := block["hooks"].([]map[string]any)
		for _, entry := range entries {
			cmd, _ := entry["command"].(string)
			if strings.Contains(cmd, "kubectl edit") &&
				strings.Contains(cmd, "flux reconcile") &&
				strings.Contains(cmd, "GitOps policy:") {
				foundPolicyMessage = true
			}
			if strings.Contains(cmd, "Pre-commit quality reminder") &&
				strings.Contains(cmd, "quality_check") {
				foundGitCommitReminder = true
			}
		}
	}

	if !foundPolicyMessage {
		t.Fatalf("expected shared GitOps policy hook to mention kubectl edit/set env and flux reconcile: %#v", preToolUse)
	}
	if !foundGitCommitReminder {
		t.Fatalf("expected pre-tool-use quality reminder hook to remain present: %#v", preToolUse)
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

notify = ["sh", "-c", "WS_ROOT=\"$(git rev-parse --show-toplevel 2>/dev/null || printf '%s' \"$PWD\")\"; WS_HASH=\"$(printf '%s' \"$WS_ROOT\" | cksum | cut -d' ' -f1)\"; CACHE_DIR=\"${HOME}/.cache/loom\"; AGENT_ID_FILE=\"${CACHE_DIR}/agent-id-codex-${WS_HASH}\"; KEEPALIVE_STAMP_FILE=\"${CACHE_DIR}/keepalive-wrap-codex-${WS_HASH}.stamp\"; mkdir -p \"$CACHE_DIR\"; if [ -s \"$AGENT_ID_FILE\" ]; then AGENT_ID=\"$(cat \"$AGENT_ID_FILE\")\"; else AGENT_ID=\"codex-${WS_HASH}\"; printf '%s' \"$AGENT_ID\" > \"$AGENT_ID_FILE\"; fi; NOW=\"$(date +%s)\"; LAST=\"$(cat \"$KEEPALIVE_STAMP_FILE\" 2>/dev/null || true)\"; case \"$LAST\" in ''|*[!0-9]*) ;; *) if [ $((NOW - LAST)) -lt 15 ]; then exit 0; fi ;; esac; printf '%s' \"$NOW\" > \"$KEEPALIVE_STAMP_FILE\"; HOOK_SESSION_ID=\"$(printf '%s' \"${INPUT:-}\" | jq -r '.session_id // empty' 2>/dev/null || true)\"; nohup loom agent keepalive-wrap --agent-id \"$AGENT_ID\" --session-id \"$HOOK_SESSION_ID\" --status active --ensure-session --infer-namespace --agent-type codex --description \"Codex keepalive wrapper session\" --quiet </dev/null >/dev/null 2>>\"${TMPDIR:-/tmp}/loom-agent-hooks.log\" &", "--"]

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
	emitCodexPreamble(&sb, reg, "/tmp/workspace", "")
	content := sb.String()

	for _, want := range []string{
		`mcp_elicitations = false`,
		`suppress_unstable_features_warning = true`,
		`sandbox_mode = "workspace-write"`,
		`writable_roots = ["/tmp/workspace"]`,
		`web_search = "live"`,
		`Git safety policy: treat pre-existing dirty worktrees as baseline context.`,
		`Before creating another multi-file worktree, inspect existing linked trees with git worktree list or workspace-clean --report --worktrees.`,
		`For multi-file work, create repo-local linked trees under <repo>/.worktrees/<branch>.`,
		`Do not create sibling repos under services/, libs/, labs/, or the workspace root.`,
		"notify =",
		`"--"]`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("expected Codex preamble to contain %q", want)
		}
	}

	for _, want := range []string{
		`CACHE_DIR=\"${HOME}/.cache/loom\"`,
		`${CACHE_DIR}/agent-id-codex-${WS_HASH}`,
		`${CACHE_DIR}/keepalive-wrap-codex-${WS_HASH}.stamp`,
		`codex-${WS_HASH}`,
		`date +%s`,
		` -lt 15 `,
		`keepalive-wrap`,
		`nohup`,
		`--agent-id \"`,
		`--description \"Codex keepalive wrapper session\"`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("expected Codex notify flow to contain %q", want)
		}
	}
	if strings.Contains(content, "codex-${WS_HASH}-$$") {
		t.Fatalf("expected Codex notify identity to avoid per-hook $$ churn, got: %s", content)
	}
}

func TestEmitCodexPreambleNilRegistry(t *testing.T) {
	// Ensure nil registry still produces valid defaults.
	var sb strings.Builder
	emitCodexPreamble(&sb, nil, "/tmp", "")
	content := sb.String()

	if !strings.Contains(content, `approval_policy = "never"`) {
		t.Error("expected default approval_policy in nil-registry Codex preamble")
	}
}

func TestEmitCodexPreamble_UsesExplicitLoomBinary(t *testing.T) {
	var sb strings.Builder
	emitCodexPreamble(&sb, testRegistry(), "/tmp/workspace", "/opt/loom/bin/loom")
	content := sb.String()

	if !strings.Contains(content, `nohup '/opt/loom/bin/loom' agent keepalive-wrap`) {
		t.Fatalf("expected explicit loom binary in codex notify hook, got: %s", content)
	}
}

func TestEmitCodexPreamble_NotifyRemainsTopLevel(t *testing.T) {
	var sb strings.Builder
	emitCodexPreamble(&sb, testRegistry(), "/tmp/workspace", "")

	var parsed map[string]any
	if err := toml.Unmarshal([]byte(sb.String()), &parsed); err != nil {
		t.Fatalf("expected generated preamble to be valid TOML: %v", err)
	}

	notify, ok := parsed["notify"]
	if !ok {
		t.Fatalf("expected top-level notify key, got: %#v", parsed)
	}
	if !containsCodexKeepaliveWrapCommand(notify) {
		t.Fatalf("expected top-level notify key to contain loom hook, got: %#v", notify)
	}
}

func TestGenerateTomlConfig_CodexUsesServerApprovalMode(t *testing.T) {
	tmpDir := t.TempDir()
	profile, err := GetPlatformProfile("codex")
	if err != nil {
		t.Fatalf("GetPlatformProfile(codex): %v", err)
	}

	params := &GenerateParams{
		Reg:           testRegistry(),
		OutputDir:     tmpDir,
		Target:        "codex",
		Profile:       profile,
		LoomMode:      true,
		LoomBinary:    "/opt/loom/bin/loom",
		WorkspaceRoot: "/tmp/workspace",
	}

	if err := generateTomlConfig(params); err != nil {
		t.Fatalf("generateTomlConfig(codex): %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "codex", "config.toml"))
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}

	// Codex CLI 4.x uses `default_tools_approval_mode = "approve"` at the MCP
	// server stanza (not `always_allow`, and not `approval_mode = "always"`
	// which isn't a valid value and regresses to prompt-every-call behavior).
	if !strings.Contains(string(content), `default_tools_approval_mode = "approve"`) {
		t.Fatalf("expected codex config to emit default_tools_approval_mode = \"approve\", got:\n%s", string(content))
	}

	// Verify granular approval_policy is emitted with mcp_elicitations = false.
	if !strings.Contains(string(content), `mcp_elicitations = false`) {
		t.Fatalf("expected codex config to emit granular approval_policy with mcp_elicitations = false, got:\n%s", string(content))
	}
}

func TestGenerateHooksConfig_WritesAndValidates(t *testing.T) {
	tmpDir := t.TempDir()
	reg := testRegistry()

	// Generate Claude hooks config
	claudeProfile, _ := GetPlatformProfile("claude")
	if err := generateHooksConfig(reg, tmpDir, "claude", claudeProfile, ""); err != nil {
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

	geminiProfile, _ := GetPlatformProfile("gemini")
	if err := generateHooksConfig(nil, tmpDir, "gemini", geminiProfile, ""); err != nil {
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

func TestGenerateHooksConfig_CodexEmitsHooksJSON(t *testing.T) {
	tmpDir := t.TempDir()

	// Codex v0.129.0 (2026-05-07) shipped a Claude-shape [hooks] block read
	// from ~/.codex/hooks.json (when [features] hooks = true). We emit
	// hooks.json — NOT settings.json — alongside config.toml.
	codexProfile, _ := GetPlatformProfile("codex")
	if err := generateHooksConfig(testRegistry(), tmpDir, "codex", codexProfile, ""); err != nil {
		t.Fatalf("generateHooksConfig(codex) failed: %v", err)
	}

	// settings.json must NOT exist for codex.
	if _, err := os.Stat(filepath.Join(tmpDir, "codex", "settings.json")); !os.IsNotExist(err) {
		t.Error("codex should not have a settings.json (uses hooks.json)")
	}

	// hooks.json must exist and contain the canonical lifecycle events.
	hooksPath := filepath.Join(tmpDir, "codex", "hooks.json")
	content, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("hooks.json not found at %s: %v", hooksPath, err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("generated hooks.json is not valid JSON: %v", err)
	}
	hooks, ok := parsed["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("generated hooks.json missing top-level hooks key: %s", string(content))
	}

	// Two canonical lifecycle events for Codex: SessionStart (once per
	// session) and PostToolUse (heartbeat, per tool call). Codex has no
	// SessionEnd event, and Stop is per-turn — session termination is
	// handled by notify + keepalive-wrap deregister-on-exit, not by a
	// hooks.json Stop entry.
	for _, evt := range []string{"SessionStart", "PostToolUse"} {
		if _, found := hooks[evt]; !found {
			t.Errorf("expected codex hooks.json to contain %q event, got keys: %v", evt, mapKeys(hooks))
		}
	}

	// Codex hooks.json must NOT contain a Stop entry. Codex `Stop` fires
	// per-turn (after each model response), so mapping it to
	// `loom agent session-end --summarize` would queue a summary on every
	// turn. The fix is to omit Stop entirely and rely on notify+keepalive
	// for true session-end. See pkg/generator/VENDOR_SPECS.md and the
	// codex profile in platform_profiles.yaml.
	if _, found := hooks["Stop"]; found {
		t.Error("codex hooks.json must NOT contain Stop (per-turn event; would spam session-end)")
	}

	// Codex hooks should NOT carry SubagentStart (Claude-only event).
	if _, found := hooks["SubagentStart"]; found {
		t.Error("codex hooks.json must NOT contain SubagentStart (Claude-only event)")
	}
}

func TestGenerateHooksConfig_CodexUsesExplicitLoomBinary(t *testing.T) {
	tmpDir := t.TempDir()
	codexProfile, _ := GetPlatformProfile("codex")

	if err := generateHooksConfig(testRegistry(), tmpDir, "codex", codexProfile, "/opt/loom/bin/loom"); err != nil {
		t.Fatalf("generateHooksConfig(codex) failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "codex", "hooks.json"))
	if err != nil {
		t.Fatalf("hooks.json not found: %v", err)
	}

	if !strings.Contains(string(content), `'/opt/loom/bin/loom' agent session-start`) {
		t.Fatalf("expected explicit loom binary in generated codex hooks.json")
	}
}

// mapKeys returns the keys of m as a slice for error messages.
func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestGenerateHooksConfig_UsesExplicitLoomBinary(t *testing.T) {
	tmpDir := t.TempDir()
	claudeProfile, _ := GetPlatformProfile("claude")

	if err := generateHooksConfig(testRegistry(), tmpDir, "claude", claudeProfile, "/opt/loom/bin/loom"); err != nil {
		t.Fatalf("generateHooksConfig(claude) failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "claude", "settings.json"))
	if err != nil {
		t.Fatalf("settings.json not found: %v", err)
	}

	if !strings.Contains(string(content), `'/opt/loom/bin/loom' agent session-start`) {
		t.Fatalf("expected explicit loom binary in generated claude hooks, got: %s", string(content))
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
			rules:       []string{"AskUserQuestion", "EnterPlanMode", "EnterWorktree", "ExitPlanMode", "KillShell", "LS", "LSP", "Monitor", "MultiEdit", "NotebookEdit", "NotebookRead", "Skill", "Task", "TaskCreate", "TaskGet", "TaskList", "TaskOutput", "TaskStop", "TaskUpdate", "TodoWrite", "ToolSearch", "WebFetch", "WebSearch"},
			wantKept:    []string{"AskUserQuestion", "EnterPlanMode", "EnterWorktree", "ExitPlanMode", "KillShell", "LS", "LSP", "Monitor", "MultiEdit", "NotebookEdit", "NotebookRead", "Skill", "Task", "TaskCreate", "TaskGet", "TaskList", "TaskOutput", "TaskStop", "TaskUpdate", "TodoWrite", "ToolSearch", "WebFetch", "WebSearch"},
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
			// Explicit empty agents entry signals that the test has the
			// authoritative policy view: no shared guardrails apply. Without
			// this, EPIC 3 / CONFIG-3 (.loom/108) embedded fallback would
			// load the gitops_flux policy from
			// pkg/generator/templates/policies/gitops_flux.yaml and add
			// ~10 extra deny rules, breaking the assertion below. See
			// LoadPolicy in pkg/generator/policies.go for the precedence rules.
			"agents": {},
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

	claudeProfile, _ := GetPlatformProfile("claude")
	config := claudeHooksConfig(reg, claudeProfile, "")

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
	claudeProfile, _ := GetPlatformProfile("claude")
	config := claudeHooksConfig(testRegistry(), claudeProfile, "")
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
		if strings.Contains(cmd, "git diff --cached --quiet --no-ext-diff") &&
			strings.Contains(cmd, "Dirty worktree detected") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected dirty-worktree nudge command in SessionStart hooks: %#v", entries)
	}
}

func TestClaudeHooksConfig_UsesPersistentAgentIdBootstrap(t *testing.T) {
	claudeProfile, _ := GetPlatformProfile("claude")
	config := claudeHooksConfig(testRegistry(), claudeProfile, "")
	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected hooks map in claude config")
	}

	sessionStart, ok := hooks["SessionStart"].([]map[string]any)
	if !ok || len(sessionStart) == 0 {
		t.Fatal("expected SessionStart hooks")
	}
	sessionHooks, ok := sessionStart[0]["hooks"].([]map[string]any)
	if !ok || len(sessionHooks) == 0 {
		t.Fatal("expected session-start hooks list")
	}

	for _, h := range sessionHooks {
		cmd, _ := h["command"].(string)
		if cmd == "" {
			continue
		}
		if strings.Contains(cmd, `--agent-id "claude-code-$PPID"`) {
			t.Fatalf("found hardcoded ppid in claude session-start hook: %s", cmd)
		}
		if strings.Contains(cmd, `--agent-id "$AGENT_ID"`) && strings.Contains(cmd, "agent-id-claude-code") {
			if !strings.Contains(cmd, "INPUT=$(cat)") {
				t.Fatalf("expected hook input capture in claude bootstrap, got: %s", cmd)
			}
			if !strings.Contains(cmd, "session_id") {
				t.Fatalf("expected session_id-derived bootstrap in claude hook, got: %s", cmd)
			}
			// Verify workspace hash remains part of the bootstrap fallback.
			if !strings.Contains(cmd, "WS_HASH") {
				t.Fatalf("expected WS_HASH in agent ID bootstrap, got: %s", cmd)
			}
			if !strings.Contains(cmd, "cksum") {
				t.Fatalf("expected cksum in agent ID bootstrap, got: %s", cmd)
			}
			if !strings.Contains(cmd, ".cache/loom") {
				t.Fatalf("expected cache-backed AGENT_ID_FILE path, got: %s", cmd)
			}
			return
		}
	}
	t.Fatalf("expected claude hooks to use persistent AGENT_ID bootstrap and --agent-id \"$AGENT_ID\"")
}

func TestGeminiHooksConfig_UsesPersistentAgentIdBootstrap(t *testing.T) {
	geminiProfile, _ := GetPlatformProfile("gemini")
	config := geminiHooksConfigFromRegistry(testRegistry(), geminiProfile, "")
	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected hooks map in gemini config")
	}

	sessionStart, ok := hooks["SessionStart"].([]map[string]any)
	if !ok || len(sessionStart) == 0 {
		t.Fatal("expected SessionStart hooks")
	}
	sessionHooks, ok := sessionStart[0]["hooks"].([]map[string]any)
	if !ok || len(sessionHooks) == 0 {
		t.Fatal("expected session-start hooks list")
	}

	for _, h := range sessionHooks {
		cmd, _ := h["command"].(string)
		if cmd == "" {
			continue
		}
		if strings.Contains(cmd, `--agent-id "gemini-cli-$PPID"`) {
			t.Fatalf("found hardcoded ppid in gemini session-start hook: %s", cmd)
		}
		if strings.Contains(cmd, `--agent-id "$AGENT_ID"`) && strings.Contains(cmd, "agent-id-gemini-cli") {
			if !strings.Contains(cmd, "INPUT=$(cat)") {
				t.Fatalf("expected hook input capture in gemini bootstrap, got: %s", cmd)
			}
			if !strings.Contains(cmd, "session_id") {
				t.Fatalf("expected session_id-derived bootstrap in gemini hook, got: %s", cmd)
			}
			// Verify workspace hash remains part of the bootstrap fallback.
			if !strings.Contains(cmd, "WS_HASH") {
				t.Fatalf("expected WS_HASH in agent ID bootstrap, got: %s", cmd)
			}
			if !strings.Contains(cmd, "cksum") {
				t.Fatalf("expected cksum in agent ID bootstrap, got: %s", cmd)
			}
			if !strings.Contains(cmd, ".cache/loom") {
				t.Fatalf("expected cache-backed AGENT_ID_FILE path, got: %s", cmd)
			}
			return
		}
	}
	t.Fatalf("expected gemini hooks to use persistent AGENT_ID bootstrap and --agent-id \"$AGENT_ID\"")
}

func TestGeminiHooksConfig_IncludesDirtyWorktreeNudge(t *testing.T) {
	geminiProfile, _ := GetPlatformProfile("gemini")
	config := geminiHooksConfigFromRegistry(testRegistry(), geminiProfile, "")
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
		if strings.Contains(cmd, "git diff --cached --quiet --no-ext-diff") &&
			strings.Contains(cmd, "Dirty worktree detected") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected dirty-worktree nudge command in SessionStart hooks: %#v", entries)
	}
}

func TestGeminiHooksConfig_EmitsApprovalAndSecuritySettings(t *testing.T) {
	geminiProfile, _ := GetPlatformProfile("gemini")
	config := geminiHooksConfigFromRegistry(testRegistry(), geminiProfile, "")

	general, ok := config["general"].(map[string]any)
	if !ok {
		t.Fatal("expected general block in gemini config")
	}
	if got := general["defaultApprovalMode"]; got != "auto_edit" {
		t.Fatalf("defaultApprovalMode=%v, want auto_edit", got)
	}
	checkpointing, ok := general["checkpointing"].(map[string]any)
	if !ok || checkpointing["enabled"] != true {
		t.Fatalf("expected checkpointing.enabled=true, got %#v", general["checkpointing"])
	}

	tools, ok := config["tools"].(map[string]any)
	if !ok {
		t.Fatal("expected tools block in gemini config")
	}
	allowed, ok := tools["allowed"].([]string)
	if !ok {
		t.Fatalf("expected tools.allowed []string, got %#v", tools["allowed"])
	}
	wantAllowed := []string{
		"run_shell_command(git)",
		"run_shell_command(echo)",
		"run_shell_command(printf)",
		"run_shell_command(rg)",
		"run_shell_command(tee)",
	}
	if len(allowed) != len(wantAllowed) {
		t.Fatalf("unexpected tools.allowed: %#v", allowed)
	}
	for i, want := range wantAllowed {
		if allowed[i] != want {
			t.Fatalf("tools.allowed[%d]=%q, want %q (full=%#v)", i, allowed[i], want, allowed)
		}
	}
	exclude, ok := tools["exclude"].([]string)
	if !ok || len(exclude) != 1 || exclude[0] != "run_shell_command(rm)" {
		t.Fatalf("unexpected tools.exclude: %#v", tools["exclude"])
	}

	security, ok := config["security"].(map[string]any)
	if !ok {
		t.Fatal("expected security block in gemini config")
	}
	if security["enablePermanentToolApproval"] != true {
		t.Fatalf("expected enablePermanentToolApproval=true, got %#v", security["enablePermanentToolApproval"])
	}
	folderTrust, ok := security["folderTrust"].(map[string]any)
	if !ok || folderTrust["enabled"] != true {
		t.Fatalf("expected folderTrust.enabled=true, got %#v", security["folderTrust"])
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to marshal Gemini settings: %v", err)
	}
	result := validator.ValidateGeminiSettings("settings.json", data)
	if result.HasErrors() {
		t.Fatalf("expected generated Gemini settings to validate cleanly, got errors: %v", result.Errors)
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
	emitCodexPreamble(&sb, reg, "/tmp", "")
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
	emitCodexPreamble(&sb, reg, "/tmp", "")
	content := sb.String()

	if !strings.Contains(content, `web_search = "disabled"`) {
		t.Error("expected web_search = 'disabled'")
	}
}

// --- Hook-scoped agent ID tests ---

func TestHookAgentIDBootstrap_ContainsWorkspaceHash(t *testing.T) {
	output := hookAgentIDBootstrap("claude-code")

	for _, want := range []string{
		"HOOK_INPUT=",
		"HOOK_SESSION_ID=",
		"SESSION_SCOPE=",
		"WS_ROOT=",
		"WS_HASH=",
		"cksum",
		"git rev-parse --show-toplevel",
		"AGENT_CACHE_DIR=",
		"${HOME:-${TMPDIR:-/tmp}}/.cache/loom",
		"agent-id-claude-code-${WS_HASH}${SESSION_SCOPE:+-${SESSION_SCOPE}}",
		`AGENT_ID="claude-code-${WS_HASH}${SESSION_SCOPE:+-${SESSION_SCOPE}}"`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("hookAgentIDBootstrap output missing %q\ngot: %s", want, output)
		}
	}

	// Verify we no longer rely on a tempdir-only identity file.
	if strings.Contains(output, `${TMPDIR:-/tmp}/loom-agent-id-claude-code`) {
		t.Error("expected hook agent id bootstrap to prefer cache-backed identity storage")
	}
}

func TestHookStaleCleanup_ChecksProcessLiveness(t *testing.T) {
	output := hookStaleCleanup()

	for _, want := range []string{
		"loom-keepalive-${AGENT_ID}.pid",
		`kill -0 "$OLD_PID"`,
		`rm -f "$PID_FILE" "$AGENT_ID_FILE"`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("hookStaleCleanup output missing %q\ngot: %s", want, output)
		}
	}
}

// --- Shared builder tests ---

func TestBuildPlatformHooks_ClaudeEventNames(t *testing.T) {
	hooks := buildPlatformHooks(testRegistry(), HookProfile{
		Enabled:          true,
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "Claude Code session",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
	}, "")

	for _, key := range []string{"SessionStart", "Stop", "PostToolUse"} {
		if _, ok := hooks[key]; !ok {
			t.Errorf("expected key %q in claude platform hooks", key)
		}
	}

	// Claude should NOT have SessionEnd or AfterTool (those are Gemini events).
	for _, key := range []string{"SessionEnd", "AfterTool"} {
		if _, ok := hooks[key]; ok {
			t.Errorf("unexpected key %q in claude platform hooks", key)
		}
	}
}

func TestBuildPlatformHooks_GeminiEventNames(t *testing.T) {
	hooks := buildPlatformHooks(testRegistry(), HookProfile{
		Enabled:          true,
		AgentID:          "gemini-cli",
		AgentType:        "gemini-cli",
		Description:      "Gemini CLI session",
		SessionEndEvent:  "SessionEnd",
		HeartbeatEvent:   "AfterTool",
		HeartbeatMatcher: "run_shell_command",
	}, "")

	for _, key := range []string{"SessionStart", "SessionEnd", "AfterTool"} {
		if _, ok := hooks[key]; !ok {
			t.Errorf("expected key %q in gemini platform hooks", key)
		}
	}

	// Gemini should NOT have Stop or PostToolUse (those are Claude events).
	for _, key := range []string{"Stop", "PostToolUse"} {
		if _, ok := hooks[key]; ok {
			t.Errorf("unexpected key %q in gemini platform hooks", key)
		}
	}
}

func TestBuildPlatformHooks_KeepaliveIsDetached(t *testing.T) {
	hooks := buildPlatformHooks(testRegistry(), HookProfile{
		Enabled:          true,
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "test",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
	}, "")

	sessionStart := hooks["SessionStart"].([]map[string]any)
	sessionHooks := sessionStart[0]["hooks"].([]map[string]any)

	foundDetachedKeepalive := false
	for _, h := range sessionHooks {
		cmd, _ := h["command"].(string)
		if strings.Contains(cmd, "keepalive") &&
			strings.Contains(cmd, "</dev/null") &&
			strings.Contains(cmd, ">/dev/null") {
			foundDetachedKeepalive = true
			break
		}
	}
	if !foundDetachedKeepalive {
		t.Error("expected keepalive hook to detach stdio from the hook runner")
	}
}

func TestBuildPlatformHooks_PkillOrphanCleanup(t *testing.T) {
	hooks := buildPlatformHooks(testRegistry(), HookProfile{
		Enabled:          true,
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "test",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
	}, "")

	// Check SessionStart keepalive hook includes pkill for orphans.
	sessionStart := hooks["SessionStart"].([]map[string]any)
	sessionHooks := sessionStart[0]["hooks"].([]map[string]any)
	foundPkillInStart := false
	for _, h := range sessionHooks {
		cmd, _ := h["command"].(string)
		if strings.Contains(cmd, "keepalive") && strings.Contains(cmd, `pkill -f "loom agent keepalive --agent-id claude-code-${WS_HASH}"`) {
			foundPkillInStart = true
			break
		}
	}
	if !foundPkillInStart {
		t.Error("expected pkill orphan cleanup in SessionStart keepalive hook")
	}

	// Check Stop hook includes pkill for orphans.
	stop := hooks["Stop"].([]map[string]any)
	stopHooks := stop[0]["hooks"].([]map[string]any)
	foundPkillInStop := false
	for _, h := range stopHooks {
		cmd, _ := h["command"].(string)
		if strings.Contains(cmd, `pkill -f "loom agent keepalive --agent-id claude-code-${WS_HASH}"`) {
			foundPkillInStop = true
			break
		}
	}
	if !foundPkillInStop {
		t.Error("expected pkill orphan cleanup in Stop hook")
	}
}

func TestBuildPlatformHooks_StaleCleanupInSessionStart(t *testing.T) {
	hooks := buildPlatformHooks(testRegistry(), HookProfile{
		Enabled:          true,
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "test",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
	}, "")

	sessionStart := hooks["SessionStart"].([]map[string]any)
	sessionHooks := sessionStart[0]["hooks"].([]map[string]any)

	foundStaleCleanup := false
	foundFastRecallStrategy := false
	for _, h := range sessionHooks {
		cmd, _ := h["command"].(string)
		if strings.Contains(cmd, "session-start") && strings.Contains(cmd, "kill -0") {
			foundStaleCleanup = true
		}
		if strings.Contains(cmd, "session-start") && strings.Contains(cmd, "--auto-recall-strategy fast") {
			foundFastRecallStrategy = true
		}
	}
	if !foundStaleCleanup {
		t.Error("expected stale cleanup (kill -0) in session-start hook chain")
	}
	if !foundFastRecallStrategy {
		t.Error("expected session-start hook to include --auto-recall-strategy fast")
	}
}

func TestBuildPlatformHooks_HeartbeatBootstrapIncludesNamespaceInference(t *testing.T) {
	hooks := buildPlatformHooks(testRegistry(), HookProfile{
		Enabled:          true,
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "Claude Code session",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
	}, "")

	postToolUse := hooks["PostToolUse"].([]map[string]any)
	entries := postToolUse[0]["hooks"].([]map[string]any)

	found := false
	for _, h := range entries {
		cmd, _ := h["command"].(string)
		if strings.Contains(cmd, "agent heartbeat") &&
			strings.Contains(cmd, "--ensure-session") &&
			strings.Contains(cmd, "--infer-namespace") &&
			strings.Contains(cmd, `--description "Claude Code`) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected heartbeat hook bootstrap to include namespace inference and description")
	}
}

func TestBuildPlatformHooks_StopHookUsesSummaryAsync(t *testing.T) {
	hooks := buildPlatformHooks(testRegistry(), HookProfile{
		Enabled:          true,
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "test",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
	}, "")

	stop := hooks["Stop"].([]map[string]any)
	stopHooks := stop[0]["hooks"].([]map[string]any)

	found := false
	for _, h := range stopHooks {
		cmd, _ := h["command"].(string)
		if strings.Contains(cmd, "session-end") && strings.Contains(cmd, "--summary-async") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected Stop hook to include --summary-async")
	}
}

func TestCodexPreamble_ContainsWorkspaceHash(t *testing.T) {
	var sb strings.Builder
	emitCodexPreamble(&sb, testRegistry(), "/tmp/workspace", "")
	content := sb.String()

	for _, want := range []string{
		"WS_HASH=",
		"cksum",
		`CACHE_DIR=\"${HOME}/.cache/loom\"`,
		"AGENT_ID_FILE",
		"${CACHE_DIR}/agent-id-codex-${WS_HASH}",
		"${CACHE_DIR}/keepalive-wrap-codex-${WS_HASH}.stamp",
		"codex-${WS_HASH}",
		"keepalive-wrap",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("Codex preamble missing %q\ngot: %s", want, content)
		}
	}

	// Old unstable agent ID patterns should be gone.
	if strings.Contains(content, `--agent-id "codex-$$"`) {
		t.Error("found old global agent ID pattern codex-$$ without workspace hash")
	}
	if strings.Contains(content, "codex-${WS_HASH}-$$") {
		t.Error("found old per-process codex agent ID pattern")
	}
}

func TestResolveHubWrapper_PreferenceOrder(t *testing.T) {
	tmp := t.TempDir()
	workspaceRoot := filepath.Join(tmp, "workspace")
	registryRoot := filepath.Join(tmp, "registry-root")

	envWrapper := writeTestWrapper(t, tmp, "env-wrapper.sh", true)
	workspaceWrapper := writeTestWrapper(t, filepath.Join(workspaceRoot, "services", "loom-core", "bin"), "mcp-hub-wrapper", true)
	installedWrapper := writeTestWrapper(t, filepath.Join(tmp, "home", ".local", "bin"), "mcp-hub-wrapper", true)
	pathWrapper := writeTestWrapper(t, filepath.Join(tmp, "path"), "mcp-hub-wrapper", true)

	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("PATH", filepath.Join(tmp, "path"))

	t.Setenv(hubWrapperOverrideEnv, envWrapper)
	got, err := resolveHubWrapper(workspaceRoot, registryRoot)
	if err != nil {
		t.Fatalf("resolveHubWrapper with env override: %v", err)
	}
	if got != envWrapper {
		t.Fatalf("resolved wrapper = %q, want env override %q", got, envWrapper)
	}

	t.Setenv(hubWrapperOverrideEnv, "")
	got, err = resolveHubWrapper(workspaceRoot, registryRoot)
	if err != nil {
		t.Fatalf("resolveHubWrapper with workspace wrapper: %v", err)
	}
	if got != workspaceWrapper {
		t.Fatalf("resolved wrapper = %q, want workspace wrapper %q", got, workspaceWrapper)
	}

	if err := os.Remove(workspaceWrapper); err != nil {
		t.Fatalf("remove workspace wrapper: %v", err)
	}
	got, err = resolveHubWrapper(workspaceRoot, registryRoot)
	if err != nil {
		t.Fatalf("resolveHubWrapper with installed wrapper: %v", err)
	}
	if got != installedWrapper {
		t.Fatalf("resolved wrapper = %q, want installed wrapper %q", got, installedWrapper)
	}

	if err := os.Remove(installedWrapper); err != nil {
		t.Fatalf("remove installed wrapper: %v", err)
	}
	got, err = resolveHubWrapper(workspaceRoot, registryRoot)
	if err != nil {
		t.Fatalf("resolveHubWrapper with PATH wrapper: %v", err)
	}
	if got != pathWrapper {
		t.Fatalf("resolved wrapper = %q, want PATH wrapper %q", got, pathWrapper)
	}
}

func TestBuildTargetMap_HubModeFailsWithoutHealthyWrapper(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("PATH", filepath.Join(tmp, "empty-path"))
	t.Setenv(hubWrapperOverrideEnv, writeTestWrapper(t, tmp, "broken-wrapper.sh", false))

	reg := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name: "agent_context",
				Common: &registry.TargetSpec{
					Command: "mcp-agent-context",
				},
			},
		},
	}

	codexProfile, _ := GetPlatformProfile("codex")
	_, err := buildTargetMap(reg, "codex", codexProfile, true, "wss://example.test/ws", false, "", tmp, filepath.Join(tmp, "registry-root"), false)
	if err == nil {
		t.Fatal("expected hub mode generation to fail when no healthy wrapper is available")
	}
	if !strings.Contains(err.Error(), "resolve hub wrapper") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildTargetMap_HubModeUsesResolvedWrapper(t *testing.T) {
	tmp := t.TempDir()
	goodWrapper := writeTestWrapper(t, tmp, "good-wrapper.sh", true)
	t.Setenv(hubWrapperOverrideEnv, goodWrapper)
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("PATH", filepath.Join(tmp, "empty-path"))

	reg := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name: "agent_context",
				Common: &registry.TargetSpec{
					Command: "mcp-agent-context",
				},
			},
		},
	}

	codexProfile, _ := GetPlatformProfile("codex")
	targets, err := buildTargetMap(reg, "codex", codexProfile, true, "wss://example.test/ws", false, "", filepath.Join(tmp, "workspace"), filepath.Join(tmp, "registry-root"), false)
	if err != nil {
		t.Fatalf("buildTargetMap: %v", err)
	}

	spec := targets["agent_context"]
	if spec == nil {
		t.Fatal("expected agent_context target spec")
	}
	if spec.Command != goodWrapper {
		t.Fatalf("hub wrapper command = %q, want %q", spec.Command, goodWrapper)
	}
	gotArgs := make([]string, 0, len(spec.Args))
	for _, a := range spec.Args {
		gotArgs = append(gotArgs, fmt.Sprintf("%v", a))
	}
	want := []string{"agent_context", "--profile", "codex", "--hub-url", "wss://example.test/ws"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("hub wrapper args = %v, want %v", gotArgs, want)
	}
}

func TestBuildTargetMap_LoomModeAntigravityAddsToolFilterArgs(t *testing.T) {
	reg := &registry.Registry{}

	agProfile, _ := GetPlatformProfile("antigravity")
	targets, err := buildTargetMap(reg, "antigravity", agProfile, false, "", true, "", "", "", false)
	if err != nil {
		t.Fatalf("buildTargetMap: %v", err)
	}

	spec := targets["loom"]
	if spec == nil {
		t.Fatal("expected loom target spec in loom-mode")
	}

	gotArgs := make([]string, 0, len(spec.Args))
	for _, a := range spec.Args {
		gotArgs = append(gotArgs, fmt.Sprintf("%v", a))
	}

	want := []string{
		"proxy",
		"--agent-hint", "antigravity",
		"--tool-profile", "antigravity-core",
		"--max-tools", "100",
	}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("loom-mode antigravity args = %v, want %v", gotArgs, want)
	}
}

func TestBuildTargetMap_LoomModeLLMClientsAddToolFilterArgs(t *testing.T) {
	reg := &registry.Registry{}

	for _, targetName := range []string{"claude", "claude_desktop", "codex"} {
		t.Run(targetName, func(t *testing.T) {
			profile, _ := GetPlatformProfile(targetName)
			targets, err := buildTargetMap(reg, targetName, profile, false, "", true, "", "", "", false)
			if err != nil {
				t.Fatalf("buildTargetMap(%s): %v", targetName, err)
			}

			spec := targets["loom"]
			if spec == nil {
				t.Fatalf("expected loom target spec in loom-mode for %s", targetName)
			}

			gotArgs := make([]string, 0, len(spec.Args))
			for _, a := range spec.Args {
				gotArgs = append(gotArgs, fmt.Sprintf("%v", a))
			}

			want := []string{
				"proxy",
				"--agent-hint", profile.LoomProxy.AgentHint,
				"--tool-profile", "llm-core",
				"--max-tools", "140",
			}
			if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
				t.Fatalf("loom-mode %s args = %v, want %v", targetName, gotArgs, want)
			}
		})
	}
}

func TestBuildTargetMap_LoomModeAppliesProxyEnv(t *testing.T) {
	reg := &registry.Registry{}
	profile, err := GetPlatformProfile("claude_desktop")
	if err != nil {
		t.Fatalf("GetPlatformProfile: %v", err)
	}

	targets, err := buildTargetMap(reg, "claude_desktop", profile, false, "", true, "", "", "", false)
	if err != nil {
		t.Fatalf("buildTargetMap: %v", err)
	}

	spec := targets["loom"]
	if spec == nil {
		t.Fatal("expected loom target spec in loom-mode")
	}
	if got := spec.Env["LOOM_PROXY_IDLE_EXIT_SECONDS"]; got != "0" {
		t.Fatalf("LOOM_PROXY_IDLE_EXIT_SECONDS = %q, want 0", got)
	}
}

func TestBuildTargetMap_LoomModeUsesExplicitBinaryAcrossPlatforms(t *testing.T) {
	reg := &registry.Registry{}
	loomBinary := "/opt/loom/bin/loom"
	targetsToCheck := []string{"antigravity", "claude", "claude_desktop", "codex", "gemini", "kilocode", "opencode", "vscode", "zed"}

	for _, targetName := range targetsToCheck {
		t.Run(targetName, func(t *testing.T) {
			profile, err := GetPlatformProfile(targetName)
			if err != nil {
				t.Fatalf("GetPlatformProfile(%s): %v", targetName, err)
			}

			targets, err := buildTargetMap(reg, targetName, profile, false, "", true, loomBinary, "/tmp/workspace", "/tmp/registry-root", false)
			if err != nil {
				t.Fatalf("buildTargetMap(%s): %v", targetName, err)
			}

			spec := targets["loom"]
			if spec == nil {
				t.Fatalf("expected loom target spec for %s", targetName)
			}
			if spec.Command != loomBinary {
				t.Fatalf("loom command for %s = %q, want %q", targetName, spec.Command, loomBinary)
			}
		})
	}
}

func writeTestWrapper(t *testing.T, dir string, name string, healthy bool) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	content := "#!/bin/sh\n"
	if healthy {
		content += "if [ \"$1\" = \"--help\" ]; then exit 0; fi\nexit 0\n"
	} else {
		content += "echo broken wrapper >&2\nexit 1\n"
	}
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write wrapper %s: %v", path, err)
	}
	return path
}

// --- CONFIG-2: Unified generator interface tests ---

func TestGenerateParams_DestDir(t *testing.T) {
	t.Parallel()
	p := &GenerateParams{OutputDir: "/out", Target: "claude"}
	if got := p.destDir(); got != "/out/claude" {
		t.Errorf("destDir() = %q, want /out/claude", got)
	}
}

func TestGenerateParams_FilePerm(t *testing.T) {
	t.Parallel()

	p := &GenerateParams{Target: "test", ResolveSecrets: false}
	if perm := p.filePerm(); perm != 0644 {
		t.Errorf("filePerm() = %o, want 0644", perm)
	}

	p.ResolveSecrets = true
	if perm := p.filePerm(); perm != 0600 {
		t.Errorf("filePerm() = %o, want 0600", perm)
	}
}

func TestProfileDrivenJSONConfig_AllJSONPlatforms(t *testing.T) {
	t.Parallel()

	profiles, err := loadProfiles()
	if err != nil {
		t.Fatalf("loadProfiles: %v", err)
	}

	for name, profile := range profiles {
		if profile.ConfigFormat != "json" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			reg := &registry.Registry{
				Servers: []*registry.Server{
					{
						Name: "test-server",
						Common: &registry.TargetSpec{
							Command:     "echo",
							Args:        []any{"hello"},
							Description: "test server",
						},
					},
				},
			}

			p := &GenerateParams{
				Reg:       reg,
				OutputDir: tmpDir,
				Target:    name,
				Profile:   profile,
			}

			if err := generateJSONConfig(p); err != nil {
				t.Fatalf("generateJSONConfig(%s): %v", name, err)
			}

			outFile := filepath.Join(tmpDir, name, profile.ConfigFile)
			data, err := os.ReadFile(outFile)
			if err != nil {
				t.Fatalf("read output: %v", err)
			}
			if !strings.HasSuffix(string(data), "\n") {
				t.Fatalf("expected trailing newline in %s output", name)
			}

			var parsed map[string]any
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("invalid JSON for %s: %v", name, err)
			}

			// Verify root key matches profile.
			if profile.Features.CommandFormat != "array" {
				rootKey := profile.ConfigRoot
				if rootKey == "" {
					rootKey = "mcpServers"
				}
				if _, ok := parsed[rootKey]; !ok {
					t.Errorf("missing root key %q in %s output", rootKey, name)
				}
			}
		})
	}
}

func TestProfileDrivenTOMLConfig_AllTOMLPlatforms(t *testing.T) {
	t.Parallel()

	profiles, err := loadProfiles()
	if err != nil {
		t.Fatalf("loadProfiles: %v", err)
	}

	for name, profile := range profiles {
		if profile.ConfigFormat != "toml" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			reg := &registry.Registry{
				Servers: []*registry.Server{
					{
						Name: "test-server",
						Common: &registry.TargetSpec{
							Command:     "echo",
							Args:        []any{"hello"},
							Description: "test server",
							Timeout:     30,
						},
					},
				},
			}

			p := &GenerateParams{
				Reg:       reg,
				OutputDir: tmpDir,
				Target:    name,
				Profile:   profile,
			}

			if err := generateTomlConfig(p); err != nil {
				t.Fatalf("generateTomlConfig(%s): %v", name, err)
			}

			outFile := filepath.Join(tmpDir, name, profile.ConfigFile)
			data, err := os.ReadFile(outFile)
			if err != nil {
				t.Fatalf("read output: %v", err)
			}

			content := string(data)
			// All TOML configs should have the mcp_servers section.
			if !strings.Contains(content, "[mcp_servers.test-server]") {
				t.Errorf("missing [mcp_servers.test-server] in %s output", name)
			}

			// Verify timeout field name matches profile.
			if profile.Features.SupportsTimeout {
				field := profile.Features.TimeoutField
				if field == "" {
					field = "timeout"
				}
				if !strings.Contains(content, field+" = ") {
					t.Errorf("missing %s field in %s output", field, name)
				}
			}

			// Verify description support.
			if profile.Features.SupportsDescription {
				if !strings.Contains(content, "description = ") {
					t.Errorf("missing description in %s output (SupportsDescription=true)", name)
				}
			}
		})
	}
}

func TestProfileDrivenConfig_ClaudeDesktopUsesCustomFilename(t *testing.T) {
	t.Parallel()

	profile, err := GetPlatformProfile("claude_desktop")
	if err != nil {
		t.Fatalf("GetPlatformProfile: %v", err)
	}

	tmpDir := t.TempDir()
	reg := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name: "test",
				Common: &registry.TargetSpec{
					Command: "echo",
					Args:    []any{"test"},
				},
			},
		},
	}

	p := &GenerateParams{
		Reg:       reg,
		OutputDir: tmpDir,
		Target:    "claude_desktop",
		Profile:   profile,
	}

	if err := generateJSONConfig(p); err != nil {
		t.Fatalf("generateJSONConfig: %v", err)
	}

	// Verify the output uses the profile-specified filename.
	expected := filepath.Join(tmpDir, "claude_desktop", "claude_desktop_config.json")
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("expected output at %s: %v", expected, err)
	}

	// Verify no timeout field (SupportsTimeout=false for claude_desktop).
	data, _ := os.ReadFile(expected)
	if strings.Contains(string(data), "timeout") {
		t.Error("claude_desktop should not have timeout field (SupportsTimeout=false)")
	}
}

func TestProfileDrivenConfig_CodexUsesToolTimeoutSec(t *testing.T) {
	t.Parallel()

	profile, err := GetPlatformProfile("codex")
	if err != nil {
		t.Fatalf("GetPlatformProfile: %v", err)
	}

	tmpDir := t.TempDir()
	reg := testRegistry()
	reg.Servers = []*registry.Server{
		{
			Name: "test",
			Common: &registry.TargetSpec{
				Command: "echo",
				Args:    []any{"test"},
				Timeout: 60,
			},
		},
	}

	p := &GenerateParams{
		Reg:       reg,
		OutputDir: tmpDir,
		Target:    "codex",
		Profile:   profile,
	}

	if err := generateTomlConfig(p); err != nil {
		t.Fatalf("generateTomlConfig: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpDir, "codex", "config.toml"))
	content := string(data)

	if !strings.Contains(content, "tool_timeout_sec = 60") {
		t.Error("codex should use tool_timeout_sec for timeout field")
	}
	if strings.Contains(content, "\ntimeout = ") {
		t.Error("codex should not have generic 'timeout' field")
	}
}

// --- Policy refs and enforcement tests ---

func TestAllPlatforms_HavePolicyRefs(t *testing.T) {
	names := AllPlatformNames()
	if len(names) < 8 {
		t.Fatalf("expected at least 8 platforms, got %d", len(names))
	}

	for _, name := range names {
		profile, err := GetPlatformProfile(name)
		if err != nil {
			t.Fatalf("GetPlatformProfile(%q): %v", name, err)
		}
		if len(profile.Hooks.PolicyRefs) == 0 {
			t.Errorf("platform %q has no policy_refs defined", name)
		}
		found := false
		for _, ref := range profile.Hooks.PolicyRefs {
			if ref == "gitops_flux" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("platform %q does not reference gitops_flux policy", name)
		}
	}
}

func TestAllPlatforms_HaveEnforcement(t *testing.T) {
	validEnforcements := map[string]bool{
		"native": true,
		"proxy":  true,
		"plugin": true,
	}

	for _, name := range AllPlatformNames() {
		profile, err := GetPlatformProfile(name)
		if err != nil {
			t.Fatalf("GetPlatformProfile(%q): %v", name, err)
		}
		enforcement := profile.Hooks.Enforcement
		if enforcement == "" {
			t.Errorf("platform %q has no enforcement field", name)
			continue
		}
		if !validEnforcements[enforcement] {
			t.Errorf("platform %q has invalid enforcement %q (expected native/proxy/plugin)", name, enforcement)
		}
	}
}

func TestNativeEnforcementPlatforms(t *testing.T) {
	// Claude and Gemini should have native enforcement.
	for _, name := range []string{"claude", "gemini"} {
		profile, err := GetPlatformProfile(name)
		if err != nil {
			t.Fatalf("GetPlatformProfile(%q): %v", name, err)
		}
		if profile.Hooks.Enforcement != "native" {
			t.Errorf("platform %q should have native enforcement, got %q", name, profile.Hooks.Enforcement)
		}
	}
}

func TestProxyEnforcementPlatforms(t *testing.T) {
	// These platforms rely on the loom proxy for enforcement.
	proxyPlatforms := []string{"codex", "vscode", "antigravity", "kilocode", "zed", "claude_desktop"}
	for _, name := range proxyPlatforms {
		profile, err := GetPlatformProfile(name)
		if err != nil {
			t.Fatalf("GetPlatformProfile(%q): %v", name, err)
		}
		if profile.Hooks.Enforcement != "proxy" {
			t.Errorf("platform %q should have proxy enforcement, got %q", name, profile.Hooks.Enforcement)
		}
	}
}

func TestPluginEnforcementPlatforms(t *testing.T) {
	profile, err := GetPlatformProfile("opencode")
	if err != nil {
		t.Fatalf("GetPlatformProfile(opencode): %v", err)
	}
	if profile.Hooks.Enforcement != "plugin" {
		t.Errorf("opencode should have plugin enforcement, got %q", profile.Hooks.Enforcement)
	}
}

func TestGeminiHooksConfig_OmitsPreToolUse(t *testing.T) {
	// Gemini does not understand PreToolUse / SubagentStart hook events.
	// Including them causes Gemini CLI to reject the entire hooks block,
	// which silently disables every Gemini lifecycle hook. The gitops_flux
	// policy is enforced at the loom proxy layer for Gemini instead.
	geminiProfile, _ := GetPlatformProfile("gemini")
	config := geminiHooksConfigFromRegistry(testRegistry(), geminiProfile, "")

	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected hooks map in gemini config")
	}

	if _, ok := hooks["PreToolUse"]; ok {
		t.Errorf("gemini hooks must NOT contain PreToolUse (Claude-only event), got: %#v", hooks["PreToolUse"])
	}
	if _, ok := hooks["SubagentStart"]; ok {
		t.Errorf("gemini hooks must NOT contain SubagentStart (Claude-only event), got: %#v", hooks["SubagentStart"])
	}
	// Gemini-supported events should still be present.
	for _, evt := range []string{"SessionStart", "SessionEnd", "AfterTool"} {
		if _, ok := hooks[evt]; !ok {
			t.Errorf("expected gemini hooks to contain %q event", evt)
		}
	}
}

func TestCodexPreamble_IncludesProxyEnforcementAnnotation(t *testing.T) {
	var sb strings.Builder
	emitCodexPreamble(&sb, testRegistry(), "/tmp/workspace", "")
	content := sb.String()

	if !strings.Contains(content, "Policy enforcement:") {
		t.Error("expected policy enforcement annotation in codex preamble")
	}
	if !strings.Contains(content, "gitops_flux") {
		t.Error("expected gitops_flux policy reference in codex preamble")
	}
	if !strings.Contains(content, "proxy") {
		t.Error("expected proxy enforcement annotation in codex preamble")
	}
}

func TestPlatformPolicySummaries(t *testing.T) {
	tests := []struct {
		name        string
		hp          HookProfile
		wantLen     int
		wantEnforce string
	}{
		{
			name: "native enforcement",
			hp: HookProfile{
				PolicyRefs:  []string{"gitops_flux"},
				Enforcement: "native",
			},
			wantLen:     1,
			wantEnforce: "native",
		},
		{
			name: "proxy enforcement",
			hp: HookProfile{
				PolicyRefs:  []string{"gitops_flux"},
				Enforcement: "proxy",
			},
			wantLen:     1,
			wantEnforce: "proxy",
		},
		{
			name: "plugin enforcement",
			hp: HookProfile{
				PolicyRefs:  []string{"gitops_flux"},
				Enforcement: "plugin",
			},
			wantLen:     1,
			wantEnforce: "plugin",
		},
		{
			name:    "no policy refs",
			hp:      HookProfile{},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summaries := PlatformPolicySummaries(tt.hp)
			if len(summaries) != tt.wantLen {
				t.Fatalf("expected %d summaries, got %d", tt.wantLen, len(summaries))
			}
			if tt.wantLen > 0 {
				if summaries[0].Enforcement != tt.wantEnforce {
					t.Errorf("expected enforcement %q, got %q", tt.wantEnforce, summaries[0].Enforcement)
				}
				if summaries[0].PolicyRef != "gitops_flux" {
					t.Errorf("expected policy ref gitops_flux, got %q", summaries[0].PolicyRef)
				}
				if summaries[0].Description == "" {
					t.Error("expected non-empty description")
				}
			}
		})
	}
}

func TestFormatPolicyComment(t *testing.T) {
	t.Run("proxy enforcement", func(t *testing.T) {
		hp := HookProfile{
			PolicyRefs:  []string{"gitops_flux"},
			Enforcement: "proxy",
		}
		comment := FormatPolicyComment(hp, "# ")
		if comment == "" {
			t.Fatal("expected non-empty comment")
		}
		if !strings.Contains(comment, "gitops_flux") {
			t.Error("comment should mention gitops_flux")
		}
		if !strings.Contains(comment, "proxy") {
			t.Error("comment should mention proxy enforcement")
		}
	})

	t.Run("native enforcement", func(t *testing.T) {
		hp := HookProfile{
			PolicyRefs:  []string{"gitops_flux"},
			Enforcement: "native",
		}
		comment := FormatPolicyComment(hp, "# ")
		if comment == "" {
			t.Fatal("expected non-empty comment")
		}
		if !strings.Contains(comment, "PreToolUse") {
			t.Error("native enforcement comment should mention PreToolUse")
		}
	})

	t.Run("no policy refs", func(t *testing.T) {
		hp := HookProfile{}
		comment := FormatPolicyComment(hp, "# ")
		if comment != "" {
			t.Errorf("expected empty comment for no policy refs, got %q", comment)
		}
	})
}

func TestGeneratedTomlConfig_KilocodeIncludesPolicyComment(t *testing.T) {
	tmpDir := t.TempDir()
	profile, err := GetPlatformProfile("kilocode")
	if err != nil {
		t.Fatalf("GetPlatformProfile(kilocode): %v", err)
	}

	params := &GenerateParams{
		Reg:       testRegistry(),
		OutputDir: tmpDir,
		Target:    "kilocode",
		Profile:   profile,
		LoomMode:  true,
	}

	if err := generateTomlConfig(params); err != nil {
		t.Fatalf("generateTomlConfig(kilocode): %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "kilocode", "config.toml"))
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}

	if !strings.Contains(string(content), "Policy enforcement:") {
		t.Error("kilocode TOML config should include policy enforcement comment")
	}
	if !strings.Contains(string(content), "gitops_flux") {
		t.Error("kilocode TOML config should reference gitops_flux policy")
	}
	if !strings.Contains(string(content), "proxy") {
		t.Error("kilocode TOML config should mention proxy enforcement")
	}
}

func TestGeneratedJSONConfig_VSCodeIncludesPolicyMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	profile, err := GetPlatformProfile("vscode")
	if err != nil {
		t.Fatalf("GetPlatformProfile(vscode): %v", err)
	}

	params := &GenerateParams{
		Reg:       testRegistry(),
		OutputDir: tmpDir,
		Target:    "vscode",
		Profile:   profile,
		LoomMode:  true,
	}

	if err := generateJSONConfig(params); err != nil {
		t.Fatalf("generateJSONConfig(vscode): %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "vscode", "mcp.json"))
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("generated mcp.json is not valid JSON: %v", err)
	}

	policyMeta, ok := parsed["_loom_policy"].(map[string]any)
	if !ok {
		t.Fatal("expected _loom_policy metadata in generated vscode config")
	}
	if _, ok := policyMeta["gitops_flux"]; !ok {
		t.Error("expected gitops_flux entry in _loom_policy metadata")
	}
}

func TestAppendHookPolicies_ProxyEnforcementNoPreToolUse(t *testing.T) {
	// For platforms with proxy enforcement, appendHookPolicies should not add
	// any PreToolUse hooks.
	hooks := map[string]any{}
	hp := HookProfile{
		PolicyRefs:  []string{"gitops_flux"},
		Enforcement: "proxy",
	}
	appendHookPolicies(hooks, testRegistry(), hp)

	if _, ok := hooks["PreToolUse"]; ok {
		t.Error("proxy enforcement should not add PreToolUse hooks")
	}
}

func TestAppendHookPolicies_NativeEnforcementAddsPreToolUse(t *testing.T) {
	hooks := map[string]any{}
	hp := HookProfile{
		PolicyRefs:  []string{"gitops_flux"},
		Enforcement: "native",
		Events:      []string{"preToolUse"},
	}
	appendHookPolicies(hooks, testRegistry(), hp)

	preToolUse, ok := hooks["PreToolUse"].([]map[string]any)
	if !ok || len(preToolUse) == 0 {
		t.Fatal("native enforcement with preToolUse event should add PreToolUse hooks")
	}
}

func TestAppendHookPolicies_NativeWithoutPreToolUseEvent_NoHook(t *testing.T) {
	// Native enforcement alone is not enough; the platform must also declare
	// preToolUse in its events list. This guards Gemini (native enforcement
	// but no preToolUse support) from receiving Claude-only hook events.
	hooks := map[string]any{}
	hp := HookProfile{
		PolicyRefs:  []string{"gitops_flux"},
		Enforcement: "native",
		Events:      []string{"sessionStart", "sessionEnd", "postToolUse"},
	}
	appendHookPolicies(hooks, testRegistry(), hp)

	if _, ok := hooks["PreToolUse"]; ok {
		t.Error("native enforcement without preToolUse event should NOT add PreToolUse hooks")
	}
}

func TestAppendHookPolicies_PluginEnforcementNoPreToolUse(t *testing.T) {
	hooks := map[string]any{}
	hp := HookProfile{
		PolicyRefs:  []string{"gitops_flux"},
		Enforcement: "plugin",
	}
	appendHookPolicies(hooks, testRegistry(), hp)

	if _, ok := hooks["PreToolUse"]; ok {
		t.Error("plugin enforcement should not add PreToolUse hooks")
	}
}
