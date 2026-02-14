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
