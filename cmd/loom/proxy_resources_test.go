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
	if len(decoded.Resources) < 1 {
		t.Fatalf("expected at least one resource")
	}
	if decoded.Resources[0].URI != "loom://servers" {
		t.Fatalf("expected first resource to be loom://servers, got %q", decoded.Resources[0].URI)
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

