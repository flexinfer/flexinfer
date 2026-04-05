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
