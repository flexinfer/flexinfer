// proxy.go contains the MCP proxy server that bridges stdio to the daemon.
package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/daemon"
	"github.com/crb2nu/loom/pkg/env"
)

// agentHintGlobal stores the --agent-hint flag value for proxy-level heartbeats.
var agentHintGlobal string

// remoteURLGlobal stores the --remote flag value for remote daemon connections.
var remoteURLGlobal string

// remoteTokenGlobal stores the --remote-token flag value.
var remoteTokenGlobal string

// lastHeartbeat tracks the unix nanos of the last heartbeat to rate-limit goroutine spawning.
var lastHeartbeat atomic.Int64

// heartbeatIntervalNanos is the minimum interval between heartbeats in nanoseconds.
var heartbeatIntervalNanos int64 = int64(5 * time.Second)

// proxyNamespace caches inferred git namespace for proxy heartbeat session bootstrap.
var (
	proxyNamespaceOnce sync.Once
	proxyNamespace     string
	proxyIdentityOnce  sync.Once
	proxyAgentID       string
)

// Proxy session state for daemon lease/epoch tracking.
var (
	proxySessionID       string
	proxyDaemonEpoch     int64
	proxySessionDisabled bool
)

const (
	defaultProxyControlRPCTimeout = 30 * time.Second
	defaultProxyToolRPCTimeout    = 60 * time.Second
	defaultProxyInitRPCTimeout    = 10 * time.Second
)

// proxyAutostartCooldown is the minimum interval between daemon autostart
// attempts. Package-level var so tests can override.
var proxyAutostartCooldown = 10 * time.Second

// proxyAutostartMaxAttempts caps the total number of autostart attempts
// to prevent process churn storms when the daemon is permanently unavailable.
var proxyAutostartMaxAttempts = 5

// proxyTransportError wraps daemon transport send/recv errors so the main loop
// can deterministically identify and reset broken connections without relying
// on string-based error classification.
type proxyTransportError struct {
	err error
}

func (e *proxyTransportError) Error() string { return e.err.Error() }
func (e *proxyTransportError) Unwrap() error { return e.err }

// runProxyWithHint wraps runProxy with agent-hint and remote support.
// When agentHint is set, the proxy fires async heartbeats to the HUD
// on each tool call, providing universal presence for hookless platforms.
func runProxyWithHint(socketPath, agentHint, remoteURL, remoteToken string) error {
	agentHintGlobal = agentHint
	remoteURLGlobal = remoteURL
	remoteTokenGlobal = remoteToken
	return runProxy(socketPath)
}

// runProxy runs loom as an MCP server, bridging stdio to the daemon
func runProxy(socketPath string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown on SIGTERM/SIGINT.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case sig := <-sigCh:
			fmt.Fprintf(os.Stderr, "loom proxy: received %s, shutting down\n", sig)
			cancel()
		case <-ctx.Done():
		}
	}()
	defer signal.Stop(sigCh)

	// Idle timeout for orphan prevention. Resets on every inbound message.
	// Uses proxyIdleExitTimeout() which supports LOOM_PROXY_IDLE_EXIT_SECONDS
	// env var, file config, and a 30s minimum bound.
	idleTimeout := proxyIdleExitTimeout()
	var idleTimer *time.Timer
	if idleTimeout > 0 {
		idleTimer = time.AfterFunc(idleTimeout, func() {
			fmt.Fprintf(os.Stderr, "loom proxy: idle timeout (%s), shutting down\n", idleTimeout)
			cancel()
		})
		defer idleTimer.Stop()
	}

	// Load file config once for proxy-side settings.
	if fileCfg, err := daemon.LoadConfigFile(); err == nil {
		proxyConfigGlobal = fileCfg.Proxy
		if fileCfg.Proxy.HeartbeatIntervalMs > 0 {
			heartbeatIntervalNanos = int64(time.Duration(fileCfg.Proxy.HeartbeatIntervalMs) * time.Millisecond)
		}
	}

	// Check session disable env var.
	proxySessionDisabled = os.Getenv("LOOM_PROXY_SESSION_DISABLE") == "1"

	// Create stdio transport for client communication
	stdio := mcp.NewStdioTransport(os.Stdin, os.Stdout)

	var daemon mcp.Transport
	var daemonConn net.Conn

	// Bounded autostart: replaces sync.Once so the proxy can re-attempt
	// daemon startup after crashes, while capping total attempts.
	autostartAttempts := 0
	lastAutostartAttempt := time.Time{}
	autostart := func() {
		if autostartAttempts >= proxyAutostartMaxAttempts {
			return
		}
		if !lastAutostartAttempt.IsZero() && time.Since(lastAutostartAttempt) < proxyAutostartCooldown {
			return
		}
		autostartAttempts++
		lastAutostartAttempt = time.Now()
		// Never write to stdout in proxy mode (it would corrupt the MCP stream).
		if err := startDaemonInBackground(socketPath); err != nil {
			fmt.Fprintf(os.Stderr, "loom proxy: daemon autostart failed (attempt %d/%d): %v\n",
				autostartAttempts, proxyAutostartMaxAttempts, err)
		}
	}

	dialWithTimeout := func(timeout time.Duration) (net.Conn, error) {
		ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		d := net.Dialer{Timeout: timeout}
		return d.DialContext(ctxWithTimeout, "unix", socketPath)
	}

	ensureDaemon := func() error {
		if daemon != nil {
			return nil
		}

		// Remote mode: connect via Streamable HTTP
		if remoteURLGlobal != "" {
			token := remoteTokenGlobal
			if token == "" {
				token = os.Getenv("LOOM_REMOTE_TOKEN")
			}

			headers := make(map[string]string)
			if token != "" {
				headers["Authorization"] = "Bearer " + token
			}

			transport := mcp.NewStreamableHTTPTransport(mcp.StreamableHTTPClientConfig{
				Endpoint: remoteURLGlobal,
				Headers:  headers,
			})

			// Initialize the remote connection
			initReq, _ := mcp.NewRequest(1, "initialize", mcp.InitializeParams{
				ProtocolVersion: mcp.ProtocolVersion20250618,
				Capabilities:    mcp.Capabilities{},
				ClientInfo:      mcp.ClientInfo{Name: "loom-proxy", Version: version},
			})
			if err := proxyRPCSend(ctx, transport, initReq, "initialize"); err != nil {
				transport.Close()
				return fmt.Errorf("remote initialize: %w", err)
			}
			if _, err := proxyRPCRecv(ctx, transport, "initialize"); err != nil {
				transport.Close()
				return fmt.Errorf("remote initialize recv: %w", err)
			}
			// Send initialized notification
			_ = proxyRPCSend(ctx, transport, &mcp.Message{JSONRPC: "2.0", Method: "notifications/initialized"}, "notifications/initialized")

			daemon = transport

			// Open a proxy session with the remote daemon (non-blocking, non-fatal).
			proxyOpenSession(ctx, transport)

			return nil
		}

		// Local mode: connect via Unix socket
		if daemonConn != nil {
			return nil
		}
		// Keep proxy responsive during MCP startup: try a fast connect first,
		// then attempt an autostart and retry briefly.
		conn, err := dialWithTimeout(250 * time.Millisecond)
		if err != nil {
			autostart()
			deadline := time.Now().Add(3 * time.Second)
			var lastErr error
			for time.Now().Before(deadline) {
				conn, lastErr = dialWithTimeout(250 * time.Millisecond)
				if lastErr == nil {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if lastErr != nil {
				return lastErr
			}
		}
		transport := mcp.NewStdioTransport(conn, conn)

		// Must initialize the daemon connection
		initReq, _ := mcp.NewRequest(1, "initialize", mcp.InitializeParams{
			ProtocolVersion: mcp.ProtocolVersion,
			Capabilities:    mcp.Capabilities{},
			ClientInfo:      mcp.ClientInfo{Name: "loom-proxy", Version: version},
		})
		if err := proxyRPCSend(ctx, transport, initReq, "initialize"); err != nil {
			_ = transport.Close()
			_ = conn.Close()
			return err
		}
		if _, err := proxyRPCRecv(ctx, transport, "initialize"); err != nil {
			_ = transport.Close()
			_ = conn.Close()
			return err
		}
		// Send initialized notification
		_ = proxyRPCSend(ctx, transport, &mcp.Message{JSONRPC: "2.0", Method: "notifications/initialized"}, "notifications/initialized")
		daemonConn = conn
		daemon = transport
		autostartAttempts = 0 // Reset budget on successful connection.

		// Open a proxy session with the daemon (non-blocking, non-fatal).
		proxyOpenSession(ctx, transport)

		return nil
	}

	// resetTransport atomically clears both daemon transport and underlying
	// socket connection so the next ensureDaemon call reconnects cleanly.
	resetTransport := func() {
		// Attempt graceful session close before tearing down transport.
		if daemon != nil && proxySessionID != "" && !proxySessionDisabled {
			proxyCloseSession(ctx, daemon)
		}
		if daemon != nil {
			daemon.Close()
			daemon = nil
		}
		if daemonConn != nil {
			daemonConn.Close()
			daemonConn = nil
		}
	}

	// Cleanup daemon state on exit (signal, idle timeout, or client disconnect).
	defer resetTransport()

	// Main message loop
	for {
		msg, err := stdio.Recv(ctx)
		if err != nil {
			return nil // Client disconnected or shutdown signal
		}

		// Reset idle timer on activity.
		if idleTimer != nil {
			idleTimer.Reset(idleTimeout)
		}

		var resp *mcp.Message

		switch msg.Method {
		case "initialize":
			// Some clients treat an initialize failure as a hard crash. Respond even if the daemon
			// is temporarily unavailable; we can connect (or autostart) lazily on the first call.
			autostart()
			resp = handleProxyInitialize(msg)

		case "notifications/initialized":
			// No response needed for notifications
			autostart()
			continue

		case "resources/templates/list":
			// No daemon needed - returns static proxy-native templates
			resp, err = handleProxyResourceTemplatesList(ctx, nil, msg)

		case "resources/list":
			// Try daemon first for full resource list, fallback to built-in only
			if derr := ensureDaemon(); derr != nil {
				// Fallback: return built-in loom:// resources only
				resp = handleProxyResourcesListBuiltinOnly(msg)
			} else {
				resp, err = handleProxyResourcesList(ctx, daemon, msg)
			}

		default:
			if err := ensureDaemon(); err != nil {
				stdio.Send(ctx, mcp.NewErrorResponse(msg.ID, mcp.InternalError, "connect to daemon failed: "+err.Error()))
				continue
			}

			switch msg.Method {
			case "tools/list":
				resp, err = handleProxyToolsList(ctx, daemon, msg)

			case "tools/call":
				resp, err = handleProxyToolsCall(ctx, daemon, msg)

			case "resources/read":
				resp, err = handleProxyResourcesRead(ctx, daemon, msg)

			case "prompts/list":
				resp, err = handleProxyPromptsList(ctx, daemon, msg)

			case "prompts/get":
				resp, err = handleProxyPromptsGet(ctx, daemon, msg)

			default:
				// Forward unknown methods to daemon
				resp, err = forwardToDaemon(ctx, daemon, msg)
			}
		}

		if err != nil {
			// Transport errors from proxyRPCSend/proxyRPCRecv are wrapped as
			// proxyTransportError, providing deterministic reset classification
			// without relying on string matching.
			var transportErr *proxyTransportError
			if errors.As(err, &transportErr) {
				resetTransport()
			}
			resp = mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error())
		}

		if resp != nil {
			if err := stdio.Send(ctx, resp); err != nil {
				return fmt.Errorf("send response: %w", err)
			}
		}
	}
}

func startDaemonInBackground(socketPath string) error {
	home, _ := os.UserHomeDir()
	logFile := filepath.Join(home, ".config", "loom", "logs", "loomd-proxy.out")
	return daemon.EnsureRunning(daemon.StartConfig{
		SocketPath: socketPath,
		LogFile:    logFile,
		Timeout:    3 * time.Second,
	})
}

func handleProxyInitialize(msg *mcp.Message) *mcp.Message {
	result := mcp.InitializeResult{
		ProtocolVersion: mcp.ProtocolVersion,
		Capabilities: mcp.Capabilities{
			Tools:     &mcp.ToolsCapability{},
			Resources: &mcp.ResourcesCapability{},
			Prompts:   &mcp.PromptsCapability{},
		},
		ServerInfo: mcp.ServerInfo{
			Name:    "loom",
			Version: version,
		},
		Instructions: "Loom MCP proxy - aggregates tools from multiple servers. Tool names are namespaced as server__toolname.",
	}
	resp, _ := mcp.NewResponse(msg.ID, result)
	return resp
}

func handleProxyToolsList(ctx context.Context, daemon mcp.Transport, msg *mcp.Message) (*mcp.Message, error) {
	// Use the daemon's cached tool aggregation endpoint
	toolsReq, _ := mcp.NewRequest(1, "loom/tools", nil)
	toolsResp, err := proxyDaemonRoundTrip(ctx, daemon, toolsReq, "tools/list")
	if err != nil {
		return nil, err
	}
	if toolsResp.Error != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, toolsResp.Error.Message), nil
	}

	// Extract just the tools array for the MCP response
	var cachedResult struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(toolsResp.Result, &cachedResult); err != nil {
		return nil, err
	}

	result := struct {
		Tools []mcp.Tool `json:"tools"`
	}{Tools: cachedResult.Tools}

	return mcp.NewResponse(msg.ID, result)
}

func handleProxyToolsCall(ctx context.Context, daemon mcp.Transport, msg *mcp.Message) (*mcp.Message, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, err.Error()), nil
	}

	// Proxy-level heartbeat: fire async heartbeat on each tool call for
	// platforms with zero hook support (Kilocode, Antigravity, etc.).
	// Rate-limited to avoid spawning unbounded goroutines.
	if agentHintGlobal != "" {
		now := time.Now().UnixNano()
		prev := lastHeartbeat.Load()
		if now-prev >= heartbeatIntervalNanos {
			if lastHeartbeat.CompareAndSwap(prev, now) {
				go proxyHeartbeat(agentHintGlobal)
			}
		}
	}

	// Parse server__toolname format
	parts := splitToolName(params.Name)
	var serverName, toolName string

	if len(parts) == 2 {
		serverName, toolName = parts[0], parts[1]
	} else {
		// Smart Routing: Let the daemon resolve it
		toolName = params.Name
	}

	// Forward to appropriate server via daemon
	resolvedAgentID := strings.TrimSpace(agentHintGlobal)
	if resolvedAgentID != "" {
		resolvedAgentID, _ = resolveProxyIdentity(resolvedAgentID)
	}
	toolCallParams := map[string]any{
		"name": toolName,
	}
	if len(params.Arguments) > 0 {
		var args map[string]any
		_ = json.Unmarshal(params.Arguments, &args)
		toolCallParams["arguments"] = args
	}
	paramsJSON, _ := json.Marshal(toolCallParams)

	callPayload := map[string]any{
		"server":    serverName,
		"tool":      toolName,
		"method":    "tools/call",
		"params":    json.RawMessage(paramsJSON),
		"arguments": params.Arguments,
		"agent_id":  resolvedAgentID,
	}
	if proxySessionID != "" && !proxySessionDisabled {
		callPayload["session_id"] = proxySessionID
	}
	callReq, _ := mcp.NewRequest(msg.ID, "loom/call", callPayload)

	resp, err := proxyDaemonRoundTrip(ctx, daemon, callReq, "tools/call")
	if err != nil {
		return nil, err
	}

	// Guardrail: prevent oversized tool responses from breaking MCP clients.
	// Some clients impose line/response size limits; large logs can cause parse failures.
	if resp.Error == nil && len(resp.Result) > 0 {
		var result mcp.CallToolResult
		if err := json.Unmarshal(resp.Result, &result); err == nil {
			if truncateCallToolResult(&result, proxyMaxToolResultBytes(), proxyMaxImageResultBytes()) {
				return mcp.NewResponse(resp.ID, result)
			}
		}
	}

	return resp, nil
}

func handleProxyResourcesList(ctx context.Context, daemon mcp.Transport, msg *mcp.Message) (*mcp.Message, error) {
	// Preferred fast path: daemon-native cached resources endpoint.
	resourcesReq, _ := mcp.NewRequest(1, "loom/resources", nil)
	resourcesResp, err := proxyDaemonRoundTrip(ctx, daemon, resourcesReq, "resources/list")
	if err != nil {
		return nil, err
	}
	if resourcesResp.Error == nil {
		var cachedResult struct {
			Resources []mcp.Resource `json:"resources"`
		}
		if err := json.Unmarshal(resourcesResp.Result, &cachedResult); err == nil {
			merged := make([]mcp.Resource, 0, len(proxyBuiltinResources())+len(cachedResult.Resources))
			seen := make(map[string]struct{}, len(proxyBuiltinResources())+len(cachedResult.Resources))
			for _, r := range proxyBuiltinResources() {
				merged = append(merged, r)
				seen[r.URI] = struct{}{}
			}
			for _, r := range cachedResult.Resources {
				if _, ok := seen[r.URI]; ok {
					continue
				}
				merged = append(merged, r)
				seen[r.URI] = struct{}{}
			}
			return mcp.NewResponse(msg.ID, struct {
				Resources []mcp.Resource `json:"resources"`
			}{Resources: merged})
		}
	}

	// Backward-compatibility fallback for older daemons without loom/resources.
	return handleProxyResourcesListLegacyFanout(ctx, daemon, msg)
}

func proxyBuiltinResources() []mcp.Resource {
	return []mcp.Resource{
		{
			URI:         "loom://servers",
			Name:        "Loom servers",
			Description: "List MCP servers managed by the loom daemon",
			MimeType:    "application/json",
		},
		{
			URI:         "loom://tools",
			Name:        "Loom tools",
			Description: "Cached aggregated tools from loom daemon",
			MimeType:    "application/json",
		},
		{
			URI:         "loom://tools/index",
			Name:        "Loom tools index",
			Description: "Paginated tools inventory index for loom daemon tools",
			MimeType:    "application/json",
		},
		{
			URI:         "loom://health",
			Name:        "Loom health",
			Description: "Health summary for all servers (local/hub) managed by loom",
			MimeType:    "application/json",
		},
		{
			URI:         "loom://config",
			Name:        "Loom config",
			Description: "Active profile and daemon configuration summary",
			MimeType:    "application/json",
		},
	}
}

func handleProxyResourcesListLegacyFanout(ctx context.Context, daemon mcp.Transport, msg *mcp.Message) (*mcp.Message, error) {
	// Legacy behavior: aggregate resources from all servers.
	serversReq, _ := mcp.NewRequest(2, "loom/servers", nil)
	serversResp, err := proxyDaemonRoundTrip(ctx, daemon, serversReq, "resources/list")
	if err != nil {
		return nil, err
	}

	var serversResult struct {
		Servers []struct {
			Name    string `json:"name"`
			Running *bool  `json:"running,omitempty"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(serversResp.Result, &serversResult); err != nil {
		return nil, err
	}

	allResources := proxyBuiltinResources()
	for _, server := range serversResult.Servers {
		// Avoid cold-starting every configured server just to enumerate resources.
		// When the daemon explicitly reports running=false, skip probing.
		if server.Running != nil && !*server.Running {
			continue
		}

		req, _ := mcp.NewRequest(3, "loom/call", map[string]any{
			"server": server.Name,
			"method": "resources/list",
		})
		resp, err := proxyDaemonRoundTrip(ctx, daemon, req, "resources/list")
		if err != nil || resp.Error != nil {
			continue
		}

		var result struct {
			Resources []mcp.Resource `json:"resources"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			continue
		}

		for _, r := range result.Resources {
			r.URI = server.Name + "__" + r.URI
			allResources = append(allResources, r)
		}
	}

	result := struct {
		Resources []mcp.Resource `json:"resources"`
	}{Resources: allResources}

	return mcp.NewResponse(msg.ID, result)
}

// handleProxyResourcesListBuiltinOnly returns only the built-in loom:// resources
// without requiring a daemon connection. Used as fallback when daemon is unavailable.
func handleProxyResourcesListBuiltinOnly(msg *mcp.Message) *mcp.Message {
	allResources := proxyBuiltinResources()

	result := struct {
		Resources []mcp.Resource `json:"resources"`
	}{Resources: allResources}

	resp, _ := mcp.NewResponse(msg.ID, result)
	return resp
}

func handleProxyResourcesRead(ctx context.Context, daemon mcp.Transport, msg *mcp.Message) (*mcp.Message, error) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, err.Error()), nil
	}

	nextID := 1
	callDaemon := func(method string, callParams any) (*mcp.Message, error) {
		req, err := mcp.NewRequest(nextID, method, callParams)
		if err != nil {
			return nil, err
		}
		nextID++
		return proxyDaemonRoundTrip(ctx, daemon, req, method)
	}

	renderJSON := func(v any) (string, error) {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return "", err
		}
		return truncateResourceText(string(b), proxyMaxResourceBytes()), nil
	}
	renderJSONNoTruncate := func(v any) (string, error) {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	}

	if strings.HasPrefix(params.URI, "loom://") {
		var payload any
		switch params.URI {
		case "loom://servers":
			resp, err := callDaemon("loom/servers", nil)
			if err != nil {
				return nil, err
			}
			if resp.Error != nil {
				return resp, nil
			}
			if err := json.Unmarshal(resp.Result, &payload); err != nil {
				payload = map[string]any{"ok": false, "error": "unmarshal loom/servers response: " + err.Error()}
			}

		case "loom://tools":
			resp, err := callDaemon("loom/tools", nil)
			if err != nil {
				return nil, err
			}
			if resp.Error != nil {
				return resp, nil
			}
			if err := json.Unmarshal(resp.Result, &payload); err != nil {
				payload = map[string]any{"ok": false, "error": "unmarshal loom/tools response: " + err.Error()}
			}

		case "loom://health":
			resp, err := callDaemon("loom/health", nil)
			if err != nil {
				return nil, err
			}
			if resp.Error != nil {
				return resp, nil
			}
			if err := json.Unmarshal(resp.Result, &payload); err != nil {
				payload = map[string]any{"ok": false, "error": "unmarshal loom/health response: " + err.Error()}
			}

		case "loom://config":
			payload = make(map[string]any)
			for _, m := range []string{"loom/status", "loom/profile", "loom/config-hash"} {
				resp, err := callDaemon(m, nil)
				if err != nil {
					payload.(map[string]any)[m] = map[string]any{"ok": false, "error": err.Error()}
					continue
				}
				if resp.Error != nil {
					payload.(map[string]any)[m] = map[string]any{"ok": false, "error": resp.Error.Message}
					continue
				}
				var v any
				if err := json.Unmarshal(resp.Result, &v); err != nil {
					payload.(map[string]any)[m] = map[string]any{"ok": false, "error": "unmarshal response: " + err.Error()}
					continue
				}
				payload.(map[string]any)[m] = v
			}

		default:
			server, page, ok, parseErr := parseLoomToolsInventoryURI(params.URI)
			if !ok {
				return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "unknown loom resource URI"), nil
			}
			if parseErr != nil {
				return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, parseErr.Error()), nil
			}

			resp, err := callDaemon("loom/tools", nil)
			if err != nil {
				return nil, err
			}
			if resp.Error != nil {
				return resp, nil
			}

			var toolsResult struct {
				Tools []mcp.Tool `json:"tools"`
			}
			if err := json.Unmarshal(resp.Result, &toolsResult); err != nil {
				return mcp.NewErrorResponse(msg.ID, mcp.InternalError, "unmarshal loom/tools response: "+err.Error()), nil
			}

			paged, err := buildToolInventoryPage(toolsResult.Tools, server, page, proxyToolPageSize(), true)
			if err != nil {
				return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, err.Error()), nil
			}

			text, err := renderJSONNoTruncate(paged)
			if err != nil {
				return mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error()), nil
			}

			return mcp.NewResponse(msg.ID, map[string]any{
				"contents": []any{
					map[string]any{
						"uri":      params.URI,
						"mimeType": "application/json",
						"text":     text,
					},
				},
			})
		}

		text, err := renderJSON(payload)
		if err != nil {
			return mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error()), nil
		}

		return mcp.NewResponse(msg.ID, map[string]any{
			"contents": []any{
				map[string]any{
					"uri":      params.URI,
					"mimeType": "application/json",
					"text":     text,
				},
			},
		})
	}

	parts := splitToolName(params.URI)
	if len(parts) != 2 {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "URI must be in format server__uri"), nil
	}
	serverName, uri := parts[0], parts[1]

	req, _ := mcp.NewRequest(msg.ID, "loom/call", map[string]any{
		"server": serverName,
		"method": "resources/read",
		"params": map[string]any{"uri": uri},
	})

	return proxyDaemonRoundTrip(ctx, daemon, req, "resources/read")
}

func handleProxyResourceTemplatesList(ctx context.Context, daemon mcp.Transport, msg *mcp.Message) (*mcp.Message, error) {
	// Some MCP clients (e.g., Codex CLI) probe for resource templates on startup.
	// Our underlying MCP servers are primarily tool-oriented and many do not
	// implement templates. Broadcasting this request to all servers can hang if a
	// server fails to respond to unknown methods.
	//
	// Instead, return proxy-native templates that don't require downstream calls.
	return mcp.NewResponse(msg.ID, map[string]any{
		"resourceTemplates": []any{
			map[string]any{
				"name":        "loom_servers",
				"description": "List MCP servers managed by the loom daemon",
				"mimeType":    "application/json",
				"uriTemplate": "loom://servers",
			},
			map[string]any{
				"name":        "loom_tools",
				"description": "Cached aggregated tools from loom daemon",
				"mimeType":    "application/json",
				"uriTemplate": "loom://tools",
			},
			map[string]any{
				"name":        "loom_tools_index",
				"description": "Paginated tools inventory index for loom daemon tools",
				"mimeType":    "application/json",
				"uriTemplate": "loom://tools/index",
			},
			map[string]any{
				"name":        "loom_tools_page",
				"description": "Paginated tools inventory page",
				"mimeType":    "application/json",
				"uriTemplate": "loom://tools/page/{page}",
			},
			map[string]any{
				"name":        "loom_tools_server_page",
				"description": "Paginated tools inventory for a specific server",
				"mimeType":    "application/json",
				"uriTemplate": "loom://tools/server/{server}/page/{page}",
			},
			map[string]any{
				"name":        "loom_health",
				"description": "Health summary for all servers (local/hub) managed by loom",
				"mimeType":    "application/json",
				"uriTemplate": "loom://health",
			},
			map[string]any{
				"name":        "loom_config",
				"description": "Active profile and daemon configuration summary",
				"mimeType":    "application/json",
				"uriTemplate": "loom://config",
			},
		},
	})
}

func handleProxyPromptsList(ctx context.Context, daemon mcp.Transport, msg *mcp.Message) (*mcp.Message, error) {
	serversReq, _ := mcp.NewRequest(1, "loom/servers", nil)
	serversResp, err := proxyDaemonRoundTrip(ctx, daemon, serversReq, "prompts/list")
	if err != nil {
		return nil, err
	}

	var serversResult struct {
		Servers []struct {
			Name string `json:"name"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(serversResp.Result, &serversResult); err != nil {
		return nil, err
	}

	allPrompts := make([]mcp.Prompt, 0)
	for _, server := range serversResult.Servers {
		req, _ := mcp.NewRequest(2, "loom/call", map[string]any{
			"server": server.Name,
			"method": "prompts/list",
		})
		resp, err := proxyDaemonRoundTrip(ctx, daemon, req, "prompts/list")
		if err != nil || resp.Error != nil {
			continue
		}

		var result struct {
			Prompts []mcp.Prompt `json:"prompts"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			continue
		}

		for _, p := range result.Prompts {
			p.Name = server.Name + "__" + p.Name
			allPrompts = append(allPrompts, p)
		}
	}

	result := struct {
		Prompts []mcp.Prompt `json:"prompts"`
	}{Prompts: allPrompts}

	return mcp.NewResponse(msg.ID, result)
}

func handleProxyPromptsGet(ctx context.Context, daemon mcp.Transport, msg *mcp.Message) (*mcp.Message, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, err.Error()), nil
	}

	parts := splitToolName(params.Name)
	if len(parts) != 2 {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "prompt name must be in format server__promptname"), nil
	}
	serverName, promptName := parts[0], parts[1]

	req, _ := mcp.NewRequest(msg.ID, "loom/call", map[string]any{
		"server": serverName,
		"method": "prompts/get",
		"params": map[string]any{
			"name":      promptName,
			"arguments": params.Arguments,
		},
	})

	return proxyDaemonRoundTrip(ctx, daemon, req, "prompts/get")
}

func forwardToDaemon(ctx context.Context, daemon mcp.Transport, msg *mcp.Message) (*mcp.Message, error) {
	return proxyDaemonRoundTrip(ctx, daemon, msg, msg.Method)
}

func proxyDaemonRoundTrip(ctx context.Context, daemon mcp.Transport, req *mcp.Message, operation string) (*mcp.Message, error) {
	if err := proxyRPCSend(ctx, daemon, req, operation); err != nil {
		return nil, err
	}
	return proxyRPCRecv(ctx, daemon, operation)
}

func proxyRPCSend(ctx context.Context, transport mcp.Transport, msg *mcp.Message, operation string) error {
	timeout := proxyRPCTimeoutForOperation(operation)
	sendCtx, cancel := context.WithTimeout(ctx, timeout)
	err := transport.Send(sendCtx, msg)
	cancel()
	if err != nil {
		return proxyRPCPhaseError(operation, "send", timeout, err)
	}
	return nil
}

func proxyRPCRecv(ctx context.Context, transport mcp.Transport, operation string) (*mcp.Message, error) {
	timeout := proxyRPCTimeoutForOperation(operation)
	recvCtx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := transport.Recv(recvCtx)
	cancel()
	if err != nil {
		return nil, proxyRPCPhaseError(operation, "recv", timeout, err)
	}
	return resp, nil
}

func proxyRPCTimeoutForOperation(operation string) time.Duration {
	op := strings.TrimSpace(operation)
	switch op {
	case "initialize":
		return normalizePositiveDuration(env.Duration("LOOM_PROXY_INIT_TIMEOUT", defaultProxyInitRPCTimeout), defaultProxyInitRPCTimeout)
	case "tools/call":
		return normalizePositiveDuration(env.Duration("LOOM_PROXY_TOOL_TIMEOUT", defaultProxyToolRPCTimeout), defaultProxyToolRPCTimeout)
	default:
		return normalizePositiveDuration(env.Duration("LOOM_PROXY_CONTROL_TIMEOUT", defaultProxyControlRPCTimeout), defaultProxyControlRPCTimeout)
	}
}

func proxyRPCPhaseError(operation, phase string, timeout time.Duration, err error) error {
	op := strings.TrimSpace(operation)
	if op == "" {
		op = "daemon rpc"
	}
	var inner error
	if isRPCTimeout(err) {
		inner = fmt.Errorf("%s timeout during %s after %s (recoverable: proxy will reconnect and retry on the next request): %w", op, phase, timeout, err)
	} else {
		inner = fmt.Errorf("%s failed during %s: %w", op, phase, err)
	}
	return &proxyTransportError{err: inner}
}

func shouldResetDaemonTransport(err error) bool {
	if err == nil {
		return false
	}
	// Primary path: structured error classification via errors.Is/errors.As.
	if isRPCTimeout(err) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.ENOTCONN) {
		return true
	}
	// Any network operation error indicates a broken transport.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	// Defense-in-depth: string fallbacks for non-standard error wrapping.
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "use of closed network connection") ||
		strings.Contains(lower, "unexpected eof")
}

func splitToolName(name string) []string {
	// Split on first "__" occurrence
	for i := 0; i < len(name)-1; i++ {
		if name[i] == '_' && name[i+1] == '_' {
			return []string{name[:i], name[i+2:]}
		}
	}
	return []string{name}
}

// proxyOpenSession opens a session with the daemon after a successful initialize.
// Non-blocking and non-fatal: older daemons that don't support sessions will return
// method_not_found, which is silently ignored.
func proxyOpenSession(ctx context.Context, transport mcp.Transport) {
	if proxySessionDisabled {
		return
	}

	openParams := map[string]any{
		"version":  version,
		"host_pid": strconv.Itoa(os.Getpid()),
	}
	if agentHintGlobal != "" {
		openParams["agent_hint"] = agentHintGlobal
	}
	if proxySessionID != "" {
		openParams["prior_session_id"] = proxySessionID
	}

	req, _ := mcp.NewRequest(99, "loom/session/open", openParams)

	sendCtx, sendCancel := context.WithTimeout(ctx, 2*time.Second)
	err := transport.Send(sendCtx, req)
	sendCancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "loom proxy: session open send failed: %v\n", err)
		return
	}

	recvCtx, recvCancel := context.WithTimeout(ctx, 2*time.Second)
	resp, err := transport.Recv(recvCtx)
	recvCancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "loom proxy: session open recv failed: %v\n", err)
		return
	}

	// Older daemons return method_not_found -- silently ignore.
	if resp.Error != nil {
		return
	}

	var result struct {
		SessionID   string `json:"session_id"`
		DaemonEpoch int64  `json:"daemon_epoch"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return
	}

	proxySessionID = result.SessionID
	proxyDaemonEpoch = result.DaemonEpoch
}

// proxyCloseSession sends a graceful session close to the daemon with a short timeout.
// The prior session ID is preserved so the next open can pass it for resume tracking.
func proxyCloseSession(ctx context.Context, transport mcp.Transport) {
	if proxySessionID == "" {
		return
	}

	req, _ := mcp.NewRequest(98, "loom/session/close", map[string]any{
		"session_id": proxySessionID,
	})

	closeCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	_ = transport.Send(closeCtx, req)
	// Best-effort recv; ignore errors (transport may already be broken).
	_, _ = transport.Recv(closeCtx)

	// proxySessionID is preserved as prior_session_id for the next open call.
}

// proxyHeartbeat fires an async heartbeat to the HUD for proxy-level agent identification.
// This provides universal heartbeat coverage for any agent using loom proxy.
func proxyHeartbeat(agentType string) {
	resolvedAgentID, resolvedAgentType := resolveProxyIdentity(agentType)
	proxyNamespaceOnce.Do(func() {
		proxyNamespace = inferGitNamespace()
	})
	bodyMap := map[string]any{
		"agent_id":       resolvedAgentID,
		"status":         "active",
		"agent_type":     resolvedAgentType,
		"ensure_session": true,
	}
	if strings.TrimSpace(proxyNamespace) != "" {
		bodyMap["namespace"] = proxyNamespace
	}
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Try port file first, fall back to default.
	port := "3333"
	if data, err := os.ReadFile(filepath.Join(os.TempDir(), "loom-hud.port")); err == nil {
		if p := strings.TrimSpace(string(data)); p != "" {
			port = p
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://127.0.0.1:"+port+"/api/agent/heartbeat",
		strings.NewReader(string(body)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func resolveProxyIdentity(agentHint string) (agentID, agentType string) {
	agentType = strings.TrimSpace(agentHint)
	if agentType == "" {
		agentType = "proxy"
	}

	proxyIdentityOnce.Do(func() {
		if override := strings.TrimSpace(os.Getenv("LOOM_PROXY_AGENT_ID")); override != "" {
			proxyAgentID = override
			return
		}

		typePart := sanitizeIDPart(agentType)
		if typePart == "" {
			typePart = "proxy"
		}

		host, err := os.Hostname()
		if err != nil {
			host = "host"
		}
		hostPart := sanitizeIDPart(host)
		if hostPart == "" {
			hostPart = "host"
		}

		pidPart := strconv.Itoa(os.Getpid())
		nsHash := namespaceDigest(inferGitNamespace())
		if nsHash != "" {
			proxyAgentID = fmt.Sprintf("%s-%s-%s-%s", typePart, hostPart, pidPart, nsHash)
			return
		}
		proxyAgentID = fmt.Sprintf("%s-%s-%s", typePart, hostPart, pidPart)
	})

	return proxyAgentID, agentType
}

func sanitizeIDPart(input string) string {
	if input == "" {
		return ""
	}
	normalized := strings.ToLower(strings.TrimSpace(input))
	var b strings.Builder
	b.Grow(len(normalized))
	prevDash := false
	for _, r := range normalized {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func namespaceDigest(namespace string) string {
	if strings.TrimSpace(namespace) == "" {
		return ""
	}
	sum := sha1.Sum([]byte(namespace))
	return hex.EncodeToString(sum[:])[:8]
}
