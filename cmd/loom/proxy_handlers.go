// proxy_handlers.go — MCP method dispatch, protocol negotiation, and request handlers.
package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func handleProxyInitialize(msg *mcp.Message) *mcp.Message {
	result := mcp.InitializeResult{
		ProtocolVersion: negotiateProxyProtocolVersion(msg.Params),
		Capabilities: mcp.Capabilities{
			Tools:     &mcp.ToolsCapability{ListChanged: true},
			Resources: &mcp.ResourcesCapability{ListChanged: true},
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

func negotiateProxyProtocolVersion(raw json.RawMessage) string {
	defaultVersion := mcp.ProtocolVersion20250618

	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(raw) == 0 || string(raw) == "null" {
		return defaultVersion
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return defaultVersion
	}

	requested := strings.TrimSpace(params.ProtocolVersion)
	switch requested {
	case mcp.ProtocolVersion20250618, mcp.ProtocolVersion:
		return requested
	default:
		return defaultVersion
	}
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
	}{Tools: filterProxyTools(cachedResult.Tools, agentHintGlobal, toolProfileGlobal, maxToolsGlobal)}

	return mcp.NewResponse(msg.ID, result)
}

func handleProxyToolsCall(ctx context.Context, daemon mcp.Transport, msg *mcp.Message) (*mcp.Message, error) {
	if policyResp, blocked := proxyFluxPolicyResponse(msg); blocked {
		return policyResp, nil
	}

	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, err.Error()), nil
	}

	// Track activity for adaptive session keepalive interval.
	lastProxyCallTime.Store(time.Now().UnixNano())

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

	// Some MCP clients prefix tool names with the connected server name
	// (for example "loom/agent_context__agent_session_start"). Strip that
	// transport-level namespace before resolving the underlying tool.
	normalizedToolName := stripProxyToolNamespace(params.Name)

	// Parse server__toolname format
	parts := splitToolName(normalizedToolName)
	var serverName, toolName string

	if len(parts) == 2 {
		serverName, toolName = parts[0], parts[1]
	} else {
		// Smart Routing: Let the daemon resolve it
		toolName = normalizedToolName
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

	// Derive tool-level timeout from arguments for long-running tools.
	toolTimeout := proxyDeriveTimeoutFromArguments(params.Arguments)

	// Per-server timeout override from routing.timeouts config.
	if serverName != "" && proxyRoutingTimeouts != nil {
		if raw, ok := proxyRoutingTimeouts[serverName]; ok {
			if d, err := time.ParseDuration(raw); err == nil && d > toolTimeout {
				toolTimeout = d
			}
		}
	}

	callPayload := map[string]any{
		"server":    serverName,
		"tool":      toolName,
		"method":    "tools/call",
		"params":    json.RawMessage(paramsJSON),
		"arguments": params.Arguments,
		"agent_id":  resolvedAgentID,
	}
	if toolTimeout > 0 {
		callPayload["_timeout"] = toolTimeout.String()
	}
	if proxySessionID != "" && !proxySessionDisabled {
		callPayload["session_id"] = proxySessionID
	}
	callReq, _ := mcp.NewRequest(msg.ID, "loom/call", callPayload)

	resp, err := proxyDaemonRoundTripWithTimeout(ctx, daemon, callReq, "tools/call", toolTimeout)
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

func splitToolName(name string) []string {
	// Split on first "__" occurrence
	for i := 0; i < len(name)-1; i++ {
		if name[i] == '_' && name[i+1] == '_' {
			return []string{name[:i], name[i+2:]}
		}
	}
	return []string{name}
}

func stripProxyToolNamespace(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	if prefix, suffix, ok := strings.Cut(trimmed, "/"); ok && prefix != "" && suffix != "" {
		return suffix
	}
	return trimmed
}
