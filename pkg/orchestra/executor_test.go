package orchestra

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/openairesponses"
)

// fakeCaller is a test double for ToolCaller.
type fakeCaller struct {
	response json.RawMessage
	err      error
	calls    []fakeCall
}

type fakeCall struct {
	Name    string
	Args    map[string]any
	Timeout time.Duration
}

func (f *fakeCaller) CallToolWithTimeout(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error) {
	f.calls = append(f.calls, fakeCall{Name: name, Args: args, Timeout: timeout})
	return f.response, f.err
}

func TestDaemonToolExecutor_ExecuteTool(t *testing.T) {
	caller := &fakeCaller{
		response: json.RawMessage(`{"content":[{"type":"text","text":"hello world"}]}`),
	}
	executor := NewDaemonToolExecutor(caller, 30*time.Second)

	call := openairesponses.ToolCall{
		CallID:    "call-1",
		ToolName:  "git__git_status",
		Arguments: json.RawMessage(`{"repo": "/tmp/test"}`),
	}

	result, err := executor.ExecuteTool(context.Background(), call, openairesponses.ExecutionIdentity{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.CallID != "call-1" {
		t.Errorf("expected call-1, got %q", result.CallID)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.ErrorText)
	}
	output, ok := result.Output.(string)
	if !ok || output != "hello world" {
		t.Errorf("expected 'hello world', got %v", result.Output)
	}

	if len(caller.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(caller.calls))
	}
	if caller.calls[0].Name != "git__git_status" {
		t.Errorf("expected git__git_status, got %q", caller.calls[0].Name)
	}
}

func TestDaemonToolExecutor_ExecuteTool_Error(t *testing.T) {
	caller := &fakeCaller{
		err: fmt.Errorf("tool execution failed"),
	}
	executor := NewDaemonToolExecutor(caller, 30*time.Second)

	call := openairesponses.ToolCall{
		CallID:    "call-2",
		ToolName:  "failing_tool",
		Arguments: json.RawMessage(`{}`),
	}

	result, err := executor.ExecuteTool(context.Background(), call, openairesponses.ExecutionIdentity{})
	if err != nil {
		t.Fatalf("ExecuteTool should not return error for tool failures: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
	if result.ErrorText == "" {
		t.Error("expected non-empty ErrorText")
	}
}

func TestDaemonToolExecutor_InvalidArguments(t *testing.T) {
	caller := &fakeCaller{}
	executor := NewDaemonToolExecutor(caller, 30*time.Second)

	call := openairesponses.ToolCall{
		CallID:    "call-3",
		ToolName:  "tool",
		Arguments: json.RawMessage(`not json`),
	}

	result, err := executor.ExecuteTool(context.Background(), call, openairesponses.ExecutionIdentity{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for invalid arguments")
	}
}

func TestExtractToolOutput_MCPFormat(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]}`)
	got := extractToolOutput(raw)
	if got != "line1\nline2" {
		t.Errorf("expected 'line1\\nline2', got %q", got)
	}
}

func TestExtractToolOutput_RawFallback(t *testing.T) {
	raw := json.RawMessage(`{"key": "value"}`)
	got := extractToolOutput(raw)
	if got != `{"key": "value"}` {
		t.Errorf("expected raw JSON, got %q", got)
	}
}
