package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestProxy_ResourcesList_IncludesLoomServersResource(t *testing.T) {
	ctx := context.Background()
	client, server := mcp.NewPipeTransport()

	// Fake daemon: respond with cached resources from loom/resources.
	go func() {
		defer server.Close()
		req, _ := server.Recv(ctx)
		if req.Method == "loom/resources" {
			resp, _ := mcp.NewResponse(req.ID, map[string]any{
				"resources": []any{
					map[string]any{"uri": "loom://servers", "name": "Loom servers"},
					map[string]any{"uri": "loom://tools", "name": "Loom tools"},
					map[string]any{"uri": "loom://health", "name": "Loom health"},
					map[string]any{"uri": "loom://config", "name": "Loom config"},
					map[string]any{"uri": "alpha__r1", "name": "R1"},
				},
			})
			_ = server.Send(ctx, resp)
		}
	}()

	msg, err := mcp.NewRequest(1, "resources/list", map[string]any{})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := handleProxyResourcesList(ctx, client, msg)
	if err != nil {
		t.Fatalf("handleProxyResourcesList returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected success response, got: %+v", resp)
	}

	var decoded struct {
		Resources []struct {
			URI string `json:"uri"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(resp.Result, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(decoded.Resources) < 5 {
		t.Fatalf("expected at least one resource")
	}
	seen := make(map[string]bool)
	for _, r := range decoded.Resources {
		seen[r.URI] = true
	}
	for _, uri := range []string{"loom://servers", "loom://tools", "loom://tools/index", "loom://health", "loom://config"} {
		if !seen[uri] {
			t.Fatalf("expected resources to include %q", uri)
		}
	}
}

func TestProxy_ResourcesList_SkipsStoppedServers(t *testing.T) {
	ctx := context.Background()
	client, server := mcp.NewPipeTransport()
	requestedServers := make(chan string, 2)

	go func() {
		defer server.Close()

		// Force legacy fallback path.
		req, _ := server.Recv(ctx)
		if req.Method == "loom/resources" {
			_ = server.Send(ctx, mcp.NewErrorResponse(req.ID, mcp.MethodNotFound, "unknown method: loom/resources"))
		}

		// loom/servers
		req, _ = server.Recv(ctx)
		if req.Method == "loom/servers" {
			resp, _ := mcp.NewResponse(req.ID, map[string]any{
				"servers": []any{
					map[string]any{"name": "alpha", "running": false},
					map[string]any{"name": "beta", "running": true},
				},
			})
			_ = server.Send(ctx, resp)
		}

		// Only running server should be queried.
		req, _ = server.Recv(ctx)
		if req.Method == "loom/call" {
			var params struct {
				Server string `json:"server"`
			}
			_ = json.Unmarshal(req.Params, &params)
			requestedServers <- params.Server
			resp, _ := mcp.NewResponse(req.ID, map[string]any{
				"resources": []any{
					map[string]any{"uri": "r-beta", "name": "R-beta"},
				},
			})
			_ = server.Send(ctx, resp)
		}
	}()

	msg, err := mcp.NewRequest(1, "resources/list", map[string]any{})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := handleProxyResourcesList(ctx, client, msg)
	if err != nil {
		t.Fatalf("handleProxyResourcesList returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected success response, got: %+v", resp)
	}

	select {
	case serverName := <-requestedServers:
		if serverName != "beta" {
			t.Fatalf("expected only running server beta to be queried, got %q", serverName)
		}
	default:
		t.Fatal("expected one resources/list probe request")
	}
}

func TestProxy_ResourcesRead_LoomServers_ReturnsContents(t *testing.T) {
	ctx := context.Background()
	client, server := mcp.NewPipeTransport()

	// Fake daemon: respond to loom/servers.
	go func() {
		defer server.Close()

		req, _ := server.Recv(ctx)
		resp, _ := mcp.NewResponse(req.ID, map[string]any{
			"servers": []any{
				map[string]any{"name": "alpha"},
			},
		})
		_ = server.Send(ctx, resp)
	}()

	msg, err := mcp.NewRequest(1, "resources/read", map[string]any{"uri": "loom://servers"})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := handleProxyResourcesRead(ctx, client, msg)
	if err != nil {
		t.Fatalf("handleProxyResourcesRead returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected success response, got: %+v", resp)
	}

	var decoded struct {
		Contents []struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resp.Result, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(decoded.Contents) != 1 {
		t.Fatalf("expected 1 content entry, got %d", len(decoded.Contents))
	}
	if decoded.Contents[0].URI != "loom://servers" {
		t.Fatalf("expected uri loom://servers, got %q", decoded.Contents[0].URI)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(decoded.Contents[0].Text), &payload); err != nil {
		t.Fatalf("contents[0].text is not valid JSON: %v", err)
	}
}

func TestProxy_ResourcesRead_LoomTools_ReturnsContents(t *testing.T) {
	t.Setenv("LOOM_PROXY_MAX_RESOURCE_BYTES", "50000")
	ctx := context.Background()
	client, server := mcp.NewPipeTransport()

	go func() {
		defer server.Close()
		req, _ := server.Recv(ctx)
		resp, _ := mcp.NewResponse(req.ID, map[string]any{
			"tools": []any{
				map[string]any{"name": "alpha__tool", "inputSchema": map[string]any{"type": "object"}},
			},
			"cachedAt":    "2026-01-17T00:00:00Z",
			"serverCount": 1,
		})
		_ = server.Send(ctx, resp)
	}()

	msg, err := mcp.NewRequest(1, "resources/read", map[string]any{"uri": "loom://tools"})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := handleProxyResourcesRead(ctx, client, msg)
	if err != nil {
		t.Fatalf("handleProxyResourcesRead returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected success response, got: %+v", resp)
	}

	var decoded struct {
		Contents []struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resp.Result, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(decoded.Contents) != 1 || decoded.Contents[0].URI != "loom://tools" {
		t.Fatalf("unexpected contents: %+v", decoded.Contents)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(decoded.Contents[0].Text), &payload); err != nil {
		t.Fatalf("contents[0].text is not valid JSON: %v", err)
	}
}

func TestProxy_ResourcesRead_LoomTools_BackwardCompatibleTruncation(t *testing.T) {
	t.Setenv("LOOM_PROXY_MAX_RESOURCE_BYTES", "300")
	ctx := context.Background()
	client, server := mcp.NewPipeTransport()

	go func() {
		defer server.Close()
		req, _ := server.Recv(ctx)
		resp, _ := mcp.NewResponse(req.ID, map[string]any{
			"tools": []any{
				map[string]any{"name": "alpha__tool", "description": strings.Repeat("x", 2000)},
			},
		})
		_ = server.Send(ctx, resp)
	}()

	msg, err := mcp.NewRequest(1, "resources/read", map[string]any{"uri": "loom://tools"})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := handleProxyResourcesRead(ctx, client, msg)
	if err != nil {
		t.Fatalf("handleProxyResourcesRead returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected success response, got: %+v", resp)
	}

	var decoded struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resp.Result, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(decoded.Contents) != 1 {
		t.Fatalf("expected 1 content entry, got %d", len(decoded.Contents))
	}
	if !strings.Contains(decoded.Contents[0].Text, "[loom] resource truncated") {
		t.Fatalf("expected truncation marker for loom://tools")
	}
}

func TestProxy_ResourcesRead_LoomToolsIndex_ReturnsNonTruncatedPage(t *testing.T) {
	t.Setenv("LOOM_PROXY_MAX_RESOURCE_BYTES", "200")
	t.Setenv(loomProxyToolPageSizeEnv, "10")

	ctx := context.Background()
	client, server := mcp.NewPipeTransport()

	go func() {
		defer server.Close()
		req, _ := server.Recv(ctx)
		resp, _ := mcp.NewResponse(req.ID, map[string]any{
			"tools": []any{
				map[string]any{"name": "agent_context__agent_task_list", "description": strings.Repeat("x", 500)},
			},
			"cachedAt":    "2026-01-17T00:00:00Z",
			"serverCount": 1,
		})
		_ = server.Send(ctx, resp)
	}()

	msg, err := mcp.NewRequest(1, "resources/read", map[string]any{"uri": "loom://tools/index"})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := handleProxyResourcesRead(ctx, client, msg)
	if err != nil {
		t.Fatalf("handleProxyResourcesRead returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected success response, got: %+v", resp)
	}

	var decoded struct {
		Contents []struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resp.Result, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(decoded.Contents) != 1 || decoded.Contents[0].URI != "loom://tools/index" {
		t.Fatalf("unexpected contents: %+v", decoded.Contents)
	}
	if strings.Contains(decoded.Contents[0].Text, "[loom] resource truncated") {
		t.Fatalf("index payload should not include truncation marker")
	}
}

func TestProxy_ResourcesRead_LoomToolsPage_BoundedWithMetadata(t *testing.T) {
	t.Setenv(loomProxyToolPageSizeEnv, "10")
	ctx := context.Background()
	client, server := mcp.NewPipeTransport()

	tools := make([]any, 0, 12)
	for i := 0; i < 12; i++ {
		tools = append(tools, map[string]any{"name": fmt.Sprintf("agent_context__tool_%d", i)})
	}

	go func() {
		defer server.Close()
		req, _ := server.Recv(ctx)
		resp, _ := mcp.NewResponse(req.ID, map[string]any{
			"tools":       tools,
			"cachedAt":    "2026-01-17T00:00:00Z",
			"serverCount": 1,
		})
		_ = server.Send(ctx, resp)
	}()

	msg, err := mcp.NewRequest(1, "resources/read", map[string]any{"uri": "loom://tools/page/1"})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := handleProxyResourcesRead(ctx, client, msg)
	if err != nil {
		t.Fatalf("handleProxyResourcesRead returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected success response, got: %+v", resp)
	}

	var decoded struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resp.Result, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(decoded.Contents) != 1 {
		t.Fatalf("expected 1 content entry, got %d", len(decoded.Contents))
	}

	var page toolInventoryPage
	if err := json.Unmarshal([]byte(decoded.Contents[0].Text), &page); err != nil {
		t.Fatalf("invalid page payload: %v", err)
	}
	if page.Server != "all" {
		t.Fatalf("server = %q, want all", page.Server)
	}
	if page.Page != 1 {
		t.Fatalf("page = %d, want 1", page.Page)
	}
	if page.PageSize != 10 {
		t.Fatalf("pageSize = %d, want 10", page.PageSize)
	}
	if page.TotalTools != 12 {
		t.Fatalf("totalTools = %d, want 12", page.TotalTools)
	}
	if page.TotalPages != 2 {
		t.Fatalf("totalPages = %d, want 2", page.TotalPages)
	}
	if len(page.Tools) != 10 {
		t.Fatalf("len(tools) = %d, want 10", len(page.Tools))
	}
}

func TestProxy_ResourcesRead_LoomToolsServerPage_Filtered(t *testing.T) {
	t.Setenv(loomProxyToolPageSizeEnv, "10")
	ctx := context.Background()
	client, server := mcp.NewPipeTransport()

	go func() {
		defer server.Close()
		req, _ := server.Recv(ctx)
		resp, _ := mcp.NewResponse(req.ID, map[string]any{
			"tools": []any{
				map[string]any{"name": "agent_context__agent_task_add"},
				map[string]any{"name": "agent_context__agent_task_list"},
				map[string]any{"name": "git__git_status"},
			},
			"cachedAt":    "2026-01-17T00:00:00Z",
			"serverCount": 2,
		})
		_ = server.Send(ctx, resp)
	}()

	msg, err := mcp.NewRequest(1, "resources/read", map[string]any{"uri": "loom://tools/server/agent_context/page/1"})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := handleProxyResourcesRead(ctx, client, msg)
	if err != nil {
		t.Fatalf("handleProxyResourcesRead returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected success response, got: %+v", resp)
	}

	var decoded struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resp.Result, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(decoded.Contents) != 1 {
		t.Fatalf("expected 1 content entry, got %d", len(decoded.Contents))
	}

	var page toolInventoryPage
	if err := json.Unmarshal([]byte(decoded.Contents[0].Text), &page); err != nil {
		t.Fatalf("invalid page payload: %v", err)
	}
	if page.Server != "agent_context" {
		t.Fatalf("server = %q, want agent_context", page.Server)
	}
	if page.TotalTools != 2 {
		t.Fatalf("totalTools = %d, want 2", page.TotalTools)
	}
	if len(page.Tools) != 2 {
		t.Fatalf("len(tools) = %d, want 2", len(page.Tools))
	}
	for _, tool := range page.Tools {
		if !strings.HasPrefix(tool.Name, "agent_context__") {
			t.Fatalf("unexpected tool in server page: %s", tool.Name)
		}
	}
}

func TestProxy_ResourcesRead_LoomToolsPaged_InvalidParams(t *testing.T) {
	tests := []struct {
		uri         string
		needsDaemon bool
	}{
		{uri: "loom://tools/page/0", needsDaemon: false},
		{uri: "loom://tools/server//page/1", needsDaemon: false},
		{uri: "loom://tools/server/missing/page/1", needsDaemon: true},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			ctx := context.Background()
			client, server := mcp.NewPipeTransport()

			if tt.needsDaemon {
				go func() {
					defer server.Close()
					req, _ := server.Recv(ctx)
					resp, _ := mcp.NewResponse(req.ID, map[string]any{
						"tools": []any{
							map[string]any{"name": "agent_context__agent_task_list"},
						},
					})
					_ = server.Send(ctx, resp)
				}()
			}

			msg, err := mcp.NewRequest(1, "resources/read", map[string]any{"uri": tt.uri})
			if err != nil {
				t.Fatalf("NewRequest failed: %v", err)
			}
			resp, err := handleProxyResourcesRead(ctx, client, msg)
			if err != nil {
				t.Fatalf("handleProxyResourcesRead returned error: %v", err)
			}
			if resp == nil || resp.Error == nil {
				t.Fatalf("expected InvalidParams error, got: %+v", resp)
			}
			if resp.Error.Code != mcp.InvalidParams {
				t.Fatalf("expected InvalidParams, got code %d", resp.Error.Code)
			}
			_ = server.Close()
		})
	}
}

func TestProxy_ResourcesRead_LoomHealth_ReturnsContents(t *testing.T) {
	ctx := context.Background()
	client, server := mcp.NewPipeTransport()

	go func() {
		defer server.Close()
		req, _ := server.Recv(ctx)
		resp, _ := mcp.NewResponse(req.ID, map[string]any{
			"servers": map[string]any{
				"alpha": map[string]any{
					"target": "local",
					"local":  map[string]any{"healthy": true},
				},
			},
		})
		_ = server.Send(ctx, resp)
	}()

	msg, err := mcp.NewRequest(1, "resources/read", map[string]any{"uri": "loom://health"})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := handleProxyResourcesRead(ctx, client, msg)
	if err != nil {
		t.Fatalf("handleProxyResourcesRead returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected success response, got: %+v", resp)
	}
}

func TestProxy_ResourcesRead_LoomConfig_ReturnsContents(t *testing.T) {
	ctx := context.Background()
	client, server := mcp.NewPipeTransport()

	go func() {
		defer server.Close()
		for i := 0; i < 3; i++ {
			req, _ := server.Recv(ctx)
			var resp *mcp.Message
			switch req.Method {
			case "loom/status":
				resp, _ = mcp.NewResponse(req.ID, map[string]any{"running": true, "servers": 1})
			case "loom/profile":
				resp, _ = mcp.NewResponse(req.ID, map[string]any{"active": "codex", "available": []any{"full", "codex"}})
			case "loom/config-hash":
				resp, _ = mcp.NewResponse(req.ID, map[string]any{"toolCount": 10, "serverCount": 1})
			default:
				resp = mcp.NewErrorResponse(req.ID, mcp.MethodNotFound, "unexpected method: "+req.Method)
			}
			_ = server.Send(ctx, resp)
		}
	}()

	msg, err := mcp.NewRequest(1, "resources/read", map[string]any{"uri": "loom://config"})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := handleProxyResourcesRead(ctx, client, msg)
	if err != nil {
		t.Fatalf("handleProxyResourcesRead returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected success response, got: %+v", resp)
	}
}
