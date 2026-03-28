// proxy.go contains the MCP proxy server that bridges stdio to the daemon.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/daemon"
)

// agentHintGlobal stores the --agent-hint flag value for proxy-level heartbeats.
var agentHintGlobal string

// remoteURLGlobal stores the --remote flag value for remote daemon connections.
var remoteURLGlobal string

// remoteTokenGlobal stores the --remote-token flag value.
var remoteTokenGlobal string

// toolProfileGlobal stores the optional per-proxy tool filter profile.
var toolProfileGlobal string

// maxToolsGlobal stores the optional per-proxy tool cap.
var maxToolsGlobal int

// lastHeartbeat tracks the unix nanos of the last heartbeat to rate-limit goroutine spawning.
var lastHeartbeat atomic.Int64

// heartbeatIntervalNanos is the minimum interval between heartbeats in nanoseconds.
var heartbeatIntervalNanos int64 = int64(5 * time.Second)

// sessionKeepaliveActive is the keepalive interval when the proxy is actively forwarding calls.
var sessionKeepaliveActive = 5 * time.Second

// sessionKeepaliveIdle is the keepalive interval when the proxy has been idle.
var sessionKeepaliveIdle = 30 * time.Second

// sessionIdleThreshold is how long since the last forwarded call before
// the proxy is considered idle for heartbeat interval purposes.
var sessionIdleThreshold = 30 * time.Second

// lastProxyCallTime tracks the last time a tool call was forwarded to the daemon (unix nanos).
var lastProxyCallTime atomic.Int64

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

// proxyStdioWriteMu serializes writes to the client stdio transport.
// Both the main loop (writing responses) and notification forwarding
// (from proxyDaemonRoundTrip) share the same stdout.
var proxyStdioWriteMu sync.Mutex

// proxyDaemonRPCMu serializes daemon request/response exchanges. The proxy uses
// a single daemon transport for both foreground tool calls and background
// session lifecycle RPCs, so concurrent send/recv pairs can steal each other's
// responses unless they are serialized.
var proxyDaemonRPCMu sync.Mutex

// proxyStdioTransport is the client-facing stdio transport, stored at package
// level so notification forwarding in proxyDaemonRoundTrip can write to it.
var proxyStdioTransport *mcp.StdioTransport

const (
	defaultProxyControlRPCTimeout = 30 * time.Second
	defaultProxyToolRPCTimeout    = 60 * time.Second
	defaultProxyInitRPCTimeout    = 10 * time.Second
	maxProxyToolRPCTimeout        = 15 * time.Minute
	autoProxyTimeoutBuffer        = 60 * time.Second
)

// proxyAutostartCooldown is the minimum interval between daemon autostart
// attempts. Package-level var so tests can override.
var proxyAutostartCooldown = 10 * time.Second

// proxyAutostartMaxAttempts caps the total number of autostart attempts
// to prevent process churn storms when the daemon is permanently unavailable.
var proxyAutostartMaxAttempts = 5

// runProxyWithHint wraps runProxy with agent-hint and remote support.
// When agentHint is set, the proxy fires async heartbeats to the HUD
// on each tool call, providing universal presence for hookless platforms.
func runProxyWithHint(socketPath, agentHint, remoteURL, remoteToken, toolProfile string, maxTools int) error {
	agentHintGlobal = agentHint
	remoteURLGlobal = remoteURL
	remoteTokenGlobal = remoteToken
	toolProfileGlobal = strings.TrimSpace(toolProfile)
	maxToolsGlobal = maxTools
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
		proxyRoutingTimeouts = fileCfg.Routing.Timeouts
		if fileCfg.Proxy.HeartbeatIntervalMs > 0 {
			heartbeatIntervalNanos = int64(time.Duration(fileCfg.Proxy.HeartbeatIntervalMs) * time.Millisecond)
			sessionKeepaliveActive = time.Duration(fileCfg.Proxy.HeartbeatIntervalMs) * time.Millisecond
		}
		if fileCfg.Proxy.IdleHeartbeatIntervalMs > 0 {
			sessionKeepaliveIdle = time.Duration(fileCfg.Proxy.IdleHeartbeatIntervalMs) * time.Millisecond
		}
	}

	// Check session disable env var.
	proxySessionDisabled = os.Getenv("LOOM_PROXY_SESSION_DISABLE") == "1"

	// Create stdio transport for client communication
	stdio := mcp.NewStdioTransport(os.Stdin, os.Stdout)
	proxyStdioTransport = stdio

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
			ProtocolVersion: mcp.ProtocolVersion20250618,
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

	// proxyMainLoopIdle tracks whether the main loop is blocked on stdio.Recv().
	// The session keepalive goroutine uses this to avoid concurrent daemon access.
	var proxyMainLoopIdle atomic.Bool

	// Session keepalive: adaptive timer that fires more often during active
	// use (sessionKeepaliveActive, default 5s) and backs off during idle
	// periods (sessionKeepaliveIdle, default 30s).
	go func() {
		timer := time.NewTimer(sessionKeepaliveActive)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if daemon == nil || proxySessionDisabled || proxySessionID == "" {
					timer.Reset(sessionKeepaliveIdle)
					continue
				}
				if !proxyMainLoopIdle.Load() {
					timer.Reset(sessionKeepaliveActive)
					continue // main loop is actively processing; skip to avoid race
				}
				proxySessionHeartbeat(ctx, daemon)
				timer.Reset(nextSessionKeepaliveInterval())
			}
		}
	}()

	// Main message loop
	for {
		proxyMainLoopIdle.Store(true)
		msg, err := stdio.Recv(ctx)
		proxyMainLoopIdle.Store(false)
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
			switch msg.Method {
			case "tools/list":
				if err := ensureDaemon(); err != nil {
					stdio.Send(ctx, mcp.NewErrorResponse(msg.ID, mcp.InternalError, "connect to daemon failed: "+err.Error()))
					continue
				}
				resp, err = handleProxyToolsList(ctx, daemon, msg)

			case "tools/call":
				lastProxyCallTime.Store(time.Now().UnixNano())
				if policyResp, blocked := proxyFluxPolicyResponse(msg); blocked {
					resp = policyResp
					break
				}
				if err := ensureDaemon(); err != nil {
					stdio.Send(ctx, mcp.NewErrorResponse(msg.ID, mcp.InternalError, "connect to daemon failed: "+err.Error()))
					continue
				}
				resp, err = handleProxyToolsCall(ctx, daemon, msg)

			case "resources/read":
				if err := ensureDaemon(); err != nil {
					stdio.Send(ctx, mcp.NewErrorResponse(msg.ID, mcp.InternalError, "connect to daemon failed: "+err.Error()))
					continue
				}
				resp, err = handleProxyResourcesRead(ctx, daemon, msg)

			case "prompts/list":
				if err := ensureDaemon(); err != nil {
					stdio.Send(ctx, mcp.NewErrorResponse(msg.ID, mcp.InternalError, "connect to daemon failed: "+err.Error()))
					continue
				}
				resp, err = handleProxyPromptsList(ctx, daemon, msg)

			case "prompts/get":
				if err := ensureDaemon(); err != nil {
					stdio.Send(ctx, mcp.NewErrorResponse(msg.ID, mcp.InternalError, "connect to daemon failed: "+err.Error()))
					continue
				}
				resp, err = handleProxyPromptsGet(ctx, daemon, msg)

			default:
				if err := ensureDaemon(); err != nil {
					stdio.Send(ctx, mcp.NewErrorResponse(msg.ID, mcp.InternalError, "connect to daemon failed: "+err.Error()))
					continue
				}
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
				// Retry once: reconnect and re-dispatch the original message.
				if reconnErr := ensureDaemon(); reconnErr == nil {
					var retryResp *mcp.Message
					var retryErr error
					switch msg.Method {
					case "tools/list":
						retryResp, retryErr = handleProxyToolsList(ctx, daemon, msg)
					case "tools/call":
						retryResp, retryErr = handleProxyToolsCall(ctx, daemon, msg)
					case "resources/list":
						retryResp, retryErr = handleProxyResourcesList(ctx, daemon, msg)
					case "resources/read":
						retryResp, retryErr = handleProxyResourcesRead(ctx, daemon, msg)
					case "prompts/list":
						retryResp, retryErr = handleProxyPromptsList(ctx, daemon, msg)
					case "prompts/get":
						retryResp, retryErr = handleProxyPromptsGet(ctx, daemon, msg)
					default:
						retryResp, retryErr = forwardToDaemon(ctx, daemon, msg)
					}
					if retryErr != nil {
						var retryTransportErr *proxyTransportError
						if errors.As(retryErr, &retryTransportErr) {
							resetTransport()
						}
						resp = mcp.NewErrorResponse(msg.ID, mcp.InternalError, retryErr.Error())
					} else {
						resp = retryResp
						err = nil
					}
				} else {
					resp = mcp.NewErrorResponse(msg.ID, mcp.InternalError,
						fmt.Sprintf("%v (reconnect failed: %v)", err, reconnErr))
				}
			}
			if err != nil && resp == nil {
				resp = mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error())
			}
		}

		if resp != nil {
			proxyStdioWriteMu.Lock()
			sendErr := stdio.Send(ctx, resp)
			proxyStdioWriteMu.Unlock()
			if sendErr != nil {
				return fmt.Errorf("send response: %w", sendErr)
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
