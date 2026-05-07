package generator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
)

// hookProfileHasEvent returns true when the platform's declared events list
// contains the given event name. Used to gate platform-specific hook emission
// (e.g. only emit SubagentStart for platforms that actually support it). The
// match is case-insensitive to tolerate "subagentStart" vs "SubagentStart"
// spellings between the YAML profile and Go event names.
func hookProfileHasEvent(hp HookProfile, event string) bool {
	target := strings.ToLower(event)
	for _, e := range hp.Events {
		if strings.ToLower(e) == target {
			return true
		}
	}
	return false
}

// hookNamespaceVars returns a shell snippet that computes NS_PROJECT (workspace-
// relative 2-level project path) and NS_BRANCH from the WS_ROOT variable set by
// hookAgentIDBootstrap. For worktrees under <repo>/.worktrees/, NS_PROJECT
// resolves to the parent repo path so namespace stays consistent.
func hookNamespaceVars() string {
	return `if echo "$WS_ROOT" | grep -q '/.worktrees/'; then ` +
		`_MAIN="${WS_ROOT%%/.worktrees/*}"; ` +
		`NS_PROJECT="$(basename "$(dirname "$_MAIN")")/$(basename "$_MAIN")"; ` +
		`else ` +
		`NS_PROJECT="$(basename "$(dirname "$WS_ROOT")")/$(basename "$WS_ROOT")"; ` +
		`fi; ` +
		`NS_BRANCH="$(git branch --show-current 2>/dev/null || echo main)"`
}

// buildPlatformHooks generates the shared SessionStart / session-end / heartbeat
// hooks for any platform that supports lifecycle hooks. Platform-specific extras
// (e.g. policy-driven PreToolUse guardrails) are appended by the caller.
// Hook parameters are read from the platform profile's HookProfile.
//
// SubagentStart is only emitted for platforms whose Events list explicitly
// declares "subagentStart" (currently Claude Code only). Other platforms like
// Gemini do not understand this event and reject the entire hooks block when
// it is present.
func buildPlatformHooks(reg *registry.Registry, hp HookProfile, loomBinary string) map[string]any {
	log := `2>>"${TMPDIR:-/tmp}/loom-agent-hooks.log"`
	bootstrap := hookAgentIDBootstrap(hp.AgentID)
	staleCleanup := hookStaleCleanup()
	nsVars := hookNamespaceVars()
	loomCmd := shellQuote(normalizeLoomBinary(loomBinary))
	policy := agentSafetyPolicyFromRegistry(reg)
	descPrefix := strings.TrimSuffix(hp.Description, " session")

	sessionStartHooks := []map[string]any{
		{
			"type": "command",
			"command": fmt.Sprintf(
				`INPUT=$(cat); %s; %s; %s; PARENT_FLAG=""; PARENT_FILE="${AGENT_CACHE_DIR}/parent-session-${AGENT_ID}"; if [ -s "$PARENT_FILE" ]; then PARENT_FLAG="--parent-session-id $(cat "$PARENT_FILE")"; rm -f "$PARENT_FILE"; elif [ -n "${LOOM_PARENT_SESSION_ID:-}" ]; then PARENT_FLAG="--parent-session-id $LOOM_PARENT_SESSION_ID"; fi; %s agent session-start --namespace "$NS_PROJECT/$NS_BRANCH" --agent-id "$AGENT_ID" --agent-type %s --description "%s · $NS_PROJECT" --auto-recall --auto-recall-strategy fast $PARENT_FLAG --quiet %s || true`,
				bootstrap, staleCleanup, nsVars, loomCmd, hp.AgentType, descPrefix, log),
		},
		{
			"type": "command",
			"command": fmt.Sprintf(
				// Kill any stale keepalives for this workspace before spawning a new one.
				// Two-layer cleanup:
				// 1. PID file glob — catches keepalives with intact PID files.
				// 2. pkill by agent-id pattern — catches orphans whose PID files
				//    were lost or started from a different binary path.
				`INPUT=$(cat); %s; `+
					`for pf in "${TMPDIR:-/tmp}"/loom-keepalive-%s-"${WS_HASH}"*.pid; do `+
					`[ -f "$pf" ] && kill "$(cat "$pf")" 2>/dev/null && rm -f "$pf"; `+
					`done; `+
					`pkill -f "loom agent keepalive --agent-id %s-${WS_HASH}" 2>/dev/null || true; `+
					`%s agent keepalive --agent-id "$AGENT_ID" --agent-type %s --quiet </dev/null >/dev/null %s &`,
				bootstrap, hp.AgentID, hp.AgentID, loomCmd, hp.AgentType, log),
		},
	}
	if policy.DirtyWorktreeNudgeOnSessionStart {
		sessionStartHooks = append(sessionStartHooks, map[string]any{
			"type":    "command",
			"command": dirtyWorktreeSessionStartNudgeCommand(policy),
		})
	}
	// Suggest worktree allocation when on main/master to avoid dirty-worktree issues.
	sessionStartHooks = append(sessionStartHooks, map[string]any{
		"type":    "command",
		"command": mainBranchWorktreeNudgeCommand(),
	})

	hooks := map[string]any{
		"SessionStart": []map[string]any{
			{
				"hooks": sessionStartHooks,
			},
		},
		hp.SessionEndEvent: []map[string]any{
			{
				"hooks": []map[string]any{
					{
						"type": "command",
						"command": fmt.Sprintf(
							// Kill keepalives matching this workspace (any session scope) — not just exact agent ID.
							// Two-layer cleanup: PID file glob + pkill for orphans from different binary paths.
							`INPUT=$(cat); %s; for pf in "${TMPDIR:-/tmp}"/loom-keepalive-%s-"${WS_HASH}"*.pid; do [ -f "$pf" ] && kill "$(cat "$pf")" 2>/dev/null && rm -f "$pf"; done; pkill -f "loom agent keepalive --agent-id %s-${WS_HASH}" 2>/dev/null || true; rm -f "$AGENT_ID_FILE"; %s agent session-end --agent-id "$AGENT_ID" --summarize --summary-async --quiet %s || true`,
							bootstrap, hp.AgentID, hp.AgentID, loomCmd, log),
					},
				},
			},
		},
		hp.HeartbeatEvent: []map[string]any{
			{
				"matcher": hp.HeartbeatMatcher,
				"hooks": []map[string]any{
					{
						"type": "command",
						"command": fmt.Sprintf(
							`INPUT=$(cat); %s; %s; %s agent heartbeat --agent-id "$AGENT_ID" --status active --ensure-session --infer-namespace --agent-type %s --description "%s · $NS_PROJECT" --quiet %s || true`,
							bootstrap, nsVars, loomCmd, hp.AgentType, descPrefix, log),
					},
				},
			},
		},
	}

	// Capture parent session ID for subagent session grouping.
	// Write to a file so the subagent's SessionStart can read it
	// (env vars don't propagate across hook subprocess boundaries).
	//
	// Only emit SubagentStart for platforms that declare it in their Events
	// list. Gemini and other platforms reject hook blocks containing unknown
	// event names, which would silently disable ALL of their hooks.
	if hookProfileHasEvent(hp, "subagentStart") {
		hooks["SubagentStart"] = []map[string]any{
			{
				"hooks": []map[string]any{
					{
						"type": "command",
						"command": fmt.Sprintf(
							`INPUT=$(cat); %s; PARENT_SID=$(%s agent session --agent-id "$AGENT_ID" --quiet 2>/dev/null | jq -r '.session.id // empty' 2>/dev/null || true); PARENT_FILE="${AGENT_CACHE_DIR}/parent-session-${AGENT_ID}"; if [ -n "$PARENT_SID" ]; then printf '%%s' "$PARENT_SID" > "$PARENT_FILE"; else rm -f "$PARENT_FILE"; fi; exit 0`,
							bootstrap, loomCmd),
					},
				},
			},
		}
	}

	return hooks
}

// appendHookPolicies dispatches shared policy refs to their hook implementations.
// For native enforcement platforms that explicitly support preToolUse, it
// generates PreToolUse guard hooks. For proxy/plugin enforcement, or for native
// platforms that lack preToolUse (e.g. Gemini), policies are enforced at the
// loom proxy layer, so no PreToolUse hooks are needed.
func appendHookPolicies(hooks map[string]any, reg *registry.Registry, hp HookProfile) {
	for _, ref := range hp.PolicyRefs {
		switch ref {
		case "gitops_flux":
			// Only emit PreToolUse hooks when the platform both uses native
			// enforcement AND declares preToolUse in its Events list. Gemini
			// uses native enforcement but does not understand PreToolUse and
			// will reject the entire hooks block if it appears.
			if hp.Enforcement == "native" && hookProfileHasEvent(hp, "preToolUse") {
				if policyHooks := gitopsFluxGuardrailHooks(reg); len(policyHooks) > 0 {
					hooks["PreToolUse"] = appendHookBlocks(hooks["PreToolUse"], policyHooks...)
				}
			}
			// For "proxy" and "plugin" enforcement, policies are enforced at the
			// loom proxy layer. No PreToolUse hooks are generated; the proxy
			// intercepts blocked commands before they reach the platform.
		}
	}
}

// PolicyEnforcementSummary describes how a policy ref is enforced for a platform.
type PolicyEnforcementSummary struct {
	PolicyRef   string // e.g. "gitops_flux"
	Enforcement string // "native", "proxy", or "plugin"
	Description string // Human-readable enforcement description
}

// PlatformPolicySummaries returns enforcement summaries for all policy refs
// defined on a platform. Used by config generators to annotate proxy-enforced
// policies in generated config comments.
func PlatformPolicySummaries(hp HookProfile) []PolicyEnforcementSummary {
	summaries := make([]PolicyEnforcementSummary, 0, len(hp.PolicyRefs))
	for _, ref := range hp.PolicyRefs {
		desc := ""
		switch hp.Enforcement {
		case "native":
			desc = "enforced via PreToolUse hooks in settings.json"
		case "proxy":
			desc = "enforced at loom proxy layer (no native hook support)"
		case "plugin":
			desc = "enforced via plugin hooks"
		default:
			desc = "enforcement method not specified"
		}
		summaries = append(summaries, PolicyEnforcementSummary{
			PolicyRef:   ref,
			Enforcement: hp.Enforcement,
			Description: desc,
		})
	}
	return summaries
}

// FormatPolicyComment returns a comment block documenting how policies are
// enforced for a given platform. Returns empty string if no policy refs exist.
func FormatPolicyComment(hp HookProfile, prefix string) string {
	summaries := PlatformPolicySummaries(hp)
	if len(summaries) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(prefix + "Policy enforcement:\n")
	for _, s := range summaries {
		sb.WriteString(fmt.Sprintf("%s  - %s: %s\n", prefix, s.PolicyRef, s.Description))
	}
	return sb.String()
}

// appendHookExtras dispatches profile-defined extras to their hook implementations.
//
// The HookProfile is needed (rather than just hp.Extras) so platform-aware
// extras like "telemetry_eventEmit" can read AgentID to decide the correct
// `loom agent event-emit --platform` flag.
func appendHookExtras(hooks map[string]any, hp HookProfile, loomBinary string) {
	for _, extra := range hp.Extras {
		switch extra {
		case "postToolUse_formatters":
			event := "PostToolUse"
			if existing, ok := hooks[event].([]map[string]any); ok {
				hooks[event] = append(existing, claudePostToolUseExtras()...)
			}
		case "postToolUse_taskSync":
			event := "PostToolUse"
			if existing, ok := hooks[event].([]map[string]any); ok {
				hooks[event] = append(existing, claudePostToolUseTaskSyncHook(loomBinary)...)
			}
		case "postSessionEnd_retrospective":
			// Append retrospective hook to whichever session-end event exists.
			retroHooks := sessionEndRetroHooks(loomBinary)
			for _, evt := range []string{"Stop", "SessionEnd"} {
				if existing, ok := hooks[evt].([]map[string]any); ok && len(existing) > 0 {
					hooks[evt] = append(existing, retroHooks...)
				}
			}
		case "sessionStart_testHealth":
			// Inject test suite health snapshot at session start.
			testHealthHooks := testHealthSessionStartHooks(loomBinary)
			if existing, ok := hooks["SessionStart"].([]map[string]any); ok && len(existing) > 0 {
				hooks["SessionStart"] = append(existing, testHealthHooks...)
			}
		case "telemetry_eventEmit":
			// Wire each canonical telemetry hook (session-start/end,
			// pre-/post-tool-use) into `loom agent event-emit`. Best-effort:
			// silently skipped for platforms whose AgentID lacks a CLI
			// normalizer (Gemini/Codex slot in via follow-up slices).
			appendTelemetryEventEmitHooks(hooks, hp, loomBinary)
		}
	}
}

// appendTelemetryEventEmitHooks walks the platform's existing hook event slots
// and, for each canonical telemetry hook the platform supports, appends a hook
// block that pipes the platform-native hook payload into
// `loom agent event-emit --hook <name> --platform <p>`.
//
// Skipped silently when the AgentID does not yet have an event-emit CLI
// normalizer (`telemetryEventEmitPlatform` returns ""). The injected hook is
// best-effort (`|| true`) so a slow or down daemon never fails the user's
// CLI session.
//
// Phase 2.1's `cmd/loom/cmd_agent_event_emit.go` is the consumer of these
// hooks; see `.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md`
// for the broader spectator plan.
func appendTelemetryEventEmitHooks(hooks map[string]any, hp HookProfile, loomBinary string) {
	platform := telemetryEventEmitPlatform(hp)
	if platform == "" {
		return
	}

	loomCmd := shellQuote(normalizeLoomBinary(loomBinary))
	log := `2>>"${TMPDIR:-/tmp}/loom-agent-hooks.log"`
	bootstrap := hookAgentIDBootstrap(hp.AgentID)

	// Walk known event slots; only inject when the slot is non-empty (mirrors
	// the existing extras' behavior of acting only on already-built events).
	// Gemini emits SessionStart/SessionEnd/AfterTool (no PreToolUse), so
	// AfterTool/BeforeTool are included alongside Claude's PreToolUse/
	// PostToolUse — canonicalTelemetryHookForEvent collapses them to the
	// platform-agnostic canonical hook name.
	for _, event := range []string{"SessionStart", "Stop", "SessionEnd", "PreToolUse", "PostToolUse", "BeforeTool", "AfterTool"} {
		canonical := canonicalTelemetryHookForEvent(event)
		if canonical == "" {
			continue
		}
		existing, ok := hooks[event].([]map[string]any)
		if !ok || len(existing) == 0 {
			continue
		}
		block := map[string]any{
			"hooks": []map[string]any{
				{
					"type": "command",
					"command": fmt.Sprintf(
						`INPUT=$(cat); %s; printf '%%s' "$INPUT" | %s agent event-emit --hook %s --platform %s --agent-id "$AGENT_ID" --quiet %s || true`,
						bootstrap, loomCmd, canonical, platform, log),
				},
			},
		}
		hooks[event] = append(existing, block)
	}
}

// telemetryEventEmitPlatform returns the value to pass as `--platform` to
// `loom agent event-emit` for the given HookProfile, or "" if the platform
// is not yet wired in cmd/loom/cmd_agent_event_emit.go. Codex is wired
// separately via codexNotifyCommand, not via this extras case (codex has no
// SessionStart/PreToolUse/PostToolUse — only `notify`).
func telemetryEventEmitPlatform(hp HookProfile) string {
	switch hp.AgentID {
	case "claude-code":
		return "claude-code"
	case "gemini-cli":
		return "gemini-cli"
	}
	return ""
}

// canonicalTelemetryHookForEvent maps a platform-native hook event name to the
// canonical hook string consumed by `loom agent event-emit --hook`. Returns ""
// for events that do not correspond to a canonical telemetry hook (e.g.
// SubagentStart, the heartbeat-only Bash|Task PostToolUse matcher, etc.).
//
// Stop and SessionEnd both map to "session-end" because Claude Code uses
// "Stop" while Gemini uses "SessionEnd" for the same lifecycle moment.
// AfterTool is Gemini's name for the post-tool-use hook; BeforeTool is its
// pre-tool-use counterpart.
func canonicalTelemetryHookForEvent(event string) string {
	switch event {
	case "SessionStart":
		return "session-start"
	case "SessionEnd", "Stop":
		return "session-end"
	case "PreToolUse", "BeforeTool":
		return "pre-tool-use"
	case "PostToolUse", "AfterTool":
		return "post-tool-use"
	}
	return ""
}

func appendHookBlocks(existing any, hooks ...map[string]any) []map[string]any {
	current, ok := existing.([]map[string]any)
	if !ok {
		current = []map[string]any{}
	}
	return append(current, hooks...)
}

// hookAgentIDBootstrap returns shell that derives a stable AGENT_ID for the
// current Claude/Gemini hook input.
//
// When hook JSON includes a session_id, the identity is scoped to that Claude
// session so subprocesses from the same CLI instance stay grouped together.
// If hook input is unavailable, we fall back to a workspace-scoped key.
func hookAgentIDBootstrap(agentID string) string {
	return fmt.Sprintf(
		`HOOK_INPUT="${INPUT:-}"; `+
			`HOOK_SESSION_ID=""; `+
			`if [ -n "$HOOK_INPUT" ]; then `+
			`HOOK_SESSION_ID="$(printf '%%s' "$HOOK_INPUT" | jq -r '.session_id // empty' 2>/dev/null || true)"; `+
			`fi; `+
			`SESSION_SCOPE=""; `+
			`if [ -n "$HOOK_SESSION_ID" ]; then `+
			`SESSION_SCOPE="$(printf '%%s' "$HOOK_SESSION_ID" | cksum | cut -d' ' -f1)"; `+
			`fi; `+
			`WS_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || printf '%%s' "$PWD")"; `+
			`WS_HASH="$(printf '%%s' "$WS_ROOT" | cksum | cut -d' ' -f1)"; `+
			`AGENT_CACHE_DIR="${HOME:-${TMPDIR:-/tmp}}/.cache/loom"; `+
			`mkdir -p "$AGENT_CACHE_DIR"; `+
			`AGENT_ID_FILE="${AGENT_CACHE_DIR}/agent-id-%s-${WS_HASH}${SESSION_SCOPE:+-${SESSION_SCOPE}}"; `+
			`if [ -s "$AGENT_ID_FILE" ]; then `+
			`AGENT_ID="$(cat "$AGENT_ID_FILE")"; `+
			`else `+
			`AGENT_ID="%s-${WS_HASH}${SESSION_SCOPE:+-${SESSION_SCOPE}}"; `+
			`printf '%%s' "$AGENT_ID" > "$AGENT_ID_FILE"; `+
			`fi`,
		agentID, agentID,
	)
}

// hookStaleCleanup returns a shell snippet that removes stale PID and agent ID
// files left behind by a previous session that crashed (Stop hook never fired).
// It checks whether the PID recorded in the keepalive PID file is still alive;
// if the process is dead, it removes both files so a fresh session can start.
func hookStaleCleanup() string {
	return `PID_FILE="${TMPDIR:-/tmp}/loom-keepalive-${AGENT_ID}.pid"; ` +
		`if [ -f "$PID_FILE" ]; then ` +
		`OLD_PID="$(cat "$PID_FILE" 2>/dev/null)"; ` +
		`if [ -n "$OLD_PID" ] && ! kill -0 "$OLD_PID" 2>/dev/null; then ` +
		`rm -f "$PID_FILE" "$AGENT_ID_FILE"; ` +
		`fi; fi`
}

// hooksConfigFromProfile builds a generic hooks config from the platform profile.
// Used for platforms that have hooks.enabled but no platform-specific wrapper.
func hooksConfigFromProfile(reg *registry.Registry, profile *PlatformProfile, loomBinary string) map[string]any {
	hooks := buildPlatformHooks(reg, profile.Hooks, loomBinary)
	appendHookExtras(hooks, profile.Hooks, loomBinary)
	return map[string]any{"hooks": hooks}
}

// emitSandboxPolicy writes a .sandbox-policy.json file for the HUD and agents.
func emitSandboxPolicy(policy *registry.SandboxPolicy, outputDir string) error {
	data := map[string]any{
		"require_sandbox":   policy.RequireSandbox,
		"recommend_sandbox": policy.RecommendSandbox,
		"auto_provision":    policy.AutoProvision,
		"default_backend":   policy.DefaultBackend,
	}
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sandbox policy: %w", err)
	}
	path := filepath.Join(outputDir, ".sandbox-policy.json")
	return os.WriteFile(path, append(out, '\n'), 0644)
}

type agentSafetyPolicy struct {
	DirtyWorktreeMode                string
	DirtyWorktreeNudgeOnSessionStart bool
	DirtyWorktreeNudgeMessage        string
}

func defaultAgentSafetyPolicy() agentSafetyPolicy {
	return agentSafetyPolicy{
		DirtyWorktreeMode:                "continue_scoped_commits",
		DirtyWorktreeNudgeOnSessionStart: true,
		DirtyWorktreeNudgeMessage:        "Dirty worktree detected. Treat pre-existing changes as baseline context, continue work, and stage/commit only files for the active task. Before creating another multi-file worktree, inspect existing linked trees with git -C <repo> worktree list or workspace-clean --report --worktrees. For multi-file work, create repo-local linked trees under <repo>/.worktrees/<branch>; do not create sibling repos under services/, libs/, labs/, or the workspace root. Escalate only if new unexpected changes appear in files you are editing.",
	}
}

func agentSafetyPolicyFromRegistry(reg *registry.Registry) agentSafetyPolicy {
	policy := defaultAgentSafetyPolicy()
	pp := registryPlatformPerms(reg, "agents")
	if pp == nil || pp.Settings == nil {
		return policy
	}

	if v, ok := pp.Settings["dirty_worktree_mode"].(string); ok && strings.TrimSpace(v) != "" {
		policy.DirtyWorktreeMode = strings.TrimSpace(v)
	}
	if v, ok := pp.Settings["dirty_worktree_nudge_on_session_start"].(bool); ok {
		policy.DirtyWorktreeNudgeOnSessionStart = v
	}
	if v, ok := pp.Settings["dirty_worktree_nudge_message"].(string); ok && strings.TrimSpace(v) != "" {
		policy.DirtyWorktreeNudgeMessage = strings.TrimSpace(v)
	}
	return policy
}

func dirtyWorktreeSessionStartNudgeCommand(policy agentSafetyPolicy) string {
	payload, err := json.Marshal(map[string]string{
		"systemMessage": policy.DirtyWorktreeNudgeMessage,
	})
	if err != nil {
		payload = []byte(`{"systemMessage":"Dirty worktree detected. Continue on this branch and stage only task-scoped files."}`)
	}

	// Keep this check fast at session start by avoiding untracked-file scans,
	// which can be expensive in large monorepos.
	return fmt.Sprintf(`if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then if ! git diff --quiet --no-ext-diff || ! git diff --cached --quiet --no-ext-diff; then printf '%%s\n' %q; fi; fi; exit 0`, string(payload))
}

// mainBranchWorktreeNudgeCommand returns a shell command that emits a systemMessage
// suggesting worktree allocation when the agent is on main or master. This is a
// non-blocking suggestion — quick single-file fixes on main are still fine.
func mainBranchWorktreeNudgeCommand() string {
	payload := `{"systemMessage":"You are on main. Before starting feature work or another multi-file change, inspect existing linked trees with git worktree list or workspace-clean --report --worktrees. If you need a new one, use agent_worktree_allocate() to create a repo-local worktree under <repo>/.worktrees/<branch>."}`
	return fmt.Sprintf(`if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then BRANCH="$(git branch --show-current 2>/dev/null)"; if [ "$BRANCH" = "main" ] || [ "$BRANCH" = "master" ]; then printf '%%s\n' %q; fi; fi; exit 0`, payload)
}

// sessionEndRetroHooks returns hook blocks that run the session retrospective
// script after the session-end hook completes.
func sessionEndRetroHooks(loomBinary string) []map[string]any {
	loomCmd := shellQuote(normalizeLoomBinary(loomBinary))
	log := `2>>"${TMPDIR:-/tmp}/loom-agent-hooks.log"`
	return []map[string]any{
		{
			"hooks": []map[string]any{
				{
					"type": "command",
					"command": fmt.Sprintf(
						`INPUT=$(cat); AGENT_CACHE_DIR="${HOME:-${TMPDIR:-/tmp}}/.cache/loom"; WS_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || echo "$PWD")"; WS_HASH="$(printf '%%s' "$WS_ROOT" | cksum | cut -d' ' -f1)"; for f in "$AGENT_CACHE_DIR"/agent-id-*-"${WS_HASH}"*; do [ -s "$f" ] && AGENT_ID="$(cat "$f")" && break; done; SCRIPT="${WS_ROOT}/mcp/skills/session-retro/scripts/session-retro.sh"; if [ -x "$SCRIPT" ]; then LOOM_BINARY=%s AGENT_ID="${AGENT_ID:-unknown}" "$SCRIPT" %s || true; fi; exit 0`,
						loomCmd, log),
				},
			},
		},
	}
}

// testHealthSessionStartHooks returns hook blocks that run a test health snapshot
// on session start. Emits a systemMessage with test pass/fail counts and build status.
func testHealthSessionStartHooks(_ string) []map[string]any {
	log := `2>>"${TMPDIR:-/tmp}/loom-agent-hooks.log"`
	return []map[string]any{
		{
			"hooks": []map[string]any{
				{
					"type": "command",
					"command": fmt.Sprintf(
						`INPUT=$(cat); REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || echo "$PWD")"; SCRIPT="${REPO_ROOT}/mcp/skills/test-health-inject/scripts/test-health-snapshot.sh"; if [ -x "$SCRIPT" ]; then "$SCRIPT" %s || true; fi; exit 0`,
						log),
				},
			},
		},
	}
}
