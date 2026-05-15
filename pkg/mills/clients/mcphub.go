package clients

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

// MCPHubConfig captures connection settings for the loom MCP hub. The
// operator reads these from env at startup and constructs one shared
// MCPHubClient that the devbox / handoff / worktree client wrappers
// share. The hub fans out to in-tree MCP servers (mcp-devbox,
// mcp-agent-context, etc.) keyed by their registered name.
type MCPHubConfig struct {
	// HubURL is the websocket endpoint, e.g. "wss://mcp.flexinfer.ai/ws".
	HubURL string
	// Profile selects which server bundle the hub exposes. Defaults to
	// "loom-mills" when empty; production deployments configure a
	// dedicated profile that whitelists the tools the operator needs.
	Profile string
	// Token is a Bearer auth token (forwarded as Authorization header).
	// Optional in dev environments.
	Token string
	// CFAccessClientID / CFAccessClientSecret authenticate the operator
	// to the Cloudflare Access tunnel sitting in front of the hub.
	// Either both must be set or both empty.
	CFAccessClientID     string
	CFAccessClientSecret string
	// ConnectTimeout caps the websocket dial. Default 10s.
	ConnectTimeout time.Duration
	// CallTimeout caps a single tools/call round trip. Default 10m.
	// Sized for devbox_quality_gate, which runs fmt + lint + test
	// against a real workspace and routinely exceeds a minute.
	CallTimeout time.Duration
	// ClientName / ClientVersion are sent on initialize. Default
	// "loom-mills-operator" / "0.1.0".
	ClientName    string
	ClientVersion string
}

// MCPHubClient is the operator's gateway to in-cluster MCP tool calls.
// One instance is shared by every wrapper client (DevboxClient,
// HandoffClient, WorktreeAllocator). It manages per-server websocket
// transports lazily: the first CallTool to a server dials + initializes;
// subsequent calls reuse the connection.
type MCPHubClient struct {
	cfg MCPHubConfig

	mu          sync.Mutex
	transports  map[string]mcp.Transport // serverName → transport
	initialized map[string]bool          // serverName → has the session been initialized
	nextID      int64

	// dial is the function used to obtain a transport. Production
	// uses the mcp-go websocket client; tests inject a fake. Set via
	// WithDialer for testability.
	dial func(ctx context.Context, serverName string) (mcp.Transport, error)
}

// NewMCPHubClient validates config and returns a ready client. It does
// NOT dial any server — connections are opened lazily on first
// CallTool. An empty HubURL is invalid in production but tests use
// WithDialer to install a fake without a URL.
func NewMCPHubClient(cfg MCPHubConfig) (*MCPHubClient, error) {
	if cfg.HubURL == "" {
		return nil, errors.New("mcphub: HubURL required")
	}
	return newMCPHubClientWithDefaults(cfg, nil), nil
}

// newMCPHubClientWithDefaults is shared by NewMCPHubClient and
// newTestMCPHubClient — it applies defaults and installs the production
// dial function when none is provided.
func newMCPHubClientWithDefaults(cfg MCPHubConfig, dial func(ctx context.Context, serverName string) (mcp.Transport, error)) *MCPHubClient {
	if cfg.Profile == "" {
		cfg.Profile = "loom-mills"
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if cfg.CallTimeout == 0 {
		cfg.CallTimeout = 10 * time.Minute
	}
	if cfg.ClientName == "" {
		cfg.ClientName = "loom-mills-operator"
	}
	if cfg.ClientVersion == "" {
		cfg.ClientVersion = "0.1.0"
	}
	c := &MCPHubClient{
		cfg:         cfg,
		transports:  make(map[string]mcp.Transport),
		initialized: make(map[string]bool),
		dial:        dial,
	}
	if c.dial == nil {
		c.dial = c.realDial
	}
	return c
}

// realDial opens a websocket transport to the hub for the named server.
func (c *MCPHubClient) realDial(ctx context.Context, serverName string) (mcp.Transport, error) {
	wsCfg := mcp.WebSocketConfig{
		URL:            c.cfg.HubURL,
		Profile:        c.cfg.Profile,
		ConnectTimeout: c.cfg.ConnectTimeout,
		ClientInfo: mcp.ClientInfo{
			Name:    c.cfg.ClientName,
			Version: c.cfg.ClientVersion,
		},
	}
	if c.cfg.Token != "" {
		wsCfg.Headers = map[string]string{"Authorization": "Bearer " + c.cfg.Token}
	}
	wsCfg.CFAccessClientID = c.cfg.CFAccessClientID
	wsCfg.CFAccessClientSecret = c.cfg.CFAccessClientSecret
	return mcp.NewWebSocketTransport(ctx, wsCfg, serverName)
}

// CallTool invokes the named tool on serverName via the hub and returns
// the raw text content of the first content block. The vast majority of
// in-tree MCP tools return a single JSON text block; callers Unmarshal
// it into their own typed response. Returns an error when the tool
// itself reports IsError=true (the body is included in the error).
//
// Resilience: a cached transport that fails with a transport-level error
// (close 1006, broken pipe, unexpected EOF, etc.) is invalidated and the
// call is retried exactly once with a freshly-dialed connection. This
// covers the case where the gateway tore the connection down between
// calls (idle deadline, gateway restart, ping/relay race) without
// surfacing an error to the operator until the next Send/Recv. Without
// this, the cached broken transport poisons every subsequent call until
// the operator process is restarted.
func (c *MCPHubClient) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (string, error) {
	if c == nil {
		return "", errors.New("mcphub: client nil")
	}
	if serverName == "" {
		return "", errors.New("mcphub: serverName required")
	}
	if toolName == "" {
		return "", errors.New("mcphub: toolName required")
	}

	body, err := c.callOnce(ctx, serverName, toolName, args)
	if err == nil || !isTransportError(err) {
		return body, err
	}
	// Transport-level failure on the cached connection: drop it and
	// retry once with a fresh dial. Most close-1006/broken-pipe cases
	// recover on the second attempt because each new WebSocket forks
	// a new mcp-devbox subprocess on the hub side.
	c.invalidate(serverName)
	return c.callOnce(ctx, serverName, toolName, args)
}

// callOnce performs a single tools/call round trip without retry. On
// any error the transport is left in c.transports (so CallTool can
// decide whether to invalidate it based on isTransportError).
func (c *MCPHubClient) callOnce(ctx context.Context, serverName, toolName string, args map[string]any) (string, error) {
	transport, err := c.transportFor(ctx, serverName)
	if err != nil {
		return "", err
	}

	callCtx, cancel := context.WithTimeout(ctx, c.cfg.CallTimeout)
	defer cancel()

	id := atomic.AddInt64(&c.nextID, 1)
	params, err := json.Marshal(mcp.CallToolParams{Name: toolName, Arguments: args})
	if err != nil {
		return "", fmt.Errorf("mcphub: marshal params: %w", err)
	}
	if err := transport.Send(callCtx, &mcp.Message{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params:  params,
	}); err != nil {
		return "", fmt.Errorf("mcphub: send %s/%s: %w", serverName, toolName, err)
	}

	for {
		msg, err := transport.Recv(callCtx)
		if err != nil {
			return "", fmt.Errorf("mcphub: recv %s/%s: %w", serverName, toolName, err)
		}
		if !idEqual(msg.ID, id) {
			// Out-of-band message (notification / response to another in-flight
			// call). The hub may multiplex; skip and keep reading.
			continue
		}
		if msg.Error != nil {
			return "", fmt.Errorf("mcphub: %s/%s: %s (code=%d)", serverName, toolName, msg.Error.Message, msg.Error.Code)
		}
		var res mcp.CallToolResult
		if err := json.Unmarshal(msg.Result, &res); err != nil {
			return "", fmt.Errorf("mcphub: decode %s/%s result: %w", serverName, toolName, err)
		}
		text := firstTextContent(res.Content)
		if res.IsError {
			return text, fmt.Errorf("mcphub: %s/%s reported error: %s", serverName, toolName, truncateText(text, 512))
		}
		return text, nil
	}
}

// invalidate closes and forgets the cached transport for serverName so
// the next CallTool dials a fresh one. Idempotent; safe to call when
// no transport is cached.
func (c *MCPHubClient) invalidate(serverName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.transports[serverName]; ok {
		_ = t.Close()
		delete(c.transports, serverName)
	}
	delete(c.initialized, serverName)
}

// isTransportError returns true for the WebSocket / TCP failure modes
// that mean the cached connection is dead and we should redial. We
// match on error text rather than wrapped types because the gorilla
// websocket library and the mcp-go transport both wrap errors as
// strings before they reach us. Conservative on purpose — JSON-RPC
// errors and tool-reported errors (IsError=true) must NOT match.
func isTransportError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	s := err.Error()
	for _, needle := range []string{
		"websocket: close",         // gorilla close-frame errors (1006, 1001, etc.)
		"unexpected EOF",           // half-closed read
		"broken pipe",              // EPIPE on Send after peer closed
		"connection reset by peer", // RST mid-flight
		"use of closed network connection",
		"transport closed", // mcp-go / fake transport
		"i/o timeout",      // ReadDeadline expiry
		"EOF",              // bare io.EOF wrapped as string
	} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// transportFor returns the transport for serverName, dialing + initializing
// on first use. Subsequent calls reuse the existing connection.
func (c *MCPHubClient) transportFor(ctx context.Context, serverName string) (mcp.Transport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.transports[serverName]; ok && c.initialized[serverName] {
		return t, nil
	}
	t, ok := c.transports[serverName]
	if !ok {
		dialCtx, cancel := context.WithTimeout(ctx, c.cfg.ConnectTimeout)
		defer cancel()
		var err error
		t, err = c.dial(dialCtx, serverName)
		if err != nil {
			return nil, fmt.Errorf("mcphub: dial %s: %w", serverName, err)
		}
		c.transports[serverName] = t
	}
	if !c.initialized[serverName] {
		if err := c.initialize(ctx, t); err != nil {
			_ = t.Close()
			delete(c.transports, serverName)
			return nil, fmt.Errorf("mcphub: initialize %s: %w", serverName, err)
		}
		c.initialized[serverName] = true
	}
	return t, nil
}

// initialize sends the MCP initialize handshake. Required before any
// tools/call traffic per the MCP spec.
func (c *MCPHubClient) initialize(ctx context.Context, t mcp.Transport) error {
	id := atomic.AddInt64(&c.nextID, 1)
	params, err := json.Marshal(mcp.InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    mcp.Capabilities{},
		ClientInfo: mcp.ClientInfo{
			Name:    c.cfg.ClientName,
			Version: c.cfg.ClientVersion,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal init params: %w", err)
	}
	initCtx, cancel := context.WithTimeout(ctx, c.cfg.ConnectTimeout)
	defer cancel()
	if err := t.Send(initCtx, &mcp.Message{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "initialize",
		Params:  params,
	}); err != nil {
		return err
	}
	for {
		msg, err := t.Recv(initCtx)
		if err != nil {
			return err
		}
		if !idEqual(msg.ID, id) {
			continue
		}
		if msg.Error != nil {
			return fmt.Errorf("initialize: %s (code=%d)", msg.Error.Message, msg.Error.Code)
		}
		// Send the initialized notification per the protocol so the
		// server starts dispatching tool calls.
		_ = t.Send(initCtx, &mcp.Message{
			JSONRPC: "2.0",
			Method:  "notifications/initialized",
		})
		return nil
	}
}

// Close releases every per-server transport. Safe to call multiple times.
func (c *MCPHubClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var first error
	for name, t := range c.transports {
		if err := t.Close(); err != nil && first == nil {
			first = fmt.Errorf("close %s: %w", name, err)
		}
	}
	c.transports = make(map[string]mcp.Transport)
	c.initialized = make(map[string]bool)
	return first
}

// idEqual compares two JSON-RPC ids that may be number or string.
// Tests pass int64 ids; the wire protocol may serialize them as
// float64 after JSON round-trip. We coerce to string for comparison.
func idEqual(a, b any) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// firstTextContent extracts the .text field of the first text-typed
// content block. Most loom-tree tools emit a single such block.
func firstTextContent(content []mcp.Content) string {
	for _, c := range content {
		if c.Type == "text" && c.Text != "" {
			return c.Text
		}
	}
	return ""
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(truncated)"
}

// MCPHubConfigFromEnv reads MCPHubConfig from the standard LOOM_*
// + CF_ACCESS_* env variables. Returns nil + ok=false when the hub
// isn't configured (HubURL empty), so callers can branch cleanly.
func MCPHubConfigFromEnv(getenv func(string) string) (MCPHubConfig, bool) {
	if getenv == nil {
		return MCPHubConfig{}, false
	}
	url := strings.TrimSpace(getenv("LOOM_MCP_HUB_URL"))
	if url == "" {
		return MCPHubConfig{}, false
	}
	return MCPHubConfig{
		HubURL:               url,
		Profile:              strings.TrimSpace(getenv("LOOM_MCP_HUB_PROFILE")),
		Token:                strings.TrimSpace(getenv("LOOM_MCP_HUB_TOKEN")),
		CFAccessClientID:     strings.TrimSpace(getenv("CF_ACCESS_CLIENT_ID")),
		CFAccessClientSecret: strings.TrimSpace(getenv("CF_ACCESS_CLIENT_SECRET")),
	}, true
}
