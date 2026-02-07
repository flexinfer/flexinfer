// Package bridge provides a persistent IPC client for the loom daemon.
//
// Unlike the CLI's one-shot dial-send-receive pattern, DaemonClient maintains
// a connection and automatically reconnects with exponential backoff on failure.
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// DaemonClient wraps a persistent connection to the loom daemon Unix socket.
// It serializes requests with a mutex and reconnects automatically on errors.
type DaemonClient struct {
	socketPath string
	conn       net.Conn
	transport  *mcp.StdioTransport
	mu         sync.Mutex
	reqID      atomic.Int64
	logger     *slog.Logger
}

// NewDaemonClient creates a new client for the given daemon socket path.
// Call Connect() to establish the initial connection.
func NewDaemonClient(socketPath string, logger *slog.Logger) *DaemonClient {
	if logger == nil {
		logger = slog.Default()
	}
	c := &DaemonClient{
		socketPath: socketPath,
		logger:     logger,
	}
	c.reqID.Store(0)
	return c
}

// Connect establishes the initial connection to the daemon.
func (c *DaemonClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectLocked()
}

// connectLocked dials the daemon socket and sets up the transport.
// Caller must hold c.mu.
func (c *DaemonClient) connectLocked() error {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
		c.transport = nil
	}

	timeout := 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("connect to daemon at %s: %w", c.socketPath, err)
	}

	c.conn = conn
	c.transport = mcp.NewStdioTransport(conn, conn)
	c.logger.Debug("connected to daemon", "socket", c.socketPath)
	return nil
}

// Close closes the underlying connection.
func (c *DaemonClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		c.transport = nil
		return err
	}
	return nil
}

// reconnect attempts to reconnect, releasing the lock during backoff sleep
// so other goroutines are not blocked. Returns with the lock held.
func (c *DaemonClient) reconnect() error {
	const (
		baseDelay  = 200 * time.Millisecond
		maxDelay   = 5 * time.Second
		maxRetries = 5
	)

	delay := baseDelay
	for attempt := range maxRetries {
		c.logger.Debug("reconnecting to daemon", "attempt", attempt+1, "delay", delay)
		// Release lock during sleep so other callers can fail fast
		// rather than blocking for the entire backoff duration.
		c.mu.Unlock()
		time.Sleep(delay)
		c.mu.Lock()

		if err := c.connectLocked(); err != nil {
			c.logger.Warn("reconnect failed", "attempt", attempt+1, "error", err)
			delay = min(delay*2, maxDelay)
			continue
		}

		c.logger.Info("reconnected to daemon", "attempt", attempt+1)
		return nil
	}

	return fmt.Errorf("failed to reconnect after %d attempts", maxRetries)
}

// call sends a JSON-RPC request and returns the result. It handles
// auto-reconnection on transport errors. Caller must NOT hold c.mu.
func (c *DaemonClient) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	result, err := c.callLocked(method, params)
	if err != nil {
		// Attempt reconnect and retry once.
		c.logger.Debug("call failed, attempting reconnect", "method", method, "error", err)
		if reconnErr := c.reconnect(); reconnErr != nil {
			return nil, fmt.Errorf("%s: %w (reconnect also failed: %v)", method, err, reconnErr)
		}
		result, err = c.callLocked(method, params)
		if err != nil {
			return nil, fmt.Errorf("%s after reconnect: %w", method, err)
		}
	}
	return result, nil
}

// callLocked performs the actual send/recv. Caller must hold c.mu.
func (c *DaemonClient) callLocked(method string, params any) (json.RawMessage, error) {
	if c.transport == nil {
		return nil, fmt.Errorf("not connected")
	}

	id := c.reqID.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := mcp.NewRequest(id, method, params)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if err := c.transport.Send(ctx, req); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	resp, err := c.transport.Recv(ctx)
	if err != nil {
		return nil, fmt.Errorf("recv: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("daemon error (%d): %s", resp.Error.Code, resp.Error.Message)
	}

	return resp.Result, nil
}

// --- Typed RPC result structs ---

// StatusResult holds the response from loom/status.
type StatusResult struct {
	Running     bool     `json:"running"`
	Servers     int      `json:"servers"`
	ActiveConns int      `json:"activeConns"`
	IdleConns   int      `json:"idleConns"`
	Processes   []string `json:"processes"`
}

// HealthEntry describes the health of one endpoint (local or hub).
type HealthEntry struct {
	Healthy      bool    `json:"healthy"`
	ConsecFails  int     `json:"consecFails"`
	AvgLatencyMs float64 `json:"avgLatencyMs"`
	ErrorMessage string  `json:"errorMessage,omitempty"`
}

// ServerHealth contains local and hub health plus the target.
type ServerHealth struct {
	Local  HealthEntry `json:"local"`
	Hub    HealthEntry `json:"hub"`
	Target string      `json:"target"`
}

// HealthResult holds the response from loom/health.
type HealthResult struct {
	Servers map[string]ServerHealth `json:"servers"`
}

// ServerInfo describes a registered MCP server.
type ServerInfo struct {
	Name        string   `json:"name"`
	Categories  []string `json:"categories,omitempty"`
	Description string   `json:"description,omitempty"`
	Running     bool     `json:"running"`
}

// ServersResult holds the response from loom/servers.
type ServersResult struct {
	Servers []ServerInfo `json:"servers"`
}

// ToolInfo describes an aggregated MCP tool.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ToolsResult holds the response from loom/tools.
type ToolsResult struct {
	Tools       []ToolInfo `json:"tools"`
	CachedAt    string     `json:"cachedAt"`
	ServerCount int        `json:"serverCount"`
}

// TunnelsResult holds the response from loom/tunnels.
type TunnelsResult struct {
	Tunnels   map[string]any `json:"tunnels"`
	Total     int            `json:"total"`
	Connected int            `json:"connected"`
}

// CacheStatsResult holds the response from loom/cache/stats.
type CacheStatsResult struct {
	Enabled   bool  `json:"enabled"`
	Entries   int   `json:"entries"`
	SizeBytes int64 `json:"sizeBytes"`
	MaxBytes  int64 `json:"maxBytes"`
	TotalHits int64 `json:"totalHits"`
}

// --- Typed RPC methods ---

// Status returns the daemon status.
func (c *DaemonClient) Status() (*StatusResult, error) {
	raw, err := c.call("loom/status", nil)
	if err != nil {
		return nil, err
	}
	var result StatusResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal status: %w", err)
	}
	return &result, nil
}

// Health returns the health of all servers.
func (c *DaemonClient) Health() (*HealthResult, error) {
	raw, err := c.call("loom/health", nil)
	if err != nil {
		return nil, err
	}
	var result HealthResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal health: %w", err)
	}
	return &result, nil
}

// Servers returns the list of registered servers.
func (c *DaemonClient) Servers() (*ServersResult, error) {
	raw, err := c.call("loom/servers", nil)
	if err != nil {
		return nil, err
	}
	var result ServersResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal servers: %w", err)
	}
	return &result, nil
}

// Tools returns the aggregated tool list.
func (c *DaemonClient) Tools() (*ToolsResult, error) {
	raw, err := c.call("loom/tools", nil)
	if err != nil {
		return nil, err
	}
	var result ToolsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal tools: %w", err)
	}
	return &result, nil
}

// Tunnels returns the SSH tunnel status.
func (c *DaemonClient) Tunnels() (*TunnelsResult, error) {
	raw, err := c.call("loom/tunnels", nil)
	if err != nil {
		return nil, err
	}
	var result TunnelsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal tunnels: %w", err)
	}
	return &result, nil
}

// CacheStats returns response cache statistics.
func (c *DaemonClient) CacheStats() (*CacheStatsResult, error) {
	raw, err := c.call("loom/cache/stats", nil)
	if err != nil {
		return nil, err
	}
	var result CacheStatsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal cache stats: %w", err)
	}
	return &result, nil
}

// CallTool invokes an MCP tool through the daemon's tools/call method.
func (c *DaemonClient) CallTool(name string, args map[string]any) (json.RawMessage, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	return c.call("tools/call", params)
}
