package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/crb2nu/loom/internal/daemon"
)

func TestSimulateRBACDecision_DryRunAndEnforce(t *testing.T) {
	cfg := daemon.RBACConfig{
		Enabled:       true,
		DefaultPolicy: "deny",
		Roles: map[string]daemon.RBACRole{
			"developer": {
				Allow: []string{"git__*"},
			},
		},
		Bindings: []daemon.RBACBinding{
			{AgentID: "agent-1", Role: "developer"},
		},
	}

	dryRun, err := simulateRBACDecision(cfg, "agent-1", "", "git", "git_status", "dry-run")
	if err != nil {
		t.Fatalf("simulate dry-run: %v", err)
	}
	enforce, err := simulateRBACDecision(cfg, "agent-1", "", "git", "git_status", "enforce")
	if err != nil {
		t.Fatalf("simulate enforce: %v", err)
	}

	if dryRun.Allowed != enforce.Allowed ||
		dryRun.AgentID != enforce.AgentID ||
		dryRun.Server != enforce.Server ||
		dryRun.Tool != enforce.Tool ||
		dryRun.Role != enforce.Role ||
		dryRun.Reason != enforce.Reason ||
		dryRun.ReasonCode != enforce.ReasonCode ||
		dryRun.MatchedRule != enforce.MatchedRule {
		t.Fatalf("dry-run decision should match enforce semantics for equivalent input: dry=%+v enforce=%+v", dryRun, enforce)
	}
	if dryRun.MatchedBinding == nil || enforce.MatchedBinding == nil {
		t.Fatalf("expected matched binding metadata: dry=%+v enforce=%+v", dryRun, enforce)
	}
	if *dryRun.MatchedBinding != *enforce.MatchedBinding {
		t.Fatalf("expected equivalent matched binding metadata: dry=%+v enforce=%+v", dryRun, enforce)
	}
	if !dryRun.DryRun {
		t.Fatalf("expected dry-run flag set on dry-run decision: %+v", dryRun)
	}
	if enforce.DryRun {
		t.Fatalf("expected dry-run flag unset on enforce decision: %+v", enforce)
	}
}

func TestSimulateRBACDecision_DisabledConfig(t *testing.T) {
	decision, err := simulateRBACDecision(daemon.DefaultRBACConfig(), "agent-1", "", "git", "git_status", "dry-run")
	if err != nil {
		t.Fatalf("simulate disabled config: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("expected allow when RBAC disabled, got %+v", decision)
	}
	if decision.Reason != "rbac disabled" {
		t.Fatalf("unexpected reason: %q", decision.Reason)
	}
}

func TestParseRBACConfigFile_RawRBACPolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rbac-policy.yaml")
	content := []byte(`
enabled: true
default_policy: deny
roles:
  readonly:
    allow:
      - "*__list_*"
bindings:
  - agent_id: "*"
    role: readonly
`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := parseRBACConfigFile(path)
	if err != nil {
		t.Fatalf("parse raw policy: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("expected parsed policy to be enabled")
	}
	if cfg.DefaultPolicy != "deny" {
		t.Fatalf("unexpected default policy: %q", cfg.DefaultPolicy)
	}
}

func TestParseRBACConfigFile_UserConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
rbac:
  enabled: true
  default_policy: allow
  roles:
    developer:
      allow:
        - "git__*"
  bindings:
    - agent_id: "codex"
      role: developer
`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := parseRBACConfigFile(path)
	if err != nil {
		t.Fatalf("parse user config: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("expected RBAC enabled")
	}
	if len(cfg.Bindings) != 1 || cfg.Bindings[0].Role != "developer" {
		t.Fatalf("unexpected bindings: %+v", cfg.Bindings)
	}
}
