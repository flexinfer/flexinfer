package bridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

func TestLocalCaller_Call(t *testing.T) {
	dispatch := func(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
		result, _ := json.Marshal(map[string]string{"status": "ok"})
		return &mcp.Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  result,
		}, nil
	}

	caller := NewLocalCaller(dispatch)
	raw, err := caller.Call("loom/status", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["status"] != "ok" {
		t.Errorf("got status=%q, want ok", got["status"])
	}
}

func TestLocalCaller_CallTool(t *testing.T) {
	var receivedMethod string
	var receivedParams map[string]any

	dispatch := func(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
		receivedMethod = msg.Method
		if msg.Params != nil {
			json.Unmarshal(msg.Params, &receivedParams)
		}
		result, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": "done"}}})
		return &mcp.Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  result,
		}, nil
	}

	caller := NewLocalCaller(dispatch)
	_, err := caller.CallTool("test_tool", map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMethod != "tools/call" {
		t.Errorf("got method=%q, want tools/call", receivedMethod)
	}
	if receivedParams["name"] != "test_tool" {
		t.Errorf("got name=%v, want test_tool", receivedParams["name"])
	}
}

func TestLocalCaller_DaemonError(t *testing.T) {
	dispatch := func(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
		return &mcp.Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error: &mcp.Error{
				Code:    -32601,
				Message: "method not found",
			},
		}, nil
	}

	caller := NewLocalCaller(dispatch)
	_, err := caller.Call("bogus/method", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); !contains(got, "daemon error") {
		t.Errorf("error %q should contain 'daemon error'", got)
	}
}

func TestLocalCaller_Timeout(t *testing.T) {
	dispatch := func(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return &mcp.Message{JSONRPC: "2.0", ID: msg.ID}, nil
		}
	}

	caller := NewLocalCaller(dispatch)
	_, err := caller.CallWithTimeout("slow/method", nil, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestLocalCaller_CircuitOpen(t *testing.T) {
	caller := NewLocalCaller(nil)
	if caller.CircuitOpen() {
		t.Error("LocalCaller.CircuitOpen() should always return false")
	}
}

func TestLocalCaller_Close(t *testing.T) {
	caller := NewLocalCaller(nil)
	if err := caller.Close(); err != nil {
		t.Errorf("LocalCaller.Close() should return nil, got %v", err)
	}
}

func TestLocalCaller_SatisfiesCaller(t *testing.T) {
	var _ Caller = (*LocalCaller)(nil)
}

func TestDaemonClient_SatisfiesCaller(t *testing.T) {
	var _ Caller = (*DaemonClient)(nil)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
