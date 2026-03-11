package sync

import (
	"encoding/json"
	"os"
	"testing"
)

func TestExtractHooksFromSettings(t *testing.T) {
	data := []byte(`{
		"hooks": {"PreToolUse": [{"type": "command", "command": "echo test"}]},
		"permissions": {"allow": ["Bash"]}
	}`)

	hooks, err := ExtractHooksFromSettings(data)
	if err != nil {
		t.Fatal(err)
	}
	if hooks == nil {
		t.Fatal("expected hooks, got nil")
	}

	var parsed map[string]any
	if err := json.Unmarshal(hooks, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["PreToolUse"]; !ok {
		t.Error("expected PreToolUse in hooks")
	}
}

func TestExtractHooksFromSettingsNoHooks(t *testing.T) {
	data := []byte(`{"permissions": {"allow": ["Bash"]}}`)

	hooks, err := ExtractHooksFromSettings(data)
	if err != nil {
		t.Fatal(err)
	}
	if hooks != nil {
		t.Fatalf("expected nil hooks, got %s", string(hooks))
	}
}

func TestMergeHooksPreservesPermissions(t *testing.T) {
	existing := []byte(`{
		"hooks": {"old": "hooks"},
		"permissions": {"allow": ["Bash", "Read"]},
		"mcpServers": {"server1": {}}
	}`)
	canonical := json.RawMessage(`{"PreToolUse": [{"type": "command", "command": "echo new"}]}`)

	merged, changed, err := MergeHooksIntoSettings(existing, canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected changed=true")
	}

	// Parse merged output
	var result map[string]json.RawMessage
	if err := json.Unmarshal(merged, &result); err != nil {
		t.Fatalf("parse merged: %v", err)
	}

	// Check hooks were replaced
	var hooks map[string]any
	if err := json.Unmarshal(result["hooks"], &hooks); err != nil {
		t.Fatal(err)
	}
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Error("expected PreToolUse in merged hooks")
	}
	if _, ok := hooks["old"]; ok {
		t.Error("old hooks should have been replaced")
	}

	// Check permissions preserved
	if _, ok := result["permissions"]; !ok {
		t.Error("permissions should be preserved")
	}

	// Check mcpServers preserved
	if _, ok := result["mcpServers"]; !ok {
		t.Error("mcpServers should be preserved")
	}
}

func TestMergeHooksSkipsWhenIdentical(t *testing.T) {
	hooks := `{"PreToolUse": [{"type": "command", "command": "echo test"}]}`
	existing := []byte(`{
  "hooks": ` + hooks + `,
  "permissions": {"allow": ["Bash"]}
}
`)
	canonical := json.RawMessage(hooks)

	_, changed, err := MergeHooksIntoSettings(existing, canonical)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("expected changed=false when hooks are identical")
	}
}

func TestMergeHooksHandlesMissingFile(t *testing.T) {
	canonical := json.RawMessage(`{"PreToolUse": []}`)

	merged, changed, err := MergeHooksIntoSettings(nil, canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected changed=true for new file")
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(merged, &result); err != nil {
		t.Fatalf("parse merged: %v", err)
	}
	if _, ok := result["hooks"]; !ok {
		t.Error("expected hooks in output")
	}
}

func TestMergeHooksHandlesInvalidJSON(t *testing.T) {
	canonical := json.RawMessage(`{"PreToolUse": []}`)

	merged, changed, err := MergeHooksIntoSettings([]byte("not json{"), canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected changed=true for invalid existing")
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(merged, &result); err != nil {
		t.Fatalf("parse merged: %v", err)
	}
	if _, ok := result["hooks"]; !ok {
		t.Error("expected hooks in output")
	}
}

func TestMergeHooksKeyOrdering(t *testing.T) {
	existing := []byte(`{
		"zebra": "last",
		"permissions": {"allow": []},
		"alpha": "first",
		"hooks": {}
	}`)
	canonical := json.RawMessage(`{"new": true}`)

	merged, _, err := MergeHooksIntoSettings(existing, canonical)
	if err != nil {
		t.Fatal(err)
	}

	// Verify key ordering: hooks, permissions, then alphabetical
	str := string(merged)
	hooksIdx := indexOf(str, `"hooks"`)
	permIdx := indexOf(str, `"permissions"`)
	alphaIdx := indexOf(str, `"alpha"`)
	zebraIdx := indexOf(str, `"zebra"`)

	if hooksIdx > permIdx {
		t.Error("hooks should come before permissions")
	}
	if permIdx > alphaIdx {
		t.Error("permissions should come before alpha")
	}
	if alphaIdx > zebraIdx {
		t.Error("alpha should come before zebra")
	}
}

func TestMergeSettingsForHome_RepoHasHooks(t *testing.T) {
	// When repo has hooks (regen case), use repo hooks at home.
	homeData := []byte(`{"hooks": {"old": true}, "permissions": {"allow": ["Bash"]}}`)
	repoData := []byte(`{"hooks": {"new": true}, "permissions": {"allow": ["Bash", "Read"]}}`)

	merged, changed, err := MergeSettingsForHome(homeData, repoData)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected changed=true")
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(merged, &result); err != nil {
		t.Fatalf("parse merged: %v", err)
	}

	// Hooks should come from repo (fresh from regen)
	var hooks map[string]any
	json.Unmarshal(result["hooks"], &hooks)
	if _, ok := hooks["new"]; !ok {
		t.Error("expected new hooks from repo")
	}
	if _, ok := hooks["old"]; ok {
		t.Error("old hooks should have been replaced")
	}
}

func TestMergeSettingsForHome_RepoNoHooks_PreservesHomeHooks(t *testing.T) {
	// When repo has no hooks (stripped previously), preserve home hooks.
	homeData := []byte(`{"hooks": {"session": true}, "permissions": {"allow": ["Bash"]}}`)
	repoData := []byte(`{"permissions": {"allow": ["Bash", "Read"]}}`)

	merged, _, err := MergeSettingsForHome(homeData, repoData)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(merged, &result); err != nil {
		t.Fatalf("parse merged: %v", err)
	}

	// Hooks should be preserved from home
	var hooks map[string]any
	json.Unmarshal(result["hooks"], &hooks)
	if _, ok := hooks["session"]; !ok {
		t.Error("expected home hooks to be preserved")
	}

	// Permissions should come from repo
	var perms map[string]any
	json.Unmarshal(result["permissions"], &perms)
	allow, _ := perms["allow"].([]any)
	if len(allow) != 2 {
		t.Errorf("expected 2 permissions from repo, got %d", len(allow))
	}
}

func TestMergeSettingsForHome_EmptyHome(t *testing.T) {
	repoData := []byte(`{"hooks": {"new": true}, "permissions": {"allow": ["Bash"]}}`)

	merged, changed, err := MergeSettingsForHome(nil, repoData)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected changed=true")
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(merged, &result); err != nil {
		t.Fatalf("parse merged: %v", err)
	}
	if _, ok := result["hooks"]; !ok {
		t.Error("expected hooks in output")
	}
}

func TestStripHooksFromFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/settings.json"
	data := []byte(`{"hooks": {"session": true}, "permissions": {"allow": ["Bash"]}}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	changed, err := StripHooksFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected changed=true")
	}

	// Verify hooks are gone
	result, _ := os.ReadFile(path)
	var m map[string]json.RawMessage
	json.Unmarshal(result, &m)
	if _, ok := m["hooks"]; ok {
		t.Error("hooks should have been stripped")
	}
	if _, ok := m["permissions"]; !ok {
		t.Error("permissions should be preserved")
	}
}

func TestStripHooksFromFile_NoHooks(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/settings.json"
	data := []byte(`{"permissions": {"allow": ["Bash"]}}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	changed, err := StripHooksFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("expected changed=false when no hooks to strip")
	}
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
