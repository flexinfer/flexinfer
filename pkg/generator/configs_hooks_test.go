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

// TestVendorLifecycleContract pins the cross-vendor agent lifecycle contract
// so future generator refactors cannot silently drop a session-start /
// session-end / heartbeat hook for any supported vendor. See
// docs/architecture/agent-lifecycle.md for the prose model.
//
// The contract for native-hook vendors (claude, gemini) is:
//   - A SessionStart hook that invokes `loom agent session-start`.
//   - A session-end hook (event name varies per vendor) invoking
//     `loom agent session-end`.
//   - A heartbeat hook (event name + matcher varies) invoking
//     `loom agent heartbeat` with `--ensure-session`.
//
// Codex has no native lifecycle hook surface beyond `notify` (which fires on
// turn completion only), so its contract is different:
//   - A `notify = [...]` entry in config.toml whose shell command invokes
//     `loom agent keepalive-wrap` with `--ensure-session` and passes
//     `--session-id`.
//
// Session-end for codex is not representable at the hook layer; the fleet
// monitor's orphan reaper (internal/hud/monitor + internal/hud/fleetview)
// catches agents left without a session after process exit.
func TestVendorLifecycleContract(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		profile, err := GetPlatformProfile("claude")
		if err != nil {
			t.Fatalf("get claude profile: %v", err)
		}
		hooks := buildPlatformHooks(testRegistry(), profile.Hooks, "")
		assertNativeLifecycleHook(t, hooks, "SessionStart", "agent session-start")
		assertNativeLifecycleHook(t, hooks, profile.Hooks.SessionEndEvent, "agent session-end")
		assertNativeLifecycleHook(t, hooks, profile.Hooks.HeartbeatEvent, "agent heartbeat")
		assertEventCommandContains(t, hooks, profile.Hooks.HeartbeatEvent, "--ensure-session")
	})

	t.Run("gemini", func(t *testing.T) {
		profile, err := GetPlatformProfile("gemini")
		if err != nil {
			t.Fatalf("get gemini profile: %v", err)
		}
		hooks := buildPlatformHooks(testRegistry(), profile.Hooks, "")
		assertNativeLifecycleHook(t, hooks, "SessionStart", "agent session-start")
		assertNativeLifecycleHook(t, hooks, profile.Hooks.SessionEndEvent, "agent session-end")
		assertNativeLifecycleHook(t, hooks, profile.Hooks.HeartbeatEvent, "agent heartbeat")
		assertEventCommandContains(t, hooks, profile.Hooks.HeartbeatEvent, "--ensure-session")
		// Gemini-specific event names differ from Claude's: pin them so a
		// future profile edit doesn't silently flip to Stop / PostToolUse.
		if profile.Hooks.SessionEndEvent != "SessionEnd" {
			t.Errorf("gemini session_end_event must be SessionEnd, got %q", profile.Hooks.SessionEndEvent)
		}
		if profile.Hooks.HeartbeatEvent != "AfterTool" {
			t.Errorf("gemini heartbeat_event must be AfterTool, got %q", profile.Hooks.HeartbeatEvent)
		}
	})

	t.Run("codex_notify_only", func(t *testing.T) {
		// Codex doesn't go through buildPlatformHooks (notify is a top-level
		// TOML key, not a named event). Exercise emitCodexPreamble and
		// assert the notify shell command invokes our keepalive wrapper
		// with the right flags.
		var sb strings.Builder
		emitCodexPreamble(&sb, testRegistry(), "/tmp/workspace", "")
		got := sb.String()

		if !strings.Contains(got, "notify = [\"sh\", \"-c\",") {
			t.Fatalf("codex preamble missing notify shell entry: %s", got)
		}
		for _, want := range []string{
			"agent keepalive-wrap",
			"--ensure-session",
			"--session-id",
			"--agent-type codex",
			"--infer-namespace",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("codex notify command missing %q; full preamble:\n%s", want, got)
			}
		}
		// Codex has no SessionStart / SessionEnd surface — make sure the
		// preamble does not invent one (a real-vendor event name would get
		// silently ignored by Codex and mislead readers of the config).
		for _, forbidden := range []string{"SessionStart", "SessionEnd", "Stop =", "PostToolUse"} {
			if strings.Contains(got, forbidden) {
				t.Errorf("codex preamble must not mention %q (codex does not support named lifecycle events)", forbidden)
			}
		}
	})
}

// assertNativeLifecycleHook asserts that `hooks[event]` is a non-empty list
// containing at least one command matching substr. Used by the vendor
// lifecycle contract test; pulled out so the failure message tells you
// exactly which event / vendor / missing command tripped the check.
func assertNativeLifecycleHook(t *testing.T, hooks map[string]any, event, substr string) {
	t.Helper()
	if event == "" {
		t.Fatalf("profile declared an empty event name for substr=%q", substr)
	}
	entries, ok := hooks[event].([]map[string]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("event %q missing from generated hooks; hooks=%#v", event, hooks)
	}
	for _, entry := range entries {
		inner, ok := entry["hooks"].([]map[string]any)
		if !ok {
			continue
		}
		for _, cmd := range inner {
			if s, ok := cmd["command"].(string); ok && strings.Contains(s, substr) {
				return
			}
		}
	}
	t.Fatalf("no command under event %q contains %q", event, substr)
}

// assertEventCommandContains is like assertNativeLifecycleHook but does not
// fail if the event is missing — it only checks commands under the event
// when the event does exist. Useful for cross-cutting assertions (e.g.
// heartbeat must always have --ensure-session when present).
func assertEventCommandContains(t *testing.T, hooks map[string]any, event, substr string) {
	t.Helper()
	entries, ok := hooks[event].([]map[string]any)
	if !ok {
		return
	}
	for _, entry := range entries {
		inner, ok := entry["hooks"].([]map[string]any)
		if !ok {
			continue
		}
		for _, cmd := range inner {
			if s, ok := cmd["command"].(string); ok && strings.Contains(s, substr) {
				return
			}
		}
	}
	t.Fatalf("event %q exists but no command contains %q", event, substr)
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
