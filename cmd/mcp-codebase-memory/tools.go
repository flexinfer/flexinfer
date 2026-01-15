package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase"
)

func registerTools(server *mcp.Server, svc *codebase.Service) {
	server.AddTool(mcp.Tool{
		Name:        "codebase_stats",
		Description: "Get basic stats about an indexed repo (counts by language/chunk_type).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repo identifier used during indexing.",
				},
				"languages": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional subset of languages to count.",
				},
				"chunk_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional subset of chunk types to count.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleStats(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "codebase_delete_repo",
		Description: "Delete all indexed vectors for a repo_id (destructive). Requires confirm=true.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_id":  map[string]any{"type": "string"},
				"confirm":  map[string]any{"type": "boolean"},
				"dry_run":  map[string]any{"type": "boolean"},
				"reason":   map[string]any{"type": "string"},
				"comment":  map[string]any{"type": "string"},
				"operator": map[string]any{"type": "string"},
			},
			Required: []string{"repo_id", "confirm"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleDeleteRepo(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "codebase_index_start",
		Description: "Start indexing a repository (async job).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"root": map[string]any{
					"type":        "string",
					"description": "Repository root path to index (absolute or relative). Defaults to current directory.",
				},
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repo identifier used for scoping in Qdrant. Defaults to CODEBASE_REPO_ID or derived value.",
				},
				"languages": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Languages to index. If omitted, indexes all supported languages.",
				},
				"exclude": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Glob patterns to exclude (relative to root).",
				},
				"full_refresh": map[string]any{
					"type":        "boolean",
					"description": "If true, delete all existing vectors for repo_id before indexing (recommended).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleIndexStart(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "codebase_index_poll",
		Description: "Poll an indexing job started with codebase_index_start.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"job_id": map[string]any{"type": "string"},
			},
			Required: []string{"job_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleIndexPoll(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "codebase_index_cancel",
		Description: "Cancel an indexing job.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"job_id": map[string]any{"type": "string"},
			},
			Required: []string{"job_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleIndexCancel(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "codebase_search",
		Description: "Semantic search across an indexed codebase.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repo identifier used during indexing.",
				},
				"query": map[string]any{"type": "string"},
				"limit": map[string]any{"type": "integer"},
				"languages": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"chunk_types": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"rerank": map[string]any{
					"type":        "string",
					"description": "Optional reranker: none|hybrid. hybrid combines vector similarity with simple lexical token overlap.",
				},
				"lexical_weight": map[string]any{
					"type":        "number",
					"description": "For rerank=hybrid: 0..1 weight for lexical overlap (default 0.15).",
				},
				"include_content": map[string]any{
					"type":        "boolean",
					"description": "If true, include full content in results (can be large).",
				},
			},
			Required: []string{"query"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleSearch(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "codebase_get_definition",
		Description: "Jump to a symbol definition (best-effort; matches indexed chunk name).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repo identifier used during indexing.",
				},
				"symbol": map[string]any{"type": "string"},
				"file_path": map[string]any{
					"type":        "string",
					"description": "Optional file path to narrow search (relative to repo root).",
				},
				"languages": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional subset of languages to search.",
				},
				"limit": map[string]any{"type": "integer"},
				"include_content": map[string]any{
					"type":        "boolean",
					"description": "If true, include full content in the returned chunk (can be large).",
				},
			},
			Required: []string{"symbol"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleGetDefinition(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "codebase_get_references",
		Description: "Find references to a symbol (best-effort: definitions by name + callers by call graph).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repo identifier used during indexing.",
				},
				"symbol": map[string]any{"type": "string"},
				"file_path": map[string]any{
					"type":        "string",
					"description": "Optional file path to narrow search (relative to repo root).",
				},
				"languages": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional subset of languages to search.",
				},
				"limit": map[string]any{"type": "integer"},
				"include_definitions": map[string]any{
					"type":        "boolean",
					"description": "Include definition candidates (chunks where name matches symbol).",
				},
				"include_callers": map[string]any{
					"type":        "boolean",
					"description": "Include caller candidates (chunks that call symbol).",
				},
				"include_modules": map[string]any{
					"type":        "boolean",
					"description": "Include module chunks in definitions (defaults false).",
				},
				"include_content": map[string]any{
					"type":        "boolean",
					"description": "If true, include full content in returned chunks (can be large).",
				},
			},
			Required: []string{"symbol"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleGetReferences(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "codebase_get_context",
		Description: "Get context for a file/line (chunk + nearby chunks + callers/callees heuristics).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repo identifier used during indexing.",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "File path relative to repo root.",
				},
				"line_number": map[string]any{"type": "integer"},
				"include_callers": map[string]any{
					"type":        "boolean",
					"description": "Include callers by scanning indexed chunks (best-effort).",
				},
				"include_callees": map[string]any{
					"type":        "boolean",
					"description": "Include callees from calls[] (best-effort).",
				},
				"related_limit": map[string]any{
					"type":        "integer",
					"description": "Max same-file related chunks to include.",
				},
				"include_content": map[string]any{
					"type":        "boolean",
					"description": "If true, include full content in returned chunks (can be large).",
				},
			},
			Required: []string{"file_path", "line_number"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleGetContext(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "codebase_find_callers",
		Description: "Find callers of a function/symbol (best-effort; uses calls[] scanning).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repo identifier used during indexing.",
				},
				"symbol": map[string]any{"type": "string"},
				"file_path": map[string]any{
					"type":        "string",
					"description": "Optional file path to narrow search (relative to repo root).",
				},
				"limit": map[string]any{"type": "integer"},
			},
			Required: []string{"symbol"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleFindCallers(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "codebase_find_callees",
		Description: "Find callees of a function/symbol (best-effort; uses calls[]).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repo identifier used during indexing.",
				},
				"symbol": map[string]any{"type": "string"},
				"file_path": map[string]any{
					"type":        "string",
					"description": "Optional file path to narrow search (relative to repo root).",
				},
				"limit": map[string]any{"type": "integer"},
			},
			Required: []string{"symbol"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleFindCallees(ctx, args)
	})
}
