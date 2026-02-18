// proxy.go contains the MCP proxy server that bridges stdio to the daemon.
package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	ctx := context.Background()

	// Load file config once for proxy-side settings.
	if fileCfg, err := daemon.LoadConfigFile(); err == nil {
		proxyConfigGlobal = fileCfg.Proxy
		if fileCfg.Proxy.HeartbeatIntervalMs > 0 {
			heartbeatIntervalNanos = int64(time.Duration(fileCfg.Proxy.HeartbeatIntervalMs) * time.Millisecond)
		}
	}

	// Create stdio transport for client communication
	stdio := mcp.NewStdioTransport(os.Stdin, os.Stdout)

	var daemon mcp.Transport
	var daemonConn net.Conn

	var autostartOnce sync.Once
	autostart := func() {
		autostartOnce.Do(func() {
			// Never write to stdout in proxy mode (it would corrupt the MCP stream).
			if err := startDaemonInBackground(socketPath); err != nil {
				fmt.Fprintf(os.Stderr, "loom proxy: daemon autostart failed: %v\n", err)
			}
		})
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
			if err := transport.Send(ctx, initReq); err != nil {
				transport.Close()
				return fmt.Errorf("remote initialize: %w", err)
			}
			if _, err := transport.Recv(ctx); err != nil {
				transport.Close()
				return fmt.Errorf("remote initialize recv: %w", err)
			}
			// Send initialized notification
			transport.Send(ctx, &mcp.Message{JSONRPC: "2.0", Method: "notifications/initialized"})

			daemon = transport
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
		daemonConn = conn
		daemon = mcp.NewStdioTransport(daemonConn, daemonConn)

		// Must initialize the daemon connection
		initReq, _ := mcp.NewRequest(1, "initialize", mcp.InitializeParams{
			ProtocolVersion: mcp.ProtocolVersion,
			Capabilities:    mcp.Capabilities{},
			ClientInfo:      mcp.ClientInfo{Name: "loom-proxy", Version: version},
		})
		if err := daemon.Send(ctx, initReq); err != nil {
			return err
		}
		if _, err := daemon.Recv(ctx); err != nil {
			return err
		}
		// Send initialized notification
		daemon.Send(ctx, &mcp.Message{JSONRPC: "2.0", Method: "notifications/initialized"})

		return nil
	}

	// Main message loop
	for {
		msg, err := stdio.Recv(ctx)
		if err != nil {
			return nil // Client disconnected
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
			// If it was a connection error, clear daemon so we reconnect next time
			if strings.Contains(err.Error(), "broken pipe") || strings.Contains(err.Error(), "EOF") {
				if daemonConn != nil {
					daemonConn.Close()
					daemonConn = nil
				}
				if daemon != nil {
					daemon.Close()
				}
				daemon = nil
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
	if err := daemon.Send(ctx, toolsReq); err != nil {
		return nil, err
	}
	toolsResp, err := daemon.Recv(ctx)
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

	callReq, _ := mcp.NewRequest(msg.ID, "loom/call", map[string]any{
		"server":    serverName,
		"tool":      toolName,
		"method":    "tools/call",
		"params":    json.RawMessage(paramsJSON),
		"arguments": params.Arguments,
		"agent_id":  resolvedAgentID,
	})

	if err := daemon.Send(ctx, callReq); err != nil {
		return nil, err
	}

	resp, err := daemon.Recv(ctx)
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
	// Similar to tools/list - aggregate resources from all servers
	serversReq, _ := mcp.NewRequest(1, "loom/servers", nil)
	if err := daemon.Send(ctx, serversReq); err != nil {
		return nil, err
	}
	serversResp, err := daemon.Recv(ctx)
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

	allResources := []mcp.Resource{
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
	for _, server := range serversResult.Servers {
		req, _ := mcp.NewRequest(2, "loom/call", map[string]any{
			"server": server.Name,
			"method": "resources/list",
		})
		if err := daemon.Send(ctx, req); err != nil {
			continue
		}
		resp, err := daemon.Recv(ctx)
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
	allResources := []mcp.Resource{
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
		if err := daemon.Send(ctx, req); err != nil {
			return nil, err
		}
		return daemon.Recv(ctx)
	}

	renderJSON := func(v any) (string, error) {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return "", err
		}
		return truncateResourceText(string(b), proxyMaxResourceBytes()), nil
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
			return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "unknown loom resource URI"), nil
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

	if err := daemon.Send(ctx, req); err != nil {
		return nil, err
	}

	return daemon.Recv(ctx)
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
	if err := daemon.Send(ctx, serversReq); err != nil {
		return nil, err
	}
	serversResp, err := daemon.Recv(ctx)
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
		if err := daemon.Send(ctx, req); err != nil {
			continue
		}
		resp, err := daemon.Recv(ctx)
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

	if err := daemon.Send(ctx, req); err != nil {
		return nil, err
	}

	return daemon.Recv(ctx)
}

func forwardToDaemon(ctx context.Context, daemon mcp.Transport, msg *mcp.Message) (*mcp.Message, error) {
	if err := daemon.Send(ctx, msg); err != nil {
		return nil, err
	}
	return daemon.Recv(ctx)
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
