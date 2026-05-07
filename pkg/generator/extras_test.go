package generator

import (
	"strings"
	"testing"
)

// TestExtraDescriptors_KnownExtrasPresent confirms that every legacy
// extra name from the pre-CONFIG-2 dispatch switch has a descriptor
// entry. Catches accidental deletion when refactoring.
func TestExtraDescriptors_KnownExtrasPresent(t *testing.T) {
	want := []string{
		"postToolUse_formatters",
		"postToolUse_taskSync",
		"postSessionEnd_retrospective",
		"sessionStart_testHealth",
		// Extensibility example shipped in CONFIG-2.
		"postToolUse_lint",
	}
	for _, name := range want {
		if _, ok := extraDescriptors[name]; !ok {
			t.Errorf("extraDescriptors missing %q", name)
		}
	}
}

// TestRenderExtraTemplate_FormattersShape verifies the
// post_tool_use_formatters template produces blocks matching the legacy
// claudePostToolUseExtras output shape: 1 block with matcher Write|Edit
// and 3 nested hook entries (black, gofmt, :latest detector).
func TestRenderExtraTemplate_FormattersShape(t *testing.T) {
	blocks, err := renderExtraTemplate("extras/post_tool_use_formatters.json.tmpl", newExtraContext("/usr/local/bin/loom", "claude-code"))
	if err != nil {
		t.Fatalf("render formatters: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if matcher, _ := blocks[0]["matcher"].(string); matcher != "Write|Edit" {
		t.Errorf("matcher = %q, want Write|Edit", matcher)
	}
	hooks, ok := blocks[0]["hooks"].([]map[string]any)
	if !ok {
		t.Fatalf("hooks is %T, want []map[string]any (canonicalizeHookBlocks should have re-typed it)", blocks[0]["hooks"])
	}
	if len(hooks) != 3 {
		t.Fatalf("expected 3 nested hooks (black, gofmt, :latest), got %d", len(hooks))
	}
	cmds := []string{}
	for _, h := range hooks {
		if cmd, ok := h["command"].(string); ok {
			cmds = append(cmds, cmd)
		}
	}
	joined := strings.Join(cmds, "\n")
	wantSubstrings := []string{"black", "gofmt", ":latest"}
	for _, sub := range wantSubstrings {
		if !strings.Contains(joined, sub) {
			t.Errorf("expected formatters output to contain %q\ngot:\n%s", sub, joined)
		}
	}
}

// TestRenderExtraTemplate_TaskSyncIncludesLoomBinary confirms the
// printf-driven taskSync template inlines the loom binary path and
// preserves the agent-id bootstrap snippet.
func TestRenderExtraTemplate_TaskSyncIncludesLoomBinary(t *testing.T) {
	loomBinary := "/custom/path/loom"
	blocks, err := renderExtraTemplate("extras/post_tool_use_task_sync.json.tmpl", newExtraContext(loomBinary, "claude-code"))
	if err != nil {
		t.Fatalf("render taskSync: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	hooks, ok := blocks[0]["hooks"].([]map[string]any)
	if !ok || len(hooks) != 1 {
		t.Fatalf("expected 1 nested hook, got %#v", blocks[0]["hooks"])
	}
	cmd, _ := hooks[0]["command"].(string)
	if !strings.Contains(cmd, loomBinary) {
		t.Errorf("expected command to contain loom binary %q, got: %s", loomBinary, cmd)
	}
	if !strings.Contains(cmd, "agent task-sync") {
		t.Errorf("expected command to contain 'agent task-sync', got: %s", cmd)
	}
	if !strings.Contains(cmd, "AGENT_ID_FILE") {
		t.Errorf("expected command to embed hookAgentIDBootstrap snippet (looked for AGENT_ID_FILE), got: %s", cmd)
	}
}

// TestRenderExtraTemplate_LintExtensibilityExample exercises the new
// post_tool_use_lint extra (the CONFIG-2 extensibility proof). Wiring
// it into a profile's extras list should add a Write|Edit matcher with
// language-specific lint commands.
func TestRenderExtraTemplate_LintExtensibilityExample(t *testing.T) {
	blocks, err := renderExtraTemplate("extras/post_tool_use_lint.json.tmpl", newExtraContext("", ""))
	if err != nil {
		t.Fatalf("render lint extra: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if matcher, _ := blocks[0]["matcher"].(string); matcher != "Write|Edit" {
		t.Errorf("matcher = %q, want Write|Edit", matcher)
	}
	hooks, _ := blocks[0]["hooks"].([]map[string]any)
	if len(hooks) != 1 {
		t.Fatalf("expected 1 nested hook, got %d", len(hooks))
	}
	cmd, _ := hooks[0]["command"].(string)
	for _, want := range []string{"gofmt", "black", "tsc"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("expected lint command to mention %q, got: %s", want, cmd)
		}
	}
}

// TestAppendHookExtras_LintWiresIntoPostToolUse verifies that adding
// "postToolUse_lint" to a HookProfile.Extras list (no Go code change)
// successfully wires the lint hook into PostToolUse. This is the
// EPIC 3 / CONFIG-2 extensibility contract.
func TestAppendHookExtras_LintWiresIntoPostToolUse(t *testing.T) {
	hooks := map[string]any{
		"PostToolUse": []map[string]any{
			{"matcher": "Bash|Task", "hooks": []map[string]any{{"type": "command", "command": "true"}}},
		},
	}
	hp := HookProfile{
		AgentID: "claude-code",
		Extras:  []string{"postToolUse_lint"},
	}
	appendHookExtras(hooks, hp, "/usr/local/bin/loom")

	blocks, ok := hooks["PostToolUse"].([]map[string]any)
	if !ok {
		t.Fatalf("PostToolUse type = %T, want []map[string]any", hooks["PostToolUse"])
	}
	// Original heartbeat block + 1 new lint block = 2 blocks.
	if len(blocks) != 2 {
		t.Fatalf("expected 2 PostToolUse blocks (heartbeat + lint), got %d", len(blocks))
	}
	// The new block should have Write|Edit matcher.
	last := blocks[len(blocks)-1]
	if matcher, _ := last["matcher"].(string); matcher != "Write|Edit" {
		t.Errorf("expected last block matcher Write|Edit, got %q", matcher)
	}
}

// TestAppendHookExtras_UnknownExtraIsIgnored ensures unknown names
// (typos, deprecated entries) silently fall through rather than crash.
// Matches legacy switch-default behavior.
func TestAppendHookExtras_UnknownExtraIsIgnored(t *testing.T) {
	hooks := map[string]any{
		"PostToolUse": []map[string]any{{"matcher": "Bash"}},
	}
	hp := HookProfile{Extras: []string{"definitely_not_a_real_extra"}}
	appendHookExtras(hooks, hp, "")

	blocks := hooks["PostToolUse"].([]map[string]any)
	if len(blocks) != 1 {
		t.Errorf("unknown extra should not modify hooks, got %d blocks", len(blocks))
	}
}
