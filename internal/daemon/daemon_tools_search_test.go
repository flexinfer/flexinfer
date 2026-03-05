package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/registry"
)

func newToolsSearchTestDaemon(tools []mcp.Tool) *Daemon {
	return &Daemon{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		registry: &registry.Registry{
			Servers: []*registry.Server{
				{Name: "tavily"},
				{Name: "github"},
				{Name: "git"},
			},
		},
		toolCache: &ToolCache{
			tools:     tools,
			updatedAt: time.Now(),
			ttl:       5 * time.Minute,
		},
	}
}

func TestHandleToolsSearch_SummaryPaginationAndServerFilter(t *testing.T) {
	d := newToolsSearchTestDaemon([]mcp.Tool{
		{
			Name:        "tavily__search",
			Description: "Search the web with Tavily",
			InputSchema: mcp.InputSchema{Type: "object"},
		},
		{
			Name:        "tavily__search_news",
			Description: "Search recent news",
			InputSchema: mcp.InputSchema{Type: "object"},
		},
		{
			Name:        "github__search_repos",
			Description: "Search GitHub repositories",
			InputSchema: mcp.InputSchema{Type: "object"},
		},
	})

	msg, _ := mcp.NewRequest(1, "loom/tools/search", map[string]any{
		"query":   "search",
		"servers": []string{"tavily"},
		"limit":   1,
		"detail":  "summary",
	})

	resp, err := d.handleToolsSearch(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleToolsSearch() error = %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("handleToolsSearch() response error = %+v", resp.Error)
	}

	var result toolsSearchResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if result.Detail != toolSearchDetailSummary {
		t.Fatalf("detail = %q, want %q", result.Detail, toolSearchDetailSummary)
	}
	if result.Total != 2 {
		t.Fatalf("total = %d, want 2", result.Total)
	}
	if result.Count != 1 {
		t.Fatalf("count = %d, want 1", result.Count)
	}
	if result.NextCursor != "1" {
		t.Fatalf("nextCursor = %q, want 1", result.NextCursor)
	}
	if len(result.Results) != 1 {
		t.Fatalf("results len = %d, want 1", len(result.Results))
	}
	if result.Results[0].Server != "tavily" {
		t.Fatalf("server = %q, want tavily", result.Results[0].Server)
	}
	if result.Results[0].Description == "" {
		t.Fatal("expected non-empty description for summary detail")
	}
	if result.Results[0].InputSchema != nil {
		t.Fatal("summary detail should not include inputSchema")
	}
}

func TestHandleToolsSearch_DetailNameAndSchema(t *testing.T) {
	d := newToolsSearchTestDaemon([]mcp.Tool{
		{
			Name:        "git__git_status",
			Description: "Get git status",
			InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{"path": map[string]any{"type": "string"}}},
		},
	})

	nameMsg, _ := mcp.NewRequest(1, "loom/tools/search", map[string]any{
		"query":  "git_status",
		"detail": "name",
	})
	nameResp, err := d.handleToolsSearch(context.Background(), nameMsg)
	if err != nil {
		t.Fatalf("name detail search error = %v", err)
	}
	if nameResp.Error != nil {
		t.Fatalf("name detail search response error = %+v", nameResp.Error)
	}

	var nameResult toolsSearchResult
	if err := json.Unmarshal(nameResp.Result, &nameResult); err != nil {
		t.Fatalf("unmarshal name response: %v", err)
	}
	if len(nameResult.Results) != 1 {
		t.Fatalf("name results len = %d, want 1", len(nameResult.Results))
	}
	if nameResult.Results[0].Name != "git__git_status" {
		t.Fatalf("name result = %q, want git__git_status", nameResult.Results[0].Name)
	}
	if nameResult.Results[0].Description != "" {
		t.Fatalf("name detail should not include description, got %q", nameResult.Results[0].Description)
	}

	schemaMsg, _ := mcp.NewRequest(2, "loom/tools/search", map[string]any{
		"query":  "git_status",
		"detail": "schema",
	})
	schemaResp, err := d.handleToolsSearch(context.Background(), schemaMsg)
	if err != nil {
		t.Fatalf("schema detail search error = %v", err)
	}
	if schemaResp.Error != nil {
		t.Fatalf("schema detail search response error = %+v", schemaResp.Error)
	}

	var schemaResult toolsSearchResult
	if err := json.Unmarshal(schemaResp.Result, &schemaResult); err != nil {
		t.Fatalf("unmarshal schema response: %v", err)
	}
	if len(schemaResult.Results) != 1 {
		t.Fatalf("schema results len = %d, want 1", len(schemaResult.Results))
	}
	if schemaResult.Results[0].InputSchema == nil {
		t.Fatal("schema detail should include inputSchema")
	}
	if schemaResult.Results[0].InputSchema.Type != "object" {
		t.Fatalf("schema type = %q, want object", schemaResult.Results[0].InputSchema.Type)
	}
}

func TestHandleToolsSearch_InvalidParams(t *testing.T) {
	d := newToolsSearchTestDaemon([]mcp.Tool{
		{
			Name:        "git__git_status",
			Description: "Get git status",
			InputSchema: mcp.InputSchema{Type: "object"},
		},
	})

	tests := []struct {
		name   string
		params map[string]any
	}{
		{
			name:   "invalid detail",
			params: map[string]any{"detail": "full"},
		},
		{
			name:   "invalid cursor",
			params: map[string]any{"cursor": "abc"},
		},
		{
			name:   "negative limit",
			params: map[string]any{"limit": -1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, _ := mcp.NewRequest(1, "loom/tools/search", tt.params)
			resp, err := d.handleToolsSearch(context.Background(), msg)
			if err != nil {
				t.Fatalf("handleToolsSearch() error = %v", err)
			}
			if resp.Error == nil {
				t.Fatal("expected invalid params error")
			}
			if resp.Error.Code != mcp.InvalidParams {
				t.Fatalf("error code = %d, want %d", resp.Error.Code, mcp.InvalidParams)
			}
		})
	}
}

func TestHandleToolGet_FindsNamespacedAndServerScoped(t *testing.T) {
	d := newToolsSearchTestDaemon([]mcp.Tool{
		{
			Name:        "tavily__search",
			Description: "Search the web",
			InputSchema: mcp.InputSchema{
				Type:       "object",
				Properties: map[string]any{"query": map[string]any{"type": "string"}},
				Required:   []string{"query"},
			},
		},
	})

	tests := []struct {
		name   string
		params map[string]any
	}{
		{
			name:   "namespaced lookup",
			params: map[string]any{"name": "tavily__search"},
		},
		{
			name:   "server scoped short lookup",
			params: map[string]any{"name": "search", "server": "tavily"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, _ := mcp.NewRequest(1, "loom/tools/get", tt.params)
			resp, err := d.handleToolGet(context.Background(), msg)
			if err != nil {
				t.Fatalf("handleToolGet() error = %v", err)
			}
			if resp.Error != nil {
				t.Fatalf("handleToolGet() response error = %+v", resp.Error)
			}

			var got toolGetResult
			if err := json.Unmarshal(resp.Result, &got); err != nil {
				t.Fatalf("unmarshal get response: %v", err)
			}
			if got.Name != "tavily__search" {
				t.Fatalf("name = %q, want tavily__search", got.Name)
			}
			if got.Server != "tavily" {
				t.Fatalf("server = %q, want tavily", got.Server)
			}
			if got.ToolName != "search" {
				t.Fatalf("toolName = %q, want search", got.ToolName)
			}
			if got.Tool.InputSchema.Type != "object" {
				t.Fatalf("inputSchema.type = %q, want object", got.Tool.InputSchema.Type)
			}
		})
	}
}

func TestHandleToolGet_InvalidAndNotFound(t *testing.T) {
	d := newToolsSearchTestDaemon([]mcp.Tool{
		{
			Name:        "git__git_status",
			Description: "Get git status",
			InputSchema: mcp.InputSchema{Type: "object"},
		},
	})

	tests := []struct {
		name   string
		params map[string]any
	}{
		{
			name:   "missing name",
			params: map[string]any{},
		},
		{
			name:   "unknown tool",
			params: map[string]any{"name": "git__missing"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, _ := mcp.NewRequest(1, "loom/tools/get", tt.params)
			resp, err := d.handleToolGet(context.Background(), msg)
			if err != nil {
				t.Fatalf("handleToolGet() error = %v", err)
			}
			if resp.Error == nil {
				t.Fatal("expected error response")
			}
			if resp.Error.Code != mcp.InvalidParams {
				t.Fatalf("error code = %d, want %d", resp.Error.Code, mcp.InvalidParams)
			}
		})
	}
}
