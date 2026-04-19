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
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/crb2nu/loom/pkg/launchctl"
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
	// callTimeout bounds each JSON-RPC request.
	callTimeout time.Duration

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
		socketPath:  socketPath,
		logger:      logger,
		cbCooldown:  cbMinCooldown,
		callTimeout: 30 * time.Second,
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
	_ = launchctl.Start(context.Background(), "com.loom.daemon")
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

// Call sends a JSON-RPC request and returns the result. It handles
// auto-reconnection on transport errors and circuit-breaking on downstream
// server failures. Caller must NOT hold c.mu.
func (c *DaemonClient) Call(method string, params any) (json.RawMessage, error) {
	return c.CallWithTimeout(method, params, 0)
}

// CallWithTimeout is like Call but accepts an optional per-call timeout override.
// A zero or negative timeout uses the client's default callTimeout.
func (c *DaemonClient) CallWithTimeout(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Apply per-call timeout override while holding the lock.
	saved := c.callTimeout
	if timeout > 0 {
		c.callTimeout = timeout
	}
	defer func() { c.callTimeout = saved }()

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
// It skips any interleaved notifications from the daemon (e.g.
// notifications/tools/list_changed) and matches the response ID to
// prevent response-ordering bugs when notifications arrive between
// a request and its response.
func (c *DaemonClient) callLocked(method string, params any) (json.RawMessage, error) {
	if c.transport == nil {
		return nil, fmt.Errorf("not connected")
	}

	timeout := c.callTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	id := c.reqID.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := mcp.NewRequest(id, method, params)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if err := c.transport.Send(ctx, req); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	// Read messages until we get the response matching our request ID.
	// The daemon may send async notifications (e.g. tools/list_changed)
	// on the same transport; skip those to avoid response mismatches.
	idStr := fmt.Sprint(id)
	for {
		resp, err := c.transport.Recv(ctx)
		if err != nil {
			return nil, fmt.Errorf("recv %s (id %s): %w", method, idStr, err)
		}

		// Skip notifications (no ID, has Method).
		if resp.ID == nil && resp.Method != "" {
			continue
		}

		// Match response ID to our request.
		if fmt.Sprint(resp.ID) != idStr {
			c.logger.Debug("skipping mismatched response",
				"method", method, "expected_id", idStr, "got_id", fmt.Sprint(resp.ID))
			continue
		}

		if resp.Error != nil {
			return nil, fmt.Errorf("daemon error (%d): %s", resp.Error.Code, resp.Error.Message)
		}

		return resp.Result, nil
	}
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

// HealthDivergence represents a disagreement between the health monitor and the router.
type HealthDivergence struct {
	MonitorHealthy  bool   `json:"monitor_healthy"`
	RouterAvailable bool   `json:"router_available"`
	Reason          string `json:"reason"`
}

// HealthDivergenceEntry is a top-level divergence summary entry.
type HealthDivergenceEntry struct {
	Server string `json:"server"`
	Reason string `json:"reason"`
}

// ServerHealth contains local and hub health plus the target.
type ServerHealth struct {
	Local      HealthEntry       `json:"local"`
	Hub        HealthEntry       `json:"hub"`
	Monitor    *HealthEntry      `json:"monitor,omitempty"`
	Target     string            `json:"target"`
	Transport  string            `json:"transport,omitempty"` // ws, stdio, sse, ssh, or unavailable
	Divergence *HealthDivergence `json:"divergence,omitempty"`
}

// HealthResult holds the response from loom/health.
type HealthResult struct {
	Servers    map[string]ServerHealth `json:"servers"`
	Divergence []HealthDivergenceEntry `json:"divergence,omitempty"`
}

// ServerInfo describes a registered MCP server.
type ServerInfo struct {
	Name        string   `json:"name"`
	Categories  []string `json:"categories,omitempty"`
	Description string   `json:"description,omitempty"`
	Running     bool     `json:"running"`
	ToolCount   int      `json:"tool_count,omitempty"`
	Transport   string   `json:"transport,omitempty"`
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
	raw, err := c.Call("loom/status", nil)
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
	raw, err := c.Call("loom/health", nil)
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
	raw, err := c.Call("loom/servers", nil)
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
	raw, err := c.Call("loom/tools", nil)
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
	raw, err := c.Call("loom/tunnels", nil)
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
	raw, err := c.Call("loom/cache/stats", nil)
	if err != nil {
		return nil, err
	}
	var result CacheStatsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal cache stats: %w", err)
	}
	return &result, nil
}

// CostStatsResult holds the response from loom/cost-stats.
type CostStatsResult struct {
	Enabled   bool              `json:"enabled"`
	Reason    string            `json:"reason,omitempty"`
	Timestamp string            `json:"timestamp,omitempty"`
	ByAgent   []CostAgentUsage  `json:"by_agent,omitempty"`
	ByServer  []CostServerUsage `json:"by_server,omitempty"`
	Totals    CostTotals        `json:"totals"`
}

// CostAgentUsage summarizes per-agent cost data.
type CostAgentUsage struct {
	AgentID       string `json:"agent_id"`
	CallCount     int64  `json:"call_count"`
	ErrorCount    int64  `json:"error_count"`
	DeniedCount   int64  `json:"denied_count"`
	CachedCount   int64  `json:"cached_count"`
	TotalDuration int64  `json:"total_duration_ms"`
}

// CostServerUsage summarizes per-server cost data.
type CostServerUsage struct {
	Server        string `json:"server"`
	CallCount     int64  `json:"call_count"`
	ErrorCount    int64  `json:"error_count"`
	TotalDuration int64  `json:"total_duration_ms"`
}

// CostTotals summarizes aggregate cost data.
type CostTotals struct {
	CallCount     int64 `json:"call_count"`
	ErrorCount    int64 `json:"error_count"`
	DeniedCount   int64 `json:"denied_count"`
	CachedCount   int64 `json:"cached_count"`
	TotalDuration int64 `json:"total_duration_ms"`
}

// CostStats returns cost/usage tracking statistics.
func (c *DaemonClient) CostStats() (*CostStatsResult, error) {
	raw, err := c.Call("loom/cost-stats", nil)
	if err != nil {
		return nil, err
	}
	var result CostStatsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal cost-stats: %w", err)
	}
	return &result, nil
}

// RBACConfigResult holds the response from loom/rbac-config.
type RBACConfigResult struct {
	Enabled       bool                `json:"enabled"`
	AuditEnabled  bool                `json:"audit_enabled"`
	DefaultPolicy string              `json:"default_policy,omitempty"`
	Roles         []RBACRoleInfo      `json:"roles,omitempty"`
	Bindings      []RBACBindingInfo   `json:"bindings,omitempty"`
	GlobalDeny    []string            `json:"global_deny,omitempty"`
	RateLimits    []RBACRateLimitInfo `json:"rate_limits,omitempty"`
	DeniedCount   int                 `json:"denied_count,omitempty"`
	RecentDenied  []RBACDeniedEntry   `json:"recent_denied,omitempty"`
}

// RBACRoleInfo describes a role definition.
type RBACRoleInfo struct {
	Name  string   `json:"name"`
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// RBACBindingInfo describes an agent-to-role binding.
type RBACBindingInfo struct {
	AgentID   string `json:"agent_id,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	Role      string `json:"role"`
}

// RBACRateLimitInfo describes a rate limit rule.
type RBACRateLimitInfo struct {
	AgentID           string `json:"agent_id,omitempty"`
	Tool              string `json:"tool,omitempty"`
	RequestsPerMinute int    `json:"requests_per_minute"`
}

// RBACDeniedEntry describes a recently denied tool call.
type RBACDeniedEntry struct {
	AgentID   string `json:"agent_id"`
	Server    string `json:"server"`
	Tool      string `json:"tool"`
	Role      string `json:"role,omitempty"`
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}

// RBACConfig returns RBAC configuration and recent denied calls.
func (c *DaemonClient) RBACConfig() (*RBACConfigResult, error) {
	raw, err := c.Call("loom/rbac-config", nil)
	if err != nil {
		return nil, err
	}
	var result RBACConfigResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal rbac-config: %w", err)
	}
	return &result, nil
}

// OTelStatusResult holds the response from loom/otel-status.
type OTelStatusResult struct {
	OTLPEndpoint         string          `json:"otlp_endpoint"`
	OTLPConfigured       bool            `json:"otlp_configured"`
	LogFormat            string          `json:"log_format"`
	JSONLogsEnabled      bool            `json:"json_logs_enabled"`
	TracedServers        int             `json:"traced_servers"`
	TotalServers         int             `json:"total_servers"`
	TraceCoverage        string          `json:"trace_coverage"`
	RuntimeConfigured    bool            `json:"runtime_otlp_configured"`
	RuntimeEnabled       bool            `json:"runtime_otlp_enabled"`
	RuntimeEndpoint      string          `json:"runtime_otlp_endpoint"`
	RuntimeProtocol      string          `json:"runtime_otlp_protocol"`
	RuntimeServiceName   string          `json:"runtime_otlp_service_name"`
	RuntimeSampleRate    float64         `json:"runtime_otlp_sample_rate"`
	RuntimeError         string          `json:"runtime_otlp_error"`
	RuntimeMeterEnabled  bool            `json:"runtime_meter_enabled"`
	RuntimeTraceSurfaces map[string]bool `json:"runtime_trace_surfaces"`
	RuntimeTraceCoverage string          `json:"runtime_trace_coverage"`
}

// OTelStatus returns observability/OTel configuration status.
func (c *DaemonClient) OTelStatus() (*OTelStatusResult, error) {
	raw, err := c.Call("loom/otel-status", nil)
	if err != nil {
		return nil, err
	}
	var result OTelStatusResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal otel-status: %w", err)
	}
	return &result, nil
}

// buildToolCallParams builds the tools/call params map for a tool invocation.
// Caller must provide `timeout`: the deadline to propagate to the daemon via
// the `_timeout` params field so the daemon can cap its own recv deadline and
// release the per-server call mutex when the caller gives up. Without this
// propagation the daemon falls back to `LOOM_DAEMON_TOOL_TIMEOUT` (60s),
// starving other callers with shorter deadlines queued on the same mutex.
// A zero `timeout` omits `_timeout` and lets the daemon use its own default.
func buildToolCallParams(name string, args map[string]any, timeout time.Duration) map[string]any {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}
	if timeout > 0 {
		params["_timeout"] = timeout.String()
	}
	return params
}

// defaultToolTimeout returns the client's configured default RPC timeout,
// held behind the daemon-client mutex.
func (c *DaemonClient) defaultToolTimeout() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.callTimeout
}

// CallTool invokes an MCP tool through the daemon's tools/call method.
// The client's default call timeout is propagated to the daemon so the
// per-server mutex is held no longer than the caller is willing to wait.
func (c *DaemonClient) CallTool(name string, args map[string]any) (json.RawMessage, error) {
	return c.Call("tools/call", buildToolCallParams(name, args, c.defaultToolTimeout()))
}

// CallToolWithTimeout is like CallTool but uses a per-call timeout override.
// See buildToolCallParams for why the timeout is propagated to the daemon.
func (c *DaemonClient) CallToolWithTimeout(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error) {
	return c.CallWithTimeout("tools/call", buildToolCallParams(name, args, timeout), timeout)
}
