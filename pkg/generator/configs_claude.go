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
	appendHookExtras(hooks, profile.Hooks, loomBinary)

	return hooks
}

// claudePostToolUseExtras and claudePostToolUseTaskSyncHook were removed
// in EPIC 3 / CONFIG-2 (.loom/108). Their content moved to embedded
// templates under pkg/generator/templates/extras/, dispatched via
// extraDescriptors in pkg/generator/extras.go. Adding a new
// "post-tool-use" extra now requires only a template file + descriptor
// entry.

// Policy guardrail helpers were generalized in EPIC 3 / CONFIG-3 (.loom/108).
// The previous gitopsFluxGuardrail* functions were name-coupled to a single
// policy. They now live in pkg/generator/policies.go as Policy + LoadPolicy
// + policyDenyRules + policyRegex + policyGuardrailHooks +
// gitCommitQualityReminderHook, and dispatch is name-agnostic so adding a
// new policy ref requires only a YAML file under
// pkg/generator/templates/policies/.

// claudePermissions builds the permissions block for Claude Code settings.json.
// It reads from the registry's platform_permissions.claude section so the allow/deny
// lists are maintained in YAML rather than Go code. Falls back to a minimal default
// if the registry has no claude entry.
func claudePermissions(reg *registry.Registry) map[string]any {
	perms := map[string]any{}

	pp := registryPlatformPerms(reg, "claude")
	// EPIC 3 / CONFIG-3: deny rules are derived from the gitops_flux policy
	// (registry-first, embedded fallback). Other policy refs declared on the
	// claude profile contribute their own deny rules below.
	sharedPolicy, _ := LoadPolicy(reg, "gitops_flux")
	if pp == nil {
		// Minimal fallback: allow loom proxy tools only.
		fallback := map[string]any{
			"allow": []string{"mcp__loom"},
		}
		if deny := policyDenyRules(sharedPolicy); len(deny) > 0 {
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
	if deny := policyDenyRules(sharedPolicy); len(deny) > 0 {
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
		pattern := `^((AskUserQuestion|Bash|Edit|EnterPlanMode|EnterWorktree|ExitPlanMode|Glob|Grep|KillShell|LS|LSP|Monitor|MultiEdit|NotebookEdit|NotebookRead|Read|Skill|Task|TaskCreate|TaskGet|TaskList|TaskOutput|TaskStop|TaskUpdate|TodoWrite|ToolSearch|WebFetch|WebSearch|Write)(\([^)]*\))?|mcp__.*)$`

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
