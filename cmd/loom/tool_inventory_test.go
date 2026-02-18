package main

import (
	"encoding/json"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestClampToolPageSize(t *testing.T) {
	if got := clampToolPageSize(1); got != minToolPageSize {
		t.Fatalf("clampToolPageSize(1) = %d, want %d", got, minToolPageSize)
	}
	if got := clampToolPageSize(250); got != 250 {
		t.Fatalf("clampToolPageSize(250) = %d, want 250", got)
	}
	if got := clampToolPageSize(999); got != maxToolPageSize {
		t.Fatalf("clampToolPageSize(999) = %d, want %d", got, maxToolPageSize)
	}
}

func TestBuildToolInventoryPage_PaginatesAndFilters(t *testing.T) {
	tools := []mcp.Tool{
		{Name: "alpha__one"},
		{Name: "alpha__two"},
		{Name: "beta__three"},
	}

	page, err := buildToolInventoryPage(tools, "alpha", 1, 10, true)
	if err != nil {
		t.Fatalf("buildToolInventoryPage error: %v", err)
	}
	if page.Server != "alpha" {
		t.Fatalf("server = %q, want alpha", page.Server)
	}
	if page.TotalTools != 2 {
		t.Fatalf("total tools = %d, want 2", page.TotalTools)
	}
	if len(page.Tools) != 2 {
		t.Fatalf("page tools len = %d, want 2", len(page.Tools))
	}
	for _, tool := range page.Tools {
		if toolServerFromName(tool.Name) != "alpha" {
			t.Fatalf("unexpected tool in filtered page: %s", tool.Name)
		}
	}
}

func TestBuildToolInventoryPage_RejectsUnknownServer(t *testing.T) {
	tools := []mcp.Tool{{Name: "alpha__one"}}
	_, err := buildToolInventoryPage(tools, "missing", 1, 10, true)
	if err == nil {
		t.Fatal("expected error for unknown server")
	}
}

func TestBuildToolInventoryPage_RejectsOutOfRangePage(t *testing.T) {
	tools := []mcp.Tool{{Name: "alpha__one"}}
	_, err := buildToolInventoryPage(tools, "", 2, 10, false)
	if err == nil {
		t.Fatal("expected page out of range error")
	}
}

func TestParseLoomToolsInventoryURI(t *testing.T) {
	tests := []struct {
		uri    string
		server string
		page   int
		ok     bool
		hasErr bool
	}{
		{"loom://tools/index", "", 1, true, false},
		{"loom://tools/page/2", "", 2, true, false},
		{"loom://tools/server/agent_context/page/3", "agent_context", 3, true, false},
		{"loom://tools/server//page/1", "", 0, true, true},
		{"loom://tools/page/0", "", 0, true, true},
		{"loom://tools/server/agent_context/page/nope", "", 0, true, true},
		{"loom://health", "", 0, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			server, page, ok, err := parseLoomToolsInventoryURI(tt.uri)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if tt.hasErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.hasErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if server != tt.server {
				t.Fatalf("server = %q, want %q", server, tt.server)
			}
			if page != tt.page {
				t.Fatalf("page = %d, want %d", page, tt.page)
			}
		})
	}
}

func TestBuildToolInventoryPage_JSONRoundTrip(t *testing.T) {
	tools := []mcp.Tool{
		{Name: "alpha__one", Description: "first"},
		{Name: "beta__two", Description: "second"},
	}

	page, err := buildToolInventoryPage(tools, "", 1, 10, false)
	if err != nil {
		t.Fatalf("buildToolInventoryPage error: %v", err)
	}

	b, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal page: %v", err)
	}

	var decoded toolInventoryPage
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal page: %v", err)
	}
	if decoded.TotalTools != 2 || len(decoded.Tools) != 2 {
		t.Fatalf("unexpected decoded payload: %+v", decoded)
	}
}
