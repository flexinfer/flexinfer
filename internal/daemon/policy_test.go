package daemon

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestDefaultGatewayPolicyConfig_Disabled(t *testing.T) {
	cfg := DefaultGatewayPolicyConfig()
	if cfg.Enabled {
		t.Fatal("default gateway policy should be disabled")
	}
}

func TestNewGatewayPolicyEnforcer_DisabledReturnsNil(t *testing.T) {
	e := NewGatewayPolicyEnforcer(DefaultGatewayPolicyConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e != nil {
		t.Fatal("expected nil enforcer when policy is disabled")
	}
}

func TestGatewayPolicyEnforcer_CheckRequest_ForbiddenArgument(t *testing.T) {
	e := NewGatewayPolicyEnforcer(GatewayPolicyConfig{
		Enabled: true,
		Request: []GatewayRequestPolicyRule{
			{
				ID:                 "deny-destructive-flag",
				Server:             "github",
				Tool:               "delete_*",
				ForbiddenArguments: []string{"force"},
				ReasonCode:         "POLICY_DESTRUCTIVE_ARG",
			},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	decision := e.CheckRequest("github", "delete_repo", callParams{
		Arguments: []byte(`{"force":true}`),
	})

	if decision.Allowed {
		t.Fatal("expected request to be denied")
	}
	if decision.RuleID != "deny-destructive-flag" {
		t.Fatalf("rule_id = %q, want %q", decision.RuleID, "deny-destructive-flag")
	}
	if decision.ReasonCode != "POLICY_DESTRUCTIVE_ARG" {
		t.Fatalf("reason_code = %q, want %q", decision.ReasonCode, "POLICY_DESTRUCTIVE_ARG")
	}
	if !strings.Contains(decision.Reason, "forbidden arguments present") {
		t.Fatalf("unexpected reason: %q", decision.Reason)
	}
}

func TestGatewayPolicyEnforcer_CheckRequest_RequiredArgumentMissing(t *testing.T) {
	e := NewGatewayPolicyEnforcer(GatewayPolicyConfig{
		Enabled: true,
		Request: []GatewayRequestPolicyRule{
			{
				Server:            "k8s_*",
				Tool:              "k8s_apply",
				RequiredArguments: []string{"file"},
			},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	decision := e.CheckRequest("k8s_apps_k3s", "k8s_apply", callParams{
		Arguments: []byte(`{"namespace":"default"}`),
	})

	if decision.Allowed {
		t.Fatal("expected request to be denied")
	}
	if decision.RuleID != "request-rule-1" {
		t.Fatalf("rule_id = %q, want %q", decision.RuleID, "request-rule-1")
	}
	if decision.ReasonCode != defaultGatewayRequestReasonCode {
		t.Fatalf("reason_code = %q, want %q", decision.ReasonCode, defaultGatewayRequestReasonCode)
	}
	if !strings.Contains(decision.Reason, "missing required arguments") {
		t.Fatalf("unexpected reason: %q", decision.Reason)
	}
}

func TestGatewayPolicyEnforcer_CheckRequest_ContainsAny(t *testing.T) {
	e := NewGatewayPolicyEnforcer(GatewayPolicyConfig{
		Enabled: true,
		Request: []GatewayRequestPolicyRule{
			{
				Server:      "github",
				Tool:        "create_issue",
				ContainsAny: []string{"password="},
				ReasonCode:  "POLICY_SECRET_LEAK",
			},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	decision := e.CheckRequest("github", "create_issue", callParams{
		Arguments: []byte(`{"body":"token=password=abc123"}`),
	})

	if decision.Allowed {
		t.Fatal("expected request to be denied")
	}
	if decision.ReasonCode != "POLICY_SECRET_LEAK" {
		t.Fatalf("reason_code = %q, want %q", decision.ReasonCode, "POLICY_SECRET_LEAK")
	}
	if !strings.Contains(decision.Reason, "blocked pattern") {
		t.Fatalf("unexpected reason: %q", decision.Reason)
	}
}

func TestGatewayPolicyEnforcer_CheckRequest_AllowedWhenNoRuleMatches(t *testing.T) {
	e := NewGatewayPolicyEnforcer(GatewayPolicyConfig{
		Enabled: true,
		Request: []GatewayRequestPolicyRule{
			{
				Server:             "github",
				Tool:               "delete_*",
				ForbiddenArguments: []string{"force"},
			},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	decision := e.CheckRequest("github", "list_repos", callParams{
		Arguments: []byte(`{"page":1}`),
	})

	if !decision.Allowed {
		t.Fatalf("expected allowed decision, got %+v", decision)
	}
}
