package bridge

import (
	"encoding/json"
	"time"
)

// Caller is the interface for making JSON-RPC calls to the loom daemon.
// Both DaemonClient (Unix socket IPC) and LocalCaller (in-process dispatch)
// satisfy this interface, enabling the HUD to run embedded in the daemon
// without a network transport layer.
type Caller interface {
	// Call sends a JSON-RPC request and returns the result.
	Call(method string, params any) (json.RawMessage, error)

	// CallWithTimeout is like Call but uses a per-call timeout override.
	CallWithTimeout(method string, params any, timeout time.Duration) (json.RawMessage, error)

	// CallTool invokes an MCP tool through the daemon's tools/call method.
	CallTool(name string, args map[string]any) (json.RawMessage, error)

	// CallToolWithTimeout is like CallTool but uses a per-call timeout override.
	CallToolWithTimeout(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error)

	// CircuitOpen reports whether the circuit breaker is currently open.
	CircuitOpen() bool

	// Close closes the underlying connection.
	Close() error
}
