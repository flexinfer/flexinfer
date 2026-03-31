package policy

import (
	"encoding/json"
	"testing"

	"github.com/crb2nu/loom/pkg/registry"
)

func testRegistryWithGuardrails(guardrails map[string]any) *registry.Registry {
	return &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"agents": {
				Settings: map[string]any{
					"guardrails": guardrails,
				},
			},
		},
	}
}

func TestNewEngineFromRegistry_GitopsFlux(t *testing.T) {
	reg := testRegistryWithGuardrails(map[string]any{
		"gitops_flux": map[string]any{
			"blocked_commands": []any{"kubectl edit", "kubectl set env"},
			"message":          "GitOps policy: blocked.",
		},
	})

	engine := NewEngineFromRegistry(reg)
	rules := engine.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Name != "gitops_flux" {
		t.Errorf("rule name = %q, want gitops_flux", rules[0].Name)
	}
	if rules[0].Message != "GitOps policy: blocked." {
		t.Errorf("rule message = %q, want %q", rules[0].Message, "GitOps policy: blocked.")
	}
}

func TestNewEngineFromRegistry_EmptyGuardrails_FallsBackToDefault(t *testing.T) {
	reg := testRegistryWithGuardrails(map[string]any{})
	engine := NewEngineFromRegistry(reg)
	rules := engine.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected default engine with 1 rule, got %d", len(rules))
	}
	if rules[0].Name != "gitops_flux" {
		t.Errorf("default rule name = %q, want gitops_flux", rules[0].Name)
	}
}

func TestNewEngineFromRegistry_NilRegistry_FallsBackToDefault(t *testing.T) {
	engine := NewEngineFromRegistry(nil)
	rules := engine.Rules()
	if len(rules) == 0 {
		t.Fatal("expected default engine to have rules")
	}
}

func TestNewEngineFromRegistry_NoPlatformPermissions_FallsBackToDefault(t *testing.T) {
	engine := NewEngineFromRegistry(&registry.Registry{})
	rules := engine.Rules()
	if len(rules) == 0 {
		t.Fatal("expected default engine to have rules")
	}
}

func TestNewEngineFromRegistry_MultipleGuardrails(t *testing.T) {
	reg := testRegistryWithGuardrails(map[string]any{
		"gitops_flux": map[string]any{
			"blocked_commands": []any{"kubectl edit"},
			"message":          "flux policy",
		},
		"dangerous_ops": map[string]any{
			"blocked_commands": []any{"rm -rf /"},
			"message":          "dangerous ops policy",
		},
	})

	engine := NewEngineFromRegistry(reg)
	rules := engine.Rules()
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	names := map[string]bool{}
	for _, r := range rules {
		names[r.Name] = true
	}
	if !names["gitops_flux"] {
		t.Error("missing gitops_flux rule")
	}
	if !names["dangerous_ops"] {
		t.Error("missing dangerous_ops rule")
	}
}

func TestNewEngineFromRegistry_DenyFieldFallback(t *testing.T) {
	reg := testRegistryWithGuardrails(map[string]any{
		"custom_policy": map[string]any{
			"deny":    []any{"terraform destroy"},
			"message": "no destroys",
		},
	})

	engine := NewEngineFromRegistry(reg)
	rules := engine.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Name != "custom_policy" {
		t.Errorf("rule name = %q, want custom_policy", rules[0].Name)
	}
}

func TestNewEngineFromRegistry_DefaultMessageWhenMissing(t *testing.T) {
	reg := testRegistryWithGuardrails(map[string]any{
		"no_msg": map[string]any{
			"blocked_commands": []any{"kubectl delete"},
		},
	})

	engine := NewEngineFromRegistry(reg)
	rules := engine.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Message == "" {
		t.Error("expected a default message, got empty")
	}
}

func TestCheck_BlocksKubectlEdit(t *testing.T) {
	reg := testRegistryWithGuardrails(map[string]any{
		"gitops_flux": map[string]any{
			"blocked_commands": []any{"kubectl edit", "kubectl set env"},
			"message":          "blocked",
		},
	})
	engine := NewEngineFromRegistry(reg)

	args, _ := json.Marshal(map[string]any{
		"command": "kubectl -n apps edit deployment/api",
	})

	msg, blocked := engine.Check("k8s_exec", args)
	if !blocked {
		t.Fatal("expected block for kubectl edit")
	}
	if msg != "blocked" {
		t.Errorf("message = %q, want %q", msg, "blocked")
	}
}

func TestCheck_BlocksKubectlSetEnv(t *testing.T) {
	reg := testRegistryWithGuardrails(map[string]any{
		"gitops_flux": map[string]any{
			"blocked_commands": []any{"kubectl edit", "kubectl set env"},
			"message":          "blocked",
		},
	})
	engine := NewEngineFromRegistry(reg)

	args, _ := json.Marshal(map[string]any{
		"command": "kubectl set env deployment/api IMAGE_TAG=latest",
	})

	_, blocked := engine.Check("k8s_exec", args)
	if !blocked {
		t.Fatal("expected block for kubectl set env")
	}
}

func TestCheck_AllowsSafeCommands(t *testing.T) {
	reg := testRegistryWithGuardrails(map[string]any{
		"gitops_flux": map[string]any{
			"blocked_commands": []any{"kubectl edit", "kubectl set env"},
			"message":          "blocked",
		},
	})
	engine := NewEngineFromRegistry(reg)

	args, _ := json.Marshal(map[string]any{
		"command": "kubectl get pods",
	})

	_, blocked := engine.Check("k8s_exec", args)
	if blocked {
		t.Fatal("expected allow for kubectl get pods")
	}
}

func TestCheck_ArrayCommand(t *testing.T) {
	reg := testRegistryWithGuardrails(map[string]any{
		"gitops_flux": map[string]any{
			"blocked_commands": []any{"kubectl edit", "kubectl set env"},
			"message":          "blocked",
		},
	})
	engine := NewEngineFromRegistry(reg)

	args, _ := json.Marshal(map[string]any{
		"command": []any{"kubectl", "-n", "apps", "set", "env", "deployment/api", "IMAGE_TAG=latest"},
	})

	_, blocked := engine.Check("k8s_exec", args)
	if !blocked {
		t.Fatal("expected block for array-form kubectl set env")
	}
}

func TestCheck_NestedJSON(t *testing.T) {
	reg := testRegistryWithGuardrails(map[string]any{
		"gitops_flux": map[string]any{
			"blocked_commands": []any{"kubectl edit", "kubectl set env"},
			"message":          "blocked",
		},
	})
	engine := NewEngineFromRegistry(reg)

	args, _ := json.Marshal(map[string]any{
		"script": map[string]any{
			"command": "kubectl set env deployment/api IMAGE_TAG=latest",
		},
	})

	_, blocked := engine.Check("k8s_exec", args)
	if !blocked {
		t.Fatal("expected block for nested kubectl set env")
	}
}

func TestCheck_NilEngine(t *testing.T) {
	var engine *Engine
	_, blocked := engine.Check("anything", nil)
	if blocked {
		t.Fatal("nil engine should not block")
	}
}

func TestCheck_EmptyArgs(t *testing.T) {
	engine := DefaultEngine()
	_, blocked := engine.Check("safe_tool", nil)
	if blocked {
		t.Fatal("empty args should not block")
	}
}

func TestCheck_ToolNameMatch(t *testing.T) {
	// If the tool name itself contains the blocked command pattern.
	reg := testRegistryWithGuardrails(map[string]any{
		"gitops_flux": map[string]any{
			"blocked_commands": []any{"kubectl edit"},
			"message":          "blocked",
		},
	})
	engine := NewEngineFromRegistry(reg)

	_, blocked := engine.Check("kubectl edit deployment", nil)
	if !blocked {
		t.Fatal("expected block when tool name matches pattern")
	}
}

func TestCheck_MultipleRulesFirstMatches(t *testing.T) {
	reg := testRegistryWithGuardrails(map[string]any{
		"policy_a": map[string]any{
			"blocked_commands": []any{"dangerous_command"},
			"message":          "policy A",
		},
		"policy_b": map[string]any{
			"blocked_commands": []any{"kubectl edit"},
			"message":          "policy B",
		},
	})
	engine := NewEngineFromRegistry(reg)

	args, _ := json.Marshal(map[string]any{
		"command": "dangerous_command --force",
	})
	msg, blocked := engine.Check("exec", args)
	if !blocked {
		t.Fatal("expected block for dangerous_command")
	}
	if msg != "policy A" {
		t.Errorf("message = %q, want %q", msg, "policy A")
	}
}

func TestCheck_CaseInsensitive(t *testing.T) {
	reg := testRegistryWithGuardrails(map[string]any{
		"gitops_flux": map[string]any{
			"blocked_commands": []any{"kubectl edit"},
			"message":          "blocked",
		},
	})
	engine := NewEngineFromRegistry(reg)

	args, _ := json.Marshal(map[string]any{
		"command": "KUBECTL EDIT deployment/api",
	})

	_, blocked := engine.Check("k8s_exec", args)
	if !blocked {
		t.Fatal("expected case-insensitive match")
	}
}

func TestDefaultEngine_Compatibility(t *testing.T) {
	engine := DefaultEngine()

	tests := []struct {
		name    string
		args    map[string]any
		blocked bool
	}{
		{
			name:    "kubectl edit blocked",
			args:    map[string]any{"command": "kubectl -n apps edit deployment/api"},
			blocked: true,
		},
		{
			name:    "kubectl set env blocked",
			args:    map[string]any{"command": "kubectl set env deployment/api IMAGE_TAG=latest"},
			blocked: true,
		},
		{
			name:    "kubectl get allowed",
			args:    map[string]any{"command": "kubectl get pods"},
			blocked: false,
		},
		{
			name:    "kubectl apply allowed",
			args:    map[string]any{"command": "kubectl apply -f deployment.yaml"},
			blocked: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, _ := json.Marshal(tt.args)
			_, blocked := engine.Check("k8s_exec", args)
			if blocked != tt.blocked {
				t.Errorf("blocked = %v, want %v", blocked, tt.blocked)
			}
		})
	}
}

func TestBuildGuardrailRegex_EmptyCommands(t *testing.T) {
	re := buildGuardrailRegex(nil)
	if re != nil {
		t.Error("expected nil for empty commands")
	}
}

func TestBuildGuardrailRegex_SingleCommand(t *testing.T) {
	re := buildGuardrailRegex([]string{"kubectl edit"})
	if re == nil {
		t.Fatal("expected non-nil regex")
	}
	if !re.MatchString("kubectl edit deployment") {
		t.Error("should match 'kubectl edit deployment'")
	}
	if !re.MatchString("kubectl -n apps edit deployment") {
		t.Error("should match with flags between")
	}
	if re.MatchString("kubectl get pods") {
		t.Error("should not match 'kubectl get pods'")
	}
}

func TestCoerceStringSlice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want int
	}{
		{"nil", nil, 0},
		{"string slice", []string{"a", "b"}, 2},
		{"any slice", []any{"a", "b"}, 2},
		{"mixed any slice", []any{"a", 42, "b"}, 2},
		{"int", 42, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := coerceStringSlice(tt.in)
			if len(got) != tt.want {
				t.Errorf("len = %d, want %d", len(got), tt.want)
			}
		})
	}
}
