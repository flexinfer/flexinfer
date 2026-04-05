package generator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
)

// buildPlatformHooks generates the shared SessionStart / session-end / heartbeat
// hooks for any platform that supports lifecycle hooks. Platform-specific extras
// (e.g. policy-driven PreToolUse guardrails) are appended by the caller.
// Hook parameters are read from the platform profile's HookProfile.
func buildPlatformHooks(reg *registry.Registry, hp HookProfile, loomBinary string) map[string]any {
	log := `2>>"${TMPDIR:-/tmp}/loom-agent-hooks.log"`
	bootstrap := hookAgentIDBootstrap(hp.AgentID)
	staleCleanup := hookStaleCleanup()
	loomCmd := shellQuote(normalizeLoomBinary(loomBinary))
	policy := agentSafetyPolicyFromRegistry(reg)

	sessionStartHooks := []map[string]any{
		{
			"type": "command",
			"command": fmt.Sprintf(
				`INPUT=$(cat); %s; %s; PARENT_FLAG=""; PARENT_FILE="${AGENT_CACHE_DIR}/parent-session-${AGENT_ID}"; if [ -s "$PARENT_FILE" ]; then PARENT_FLAG="--parent-session-id $(cat "$PARENT_FILE")"; rm -f "$PARENT_FILE"; elif [ -n "${LOOM_PARENT_SESSION_ID:-}" ]; then PARENT_FLAG="--parent-session-id $LOOM_PARENT_SESSION_ID"; fi; %s agent session-start --namespace "$(basename $(git rev-parse --show-toplevel 2>/dev/null || echo ${PWD##*/}))/$(git branch --show-current 2>/dev/null || echo main)" --agent-id "$AGENT_ID" --agent-type %s --description %q --auto-recall --auto-recall-strategy fast $PARENT_FLAG --quiet %s || true`,
				bootstrap, staleCleanup, loomCmd, hp.AgentType, hp.Description, log),
		},
		{
			"type": "command",
			"command": fmt.Sprintf(
				// Let keepalive own its PID file lifecycle so repeated SessionStart hooks
				// (for example after compact/relaunch) do not race old/new helpers.
				`INPUT=$(cat); %s; %s agent keepalive --agent-id "$AGENT_ID" --agent-type %s --quiet </dev/null >/dev/null %s &`,
				bootstrap, loomCmd, hp.AgentType, log),
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
							`INPUT=$(cat); %s; PID_FILE="${TMPDIR:-/tmp}/loom-keepalive-${AGENT_ID}.pid"; [ -f "$PID_FILE" ] && kill "$(cat "$PID_FILE")" 2>/dev/null; rm -f "$PID_FILE"; rm -f "$AGENT_ID_FILE"; %s agent session-end --agent-id "$AGENT_ID" --summarize --summary-async --quiet %s || true`,
							bootstrap, loomCmd, log),
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
							`INPUT=$(cat); %s; %s agent heartbeat --agent-id "$AGENT_ID" --status active --ensure-session --infer-namespace --agent-type %s --description %q --quiet %s || true`,
							bootstrap, loomCmd, hp.AgentType, hp.Description, log),
					},
				},
			},
		},
		// Capture parent session ID for subagent session grouping.
		// Write to a file so the subagent's SessionStart can read it
		// (env vars don't propagate across hook subprocess boundaries).
		"SubagentStart": []map[string]any{
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
		},
	}

	return hooks
}

// appendHookPolicies dispatches shared policy refs to their hook implementations.
// For native enforcement platforms (preToolUse support), it generates PreToolUse
// guard hooks. For proxy/plugin enforcement, policies are enforced at the loom
// proxy layer, so no PreToolUse hooks are needed.
func appendHookPolicies(hooks map[string]any, reg *registry.Registry, hp HookProfile) {
	for _, ref := range hp.PolicyRefs {
		switch ref {
		case "gitops_flux":
			if hp.Enforcement == "native" {
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
func appendHookExtras(hooks map[string]any, extras []string, loomBinary string) {
	for _, extra := range extras {
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
		}
	}
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
	appendHookExtras(hooks, profile.Hooks.Extras, loomBinary)
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
		DirtyWorktreeNudgeMessage:        "Dirty worktree detected. Treat pre-existing changes as baseline context, continue work, and stage/commit only files for the active task. Escalate only if new unexpected changes appear in files you are editing.",
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
	payload := `{"systemMessage":"You are on main. For feature work or multi-file changes, consider using agent_worktree_allocate() to create an isolated branch and worktree before making changes."}`
	return fmt.Sprintf(`if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then BRANCH="$(git branch --show-current 2>/dev/null)"; if [ "$BRANCH" = "main" ] || [ "$BRANCH" = "master" ]; then printf '%%s\n' %q; fi; fi; exit 0`, payload)
}
