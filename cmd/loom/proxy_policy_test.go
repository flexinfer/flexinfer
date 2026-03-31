package main

import (
	"context"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/policy"
	"github.com/crb2nu/loom/pkg/registry"
)

// defaultPolicyMessage returns the denial message from the default engine's
// gitops_flux rule. Used to validate backward compatibility without
// hard-coding the constant in tests.
func defaultPolicyMessage() string {
	for _, r := range policy.DefaultEngine().Rules() {
		if r.Name == "gitops_flux" {
			return r.Message
		}
	}
	return ""
}

func TestProxyFluxPolicyResponse_BlocksUnsafeKubectlCommands(t *testing.T) {
	// Ensure the default engine is active for these tests.
	oldEngine := proxyPolicyEngine
	proxyPolicyEngine = policy.DefaultEngine()
	defer func() { proxyPolicyEngine = oldEngine }()

	wantMsg := defaultPolicyMessage()
	if wantMsg == "" {
		t.Fatal("default engine has no gitops_flux rule")
	}

	tests := []struct {
		name string
		args map[string]any
	}{
		{
			name: "string command",
			args: map[string]any{
				"command": "kubectl -n apps edit deployment/api",
			},
		},
		{
			name: "array command",
			args: map[string]any{
				"command": []any{"kubectl", "-n", "apps", "set", "env", "deployment/api", "IMAGE_TAG=latest"},
			},
		},
		{
			name: "nested shell command",
			args: map[string]any{
				"script": map[string]any{
					"command": "kubectl set env deployment/api IMAGE_TAG=latest",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := mcp.NewRequest(1, "tools/call", map[string]any{
				"name":      "k8s_exec",
				"arguments": tt.args,
			})
			if err != nil {
				t.Fatalf("mcp.NewRequest: %v", err)
			}

			resp, blocked := proxyFluxPolicyResponse(msg)
			if !blocked {
				t.Fatalf("expected policy block")
			}
			if resp == nil || resp.Error == nil {
				t.Fatalf("expected policy error response")
			}
			if got := resp.Error.Message; got != wantMsg {
				t.Fatalf("error message = %q, want %q", got, wantMsg)
			}
		})
	}
}

func TestProxyFluxPolicyResponse_AllowsSafeCommands(t *testing.T) {
	oldEngine := proxyPolicyEngine
	proxyPolicyEngine = policy.DefaultEngine()
	defer func() { proxyPolicyEngine = oldEngine }()

	msg, err := mcp.NewRequest(1, "tools/call", map[string]any{
		"name":      "k8s_exec",
		"arguments": map[string]any{"command": []any{"kubectl", "get", "pods"}},
	})
	if err != nil {
		t.Fatalf("mcp.NewRequest: %v", err)
	}

	resp, blocked := proxyFluxPolicyResponse(msg)
	if blocked {
		t.Fatalf("unexpected policy block: %#v", resp)
	}
}

func TestProxyFluxPolicyResponse_RegistryDrivenEngine(t *testing.T) {
	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"agents": {
				Settings: map[string]any{
					"guardrails": map[string]any{
						"gitops_flux": map[string]any{
							"blocked_commands": []any{"kubectl edit", "kubectl set env"},
							"message":          "Registry policy: blocked.",
						},
					},
				},
			},
		},
	}

	oldEngine := proxyPolicyEngine
	proxyPolicyEngine = policy.NewEngineFromRegistry(reg)
	defer func() { proxyPolicyEngine = oldEngine }()

	msg, err := mcp.NewRequest(1, "tools/call", map[string]any{
		"name":      "k8s_exec",
		"arguments": map[string]any{"command": "kubectl edit deployment/api"},
	})
	if err != nil {
		t.Fatalf("mcp.NewRequest: %v", err)
	}

	resp, blocked := proxyFluxPolicyResponse(msg)
	if !blocked {
		t.Fatal("expected block with registry-driven engine")
	}
	if resp.Error.Message != "Registry policy: blocked." {
		t.Fatalf("error message = %q, want %q", resp.Error.Message, "Registry policy: blocked.")
	}
}

func TestProxyFluxPolicyResponse_CustomGuardrailBlocksNewCommand(t *testing.T) {
	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"agents": {
				Settings: map[string]any{
					"guardrails": map[string]any{
						"custom_guardrail": map[string]any{
							"blocked_commands": []any{"terraform destroy"},
							"message":          "No terraform destroy allowed.",
						},
					},
				},
			},
		},
	}

	oldEngine := proxyPolicyEngine
	proxyPolicyEngine = policy.NewEngineFromRegistry(reg)
	defer func() { proxyPolicyEngine = oldEngine }()

	// This should be blocked by the custom guardrail.
	msg, err := mcp.NewRequest(1, "tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{"command": "terraform destroy --auto-approve"},
	})
	if err != nil {
		t.Fatalf("mcp.NewRequest: %v", err)
	}

	resp, blocked := proxyFluxPolicyResponse(msg)
	if !blocked {
		t.Fatal("expected block for terraform destroy with custom guardrail")
	}
	if resp.Error.Message != "No terraform destroy allowed." {
		t.Fatalf("error message = %q, want %q", resp.Error.Message, "No terraform destroy allowed.")
	}

	// kubectl edit should be allowed since we only have the custom guardrail.
	msg2, err := mcp.NewRequest(2, "tools/call", map[string]any{
		"name":      "k8s_exec",
		"arguments": map[string]any{"command": "kubectl edit deployment/api"},
	})
	if err != nil {
		t.Fatalf("mcp.NewRequest: %v", err)
	}

	_, blocked2 := proxyFluxPolicyResponse(msg2)
	if blocked2 {
		t.Fatal("kubectl edit should be allowed when only custom guardrail is configured")
	}
}

func TestProxyFluxPolicyResponse_NilEngine_FallsBackToDefault(t *testing.T) {
	oldEngine := proxyPolicyEngine
	proxyPolicyEngine = nil
	defer func() { proxyPolicyEngine = oldEngine }()

	msg, err := mcp.NewRequest(1, "tools/call", map[string]any{
		"name":      "k8s_exec",
		"arguments": map[string]any{"command": "kubectl edit deployment/api"},
	})
	if err != nil {
		t.Fatalf("mcp.NewRequest: %v", err)
	}

	_, blocked := proxyFluxPolicyResponse(msg)
	if !blocked {
		t.Fatal("nil engine should fall back to default and block kubectl edit")
	}
}

func TestHandleProxyToolsCall_BlocksBeforeDaemonDispatch(t *testing.T) {
	oldEngine := proxyPolicyEngine
	oldAgentHint := agentHintGlobal
	oldSessionID := proxySessionID
	oldSessionDisabled := proxySessionDisabled
	defer func() {
		proxyPolicyEngine = oldEngine
		agentHintGlobal = oldAgentHint
		proxySessionID = oldSessionID
		proxySessionDisabled = oldSessionDisabled
	}()

	proxyPolicyEngine = policy.DefaultEngine()
	agentHintGlobal = ""
	proxySessionID = ""
	proxySessionDisabled = false

	transport := &sessionStubTransport{
		recvQueue: []*mcp.Message{},
	}

	msg, err := mcp.NewRequest(1, "tools/call", map[string]any{
		"name":      "k8s_exec",
		"arguments": map[string]any{"command": "kubectl edit deployment/api"},
	})
	if err != nil {
		t.Fatalf("mcp.NewRequest: %v", err)
	}

	resp, err := handleProxyToolsCall(context.Background(), transport, msg)
	if err != nil {
		t.Fatalf("handleProxyToolsCall returned error: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatalf("expected blocked error response")
	}

	wantMsg := defaultPolicyMessage()
	if got := resp.Error.Message; got != wantMsg {
		t.Fatalf("error message = %q, want %q", got, wantMsg)
	}
	if len(transport.sentMessages) != 0 {
		t.Fatalf("expected no daemon dispatch, got %d messages", len(transport.sentMessages))
	}
}
