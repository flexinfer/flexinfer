package sync

import (
	"encoding/json"
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

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
