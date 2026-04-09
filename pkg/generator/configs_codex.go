package generator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
)

// emitCodexPreamble writes Codex-specific top-level TOML settings.
// Settings are read from the registry's platform_permissions.codex section.
func emitCodexPreamble(sb *strings.Builder, reg *registry.Registry, workspaceRoot string, loomBinary string) {
	pp := registryPlatformPerms(reg, "codex")
	policy := agentSafetyPolicyFromRegistry(reg)
	loomCmd := shellQuote(normalizeLoomBinary(loomBinary))

	// Defaults when registry has no codex entry.
	suppressWarning := true
	sandboxMode := "workspace-write"
	webSearchMode := "live"
	features := map[string]any{
		"apply_patch_freeform": true,
		"unified_exec":         true,
		"collaboration_modes":  true,
	}

	// approval_policy supports two forms:
	//   string: approval_policy = "never"
	//   granular: approval_policy = { granular = { mcp_elicitations = false, ... } }
	// The granular form is preferred because "never" doesn't reliably suppress
	// MCP tool prompts in Codex >=4.x.
	var approvalPolicyStr string
	var approvalPolicyGranular map[string]any

	// Override from registry settings if present.
	if pp != nil && pp.Settings != nil {
		switch v := pp.Settings["approval_policy"].(type) {
		case string:
			approvalPolicyStr = v
		case map[string]any:
			if granular, ok := v["granular"].(map[string]any); ok {
				approvalPolicyGranular = granular
			}
		}
		if v, ok := pp.Settings["suppress_unstable_features_warning"].(bool); ok {
			suppressWarning = v
		}
		if v, ok := pp.Settings["sandbox_mode"].(string); ok {
			sandboxMode = v
		}
		if v, ok := pp.Settings["web_search"].(string); ok && v != "" {
			webSearchMode = v
		}
		if v, ok := pp.Settings["features"].(map[string]any); ok {
			features = v
		}
	}

	// Emit approval_policy in the appropriate form.
	if len(approvalPolicyGranular) > 0 {
		var parts []string
		for _, key := range []string{"sandbox_approval", "rules", "mcp_elicitations", "request_permissions", "skill_approval"} {
			if v, ok := approvalPolicyGranular[key]; ok {
				switch val := v.(type) {
				case bool:
					parts = append(parts, fmt.Sprintf("%s = %t", key, val))
				}
			}
		}
		sort.Strings(parts)
		fmt.Fprintf(sb, "approval_policy = { granular = { %s } }\n\n", strings.Join(parts, ", "))
	} else {
		if approvalPolicyStr == "" {
			approvalPolicyStr = "never"
		}
		fmt.Fprintf(sb, "approval_policy = %q\n\n", approvalPolicyStr)
	}

	if suppressWarning {
		sb.WriteString("suppress_unstable_features_warning = true\n")
	}

	// Emit features as inline TOML table.
	var featureParts []string
	for k, v := range features {
		switch val := v.(type) {
		case bool:
			featureParts = append(featureParts, fmt.Sprintf("%s = %t", k, val))
		case string:
			featureParts = append(featureParts, fmt.Sprintf("%s = %q", k, val))
		}
	}
	sort.Strings(featureParts)
	fmt.Fprintf(sb, "features = { %s }\n\n", strings.Join(featureParts, ", "))

	fmt.Fprintf(sb, "sandbox_mode = %q\n", sandboxMode)
	fmt.Fprintf(sb, "sandbox_workspace_write = { network_access = true, writable_roots = [%q] }\n\n", workspaceRoot)

	// Enable Codex builtin web search tool (controls internet access for web.run/web_search).
	// Values: disabled, cached, live.
	if webSearchMode != "" {
		fmt.Fprintf(sb, "web_search = %q\n\n", webSearchMode)
	}

	// Emit optional model overrides.
	if pp != nil && pp.Settings != nil {
		if v, ok := pp.Settings["model"].(string); ok && v != "" {
			fmt.Fprintf(sb, "model = %q\n", v)
		}
		if v, ok := pp.Settings["model_reasoning_effort"].(string); ok && v != "" {
			fmt.Fprintf(sb, "model_reasoning_effort = %q\n", v)
		}
		sb.WriteString("\n")
	}

	// Emit policy enforcement annotations from platform profile.
	codexProfile, _ := GetPlatformProfile("codex")
	if codexProfile != nil {
		if comment := FormatPolicyComment(codexProfile.Hooks, "# "); comment != "" {
			sb.WriteString(comment)
			sb.WriteString("\n")
		}
	}

	sb.WriteString("# Git safety policy: treat pre-existing dirty worktrees as baseline context.\n")
	if policy.DirtyWorktreeMode == "continue_scoped_commits" {
		sb.WriteString("# Continue on current branch/worktree; stage+commit only files changed for the active task.\n")
		sb.WriteString("# Escalate only when new unexpected changes appear in files you are editing.\n\n")
	} else {
		fmt.Fprintf(sb, "# Dirty-worktree mode: %s\n\n", policy.DirtyWorktreeMode)
	}

	// Codex notify runs on every turn, so use a workspace-scoped persistent
	// AGENT_ID_FILE to avoid per-hook process-ID churn that fragments identity.
	// The workspace hash from cksum matches the scheme used by hookAgentIDBootstrap
	// for Claude/Gemini, avoiding cross-workspace agent ID collisions.
	// Emit notify before any [agents.*] tables so TOML keeps it at top level.
	sb.WriteString("# Agent lifecycle: heartbeat on turn completion (rate-limited to avoid notify storms)\n")
	fmt.Fprintf(sb, `notify = ["sh", "-c", %q, "--"]`, codexNotifyCommand(loomCmd))
	sb.WriteString("\n\n")

	// Emit [agents] section for multi-agent support if configured in registry.
	emitCodexAgents(sb, pp)
}

func codexNotifyCommand(loomCmd string) string {
	return fmt.Sprintf(`WS_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || printf '%%s' "$PWD")"; WS_HASH="$(printf '%%s' "$WS_ROOT" | cksum | cut -d' ' -f1)"; CACHE_DIR="${HOME}/.cache/loom"; AGENT_ID_FILE="${CACHE_DIR}/agent-id-codex-${WS_HASH}"; HEARTBEAT_STAMP_FILE="${CACHE_DIR}/notify-heartbeat-codex-${WS_HASH}.stamp"; mkdir -p "$CACHE_DIR"; if [ -s "$AGENT_ID_FILE" ]; then AGENT_ID="$(cat "$AGENT_ID_FILE")"; else AGENT_ID="codex-${WS_HASH}"; printf '%%s' "$AGENT_ID" > "$AGENT_ID_FILE"; fi; NOW="$(date +%%s)"; LAST="$(cat "$HEARTBEAT_STAMP_FILE" 2>/dev/null || true)"; case "$LAST" in ''|*[!0-9]*) ;; *) if [ $((NOW - LAST)) -lt 15 ]; then exit 0; fi ;; esac; printf '%%s' "$NOW" > "$HEARTBEAT_STAMP_FILE"; exec %s agent heartbeat --agent-id "$AGENT_ID" --status active --ensure-session --infer-namespace --agent-type codex --description "Codex notify session" --quiet 2>>"${TMPDIR:-/tmp}/loom-agent-hooks.log" || true`, loomCmd)
}

// emitCodexAgents writes the [agents] TOML section for Codex multi-agent support.
func emitCodexAgents(sb *strings.Builder, pp *registry.PlatformPermission) {
	if pp == nil || pp.Settings == nil {
		return
	}
	agents, ok := pp.Settings["agents"].(map[string]any)
	if !ok || len(agents) == 0 {
		return
	}

	sb.WriteString("[agents]\n")

	// Emit top-level agent settings.
	for _, key := range []string{"max_threads", "max_depth", "job_max_runtime_seconds"} {
		if v, exists := agents[key]; exists {
			switch val := v.(type) {
			case int:
				fmt.Fprintf(sb, "%s = %d\n", key, val)
			case float64:
				fmt.Fprintf(sb, "%s = %d\n", key, int(val))
			}
		}
	}
	sb.WriteString("\n")

	// Emit named agent definitions as [agents.<name>] sections.
	if defs, ok := agents["definitions"].(map[string]any); ok {
		// Sort keys for deterministic output.
		names := make([]string, 0, len(defs))
		for name := range defs {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			def, ok := defs[name].(map[string]any)
			if !ok {
				continue
			}
			fmt.Fprintf(sb, "[agents.%s]\n", name)
			if desc, ok := def["description"].(string); ok {
				fmt.Fprintf(sb, "description = %q\n", desc)
			}
			sb.WriteString("\n")
		}
	}
}
