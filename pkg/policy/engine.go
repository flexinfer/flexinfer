// Package policy provides a data-driven policy engine for enforcing guardrails
// on MCP tool calls. Rules are loaded from the registry's
// platform_permissions.agents.settings.guardrails section so new policies can
// be added by editing YAML without recompiling.
package policy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
)

// PolicyRule describes a single guardrail: a compiled regex pattern and the
// denial message returned when a tool call matches.
type PolicyRule struct {
	Name    string
	Pattern *regexp.Regexp
	Message string
}

// Engine holds an ordered set of PolicyRules and evaluates tool call arguments
// against them.
type Engine struct {
	rules []PolicyRule
}

// Rules returns a copy of the engine's policy rules (useful for testing).
func (e *Engine) Rules() []PolicyRule {
	if e == nil {
		return nil
	}
	out := make([]PolicyRule, len(e.rules))
	copy(out, e.rules)
	return out
}

// DefaultEngine returns an Engine with the hard-coded default policy that was
// historically baked into the proxy. This is the fallback when no registry is
// available.
func DefaultEngine() *Engine {
	return &Engine{
		rules: []PolicyRule{
			{
				Name:    "gitops_flux",
				Pattern: regexp.MustCompile(`(?i)\bkubectl\b(?:\s+\S+)*\s+(?:edit\b|set\s+env\b)`),
				Message: "GitOps policy: kubectl edit and kubectl set env are blocked in loom proxy. Edit the manifest in Git, commit and push the change, then run flux reconcile.",
			},
		},
	}
}

// NewEngineFromRegistry reads every guardrail entry under
// platform_permissions.agents.settings.guardrails, compiles the blocked
// commands into a single regex per guardrail, and returns an Engine.
// Falls back to DefaultEngine when the registry has no guardrails data.
func NewEngineFromRegistry(reg *registry.Registry) *Engine {
	if reg == nil || reg.PlatformPermissions == nil {
		return DefaultEngine()
	}

	pp := reg.PlatformPermissions["agents"]
	if pp == nil || pp.Settings == nil {
		return DefaultEngine()
	}

	guardrails, ok := pp.Settings["guardrails"].(map[string]any)
	if !ok || len(guardrails) == 0 {
		return DefaultEngine()
	}

	var rules []PolicyRule
	for name, raw := range guardrails {
		entry, ok := raw.(map[string]any)
		if !ok || len(entry) == 0 {
			continue
		}

		cmds := coerceStringSlice(entry["blocked_commands"])
		if len(cmds) == 0 {
			cmds = coerceStringSlice(entry["deny"])
		}
		if len(cmds) == 0 {
			continue
		}

		pattern := buildGuardrailRegex(cmds)
		if pattern == nil {
			continue
		}

		message := ""
		if msg, ok := entry["message"].(string); ok {
			message = strings.TrimSpace(msg)
		}
		if message == "" {
			message = fmt.Sprintf("Policy %q: blocked command detected. Check the registry guardrails for details.", name)
		}

		rules = append(rules, PolicyRule{
			Name:    name,
			Pattern: pattern,
			Message: message,
		})
	}

	if len(rules) == 0 {
		return DefaultEngine()
	}
	return &Engine{rules: rules}
}

// Check inspects a tool call (tool name and JSON arguments) against all rules.
// Returns the denial message and true if the call should be blocked, or ("", false)
// if it is allowed.
func (e *Engine) Check(toolName string, args json.RawMessage) (string, bool) {
	if e == nil || len(e.rules) == 0 {
		return "", false
	}

	for _, rule := range e.rules {
		if containsMatch(rule.Pattern, toolName) {
			return rule.Message, true
		}
		if matchesJSON(rule.Pattern, args) {
			return rule.Message, true
		}
	}
	return "", false
}

// matchesJSON unmarshals raw JSON and recursively inspects all string values
// for a pattern match.
func matchesJSON(pattern *regexp.Regexp, raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return containsMatch(pattern, string(raw))
	}
	return containsMatchAny(pattern, decoded)
}

// containsMatchAny recursively walks an arbitrary JSON-decoded value and
// checks all string leaves against the pattern.
func containsMatchAny(pattern *regexp.Regexp, v any) bool {
	switch typed := v.(type) {
	case string:
		return containsMatch(pattern, typed)
	case []any:
		// First, try joining string elements (handles ["kubectl", "-n", "apps", "edit", ...]).
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			switch child := item.(type) {
			case string:
				parts = append(parts, child)
			default:
				if containsMatchAny(pattern, child) {
					return true
				}
			}
		}
		if pattern.MatchString(strings.Join(parts, " ")) {
			return true
		}
		// Also check each element individually.
		for _, item := range typed {
			if containsMatchAny(pattern, item) {
				return true
			}
		}
		return false
	case map[string]any:
		for _, item := range typed {
			if containsMatchAny(pattern, item) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// containsMatch normalizes whitespace before matching.
func containsMatch(pattern *regexp.Regexp, s string) bool {
	return pattern.MatchString(strings.Join(strings.Fields(s), " "))
}

// buildGuardrailRegex compiles a list of blocked commands into a single regex.
// Each command like "kubectl edit" becomes a pattern that matches the command
// with optional arguments before and after.
func buildGuardrailRegex(commands []string) *regexp.Regexp {
	if len(commands) == 0 {
		return nil
	}

	parts := make([]string, 0, len(commands))
	for _, cmd := range commands {
		words := strings.Fields(cmd)
		if len(words) == 0 {
			continue
		}

		// Build a pattern that matches the command words with possible
		// intervening flags/arguments. The first word is anchored with \b,
		// and subsequent words allow optional intervening tokens.
		var sb strings.Builder
		sb.WriteString(`\b`)
		sb.WriteString(regexp.QuoteMeta(words[0]))
		sb.WriteString(`\b`)
		for _, w := range words[1:] {
			sb.WriteString(`(?:\s+\S+)*\s+`)
			sb.WriteString(regexp.QuoteMeta(w))
			sb.WriteString(`\b`)
		}
		parts = append(parts, sb.String())
	}
	if len(parts) == 0 {
		return nil
	}

	combined := "(?i)(?:" + strings.Join(parts, "|") + ")"
	re, err := regexp.Compile(combined)
	if err != nil {
		return nil
	}
	return re
}

// coerceStringSlice converts []any{string,...} or []string to []string.
func coerceStringSlice(v any) []string {
	switch vv := v.(type) {
	case nil:
		return nil
	case []string:
		return vv
	case []any:
		out := make([]string, 0, len(vv))
		for _, x := range vv {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
