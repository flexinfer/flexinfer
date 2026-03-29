package orchestra

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/crb2nu/loom/pkg/openairesponses"
)

// ToolCaller abstracts the daemon's tool call dispatch. Satisfied by
// bridge.LocalCaller (in-process) or bridge.Caller (socket).
type ToolCaller interface {
	CallToolWithTimeout(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error)
}

// DaemonToolExecutor implements openairesponses.ToolExecutor by dispatching
// tool calls through the daemon's call pipeline via a ToolCaller.
type DaemonToolExecutor struct {
	caller  ToolCaller
	timeout time.Duration
}

// NewDaemonToolExecutor creates an executor with the given caller and per-tool timeout.
func NewDaemonToolExecutor(caller ToolCaller, timeout time.Duration) *DaemonToolExecutor {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &DaemonToolExecutor{caller: caller, timeout: timeout}
}

// ExecuteTool dispatches a single tool call through the daemon pipeline.
func (e *DaemonToolExecutor) ExecuteTool(_ context.Context, call openairesponses.ToolCall, _ openairesponses.ExecutionIdentity) (openairesponses.ToolResult, error) {
	var args map[string]any
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return openairesponses.ToolResult{
				CallID:    call.CallID,
				IsError:   true,
				ErrorText: fmt.Sprintf("invalid tool arguments: %v", err),
			}, nil
		}
	}

	raw, err := e.caller.CallToolWithTimeout(call.ToolName, args, e.timeout)
	if err != nil {
		return openairesponses.ToolResult{
			CallID:    call.CallID,
			IsError:   true,
			ErrorText: err.Error(),
		}, nil
	}

	// Try to extract text content from the MCP response.
	output := extractToolOutput(raw)

	return openairesponses.ToolResult{
		CallID:     call.CallID,
		Output:     output,
		RawPayload: raw,
	}, nil
}

// extractToolOutput attempts to pull text from an MCP tool response.
func extractToolOutput(raw json.RawMessage) string {
	// MCP responses typically have {"content": [{"type":"text","text":"..."}]}
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &resp); err == nil && len(resp.Content) > 0 {
		var texts []string
		for _, c := range resp.Content {
			if c.Type == "text" && c.Text != "" {
				texts = append(texts, c.Text)
			}
		}
		if len(texts) > 0 {
			return joinStrings(texts, "\n")
		}
	}
	// Fallback: return the raw JSON.
	return string(raw)
}

func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for _, s := range strs[1:] {
		result += sep + s
	}
	return result
}
