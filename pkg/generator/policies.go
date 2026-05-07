package generator

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/crb2nu/loom/pkg/registry"
)

// Policy describes a named guardrail (e.g. "gitops_flux", "secrets_scan").
// Policies declare blocked command patterns that get translated into native
// PreToolUse hooks for platforms with native enforcement, and into proxy-
// layer deny rules for everyone else. EPIC 3 / CONFIG-3 (.loom/108).
//
// Sources of truth, in priority order:
//  1. registry.platform_permissions.agents.guardrails.<name> — operator
//     override. Matches the legacy schema, so existing
//     mcp/context/registry.yaml content keeps working unchanged.
//  2. pkg/generator/templates/policies/<name>.yaml — embedded fallback.
//     Lets new policies (e.g. secrets_scan) ship without a registry edit
//     and keeps loom-core self-contained for users who never customized
//     the registry.
type Policy struct {
	Name            string   `yaml:"name"`
	Description     string   `yaml:"description"`
	BlockedCommands []string `yaml:"blocked_commands"`
	Message         string   `yaml:"message"`
}

var (
	embeddedPolicyCache   = map[string]*Policy{}
	embeddedPolicyCacheMu sync.Mutex
)

// LoadPolicy returns the named policy. Resolution rules:
//
//  1. If the registry has an `agents` PlatformPermission entry, that's
//     the authoritative source. Returns the policy data from
//     `agents.guardrails.<name>` if present, or nil if absent. Embedded
//     templates are NOT consulted — operators who declare an `agents`
//     section have explicitly expressed their full policy state, and
//     ignoring an absent entry there preserves backwards compatibility
//     with pre-CONFIG-3 tests and registries.
//  2. If the registry has no `agents` entry at all (or the registry is
//     nil), fall back to the embedded
//     pkg/generator/templates/policies/<name>.yaml file. This is the
//     path that lets new policies (e.g. secrets_scan) ship without a
//     registry edit, and keeps loom-core self-contained for users who
//     never customized the registry.
//
// Returns (nil, nil) if neither source defines the policy. Callers
// should treat that as "skip this policy" rather than an error.
func LoadPolicy(reg *registry.Registry, name string) (*Policy, error) {
	if name == "" {
		return nil, nil
	}
	if registryHasAgentsEntry(reg) {
		return loadPolicyFromRegistry(reg, name), nil
	}
	return loadPolicyFromEmbedded(name)
}

// registryHasAgentsEntry returns true when the registry has a non-nil
// platform_permissions["agents"] entry. Used to decide whether the
// caller has expressed an opinion about agent policies — if yes, that
// opinion is authoritative even when a specific policy is missing from
// the agents.guardrails map.
func registryHasAgentsEntry(reg *registry.Registry) bool {
	if reg == nil || reg.PlatformPermissions == nil {
		return false
	}
	pp, ok := reg.PlatformPermissions["agents"]
	return ok && pp != nil
}

// loadPolicyFromRegistry parses the legacy
// platform_permissions.agents.guardrails.<name> block. Returns nil when
// missing or empty.
func loadPolicyFromRegistry(reg *registry.Registry, name string) *Policy {
	pp := registryPlatformPerms(reg, "agents")
	if pp == nil || pp.Settings == nil {
		return nil
	}
	guardrails, ok := pp.Settings["guardrails"].(map[string]any)
	if !ok || len(guardrails) == 0 {
		return nil
	}
	raw, ok := guardrails[name].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}

	policy := &Policy{Name: name}
	if cmds := coerceStringSlice(raw["blocked_commands"]); len(cmds) > 0 {
		policy.BlockedCommands = cmds
	} else if cmds := coerceStringSlice(raw["deny"]); len(cmds) > 0 {
		// Legacy alias used by some registry entries.
		policy.BlockedCommands = cmds
	}
	if msg, ok := raw["message"].(string); ok && strings.TrimSpace(msg) != "" {
		policy.Message = strings.TrimSpace(msg)
	}
	if desc, ok := raw["description"].(string); ok {
		policy.Description = strings.TrimSpace(desc)
	}
	if len(policy.BlockedCommands) == 0 && policy.Message == "" {
		// Treat an entry with neither commands nor a message as absent —
		// matches the legacy gitopsFluxGuardrailPolicyFromRegistry behavior.
		return nil
	}
	return policy
}

// loadPolicyFromEmbedded reads pkg/generator/templates/policies/<name>.yaml
// from the embedded FS. Returns nil on os.IsNotExist; any other read or
// parse error surfaces.
func loadPolicyFromEmbedded(name string) (*Policy, error) {
	embeddedPolicyCacheMu.Lock()
	defer embeddedPolicyCacheMu.Unlock()

	if cached, ok := embeddedPolicyCache[name]; ok {
		return cached, nil
	}

	path := filepath.Join("templates", "policies", name+".yaml")
	data, err := templatesFS.ReadFile(path)
	if err != nil {
		// File missing → no embedded policy. Treat as nil so the caller
		// can decide whether absence is an error.
		if isFSNotExist(err) {
			embeddedPolicyCache[name] = nil
			return nil, nil
		}
		return nil, fmt.Errorf("read embedded policy %s: %w", path, err)
	}

	var p Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse embedded policy %s: %w", path, err)
	}
	if p.Name == "" {
		p.Name = name
	}
	embeddedPolicyCache[name] = &p
	return &p, nil
}

// isFSNotExist tolerates the embed.FS-specific error string. fs.ErrNotExist
// is the canonical error but embed wrapping doesn't always preserve it
// across go versions.
func isFSNotExist(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "file does not exist") ||
		strings.Contains(err.Error(), "no such file or directory")
}

// policyDenyRules translates a Policy's BlockedCommands into Claude-style
// permission deny rules: each command becomes Bash(<cmd> *). Returns an
// empty slice when the policy has no blocked commands.
func policyDenyRules(policy *Policy) []string {
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

// policyRegex translates a Policy's BlockedCommands into a single
// alternation regex suitable for grep -qE inside a PreToolUse hook.
func policyRegex(policy *Policy) string {
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
		// Allow regex-quoted spaces back to plain spaces so multi-word
		// commands like "kubectl rollout restart" match natural input.
		quoted = strings.ReplaceAll(quoted, `\ `, " ")
		parts = append(parts, quoted)
	}
	if len(parts) == 0 {
		return ""
	}
	return `^[[:space:]]*(` + strings.Join(parts, "|") + `)([[:space:]]|$)`
}

// policyGuardrailHooks builds the PreToolUse hook block for a policy.
// Used by appendHookPolicies for native-enforcement platforms whose
// Events list includes preToolUse. Returns nil for policies with no
// blocked commands so callers can skip cleanly.
//
// Policy-agnostic ancillary hooks (e.g. the git-commit quality reminder)
// live in gitCommitQualityReminderHook so they're emitted once per
// platform rather than once per policy ref.
func policyGuardrailHooks(policy *Policy) []map[string]any {
	if policy == nil {
		return nil
	}
	pattern := policyRegex(policy)
	if pattern == "" {
		return nil
	}
	message := policy.Message
	if message == "" {
		message = fmt.Sprintf("Policy %q blocked the requested command.", policy.Name)
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
	}
}

// gitCommitQualityReminderHook returns the policy-agnostic Pre-commit
// quality reminder hook. Emitted once per platform (regardless of how
// many policy refs are declared) so the reminder doesn't multiply when
// a platform adopts more than one policy.
func gitCommitQualityReminderHook() map[string]any {
	return map[string]any{
		"matcher": "Bash",
		"hooks": []map[string]any{
			{
				"type":    "command",
				"command": `INPUT=$(cat); CMD=$(echo "$INPUT" | jq -r '.tool_input.command // ""'); if echo "$CMD" | grep -qE '^[[:space:]]*git[[:space:]]+commit([[:space:]]|$)'; then echo '{"systemMessage":"Pre-commit quality reminder: consider running quality_check (or quality_lint / quality_test) before committing to catch issues early."}'; fi; exit 0`,
			},
		},
	}
}
