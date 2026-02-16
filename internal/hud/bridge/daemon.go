// Package bridge provides a persistent IPC client for the loom daemon.
//
// Unlike the CLI's one-shot dial-send-receive pattern, DaemonClient maintains
// a connection and automatically reconnects with exponential backoff on failure.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// Circuit breaker states for downstream server failure detection.
type circuitState int

const (
	circuitClosed   circuitState = iota // Normal operation.
	circuitOpen                         // Failing — skip calls, wait for cooldown.
	circuitHalfOpen                     // Testing recovery with a single probe.
)

const (
	cbFailureThreshold = 3                // Consecutive failures before opening.
	cbMinCooldown      = 10 * time.Second // Initial cooldown when circuit opens.
	cbMaxCooldown      = 60 * time.Second // Maximum cooldown between retries.
)

// ErrCircuitOpen is returned when the circuit breaker is open and the
// cooldown has not elapsed. Callers should back off.
var ErrCircuitOpen = errors.New("circuit breaker open: downstream server unavailable")

// DaemonClient wraps a persistent connection to the loom daemon Unix socket.
// It serializes requests with a mutex and reconnects automatically on errors.
// A circuit breaker tracks consecutive "server unavailable" errors from the
// daemon (indicating a stale downstream connection) and triggers an automatic
// reload to clear them.
type DaemonClient struct {
	socketPath string
	conn       net.Conn
	transport  *mcp.StdioTransport
	mu         sync.Mutex
	reqID      atomic.Int64
	logger     *slog.Logger

	// Circuit breaker for downstream server failures.
	cbState       circuitState
	cbFailures    int
	cbLastFailure time.Time
	cbCooldown    time.Duration

	// Autostart rate-limit to avoid spamming launchctl on repeated failures.
	lastAutostart time.Time
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
		cbCooldown: cbMinCooldown,
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
		// Best-effort autostart for launchctl-managed daemons (e.g., the HUD TUI).
		// If the socket is missing/refused, try to start the service and retry once.
		if c.maybeAutostart(err) {
			retryCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			conn, err = (&net.Dialer{Timeout: 2 * time.Second}).DialContext(retryCtx, "unix", c.socketPath)
		}
		if err != nil {
			return fmt.Errorf("connect to daemon at %s: %w", c.socketPath, err)
		}
	}

	c.conn = conn
	c.transport = mcp.NewStdioTransport(conn, conn)
	c.logger.Debug("connected to daemon", "socket", c.socketPath)
	return nil
}

func (c *DaemonClient) maybeAutostart(err error) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	// Only autostart on "socket missing" or "connection refused" errors.
	if !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ECONNREFUSED) {
		return false
	}
	// Rate-limit to keep logs and launchctl quiet.
	if time.Since(c.lastAutostart) < 5*time.Second {
		return false
	}
	c.lastAutostart = time.Now()
	_ = exec.Command("launchctl", "start", "com.loom.daemon").Run() //nolint:noctx // best-effort
	return true
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
// auto-reconnection on transport errors and circuit-breaking on downstream
// server failures. Caller must NOT hold c.mu.
func (c *DaemonClient) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Circuit breaker: check state before making the call.
	if c.cbState == circuitOpen {
		if time.Since(c.cbLastFailure) < c.cbCooldown {
			return nil, ErrCircuitOpen
		}
		// Cooldown elapsed — transition to half-open for a probe.
		c.cbState = circuitHalfOpen
		c.logger.Info("circuit breaker half-open, probing downstream")
	}

	result, err := c.callLocked(method, params)
	if err != nil {
		// Only reconnect on transport-level failures. Daemon error responses
		// indicate the daemon is reachable and reconnecting would just retry
		// the same failing RPC, creating avoidable churn.
		if !isTransportError(err) {
			c.recordFailure(err)
			return nil, fmt.Errorf("%s: %w", method, err)
		}
		c.logger.Warn("daemon transport call failed; reconnecting", "method", method, "error", err)

		// Attempt reconnect and retry once.
		c.logger.Debug("call failed, attempting reconnect", "method", method, "error", err)
		if reconnErr := c.reconnect(); reconnErr != nil {
			c.recordFailure(err)
			return nil, fmt.Errorf("%s: %w (reconnect also failed: %v)", method, err, reconnErr)
		}
		result, err = c.callLocked(method, params)
		if err != nil {
			c.recordFailure(err)
			return nil, fmt.Errorf("%s after reconnect: %w", method, err)
		}
	}

	// Success — reset circuit breaker.
	if c.cbState != circuitClosed {
		c.logger.Info("circuit breaker closed, downstream recovered")
	}
	c.cbState = circuitClosed
	c.cbFailures = 0
	c.cbCooldown = cbMinCooldown
	return result, nil
}

// recordFailure tracks a downstream failure and opens the circuit breaker
// when the threshold is reached. Caller must hold c.mu.
func (c *DaemonClient) recordFailure(err error) {
	if !isServerUnavailable(err) {
		return
	}
	c.cbFailures++
	c.cbLastFailure = time.Now()

	if c.cbFailures >= cbFailureThreshold && c.cbState != circuitOpen {
		c.logger.Warn("circuit breaker open: downstream server unavailable",
			"failures", c.cbFailures, "cooldown", c.cbCooldown)
		c.cbState = circuitOpen
		c.triggerDaemonReload()
		// Double cooldown for next time (exponential backoff), capped.
		c.cbCooldown = min(c.cbCooldown*2, cbMaxCooldown)
	}
}

// isServerUnavailable checks if an error indicates the daemon's downstream
// MCP server connection is stale/broken.
func isServerUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "server unavailable") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "bad handshake")
}

// isTransportError checks if the error is from socket/transport I/O rather
// than an application-level daemon RPC error.
func isTransportError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	msg := err.Error()
	// Daemon RPC error responses mean we successfully talked to the daemon.
	// Even if the message mentions "broken pipe", that refers to a downstream
	// server transport, not the HUD<->daemon socket.
	if strings.Contains(msg, "daemon error") {
		return false
	}

	return strings.Contains(msg, "not connected") ||
		strings.Contains(msg, "send:") ||
		strings.Contains(msg, "recv:") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "use of closed network connection")
}

// triggerDaemonReload sends a loom/reload to the daemon to clear stale
// server connections. Caller must hold c.mu.
func (c *DaemonClient) triggerDaemonReload() {
	c.logger.Info("triggering daemon reload to clear stale server connections")
	if _, err := c.callLocked("loom/reload", nil); err != nil {
		c.logger.Warn("daemon reload failed", "error", err)
	}
}

// CircuitOpen reports whether the circuit breaker is currently open.
// Monitors can check this to skip unnecessary polling.
func (c *DaemonClient) CircuitOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cbState == circuitOpen
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
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema mcp.InputSchema `json:"inputSchema"`
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
