package generator

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/validator"
)

// claudeHooksConfig returns a Claude Code settings.json with lifecycle hooks,
// policy-driven guardrails, and default-allow permissions. Permissions are read
// from the registry's platform_permissions.claude section; hook shape remains in
// Go while the guarded command data comes from shared registry policy.
func claudeHooksConfig(reg *registry.Registry, profile *PlatformProfile, loomBinary string) map[string]any {
	return map[string]any{
		"$schema":     "https://json.schemastore.org/claude-code-settings.json",
		"permissions": claudePermissions(reg),
		"hooks":       claudeHooks(reg, profile, loomBinary),
	}
}

// claudeHooks returns the hooks block for Claude Code settings.json.
func claudeHooks(reg *registry.Registry, profile *PlatformProfile, loomBinary string) map[string]any {
	hooks := buildPlatformHooks(reg, profile.Hooks, loomBinary)

	// Append shared policy hooks before the remaining profile-specific extras.
	appendHookPolicies(hooks, reg, profile.Hooks)

	// Append extras defined in the profile (e.g. postToolUse_formatters, postToolUse_taskSync).
	appendHookExtras(hooks, profile.Hooks.Extras, loomBinary)

	return hooks
}

// claudePostToolUseExtras returns the Write/Edit formatter hooks specific to
// Claude Code, appended to the shared heartbeat PostToolUse hooks.
func claudePostToolUseExtras() []map[string]any {
	return []map[string]any{
		{
			"matcher": "Write|Edit",
			"hooks": []map[string]any{
				{
					"type":    "command",
					"command": `jq -r '.tool_input.file_path // ""' | { read f; [[ "$f" == *.py ]] && black "$f" 2>/dev/null; exit 0; }`,
				},
				{
					"type":    "command",
					"command": `jq -r '.tool_input.file_path // ""' | { read f; [[ "$f" == *.go ]] && gofmt -w "$f" 2>/dev/null && goimports -w "$f" 2>/dev/null; exit 0; }`,
				},
				{
					"type":    "command",
					"command": `jq -r '.tool_input.new_string // .tool_input.content // ""' | { read content; if echo "$content" | grep -qE 'image:.*:latest'; then echo '{"systemMessage":"Noticed :latest tag - consider pinning to a specific version for reproducibility."}'; fi; exit 0; }`,
				},
			},
		},
	}
}

// claudePostToolUseTaskSyncHook returns the PostToolUse hook that syncs native
// Claude Code task tools (TaskCreate, TaskUpdate, TodoWrite) to the loom
// agent-context task system via `loom agent task-sync`.
func claudePostToolUseTaskSyncHook(loomBinary string) []map[string]any {
	loomCmd := shellQuote(normalizeLoomBinary(loomBinary))
	return []map[string]any{
		{
			"matcher": "TaskCreate|TaskUpdate|TodoWrite",
			"hooks": []map[string]any{
				{
					"type": "command",
					"command": fmt.Sprintf(
						`INPUT=$(cat); %s; echo "$INPUT" | %s agent task-sync --agent-id "$AGENT_ID" --quiet 2>>"${TMPDIR:-/tmp}/loom-agent-hooks.log" || true`,
						hookAgentIDBootstrap("claude-code"), loomCmd),
				},
			},
		},
	}
}

type gitopsFluxGuardrailPolicy struct {
	BlockedCommands []string
	Message         string
}

func gitopsFluxGuardrailPolicyFromRegistry(reg *registry.Registry) *gitopsFluxGuardrailPolicy {
	pp := registryPlatformPerms(reg, "agents")
	if pp == nil || pp.Settings == nil {
		return nil
	}

	guardrails, ok := pp.Settings["guardrails"].(map[string]any)
	if !ok || len(guardrails) == 0 {
		return nil
	}
	raw, ok := guardrails["gitops_flux"].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}

	policy := &gitopsFluxGuardrailPolicy{}
	if cmds := coerceStringSlice(raw["blocked_commands"]); len(cmds) > 0 {
		policy.BlockedCommands = cmds
	} else if cmds := coerceStringSlice(raw["deny"]); len(cmds) > 0 {
		policy.BlockedCommands = cmds
	}
	if msg, ok := raw["message"].(string); ok && strings.TrimSpace(msg) != "" {
		policy.Message = strings.TrimSpace(msg)
	}
	return policy
}

func gitopsFluxGuardrailDenyRules(policy *gitopsFluxGuardrailPolicy) []string {
	if policy == nil {
		return nil
	}
	rules := make([]string, 0, len(policy.BlockedCommands))
	for _, cmd := range policy.BlockedCommands {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		rules = append(rules, fmt.Sprintf("Bash(%s *)", cmd))
	}
	return rules
}

func gitopsFluxGuardrailRegex(policy *gitopsFluxGuardrailPolicy) string {
	if policy == nil || len(policy.BlockedCommands) == 0 {
		return ""
	}

	parts := make([]string, 0, len(policy.BlockedCommands))
	for _, cmd := range policy.BlockedCommands {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		quoted := regexp.QuoteMeta(cmd)
		quoted = strings.ReplaceAll(quoted, `\ `, " ")
		parts = append(parts, quoted)
	}
	if len(parts) == 0 {
		return ""
	}
	return `^[[:space:]]*(` + strings.Join(parts, "|") + `)([[:space:]]|$)`
}

// gitopsFluxGuardrailHooks returns the PreToolUse hooks backed by shared
// GitOps/Flux policy data from platform_permissions.agents. Used by any
// platform with native enforcement (preToolUse support).
func gitopsFluxGuardrailHooks(reg *registry.Registry) []map[string]any {
	policy := gitopsFluxGuardrailPolicyFromRegistry(reg)
	if policy == nil {
		return nil
	}

	message := policy.Message
	if message == "" {
		message = "GitOps policy: kubectl edit/set env bypasses git history. Edit manifests and use flux reconcile."
	}
	pattern := gitopsFluxGuardrailRegex(policy)
	if pattern == "" {
		return nil
	}

	return []map[string]any{
		{
			"matcher": "Bash",
			"hooks": []map[string]any{
				{
					"type": "command",
					"command": fmt.Sprintf(
						`INPUT=$(cat); CMD=$(echo "$INPUT" | jq -r '.tool_input.command // ""'); if echo "$CMD" | grep -qE %q; then printf '%%s\n' %q >&2; exit 2; fi; exit 0`,
						pattern, message,
					),
				},
			},
		},
		{
			"matcher": "Bash",
			"hooks": []map[string]any{
				{
					"type":    "command",
					"command": `INPUT=$(cat); CMD=$(echo "$INPUT" | jq -r '.tool_input.command // ""'); if echo "$CMD" | grep -qE '^[[:space:]]*git[[:space:]]+commit([[:space:]]|$)'; then echo '{"systemMessage":"Pre-commit quality reminder: consider running quality_check (or quality_lint / quality_test) before committing to catch issues early."}'; fi; exit 0`,
				},
			},
		},
	}
}

// claudePermissions builds the permissions block for Claude Code settings.json.
// It reads from the registry's platform_permissions.claude section so the allow/deny
// lists are maintained in YAML rather than Go code. Falls back to a minimal default
// if the registry has no claude entry.
func claudePermissions(reg *registry.Registry) map[string]any {
	perms := map[string]any{}

	pp := registryPlatformPerms(reg, "claude")
	sharedPolicy := gitopsFluxGuardrailPolicyFromRegistry(reg)
	if pp == nil {
		// Minimal fallback: allow loom proxy tools only.
		fallback := map[string]any{
			"allow": []string{"mcp__loom"},
		}
		if deny := gitopsFluxGuardrailDenyRules(sharedPolicy); len(deny) > 0 {
			fallback["deny"] = deny
		}
		return fallback
	}

	if len(pp.AdditionalDirectories) > 0 {
		perms["additionalDirectories"] = pp.AdditionalDirectories
	}
	if pp.Settings != nil {
		if mode, ok := pp.Settings["default_mode"].(string); ok && mode != "" {
			perms["defaultMode"] = mode
		}
		// Optional keys stored under platform_permissions.claude.settings until the
		// registry schema grows first-class fields.
		if ask := coerceStringSlice(pp.Settings["ask"]); len(ask) > 0 {
			perms["ask"] = ask
		}
		if v, ok := pp.Settings["disable_bypass_permissions_mode"].(string); ok && v != "" {
			perms["disableBypassPermissionsMode"] = v
		}
	}

	// Claude Code rejects settings.json when permission rules don't match its
	// upstream schema regex. Filter invalid entries so we always emit a schema-valid
	// settings.json, and warn so the registry can be corrected.
	if len(pp.Allow) > 0 {
		allow, dropped := filterClaudePermissionRules(pp.Allow)
		if len(dropped) > 0 {
			fmt.Fprintf(os.Stderr, "WARN  [claude] dropping %d invalid permissions.allow entries: %s\n", len(dropped), strings.Join(dropped, ", "))
		}
		if len(allow) > 0 {
			perms["allow"] = allow
		}
	}
	if len(pp.Deny) > 0 {
		deny, dropped := filterClaudePermissionRules(pp.Deny)
		if len(dropped) > 0 {
			fmt.Fprintf(os.Stderr, "WARN  [claude] dropping %d invalid permissions.deny entries: %s\n", len(dropped), strings.Join(dropped, ", "))
		}
		if len(deny) > 0 {
			perms["deny"] = deny
		}
	}
	if deny := gitopsFluxGuardrailDenyRules(sharedPolicy); len(deny) > 0 {
		if existing, ok := perms["deny"].([]string); ok {
			perms["deny"] = append(existing, deny...)
		} else {
			perms["deny"] = deny
		}
	}
	if askAny, ok := perms["ask"].([]string); ok && len(askAny) > 0 {
		ask, dropped := filterClaudePermissionRules(askAny)
		if len(dropped) > 0 {
			fmt.Fprintf(os.Stderr, "WARN  [claude] dropping %d invalid permissions.ask entries: %s\n", len(dropped), strings.Join(dropped, ", "))
		}
		if len(ask) > 0 {
			perms["ask"] = ask
		} else {
			delete(perms, "ask")
		}
	}
	return perms
}

var (
	claudePermRuleOnce sync.Once
	claudePermRuleRE   *regexp.Regexp
)

func claudePermissionRuleRegexp() *regexp.Regexp {
	claudePermRuleOnce.Do(func() {
		// Default to a conservative RE2-compatible regex. Claude's upstream schema
		// uses lookaheads that Go's regexp doesn't support, so we cannot compile it
		// verbatim.
		pattern := `^((AskUserQuestion|Bash|Edit|EnterPlanMode|EnterWorktree|ExitPlanMode|Glob|Grep|KillShell|LS|LSP|MultiEdit|NotebookEdit|NotebookRead|Read|Skill|Task|TaskCreate|TaskGet|TaskList|TaskOutput|TaskStop|TaskUpdate|TodoWrite|ToolSearch|WebFetch|WebSearch|Write)(\([^)]*\))?|mcp__.*)$`

		if schemaBytes, ok := validator.GetEmbeddedSchema("claude_settings.json"); ok && len(schemaBytes) > 0 {
			var raw map[string]any
			if err := json.Unmarshal(schemaBytes, &raw); err == nil {
				if defs, ok := raw["$defs"].(map[string]any); ok {
					if pr, ok := defs["permissionRule"].(map[string]any); ok {
						if p, ok := pr["pattern"].(string); ok && p != "" {
							// Skip patterns with unsupported tokens (lookaheads/lookbehinds).
							if !strings.Contains(p, "(?") {
								pattern = p
							}
						}
					}
				}
			}
		}

		re, err := regexp.Compile(pattern)
		if err != nil {
			// Fall back to a minimal safe regex rather than failing generation.
			re = regexp.MustCompile(`^(mcp__.*|Bash(\([^)]*\))?|Read(\([^)]*\))?|Write(\([^)]*\))?|Edit(\([^)]*\))?|MultiEdit(\([^)]*\))?|Task(\([^)]*\))?|Glob(\([^)]*\))?|Grep(\([^)]*\))?|ToolSearch(\([^)]*\))?|LS(\([^)]*\))?|WebFetch(\([^)]*\))?|WebSearch(\([^)]*\))?)$`)
		}
		claudePermRuleRE = re
	})
	return claudePermRuleRE
}

func filterClaudePermissionRules(rules []string) (kept []string, dropped []string) {
	re := claudePermissionRuleRegexp()
	for _, r := range rules {
		if r == "" {
			continue
		}
		if re.MatchString(r) {
			kept = append(kept, r)
		} else {
			dropped = append(dropped, r)
		}
	}
	return kept, dropped
}
