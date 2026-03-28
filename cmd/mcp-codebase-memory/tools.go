package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase"
	"github.com/crb2nu/loom/pkg/mcpscaffold"
	"github.com/crb2nu/loom/pkg/validate"
)

func registerTools(srv *mcpscaffold.Server, svc *codebase.Service) {
	srv.AddTracedTool(mcp.Tool{
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
		v := validate.NewArgs(args)
		_ = v.String("repo_id", "")      // optional
		_ = v.StringSlice("languages")   // optional
		_ = v.StringSlice("chunk_types") // optional
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleStats(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
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
		v := validate.NewArgs(args)
		_ = v.Required("repo_id")
		_ = v.RequiredBool("confirm")
		_ = v.Bool("dry_run", false) // optional
		_ = v.String("reason", "")   // optional
		_ = v.String("comment", "")  // optional
		_ = v.String("operator", "") // optional
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleDeleteRepo(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
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
					"description": "Glob patterns to exclude (relative to root). Supports .gitignore-style patterns with optional ! negation. Rules from nested .gitignore files are also applied; explicit exclude patterns are evaluated after built-in and .gitignore patterns (so later ! rules can re-include).",
				},
				"full_refresh": map[string]any{
					"type":        "boolean",
					"description": "If true, delete all existing vectors for repo_id before indexing (recommended).",
				},
				"git_metadata": map[string]any{
					"type":        "boolean",
					"description": "If true, annotate chunks with git blame metadata (can be slow). Defaults to CODEBASE_GIT_METADATA.",
				},
				"embeddings": map[string]any{
					"type":        "boolean",
					"description": "If false, store chunks with dummy vectors (enables non-embedding tools like text search/definition/context; semantic search will not be useful). Defaults to !CODEBASE_DISABLE_EMBEDDINGS.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		_ = v.String("root", ".")         // optional with default
		_ = v.String("repo_id", "")       // optional
		_ = v.StringSlice("languages")    // optional
		_ = v.StringSlice("exclude")      // optional
		_ = v.Bool("full_refresh", true)  // optional with default
		_ = v.Bool("git_metadata", false) // optional
		_ = v.Bool("embeddings", true)    // optional
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleIndexStart(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
		Name:        "codebase_watch_start",
		Description: "Start watching a repository for changes and incrementally update the index (async job).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"root": map[string]any{
					"type":        "string",
					"description": "Repository root path to watch (absolute or relative). Defaults to current directory.",
				},
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repo identifier used for scoping in Qdrant. Defaults to CODEBASE_REPO_ID or derived value.",
				},
				"languages": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Languages to watch. If omitted, watches all supported languages.",
				},
				"exclude": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Glob patterns to exclude (relative to root). Supports .gitignore-style patterns with optional ! negation. Rules from nested .gitignore files are also applied; explicit exclude patterns are evaluated after built-in and .gitignore patterns (so later ! rules can re-include).",
				},
				"debounce_ms": map[string]any{
					"type":        "integer",
					"description": "Debounce window in milliseconds (default 750ms).",
				},
				"git_metadata": map[string]any{
					"type":        "boolean",
					"description": "If true, annotate chunks with git blame metadata (can be slow). Defaults to CODEBASE_GIT_METADATA.",
				},
				"embeddings": map[string]any{
					"type":        "boolean",
					"description": "If false, store chunks with dummy vectors (enables non-embedding tools like text search/definition/context; semantic search will not be useful). Defaults to !CODEBASE_DISABLE_EMBEDDINGS.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		_ = v.String("root", ".")         // optional with default
		_ = v.String("repo_id", "")       // optional
		_ = v.StringSlice("languages")    // optional
		_ = v.StringSlice("exclude")      // optional
		_ = v.Int("debounce_ms", 750)     // optional with default
		_ = v.Bool("git_metadata", false) // optional
		_ = v.Bool("embeddings", true)    // optional
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleWatchStart(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
		Name:        "codebase_watch_poll",
		Description: "Poll a watch job started with codebase_watch_start.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"watch_id": map[string]any{"type": "string"},
			},
			Required: []string{"watch_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		_ = v.Required("watch_id")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleWatchPoll(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
		Name:        "codebase_watch_stop",
		Description: "Stop a watch job.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"watch_id": map[string]any{"type": "string"},
			},
			Required: []string{"watch_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		_ = v.Required("watch_id")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleWatchStop(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
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
		v := validate.NewArgs(args)
		_ = v.Required("job_id")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleIndexPoll(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
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
		v := validate.NewArgs(args)
		_ = v.Required("job_id")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleIndexCancel(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
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
		v := validate.NewArgs(args)
		_ = v.String("repo_id", "") // optional
		_ = v.Required("query")
		_ = v.Int("limit", 10)               // optional with default
		_ = v.StringSlice("languages")       // optional
		_ = v.StringSlice("chunk_types")     // optional
		_ = v.String("rerank", "")           // optional
		_ = v.Float("lexical_weight", 0.15)  // optional with default
		_ = v.Bool("include_content", false) // optional
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleSearch(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
		Name:        "codebase_text_search",
		Description: "Lexical fallback search (no embeddings): scans stored chunk payloads for query tokens.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repo identifier used during indexing.",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "Substring/token query to match against signature/docstring/content.",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "Optional file path to restrict search (relative to repo root).",
				},
				"languages": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional subset of languages to scan.",
				},
				"chunk_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional subset of chunk types to scan.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results (default 10, max 200).",
				},
				"max_scan": map[string]any{
					"type":        "integer",
					"description": "Maximum chunks to scan before stopping (default 2000).",
				},
				"case_sensitive": map[string]any{
					"type":        "boolean",
					"description": "If true, do case-sensitive matches (default false).",
				},
				"include_content": map[string]any{
					"type":        "boolean",
					"description": "If true, include full content in returned chunks (can be large).",
				},
			},
			Required: []string{"query"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		_ = v.String("repo_id", "") // optional
		_ = v.Required("query")
		_ = v.String("file_path", "")        // optional
		_ = v.StringSlice("languages")       // optional
		_ = v.StringSlice("chunk_types")     // optional
		_ = v.Int("limit", 10)               // optional with default
		_ = v.Int("max_scan", 2000)          // optional with default
		_ = v.Bool("case_sensitive", false)  // optional
		_ = v.Bool("include_content", false) // optional
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleTextSearch(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
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
		v := validate.NewArgs(args)
		_ = v.String("repo_id", "") // optional
		_ = v.Required("symbol")
		_ = v.String("file_path", "")        // optional
		_ = v.StringSlice("languages")       // optional
		_ = v.Int("limit", 10)               // optional with default
		_ = v.Bool("include_content", false) // optional
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleGetDefinition(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
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
		v := validate.NewArgs(args)
		_ = v.String("repo_id", "") // optional
		_ = v.Required("symbol")
		_ = v.String("file_path", "")           // optional
		_ = v.StringSlice("languages")          // optional
		_ = v.Int("limit", 10)                  // optional with default
		_ = v.Bool("include_definitions", true) // optional
		_ = v.Bool("include_callers", true)     // optional
		_ = v.Bool("include_modules", false)    // optional
		_ = v.Bool("include_content", false)    // optional
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleGetReferences(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
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
		v := validate.NewArgs(args)
		_ = v.String("repo_id", "") // optional
		_ = v.Required("file_path")
		_ = v.RequiredInt("line_number")
		_ = v.Bool("include_callers", false) // optional
		_ = v.Bool("include_callees", false) // optional
		_ = v.Int("related_limit", 5)        // optional with default
		_ = v.Bool("include_content", false) // optional
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleGetContext(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
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
		v := validate.NewArgs(args)
		_ = v.String("repo_id", "") // optional
		_ = v.Required("symbol")
		_ = v.String("file_path", "") // optional
		_ = v.Int("limit", 10)        // optional with default
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleFindCallers(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
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
		v := validate.NewArgs(args)
		_ = v.String("repo_id", "") // optional
		_ = v.Required("symbol")
		_ = v.String("file_path", "") // optional
		_ = v.Int("limit", 10)        // optional with default
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleFindCallees(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
		Name:        "codebase_call_graph",
		Description: "Build a best-effort call graph around a symbol using stored calls[] + caller queries (BFS).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repo identifier used during indexing.",
				},
				"symbol": map[string]any{
					"type":        "string",
					"description": "Root symbol name.",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "Optional file path to disambiguate the root definition (relative to repo root).",
				},
				"languages": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Optional subset of languages when resolving definitions.",
				},
				"direction": map[string]any{
					"type":        "string",
					"description": "Graph direction: out (callees), in (callers), or both.",
				},
				"depth": map[string]any{
					"type":        "integer",
					"description": "BFS depth (default 2, max 10).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Per-step query limit for caller/definition lookups (default CODEBASE_SCROLL_LIMIT).",
				},
				"max_nodes": map[string]any{
					"type":        "integer",
					"description": "Maximum nodes to explore (default 200).",
				},
				"include_external": map[string]any{
					"type":        "boolean",
					"description": "If true, include external/builtin calls as nodes when possible (default true).",
				},
				"render": map[string]any{
					"type":        "string",
					"description": "Optional renderer: none|mermaid|dot (default none).",
				},
			},
			Required: []string{"symbol"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		_ = v.String("repo_id", "") // optional
		_ = v.Required("symbol")
		_ = v.String("file_path", "")        // optional
		_ = v.StringSlice("languages")       // optional
		_ = v.String("direction", "both")    // optional with default
		_ = v.Int("depth", 2)                // optional with default
		_ = v.Int("limit", 100)              // optional with default
		_ = v.Int("max_nodes", 200)          // optional with default
		_ = v.Bool("include_external", true) // optional
		_ = v.String("render", "none")       // optional with default
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleCallGraph(ctx, args)
	})

	srv.AddTracedTool(mcp.Tool{
		Name:        "codebase_module_graph",
		Description: "Build a best-effort module dependency graph from indexed module chunks (imports edges).",
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
					"description": "Optional subset of languages (filters module chunks by language).",
				},
				"max_files": map[string]any{
					"type":        "integer",
					"description": "Maximum module chunks/files to consider (default 512).",
				},
				"max_edges": map[string]any{
					"type":        "integer",
					"description": "Maximum edges to return (default 4000).",
				},
				"include_external": map[string]any{
					"type":        "boolean",
					"description": "If false, only include resolved internal edges where possible (default true).",
				},
				"render": map[string]any{
					"type":        "string",
					"description": "Optional renderer: none|mermaid|dot (default none).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		_ = v.String("repo_id", "")          // optional
		_ = v.StringSlice("languages")       // optional
		_ = v.Int("max_files", 512)          // optional with default
		_ = v.Int("max_edges", 4000)         // optional with default
		_ = v.Bool("include_external", true) // optional
		_ = v.String("render", "none")       // optional with default
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}
		return svc.HandleModuleGraph(ctx, args)
	})
}
