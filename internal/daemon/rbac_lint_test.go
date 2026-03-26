package daemon

import (
	"strings"
	"testing"
)

func TestLintRBACConfig(t *testing.T) {
	t.Run("disabled rbac has no issues", func(t *testing.T) {
		cfg := RBACConfig{Enabled: false}
		if got := LintRBACConfig(cfg); len(got) != 0 {
			t.Fatalf("expected no issues, got %d", len(got))
		}
	})

	t.Run("reports policy traps", func(t *testing.T) {
		cfg := RBACConfig{
			Enabled:       true,
			DefaultPolicy: "maybe",
			Roles: map[string]RBACRole{
				"dev": {
					Allow: []string{"git__*"},
					Deny:  []string{"git__*"},
				},
			},
			Bindings: []RBACBinding{
				{AgentID: "alpha", Role: "dev"},
				{AgentID: "alpha", Role: "dev"},
				{AgentType: "codex", Role: "dev"},
				{AgentType: "codex", Role: "dev"},
				{AgentID: "*", Role: "dev"},
				{AgentID: "*", Role: "dev"},
				{AgentID: "beta", Role: "missing"},
			},
		}

		got := LintRBACConfig(cfg)
		if len(got) != 6 {
			t.Fatalf("expected 6 issues, got %d: %+v", len(got), got)
		}

		var sawInvalidPolicy bool
		var sawUndefinedRole bool
		var sawWildcard bool
		for _, issue := range got {
			switch issue.Code {
			case "rbac.invalid_default_policy":
				sawInvalidPolicy = true
			case "rbac.undefined_role":
				sawUndefinedRole = true
			case "rbac.duplicate_wildcard_binding":
				sawWildcard = true
			}
		}

		if !sawInvalidPolicy || !sawUndefinedRole || !sawWildcard {
			t.Fatalf("missing expected issue codes: %+v", got)
		}
	})
}

func TestValidateRBACConfig(t *testing.T) {
	cfg := RBACConfig{
		Enabled:       true,
		DefaultPolicy: "invalid",
	}

	err := ValidateRBACConfig(cfg)
	if err == nil {
		t.Fatal("expected lint error, got nil")
	}
	if !strings.Contains(err.Error(), "rbac.invalid_default_policy") {
		t.Fatalf("unexpected error text: %v", err)
	}
}
