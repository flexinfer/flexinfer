package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/openairesponses"
)

func TestResponsesToolAdapterBuildTools(t *testing.T) {
	adapter := &responsesToolAdapter{
		rpc: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			if method != "loom/tools" {
				t.Fatalf("method = %s, want loom/tools", method)
			}
			if params != nil {
				t.Fatalf("expected nil params, got %#v", params)
			}
			return mustJSONResult(t, map[string]any{
				"tools": []map[string]any{
					{
						"name":        "math__add",
						"description": "Add numbers",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"a": map[string]any{"type": "number"},
							},
						},
					},
				},
			}), nil
		},
	}

	tools, err := adapter.BuildTools(context.Background(), openairesponses.ExecutionIdentity{})
	if err != nil {
		t.Fatalf("build tools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d", len(tools))
	}
	if tools[0].Name != "math__add" {
		t.Fatalf("tool name = %q", tools[0].Name)
	}
	if _, err := adapter.ResolveCall(context.Background(), openairesponses.ToolCall{ToolName: "missing__tool"}); err == nil {
		t.Fatal("expected unknown tool error")
	}
}

func TestResponsesToolExecutorExecuteTool(t *testing.T) {
	executor := &responsesToolExecutor{
		rpc: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			if method != "tools/call" {
				t.Fatalf("method = %s, want tools/call", method)
			}
			paramMap, ok := params.(map[string]any)
			if !ok {
				t.Fatalf("params type = %T", params)
			}
			if paramMap["agent_id"] != "agent-1" {
				t.Fatalf("agent_id = %#v", paramMap["agent_id"])
			}
			if paramMap["session_id"] != "sess-1" {
				t.Fatalf("session_id = %#v", paramMap["session_id"])
			}
			if _, ok := paramMap["_timeout"].(string); !ok {
				t.Fatalf("timeout hint missing from params: %#v", paramMap)
			}

			envelope := mcp.CallToolResult{
				Content: []mcp.Content{{Type: "text", Text: `{"sum":42}`}},
			}
			return mustJSONResult(t, envelope), nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := executor.ExecuteTool(ctx, openairesponses.ToolCall{
		CallID:    "call_1",
		ToolName:  "math__add",
		Arguments: json.RawMessage(`{"a":2,"b":40}`),
	}, openairesponses.ExecutionIdentity{
		AgentID:   "agent-1",
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("output type = %T", result.Output)
	}
	if output["sum"] != float64(42) {
		t.Fatalf("sum = %#v", output["sum"])
	}
}

func TestResponsesToolExecutorPropagatesRPCError(t *testing.T) {
	executor := &responsesToolExecutor{
		rpc: func(_ context.Context, _ string, _ any) (json.RawMessage, error) {
			return nil, errors.New("daemon unavailable")
		},
	}
	_, err := executor.ExecuteTool(context.Background(), openairesponses.ToolCall{
		ToolName:  "math__add",
		Arguments: json.RawMessage(`{}`),
	}, openairesponses.ExecutionIdentity{})
	if err == nil || err.Error() != "daemon unavailable" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustJSONResult(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return b
}
