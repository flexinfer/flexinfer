package generator

import (
	"strings"
	"testing"
)

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

	cmd, ok := hook["command"].(string)
	if !ok || cmd == "" {
		t.Fatal("expected non-empty command string")
	}

	// Command should reference the test-health-snapshot script
	if !strings.Contains(cmd, "test-health-snapshot.sh") {
		t.Error("command should reference test-health-snapshot.sh")
	}

	// Command should have safety guards: || true and exit 0
	if !strings.Contains(cmd, "|| true") {
		t.Error("command should use || true to avoid blocking session start")
	}
	if !strings.Contains(cmd, "exit 0") {
		t.Error("command should end with exit 0 to never block session start")
	}

	// Command should check script is executable before running
	if !strings.Contains(cmd, "-x") {
		t.Error("command should check script is executable with -x")
	}

	// Command should log stderr
	if !strings.Contains(cmd, "loom-agent-hooks.log") {
		t.Error("command should redirect stderr to hooks log")
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
