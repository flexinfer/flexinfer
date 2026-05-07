package generator

import (
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/registry"
)

// TestLoadPolicy_RegistryWinsWhenAgentsEntryPresent confirms the
// authoritative-registry rule: when the registry has an `agents`
// PlatformPermission entry, that's the source of truth even if the
// requested policy is missing from agents.guardrails.
func TestLoadPolicy_RegistryWinsWhenAgentsEntryPresent(t *testing.T) {
	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"agents": {}, // empty: operator says "no shared guardrails"
		},
	}
	p, err := LoadPolicy(reg, "gitops_flux")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Fatalf("expected nil policy when agents entry is empty, got %+v", p)
	}
}

// TestLoadPolicy_EmbeddedFallbackWhenNoAgentsEntry exercises the
// embedded-fallback path for users who haven't customized the registry.
// This is the path that lets new policies ship without a registry edit.
func TestLoadPolicy_EmbeddedFallbackWhenNoAgentsEntry(t *testing.T) {
	reg := &registry.Registry{} // no agents entry at all
	p, err := LoadPolicy(reg, "gitops_flux")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatalf("expected embedded gitops_flux policy to load, got nil")
	}
	if p.Name != "gitops_flux" {
		t.Errorf("expected Name=gitops_flux, got %q", p.Name)
	}
	if len(p.BlockedCommands) == 0 {
		t.Errorf("expected non-empty BlockedCommands from embedded YAML")
	}
	if !strings.Contains(strings.Join(p.BlockedCommands, " "), "kubectl") {
		t.Errorf("expected at least one kubectl entry in embedded BlockedCommands, got %v", p.BlockedCommands)
	}
}

// TestLoadPolicy_SecretsScan confirms the extensibility example added
// in this slice: a new embedded policy with no registry entry can be
// loaded without any Go change beyond the YAML file.
func TestLoadPolicy_SecretsScan(t *testing.T) {
	reg := &registry.Registry{} // no registry override
	p, err := LoadPolicy(reg, "secrets_scan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatalf("expected embedded secrets_scan policy, got nil")
	}
	if p.Name != "secrets_scan" {
		t.Errorf("expected Name=secrets_scan, got %q", p.Name)
	}
	if !strings.Contains(p.Message, "credentials") {
		t.Errorf("expected secrets_scan message to mention credentials, got %q", p.Message)
	}
}

// TestLoadPolicy_NilRegistry tolerates a nil *Registry pointer (uses
// embedded). Some test paths construct empty registries this way.
func TestLoadPolicy_NilRegistry(t *testing.T) {
	p, err := LoadPolicy(nil, "gitops_flux")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatalf("expected nil registry to fall back to embedded policy")
	}
}

// TestLoadPolicy_UnknownPolicy returns nil with no error for a policy
// name that doesn't exist anywhere — the dispatch loop in
// appendHookPolicies treats nil as "skip this ref".
func TestLoadPolicy_UnknownPolicy(t *testing.T) {
	reg := &registry.Registry{}
	p, err := LoadPolicy(reg, "definitely_not_a_real_policy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Fatalf("expected nil for unknown policy, got %+v", p)
	}
}

// TestPolicyDenyRules_Format spot-checks that the Bash(<cmd> *) wrapping
// matches the legacy Claude permission rule format.
func TestPolicyDenyRules_Format(t *testing.T) {
	policy := &Policy{
		BlockedCommands: []string{"kubectl apply", "kubectl rollout restart", " "},
	}
	rules := policyDenyRules(policy)
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules (whitespace-only entry filtered), got %d: %v", len(rules), rules)
	}
	if rules[0] != "Bash(kubectl apply *)" {
		t.Errorf("rule[0] = %q, want Bash(kubectl apply *)", rules[0])
	}
}

// TestPolicyRegex_AlternationAndSpaces verifies the regex builder
// produces the same shape as the legacy gitopsFluxGuardrailRegex helper:
// quoted spaces are unescaped so multi-word commands match natural input.
func TestPolicyRegex_AlternationAndSpaces(t *testing.T) {
	policy := &Policy{
		BlockedCommands: []string{"kubectl apply", "kubectl rollout undo"},
	}
	re := policyRegex(policy)
	wantParts := []string{
		`^[[:space:]]*(kubectl apply|kubectl rollout undo)([[:space:]]|$)`,
	}
	for _, part := range wantParts {
		if !strings.Contains(re, part) {
			t.Errorf("regex missing part %q\ngot: %q", part, re)
		}
	}
}
