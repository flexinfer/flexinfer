package main

import (
	"context"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestProxyFluxPolicyResponse_BlocksUnsafeKubectlCommands(t *testing.T) {
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
			if got := resp.Error.Message; got != proxyFluxFirstPolicyMessage {
				t.Fatalf("error message = %q, want %q", got, proxyFluxFirstPolicyMessage)
			}
		})
	}
}

func TestProxyFluxPolicyResponse_AllowsSafeCommands(t *testing.T) {
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

func TestHandleProxyToolsCall_BlocksBeforeDaemonDispatch(t *testing.T) {
	oldAgentHint := agentHintGlobal
	oldSessionID := proxySessionID
	oldSessionDisabled := proxySessionDisabled
	defer func() {
		agentHintGlobal = oldAgentHint
		proxySessionID = oldSessionID
		proxySessionDisabled = oldSessionDisabled
	}()

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
	if got := resp.Error.Message; got != proxyFluxFirstPolicyMessage {
		t.Fatalf("error message = %q, want %q", got, proxyFluxFirstPolicyMessage)
	}
	if len(transport.sentMessages) != 0 {
		t.Fatalf("expected no daemon dispatch, got %d messages", len(transport.sentMessages))
	}
}
