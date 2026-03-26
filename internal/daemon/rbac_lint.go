package daemon

import (
	"fmt"
	"sort"
	"strings"
)

// RBACLintIssue describes one policy lint finding.
type RBACLintIssue struct {
	Code     string
	Severity RBACLintSeverity
	Path     string
	Message  string
}

// RBACLintSeverity defines issue severity reported by lint.
type RBACLintSeverity string

const (
	// RBACLintError fails validation.
	RBACLintError RBACLintSeverity = "error"
	// RBACLintWarning is informational and does not fail validation.
	RBACLintWarning RBACLintSeverity = "warning"
)

// LintRBACConfig returns deterministic RBAC policy issues.
func LintRBACConfig(cfg RBACConfig) []RBACLintIssue {
	if !cfg.Enabled {
		return nil
	}

	var issues []RBACLintIssue
	defaultPolicy := strings.ToLower(strings.TrimSpace(cfg.DefaultPolicy))
	if defaultPolicy != "allow" && defaultPolicy != "deny" {
		issues = append(issues, RBACLintIssue{
			Code:     "rbac.invalid_default_policy",
			Severity: RBACLintError,
			Path:     "rbac.default_policy",
			Message:  fmt.Sprintf("default_policy must be %q or %q, got %q", "allow", "deny", cfg.DefaultPolicy),
		})
	}

	roleNames := make(map[string]struct{}, len(cfg.Roles))
	for name := range cfg.Roles {
		roleNames[name] = struct{}{}
	}

	firstExact := map[string]int{}
	firstType := map[string]int{}
	firstWildcard := -1
	for i, binding := range cfg.Bindings {
		if _, ok := roleNames[binding.Role]; !ok {
			issues = append(issues, RBACLintIssue{
				Code:     "rbac.undefined_role",
				Severity: RBACLintError,
				Path:     fmt.Sprintf("rbac.bindings[%d].role", i),
				Message:  fmt.Sprintf("bindings[%d] references undefined role %q", i, binding.Role),
			})
		}

		if binding.AgentID != "" && binding.AgentID != "*" {
			if prev, ok := firstExact[binding.AgentID]; ok {
				issues = append(issues, RBACLintIssue{
					Code:     "rbac.duplicate_exact_binding",
					Severity: RBACLintError,
					Path:     fmt.Sprintf("rbac.bindings[%d].agent_id", i),
					Message: fmt.Sprintf(
						"bindings[%d] exact agent_id %q is unreachable; first exact binding at index %d wins",
						i, binding.AgentID, prev,
					),
				})
			} else {
				firstExact[binding.AgentID] = i
			}
		}

		if binding.AgentType != "" {
			if prev, ok := firstType[binding.AgentType]; ok {
				issues = append(issues, RBACLintIssue{
					Code:     "rbac.duplicate_type_binding",
					Severity: RBACLintError,
					Path:     fmt.Sprintf("rbac.bindings[%d].agent_type", i),
					Message: fmt.Sprintf(
						"bindings[%d] agent_type %q is unreachable for type resolution; first type binding at index %d wins",
						i, binding.AgentType, prev,
					),
				})
			} else {
				firstType[binding.AgentType] = i
			}
		}

		if binding.AgentID == "*" {
			if firstWildcard >= 0 {
				issues = append(issues, RBACLintIssue{
					Code:     "rbac.duplicate_wildcard_binding",
					Severity: RBACLintError,
					Path:     fmt.Sprintf("rbac.bindings[%d].agent_id", i),
					Message: fmt.Sprintf(
						"bindings[%d] wildcard binding is unreachable; first wildcard binding at index %d wins",
						i, firstWildcard,
					),
				})
			} else {
				firstWildcard = i
			}
		}
	}

	roleNamesSorted := make([]string, 0, len(cfg.Roles))
	for roleName := range cfg.Roles {
		roleNamesSorted = append(roleNamesSorted, roleName)
	}
	sort.Strings(roleNamesSorted)

	for _, roleName := range roleNamesSorted {
		role := cfg.Roles[roleName]
		denyPatterns := make(map[string]struct{}, len(role.Deny))
		for _, pattern := range role.Deny {
			denyPatterns[pattern] = struct{}{}
		}
		for _, pattern := range role.Allow {
			if _, ok := denyPatterns[pattern]; ok {
				issues = append(issues, RBACLintIssue{
					Code:     "rbac.allow_deny_overlap",
					Severity: RBACLintError,
					Path:     fmt.Sprintf("rbac.roles.%s", roleName),
					Message:  fmt.Sprintf("role %q has overlapping allow/deny pattern %q (deny always wins)", roleName, pattern),
				})
			}
		}
	}

	return issues
}

// HasRBACLintErrors reports whether issues contain any error severity items.
func HasRBACLintErrors(issues []RBACLintIssue) bool {
	for _, issue := range issues {
		if issue.Severity == RBACLintError {
			return true
		}
	}
	return false
}

// ValidateRBACConfig returns an aggregated error when RBAC policy lint fails.
func ValidateRBACConfig(cfg RBACConfig) error {
	issues := LintRBACConfig(cfg)
	if len(issues) == 0 {
		return nil
	}

	lines := make([]string, 0, len(issues))
	for _, issue := range issues {
		lines = append(lines, fmt.Sprintf("%s (%s): %s", issue.Code, issue.Path, issue.Message))
	}
	return fmt.Errorf("rbac lint failed (%d issues):\n  - %s", len(issues), strings.Join(lines, "\n  - "))
}
