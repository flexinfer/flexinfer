package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

// Dispatch is the function signature for in-process message dispatch.
// It matches the daemon's handleMessage method signature.
type Dispatch func(ctx context.Context, msg *mcp.Message) (*mcp.Message, error)

// LocalCaller implements Caller by dispatching JSON-RPC requests directly
// to the daemon's handleMessage function in-process. No socket, no transport,
// no circuit breaker — the HUD and daemon share the same process.
type LocalCaller struct {
	dispatch Dispatch
}

// NewLocalCaller creates a LocalCaller that dispatches to the given function.
func NewLocalCaller(dispatch Dispatch) *LocalCaller {
	return &LocalCaller{dispatch: dispatch}
}

// Call sends a JSON-RPC request in-process.
func (c *LocalCaller) Call(method string, params any) (json.RawMessage, error) {
	return c.CallWithTimeout(method, params, 30*time.Second)
}

// CallWithTimeout sends a JSON-RPC request with a timeout.
func (c *LocalCaller) CallWithTimeout(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := mcp.NewRequest(int64(1), method, params)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.dispatch(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if resp == nil {
		return nil, fmt.Errorf("%s: nil response", method)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("daemon error (%d): %s", resp.Error.Code, resp.Error.Message)
	}

	return resp.Result, nil
}

// CallTool invokes an MCP tool in-process.
func (c *LocalCaller) CallTool(name string, args map[string]any) (json.RawMessage, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	return c.Call("tools/call", params)
}

// CallToolWithTimeout invokes an MCP tool with a timeout.
func (c *LocalCaller) CallToolWithTimeout(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	return c.CallWithTimeout("tools/call", params, timeout)
}

// CircuitOpen always returns false — in-process calls have no transport failures.
func (c *LocalCaller) CircuitOpen() bool { return false }

// Close is a no-op for in-process dispatch.
func (c *LocalCaller) Close() error { return nil }
