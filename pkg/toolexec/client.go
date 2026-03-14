// Package toolexec provides a minimal MCP client that routes tool calls back
// through the loom daemon via its Unix socket. It is used by the workflow
// engine inside mcp-agent-context to execute tool steps against any server
// in the daemon's pool.
package toolexec

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.flexinfer.ai/libs/fi-accel/go/fiaccel"
	"gitlab.flexinfer.ai/libs/mcp-go"
)

// Config holds configuration for the daemon loopback client.
type Config struct {
	SocketPath string // Unix socket path (typically from LOOM_SOCKET)
}

// Client is a lightweight MCP client that dials the loom daemon Unix socket
// and issues tools/call requests. It is intentionally simpler than the HUD
// bridge client (no circuit breaker, no autostart) because the workflow
// engine already provides retry logic (MaxRetries, RetryDelay).
type Client struct {
	socketPath string

	mu        sync.Mutex
	conn      net.Conn
	transport *mcp.StdioTransport
	reqID     atomic.Int64
}

// New creates a new Client. Returns nil if cfg.SocketPath is empty.
func New(cfg Config) *Client {
	if cfg.SocketPath == "" {
		return nil
	}
	return &Client{socketPath: cfg.SocketPath}
}

// Execute satisfies the agentcontext.ToolExecutor signature. It sends a
// tools/call request to the daemon, combining serverName and toolName with
// the "__" namespace separator used by the daemon router.
func (c *Client) Execute(ctx context.Context, serverName, toolName string, args map[string]any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureConnectedLocked(ctx); err != nil {
		return nil, fmt.Errorf("toolexec: connect: %w", err)
	}

	// Build namespaced tool name for daemon routing.
	name := serverName + "__" + toolName

	id := c.reqID.Add(1)
	req, err := mcp.NewRequest(id, "tools/call", mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("toolexec: create request: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if err := c.transport.Send(callCtx, req); err != nil {
		c.resetLocked()
		return nil, fmt.Errorf("toolexec: send: %w", err)
	}

	resp, err := c.transport.Recv(callCtx)
	if err != nil {
		c.resetLocked()
		return nil, fmt.Errorf("toolexec: recv: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("toolexec: daemon error (%d): %s", resp.Error.Code, resp.Error.Message)
	}

	return parseToolResult(resp.Result)
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeLocked()
}

// ensureConnectedLocked lazily connects and initializes the daemon session.
// Caller must hold c.mu.
func (c *Client) ensureConnectedLocked(ctx context.Context) error {
	if c.transport != nil {
		return nil
	}
	return c.connectLocked(ctx)
}

// connectLocked dials the daemon socket and sends the MCP initialize
// handshake. Caller must hold c.mu.
func (c *Client) connectLocked(ctx context.Context) error {
	c.closeLocked()

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.socketPath, err)
	}

	c.conn = conn
	c.transport = mcp.NewStdioTransport(conn, conn)

	// MCP initialize handshake.
	initReq, err := mcp.NewRequest(c.reqID.Add(1), "initialize", mcp.InitializeParams{
		ProtocolVersion: mcp.ProtocolVersion20250618,
		Capabilities:    mcp.Capabilities{},
		ClientInfo:      mcp.ClientInfo{Name: "toolexec", Version: "1.0.0"},
	})
	if err != nil {
		c.closeLocked()
		return fmt.Errorf("create init request: %w", err)
	}

	initCtx, initCancel := context.WithTimeout(ctx, 5*time.Second)
	defer initCancel()

	if err := c.transport.Send(initCtx, initReq); err != nil {
		c.closeLocked()
		return fmt.Errorf("send init: %w", err)
	}

	resp, err := c.transport.Recv(initCtx)
	if err != nil {
		c.closeLocked()
		return fmt.Errorf("recv init: %w", err)
	}

	if resp.Error != nil {
		c.closeLocked()
		return fmt.Errorf("init error (%d): %s", resp.Error.Code, resp.Error.Message)
	}

	return nil
}

// resetLocked tears down the connection so the next call reconnects.
// Caller must hold c.mu.
func (c *Client) resetLocked() {
	c.closeLocked()
}

// closeLocked closes the connection if open. Caller must hold c.mu.
func (c *Client) closeLocked() error {
	c.transport = nil
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// parseToolResult extracts a map from the daemon's CallToolResult envelope.
// The daemon returns a standard MCP CallToolResult with Content blocks.
// We parse the first text content block as JSON, falling back to returning
// the raw text under a "result" key.
func parseToolResult(raw json.RawMessage) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}

	// Try parsing as CallToolResult envelope.
	var ctr mcp.CallToolResult
	if err := json.Unmarshal(raw, &ctr); err == nil && len(ctr.Content) > 0 {
		if ctr.IsError {
			text := ctr.Content[0].Text
			return nil, fmt.Errorf("tool error: %s", text)
		}

		// Extract text from first content block.
		text := ctr.Content[0].Text
		if text == "" {
			return map[string]any{"result": ""}, nil
		}

		if value, ok := parseEmbeddedJSONText(text); ok {
			switch typed := value.(type) {
			case map[string]any:
				return typed, nil
			case []any:
				return map[string]any{"result": typed}, nil
			}
		}

		// Return raw text.
		return map[string]any{"result": text}, nil
	}

	// Fallback: try parsing raw result directly as a JSON object.
	var direct map[string]any
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct, nil
	}

	return map[string]any{"raw": string(raw)}, nil
}

func parseEmbeddedJSONText(text string) (any, bool) {
	raw, err := fiaccel.ExtractEmbeddedJSON([]byte(text))
	if err != nil {
		return nil, false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}
