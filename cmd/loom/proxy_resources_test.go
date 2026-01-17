package main

import (
	"context"
	"encoding/json"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestProxy_ResourcesList_IncludesLoomServersResource(t *testing.T) {
	ctx := context.Background()
	client, server := mcp.NewPipeTransport()

	// Fake daemon: respond to loom/servers and two resources/list calls.
	go func() {
		defer server.Close()

		// loom/servers
		req, _ := server.Recv(ctx)
		if req.Method == "loom/servers" {
			resp, _ := mcp.NewResponse(req.ID, map[string]any{
				"servers": []any{
					map[string]any{"name": "alpha"},
					map[string]any{"name": "beta"},
				},
			})
			_ = server.Send(ctx, resp)
		}

		// alpha resources/list
		req, _ = server.Recv(ctx)
		if req.Method == "loom/call" {
			resp, _ := mcp.NewResponse(req.ID, map[string]any{
				"resources": []any{
					map[string]any{"uri": "r1", "name": "R1"},
				},
			})
			_ = server.Send(ctx, resp)
		}

		// beta resources/list
		req, _ = server.Recv(ctx)
		if req.Method == "loom/call" {
			resp, _ := mcp.NewResponse(req.ID, map[string]any{
				"resources": []any{
					map[string]any{"uri": "r2", "name": "R2"},
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
	if len(decoded.Resources) < 4 {
		t.Fatalf("expected at least one resource")
	}
	seen := make(map[string]bool)
	for _, r := range decoded.Resources {
		seen[r.URI] = true
	}
	for _, uri := range []string{"loom://servers", "loom://tools", "loom://health", "loom://config"} {
		if !seen[uri] {
			t.Fatalf("expected resources to include %q", uri)
		}
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
