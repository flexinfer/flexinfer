package generator

import (
	"strings"
	"testing"
)

func TestHookNamespaceVars_HandlesClaudeWorktrees(t *testing.T) {
	got := hookNamespaceVars()
	// Regression: prior version only matched "/.worktrees/" and treated
	// "<repo>/.claude/worktrees/<branch>" paths as nested project roots,
	// producing a "worktrees/<branch>/claude/<branch>" namespace instead
	// of the parent repo's "<workspace>/<repo>/<branch>".
	if !strings.Contains(got, "/.claude/worktrees/") {
		t.Errorf("hookNamespaceVars() missing claude-worktrees branch; got: %q", got)
	}
	if !strings.Contains(got, "${WS_ROOT%%/.claude/worktrees/*}") {
		t.Errorf("hookNamespaceVars() missing claude-worktrees parameter expansion; got: %q", got)
	}
	// Existing standard worktree pattern must still be supported.
	if !strings.Contains(got, "${WS_ROOT%%/.worktrees/*}") {
		t.Errorf("hookNamespaceVars() lost standard-worktrees parameter expansion; got: %q", got)
	}
}

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

	appendHookExtras(hooks, HookProfile{Extras: []string{"postSessionEnd_retrospective"}}, "")

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

	appendHookExtras(hooks, HookProfile{Extras: []string{"postSessionEnd_retrospective"}}, "")

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

	appendHookExtras(hooks, HookProfile{Extras: []string{"postSessionEnd_retrospective"}}, "")

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

	appendHookExtras(hooks, HookProfile{Extras: []string{"sessionStart_testHealth"}}, "/usr/local/bin/loom")

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

	appendHookExtras(hooks, HookProfile{Extras: []string{"sessionStart_testHealth"}}, "/usr/local/bin/loom")

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
	appendHookExtras(hooks, HookProfile{Extras: []string{"unknown_extra"}}, "/usr/local/bin/loom")

	sessionStart := hooks["SessionStart"].([]map[string]any)
	if len(sessionStart) != 1 {
		t.Errorf("expected 1 SessionStart block (unchanged), got %d", len(sessionStart))
	}
}

// hooksConfigContainsRetro returns true when any command in the platform
// hooks map references session-retro.sh. Used to assert end-to-end opt-in
// wiring through hooksConfigFromProfile.
func hooksConfigContainsRetro(t *testing.T, cfg map[string]any) bool {
	t.Helper()
	hooks, ok := cfg["hooks"].(map[string]any)
	if !ok {
		return false
	}
	for _, v := range hooks {
		entries, ok := v.([]map[string]any)
		if !ok {
			continue
		}
		for _, entry := range entries {
			inner, ok := entry["hooks"].([]map[string]any)
			if !ok {
				continue
			}
			for _, h := range inner {
				if cmd, ok := h["command"].(string); ok && strings.Contains(cmd, "session-retro.sh") {
					return true
				}
			}
		}
	}
	return false
}

// TestHooksConfigFromProfile_RetroOptIn_Claude verifies the
// postSessionEnd_retrospective extra is opt-in for Claude: absent by default,
// present when the profile's extras list includes it. This pins the opt-in
// contract end-to-end through hooksConfigFromProfile (the path loom sync uses
// to materialize Claude's settings.json).
func TestHooksConfigFromProfile_RetroOptIn_Claude(t *testing.T) {
	profile, err := GetPlatformProfile("claude")
	if err != nil {
		t.Fatalf("get claude profile: %v", err)
	}

	// Default profile (no retro extra) should NOT generate the retro hook.
	cfgDefault := hooksConfigFromProfile(testRegistry(), profile, "")
	if hooksConfigContainsRetro(t, cfgDefault) {
		t.Error("retro hook generated for Claude without postSessionEnd_retrospective extra")
	}

	// Clone the profile with an added extras entry; do not mutate the cached
	// profile registry returned by GetPlatformProfile.
	clone := *profile
	clone.Hooks.Extras = append([]string{}, profile.Hooks.Extras...)
	clone.Hooks.Extras = append(clone.Hooks.Extras, "postSessionEnd_retrospective")

	cfgEnabled := hooksConfigFromProfile(testRegistry(), &clone, "")
	if !hooksConfigContainsRetro(t, cfgEnabled) {
		t.Error("retro hook missing for Claude with postSessionEnd_retrospective extra enabled")
	}
}

// TestHooksConfigFromProfile_RetroOptIn_Gemini verifies the same opt-in
// contract for Gemini. Gemini uses SessionEnd (not Stop), so this also
// guards against the dispatcher only handling Claude's event name.
func TestHooksConfigFromProfile_RetroOptIn_Gemini(t *testing.T) {
	profile, err := GetPlatformProfile("gemini")
	if err != nil {
		t.Fatalf("get gemini profile: %v", err)
	}

	cfgDefault := hooksConfigFromProfile(testRegistry(), profile, "")
	if hooksConfigContainsRetro(t, cfgDefault) {
		t.Error("retro hook generated for Gemini without postSessionEnd_retrospective extra")
	}

	clone := *profile
	clone.Hooks.Extras = append([]string{}, profile.Hooks.Extras...)
	clone.Hooks.Extras = append(clone.Hooks.Extras, "postSessionEnd_retrospective")

	cfgEnabled := hooksConfigFromProfile(testRegistry(), &clone, "")
	if !hooksConfigContainsRetro(t, cfgEnabled) {
		t.Error("retro hook missing for Gemini with postSessionEnd_retrospective extra enabled")
	}

	// Sanity: the retro hook must have landed on Gemini's SessionEnd event,
	// not on Claude's Stop event (Gemini does not emit Stop).
	hooks := cfgEnabled["hooks"].(map[string]any)
	if _, ok := hooks["Stop"]; ok {
		t.Error("Gemini hooks must not contain a Stop event (Gemini uses SessionEnd)")
	}
	sessionEnd, ok := hooks["SessionEnd"].([]map[string]any)
	if !ok || len(sessionEnd) == 0 {
		t.Fatal("Gemini hooks missing SessionEnd entries")
	}
	var foundRetroOnSessionEnd bool
	for _, entry := range sessionEnd {
		inner, ok := entry["hooks"].([]map[string]any)
		if !ok {
			continue
		}
		for _, h := range inner {
			if cmd, ok := h["command"].(string); ok && strings.Contains(cmd, "session-retro.sh") {
				foundRetroOnSessionEnd = true
			}
		}
	}
	if !foundRetroOnSessionEnd {
		t.Error("retro hook did not attach to Gemini SessionEnd event")
	}
}

// --- Phase 2.2: telemetry_eventEmit hook wiring ---
//
// These tests cover the new "telemetry_eventEmit" extras case that wires each
// platform's native lifecycle hooks into `loom agent event-emit` (Phase 2.1's
// CLI). Spec: `.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md`.

func TestCanonicalTelemetryHookForEvent(t *testing.T) {
	cases := map[string]string{
		"SessionStart":  "session-start",
		"SessionEnd":    "session-end",
		"Stop":          "session-end",  // Claude Code uses Stop for the same lifecycle moment.
		"PreToolUse":    "pre-tool-use", // Claude
		"PostToolUse":   "post-tool-use",
		"BeforeTool":    "pre-tool-use",  // Gemini-native name (Phase 2.2b).
		"AfterTool":     "post-tool-use", // Gemini-native name.
		"SubagentStart": "",              // Subagent events are not canonical telemetry hooks.
		"":              "",
	}
	for event, want := range cases {
		if got := canonicalTelemetryHookForEvent(event); got != want {
			t.Errorf("canonicalTelemetryHookForEvent(%q) = %q, want %q", event, got, want)
		}
	}
}

func TestTelemetryEventEmitPlatform_ClaudeCode(t *testing.T) {
	if got := telemetryEventEmitPlatform(HookProfile{AgentID: "claude-code"}); got != "claude-code" {
		t.Errorf("claude-code platform: got %q, want %q", got, "claude-code")
	}
}

func TestTelemetryEventEmitPlatform_GeminiCLI(t *testing.T) {
	if got := telemetryEventEmitPlatform(HookProfile{AgentID: "gemini-cli"}); got != "gemini-cli" {
		t.Errorf("gemini-cli platform: got %q, want %q", got, "gemini-cli")
	}
}

func TestTelemetryEventEmitPlatform_UnsupportedReturnsEmpty(t *testing.T) {
	// Codex is wired separately via codexNotifyCommand (not the extras
	// case), so it stays empty here. Other agents without a normalizer
	// also stay empty so the case is a no-op rather than emitting hooks
	// that publish broken payloads.
	for _, agentID := range []string{"codex", "kilocode", "", "unknown"} {
		if got := telemetryEventEmitPlatform(HookProfile{AgentID: agentID}); got != "" {
			t.Errorf("AgentID=%q: got %q, want empty", agentID, got)
		}
	}
}

func TestAppendHookExtras_TelemetryEventEmit_ClaudeCodeWiresAllFourEvents(t *testing.T) {
	// Build Claude Code base hooks (Stop + PostToolUse populated by buildPlatformHooks).
	hp := HookProfile{
		Enabled:          true,
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "Claude Code session",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
		Events:           []string{"sessionStart", "sessionEnd", "preToolUse", "postToolUse"},
		Extras:           []string{"telemetry_eventEmit"},
	}
	hooks := buildPlatformHooks(testRegistry(), hp, "/usr/local/bin/loom")

	// PreToolUse is not populated by buildPlatformHooks alone; Claude's
	// PreToolUse blocks come from policy refs (gitops_flux). Inject a minimal
	// existing block so the extras case has a slot to append to. This mirrors
	// production where appendHookPolicies runs before appendHookExtras.
	hooks["PreToolUse"] = []map[string]any{
		{"hooks": []map[string]any{{"type": "command", "command": "true"}}},
	}

	appendHookExtras(hooks, hp, "/usr/local/bin/loom")

	for _, event := range []string{"SessionStart", "Stop", "PreToolUse", "PostToolUse"} {
		blocks, ok := hooks[event].([]map[string]any)
		if !ok {
			t.Errorf("event=%s: hooks slot missing or wrong type", event)
			continue
		}
		var found bool
		for _, b := range blocks {
			inner, ok := b["hooks"].([]map[string]any)
			if !ok {
				continue
			}
			for _, h := range inner {
				cmd, _ := h["command"].(string)
				if strings.Contains(cmd, "agent event-emit") && strings.Contains(cmd, "--platform claude-code") {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Errorf("event=%s: telemetry_eventEmit did not inject a `loom agent event-emit --platform claude-code` hook", event)
		}
	}
}

func TestAppendHookExtras_TelemetryEventEmit_UsesCanonicalHookNames(t *testing.T) {
	hp := HookProfile{
		Enabled:          true,
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "Claude Code session",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
		Events:           []string{"sessionStart", "sessionEnd", "preToolUse", "postToolUse"},
		Extras:           []string{"telemetry_eventEmit"},
	}
	hooks := buildPlatformHooks(testRegistry(), hp, "/usr/local/bin/loom")
	hooks["PreToolUse"] = []map[string]any{
		{"hooks": []map[string]any{{"type": "command", "command": "true"}}},
	}
	appendHookExtras(hooks, hp, "/usr/local/bin/loom")

	expectedHookForEvent := map[string]string{
		"SessionStart": "--hook session-start",
		"Stop":         "--hook session-end",
		"PreToolUse":   "--hook pre-tool-use",
		"PostToolUse":  "--hook post-tool-use",
	}
	for event, expectedFlag := range expectedHookForEvent {
		blocks, _ := hooks[event].([]map[string]any)
		var ok bool
		for _, b := range blocks {
			inner, _ := b["hooks"].([]map[string]any)
			for _, h := range inner {
				cmd, _ := h["command"].(string)
				if strings.Contains(cmd, "agent event-emit") && strings.Contains(cmd, expectedFlag) {
					ok = true
					break
				}
			}
			if ok {
				break
			}
		}
		if !ok {
			t.Errorf("event=%s: did not find event-emit hook with flag %q", event, expectedFlag)
		}
	}
}

func TestAppendHookExtras_TelemetryEventEmit_NoOpForUnsupportedPlatform(t *testing.T) {
	// Codex is wired separately via codexNotifyCommand (Phase 2.2c), and
	// kilocode has no native hook surface — both must skip the extras case
	// silently rather than emit hooks pointing at a --platform the CLI
	// would reject.
	hp := HookProfile{
		Enabled:          true,
		AgentID:          "kilocode",
		AgentType:        "kilocode",
		Description:      "Kilocode session",
		SessionEndEvent:  "SessionEnd",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "",
		Events:           []string{"sessionStart", "sessionEnd"},
		Extras:           []string{"telemetry_eventEmit"},
	}
	hooks := buildPlatformHooks(testRegistry(), hp, "/usr/local/bin/loom")
	startBefore := len(hooks["SessionStart"].([]map[string]any))
	endBefore := len(hooks["SessionEnd"].([]map[string]any))

	appendHookExtras(hooks, hp, "/usr/local/bin/loom")

	startAfter := len(hooks["SessionStart"].([]map[string]any))
	endAfter := len(hooks["SessionEnd"].([]map[string]any))
	if startAfter != startBefore {
		t.Errorf("SessionStart grew for unsupported platform: before=%d after=%d", startBefore, startAfter)
	}
	if endAfter != endBefore {
		t.Errorf("SessionEnd grew for unsupported platform: before=%d after=%d", endBefore, endAfter)
	}
}

// --- Phase 2.2b/c: Gemini and Codex generator wiring ---

func TestAppendHookExtras_TelemetryEventEmit_GeminiWiresThreeEvents(t *testing.T) {
	// Gemini emits SessionStart + SessionEnd (via session_end_event) +
	// AfterTool (via heartbeat_event) — no PreToolUse. The extras case
	// should inject event-emit on each of those three slots.
	hp := HookProfile{
		Enabled:          true,
		AgentID:          "gemini-cli",
		AgentType:        "gemini-cli",
		Description:      "Gemini CLI session",
		SessionEndEvent:  "SessionEnd",
		HeartbeatEvent:   "AfterTool",
		HeartbeatMatcher: "run_shell_command",
		Events:           []string{"sessionStart", "sessionEnd", "postToolUse"},
		Extras:           []string{"telemetry_eventEmit"},
	}
	hooks := buildPlatformHooks(testRegistry(), hp, "/usr/local/bin/loom")
	appendHookExtras(hooks, hp, "/usr/local/bin/loom")

	for _, event := range []string{"SessionStart", "SessionEnd", "AfterTool"} {
		blocks, _ := hooks[event].([]map[string]any)
		var found bool
		for _, b := range blocks {
			inner, _ := b["hooks"].([]map[string]any)
			for _, h := range inner {
				cmd, _ := h["command"].(string)
				if strings.Contains(cmd, "agent event-emit") && strings.Contains(cmd, "--platform gemini-cli") {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Errorf("event=%s: gemini telemetry_eventEmit did not inject `--platform gemini-cli` hook", event)
		}
	}
}

func TestAppendHookExtras_TelemetryEventEmit_GeminiAfterToolMapsToPostToolUse(t *testing.T) {
	// AfterTool is Gemini-native; the canonical hook flag passed to the
	// CLI must be --hook post-tool-use so the event-emit normalizer
	// produces tool.call.end.
	hp := HookProfile{
		Enabled:          true,
		AgentID:          "gemini-cli",
		AgentType:        "gemini-cli",
		Description:      "Gemini CLI session",
		SessionEndEvent:  "SessionEnd",
		HeartbeatEvent:   "AfterTool",
		HeartbeatMatcher: "run_shell_command",
		Events:           []string{"sessionStart", "sessionEnd", "postToolUse"},
		Extras:           []string{"telemetry_eventEmit"},
	}
	hooks := buildPlatformHooks(testRegistry(), hp, "/usr/local/bin/loom")
	appendHookExtras(hooks, hp, "/usr/local/bin/loom")

	blocks, _ := hooks["AfterTool"].([]map[string]any)
	var afterToolEmitsPost bool
	for _, b := range blocks {
		inner, _ := b["hooks"].([]map[string]any)
		for _, h := range inner {
			cmd, _ := h["command"].(string)
			if strings.Contains(cmd, "agent event-emit") && strings.Contains(cmd, "--hook post-tool-use") && strings.Contains(cmd, "--platform gemini-cli") {
				afterToolEmitsPost = true
				break
			}
		}
		if afterToolEmitsPost {
			break
		}
	}
	if !afterToolEmitsPost {
		t.Error("AfterTool slot must inject --hook post-tool-use --platform gemini-cli")
	}
}

func TestGeminiProfile_OptedIntoTelemetryEventEmit(t *testing.T) {
	profiles, err := loadProfiles()
	if err != nil {
		t.Fatalf("loadProfiles: %v", err)
	}
	profile, ok := profiles["gemini"]
	if !ok {
		t.Fatal("gemini profile missing from registry")
	}
	if !containsString(profile.Hooks.Extras, "telemetry_eventEmit") {
		t.Errorf("gemini profile extras missing telemetry_eventEmit: got %v", profile.Hooks.Extras)
	}
}

func TestCodexNotifyCommand_TelemetryEmitDisabledByDefault(t *testing.T) {
	// telemetryEmit=false: command is unchanged from Phase 2.1 baseline.
	cmd := codexNotifyCommand("loom", false)
	if strings.Contains(cmd, "agent event-emit") {
		t.Error("codex notify must not contain event-emit when telemetryEmit=false")
	}
}

func TestCodexNotifyCommand_TelemetryEmitWiresPostToolUse(t *testing.T) {
	cmd := codexNotifyCommand("loom", true)
	if !strings.Contains(cmd, "agent event-emit --hook post-tool-use --platform codex") {
		t.Errorf("codex notify with telemetryEmit=true must pipe to event-emit --hook post-tool-use --platform codex; got: %s", cmd)
	}
	if !strings.Contains(cmd, "|| true") {
		t.Errorf("codex notify event-emit must be best-effort (|| true); got: %s", cmd)
	}
	if !strings.Contains(cmd, "--quiet") {
		t.Errorf("codex notify event-emit must use --quiet (hook context); got: %s", cmd)
	}
}

func TestCodexProfile_OptedIntoTelemetryEventEmit(t *testing.T) {
	profiles, err := loadProfiles()
	if err != nil {
		t.Fatalf("loadProfiles: %v", err)
	}
	profile, ok := profiles["codex"]
	if !ok {
		t.Fatal("codex profile missing from registry")
	}
	if !containsString(profile.Hooks.Extras, "telemetry_eventEmit") {
		t.Errorf("codex profile extras missing telemetry_eventEmit: got %v", profile.Hooks.Extras)
	}
}

func TestAppendHookExtras_TelemetryEventEmit_NoOpWhenExtraAbsent(t *testing.T) {
	hp := HookProfile{
		Enabled:          true,
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "Claude Code session",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
		Events:           []string{"sessionStart", "sessionEnd", "preToolUse", "postToolUse"},
		// No Extras; baseline should be untouched by the case.
	}
	hooks := buildPlatformHooks(testRegistry(), hp, "/usr/local/bin/loom")
	startBefore := len(hooks["SessionStart"].([]map[string]any))
	stopBefore := len(hooks["Stop"].([]map[string]any))
	postBefore := len(hooks["PostToolUse"].([]map[string]any))

	appendHookExtras(hooks, hp, "/usr/local/bin/loom")

	if got := len(hooks["SessionStart"].([]map[string]any)); got != startBefore {
		t.Errorf("SessionStart grew without telemetry_eventEmit extra: before=%d after=%d", startBefore, got)
	}
	if got := len(hooks["Stop"].([]map[string]any)); got != stopBefore {
		t.Errorf("Stop grew without telemetry_eventEmit extra: before=%d after=%d", stopBefore, got)
	}
	if got := len(hooks["PostToolUse"].([]map[string]any)); got != postBefore {
		t.Errorf("PostToolUse grew without telemetry_eventEmit extra: before=%d after=%d", postBefore, got)
	}
}

func TestAppendHookExtras_TelemetryEventEmit_BestEffortOrTrue(t *testing.T) {
	// The injected event-emit hook must end with `|| true` so a slow or down
	// daemon never causes the platform's hook chain to fail (which could break
	// a user's CLI session). Mirrors the existing extras' best-effort posture.
	hp := HookProfile{
		Enabled:          true,
		AgentID:          "claude-code",
		AgentType:        "claude-code",
		Description:      "Claude Code session",
		SessionEndEvent:  "Stop",
		HeartbeatEvent:   "PostToolUse",
		HeartbeatMatcher: "Bash|Task",
		Events:           []string{"sessionStart", "sessionEnd", "preToolUse", "postToolUse"},
		Extras:           []string{"telemetry_eventEmit"},
	}
	hooks := buildPlatformHooks(testRegistry(), hp, "/usr/local/bin/loom")
	appendHookExtras(hooks, hp, "/usr/local/bin/loom")

	blocks, _ := hooks["SessionStart"].([]map[string]any)
	for _, b := range blocks {
		inner, _ := b["hooks"].([]map[string]any)
		for _, h := range inner {
			cmd, _ := h["command"].(string)
			if strings.Contains(cmd, "agent event-emit") {
				if !strings.Contains(cmd, "|| true") {
					t.Errorf("event-emit hook is not best-effort (missing `|| true`): %s", cmd)
				}
				if !strings.Contains(cmd, "--quiet") {
					t.Errorf("event-emit hook missing --quiet flag (required for hook context): %s", cmd)
				}
			}
		}
	}
}

func TestClaudeProfile_OptedIntoTelemetryEventEmit(t *testing.T) {
	// Lock in the YAML wiring: Claude Code's profile must carry
	// "telemetry_eventEmit" in its extras so `loom sync claude --regen`
	// generates the new hooks.
	profiles, err := loadProfiles()
	if err != nil {
		t.Fatalf("loadProfiles: %v", err)
	}
	profile, ok := profiles["claude"]
	if !ok {
		t.Fatal("claude profile missing from registry")
	}
	var found bool
	for _, e := range profile.Hooks.Extras {
		if e == "telemetry_eventEmit" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("claude profile extras missing telemetry_eventEmit: got %v", profile.Hooks.Extras)
	}
}
