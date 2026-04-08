package generator

import (
	"strings"
	"testing"
)

func TestSessionEndRetroHooks_ReturnsNonEmpty(t *testing.T) {
	hooks := sessionEndRetroHooks("")
	if len(hooks) == 0 {
		t.Fatal("expected non-empty retro hooks")
	}
}

func TestSessionEndRetroHooks_ContainsScript(t *testing.T) {
	hooks := sessionEndRetroHooks("/usr/local/bin/loom")
	if len(hooks) == 0 {
		t.Fatal("expected non-empty retro hooks")
	}

	block, ok := hooks[0]["hooks"].([]map[string]any)
	if !ok || len(block) == 0 {
		t.Fatal("expected hooks block with at least one entry")
	}

	cmd, ok := block[0]["command"].(string)
	if !ok || cmd == "" {
		t.Fatal("expected non-empty command string")
	}

	if !strings.Contains(cmd, "session-retro.sh") {
		t.Errorf("command should reference session-retro.sh, got: %s", cmd)
	}
}

func TestTestHealthSessionStartHooks_Structure(t *testing.T) {
	hooks := testHealthSessionStartHooks("/usr/local/bin/loom")
	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook block, got %d", len(hooks))
	}

	block := hooks[0]
	innerHooks, ok := block["hooks"].([]map[string]any)
	if !ok {
		t.Fatal("expected hooks key with []map[string]any value")
	}
	if len(innerHooks) != 1 {
		t.Fatalf("expected 1 inner hook, got %d", len(innerHooks))
	}

	hook := innerHooks[0]
	if hook["type"] != "command" {
		t.Errorf("expected type=command, got %v", hook["type"])
	}

	cmd2, ok2 := hook["command"].(string)
	if !ok2 || cmd2 == "" {
		t.Fatal("expected non-empty command string")
	}
}

func TestSessionEndRetroHooks_UsesLoomBinary(t *testing.T) {
	hooks := sessionEndRetroHooks("/custom/path/loom")
	block := hooks[0]["hooks"].([]map[string]any)
	cmd := block[0]["command"].(string)

	if !strings.Contains(cmd, "/custom/path/loom") {
		t.Errorf("command should contain custom loom binary path, got: %s", cmd)
	}
}

func TestSessionEndRetroHooks_DefaultLoomBinary(t *testing.T) {
	hooks := sessionEndRetroHooks("")
	block := hooks[0]["hooks"].([]map[string]any)
	cmd := block[0]["command"].(string)

	if !strings.Contains(cmd, "LOOM_BINARY=") {
		t.Errorf("command should set LOOM_BINARY, got: %s", cmd)
	}
}

func TestAppendHookExtras_Retrospective_AppendsToStop(t *testing.T) {
	// Build base hooks with Claude's "Stop" event.
	hooks := buildPlatformHooks(testRegistry(), HookProfile{
		Enabled:          true,
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "Claude Code session",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
	}, "")

	stopBefore := len(hooks["Stop"].([]map[string]any))

	appendHookExtras(hooks, []string{"postSessionEnd_retrospective"}, "")

	stopAfter := len(hooks["Stop"].([]map[string]any))
	if stopAfter <= stopBefore {
		t.Errorf("expected Stop hooks to grow after appending retrospective, before=%d after=%d", stopBefore, stopAfter)
	}
}

func TestAppendHookExtras_Retrospective_AppendsToSessionEnd(t *testing.T) {
	// Build base hooks with Gemini's "SessionEnd" event.
	hooks := buildPlatformHooks(testRegistry(), HookProfile{
		Enabled:          true,
		AgentID:          "gemini-cli",
		AgentType:        "gemini-cli",
		Description:      "Gemini CLI session",
		SessionEndEvent:  "SessionEnd",
		HeartbeatEvent:   "AfterTool",
		HeartbeatMatcher: "",
	}, "")

	endBefore := len(hooks["SessionEnd"].([]map[string]any))

	appendHookExtras(hooks, []string{"postSessionEnd_retrospective"}, "")

	endAfter := len(hooks["SessionEnd"].([]map[string]any))
	if endAfter <= endBefore {
		t.Errorf("expected SessionEnd hooks to grow after appending retrospective, before=%d after=%d", endBefore, endAfter)
	}
}

func TestAppendHookExtras_Retrospective_DoesNotAffectMissingEvent(t *testing.T) {
	// Build hooks for Claude (has Stop but not SessionEnd).
	hooks := buildPlatformHooks(testRegistry(), HookProfile{
		Enabled:          true,
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "Claude Code session",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
	}, "")

	appendHookExtras(hooks, []string{"postSessionEnd_retrospective"}, "")

	// SessionEnd should not exist because Claude uses Stop.
	if _, ok := hooks["SessionEnd"]; ok {
		t.Error("retrospective should not create SessionEnd key when it does not exist")
	}
}

func TestAppendHookExtras_Retrospective_CommandContainsExitZero(t *testing.T) {
	hooks := sessionEndRetroHooks("")
	block := hooks[0]["hooks"].([]map[string]any)
	cmd := block[0]["command"].(string)

	if !strings.Contains(cmd, "exit 0") {
		t.Errorf("retro hook command should end with exit 0 for safety, got: %s", cmd)
	}
}

func TestAppendHookExtras_Retrospective_CommandContainsOrTrue(t *testing.T) {
	hooks := sessionEndRetroHooks("")
	block := hooks[0]["hooks"].([]map[string]any)
	cmd := block[0]["command"].(string)

	if !strings.Contains(cmd, "|| true") {
		t.Errorf("retro hook command should contain || true for fault tolerance, got: %s", cmd)
	}
}

func TestAppendHookExtras_SessionStartTestHealth(t *testing.T) {
	// Build a minimal hooks map with SessionStart already populated
	hooks := map[string]any{
		"SessionStart": []map[string]any{
			{
				"hooks": []map[string]any{
					{"type": "command", "command": "echo existing"},
				},
			},
		},
	}

	appendHookExtras(hooks, []string{"sessionStart_testHealth"}, "/usr/local/bin/loom")

	sessionStart, ok := hooks["SessionStart"].([]map[string]any)
	if !ok {
		t.Fatal("SessionStart should be []map[string]any")
	}

	// Should have appended a new block (original 1 + test health 1)
	if len(sessionStart) != 2 {
		t.Fatalf("expected 2 SessionStart blocks after appending test health, got %d", len(sessionStart))
	}

	// Verify the appended block contains test-health-snapshot.sh
	appendedBlock := sessionStart[1]
	innerHooks, ok := appendedBlock["hooks"].([]map[string]any)
	if !ok {
		t.Fatal("appended block should have hooks key")
	}
	cmd, ok := innerHooks[0]["command"].(string)
	if !ok {
		t.Fatal("expected command string in appended hook")
	}
	if !strings.Contains(cmd, "test-health-snapshot.sh") {
		t.Error("appended hook should reference test-health-snapshot.sh")
	}
}

func TestAppendHookExtras_SessionStartTestHealth_NoExisting(t *testing.T) {
	// If SessionStart is not present or empty, the hook should not be added
	hooks := map[string]any{}

	appendHookExtras(hooks, []string{"sessionStart_testHealth"}, "/usr/local/bin/loom")

	// SessionStart key should not exist since there was nothing to append to
	if _, ok := hooks["SessionStart"]; ok {
		t.Error("SessionStart should not be created when no existing hooks present")
	}
}

func TestBuildPlatformHooks_OmitsSubagentStartWhenNotDeclared(t *testing.T) {
	// Gemini does not declare subagentStart; the hook generator must not
	// emit a SubagentStart block. Including it causes Gemini CLI to reject
	// the entire hooks block.
	hooks := buildPlatformHooks(testRegistry(), HookProfile{
		Enabled:          true,
		AgentID:          "gemini-cli",
		AgentType:        "gemini-cli",
		Description:      "Gemini CLI session",
		SessionEndEvent:  "SessionEnd",
		HeartbeatEvent:   "AfterTool",
		HeartbeatMatcher: "run_shell_command",
		Events:           []string{"sessionStart", "sessionEnd", "postToolUse"},
	}, "")

	if _, ok := hooks["SubagentStart"]; ok {
		t.Error("expected no SubagentStart hooks when subagentStart is not in events list")
	}
}

func TestBuildPlatformHooks_EmitsSubagentStartWhenDeclared(t *testing.T) {
	// Claude declares subagentStart; the hook generator must emit it.
	hooks := buildPlatformHooks(testRegistry(), HookProfile{
		Enabled:          true,
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "Claude Code session",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
		Events:           []string{"sessionStart", "sessionEnd", "preToolUse", "postToolUse", "subagentStart"},
	}, "")

	if _, ok := hooks["SubagentStart"]; !ok {
		t.Error("expected SubagentStart hooks when subagentStart is in events list")
	}
}

func TestHookProfileHasEvent_CaseInsensitive(t *testing.T) {
	hp := HookProfile{Events: []string{"SubagentStart", "preToolUse"}}
	if !hookProfileHasEvent(hp, "subagentStart") {
		t.Error("expected case-insensitive match for subagentStart")
	}
	if !hookProfileHasEvent(hp, "PRETOOLUSE") {
		t.Error("expected case-insensitive match for preToolUse")
	}
	if hookProfileHasEvent(hp, "postToolUse") {
		t.Error("expected no match for postToolUse")
	}
}

func TestAppendHookExtras_UnknownExtra(t *testing.T) {
	hooks := map[string]any{
		"SessionStart": []map[string]any{
			{
				"hooks": []map[string]any{
					{"type": "command", "command": "echo existing"},
				},
			},
		},
	}

	// Unknown extras should be silently ignored
	appendHookExtras(hooks, []string{"unknown_extra"}, "/usr/local/bin/loom")

	sessionStart := hooks["SessionStart"].([]map[string]any)
	if len(sessionStart) != 1 {
		t.Errorf("expected 1 SessionStart block (unchanged), got %d", len(sessionStart))
	}
}
