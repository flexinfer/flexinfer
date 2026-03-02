package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerContextTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	// Context Storage Tools

	server.AddTool(mcp.Tool{
		Name:        "agent_context_add",
		Description: "Unified store: add entries to context, memory, or knowledge graph. Use the durability hint to route: 'session' (default) stores to context backend, 'persistent' promotes to long-term memory, 'graph' creates a knowledge graph entity.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID to add context to.",
				},
				"entries": map[string]any{
					"type":        "array",
					"description": "Array of entries to add. Each entry is routed based on its durability hint.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"durability": map[string]any{
								"type":        "string",
								"enum":        []string{"session", "persistent", "graph"},
								"description": "Storage routing: 'session' (default) = context store, 'persistent' = long-term memory, 'graph' = knowledge graph entity.",
							},
							"entry_type": map[string]any{
								"type":        "string",
								"enum":        []string{"file_read", "decision", "finding", "question", "note", "error", "code_context"},
								"description": "Type of context entry. For graph durability, also used as entity type.",
							},
							"title": map[string]any{
								"type":        "string",
								"description": "Short descriptive title. For graph durability, used as entity name.",
							},
							"content": map[string]any{
								"type":        "string",
								"description": "Full content text. For graph durability, used as entity description.",
							},
							"file_path": map[string]any{
								"type":        "string",
								"description": "File path (for file_read entries or graph entities).",
							},
							"line_start": map[string]any{
								"type":        "integer",
								"description": "Start line (for file_read entries).",
							},
							"line_end": map[string]any{
								"type":        "integer",
								"description": "End line (for file_read entries).",
							},
							"tags": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Tags for categorization.",
							},
							"metadata": map[string]any{
								"type":        "object",
								"description": "Additional structured metadata. For graph durability, stored as entity properties.",
							},
							"visibility": map[string]any{
								"type":        "string",
								"enum":        []string{"private", "shared", "public"},
								"description": "Who can access this entry (default: private). Session durability only.",
							},
							"shared_with": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Agent IDs to share with (for visibility=shared).",
							},
						},
						"required": []string{"entry_type", "title", "content"},
					},
				},
			},
			Required: []string{"session_id", "entries"},
		},
	}, traced(tracer, "agent_context_add", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextAdd(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name:        "agent_context_get",
		Description: "Retrieve specific context entries by ID.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"entry_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entry IDs to retrieve.",
				},
			},
			Required: []string{"entry_ids"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextGet(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_context_delete",
		Description: "Delete context entries by ID. Requires confirm=true.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"entry_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entry IDs to delete.",
				},
				"confirm": map[string]any{
					"type":        "boolean",
					"description": "Must be true to confirm deletion.",
				},
			},
			Required: []string{"entry_ids", "confirm"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextDelete(ctx, args)
	})

	// Context Retrieval Tools

	server.AddTool(mcp.Tool{
		Name:        "agent_context_search",
		Description: "Semantic search across agent context entries. Returns entries most similar to the query.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query text.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter to specific agent.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter to specific session.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"entry_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by entry types.",
				},
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by tags (any match).",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "Filter to entries about a specific file.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results (default: 10).",
				},
				"include_content": map[string]any{
					"type":        "boolean",
					"description": "Include full content in results (default: true).",
				},
			},
			Required: []string{"query"},
		},
	}, traced(tracer, "agent_context_search", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextSearch(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name:        "agent_context_recall",
		Description: "Intelligently recall relevant context for a task, optimized for token budget. Prioritizes decisions and summaries, then semantic matches.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "What are you trying to do? (used for relevance).",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter to specific agent.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter to specific session.",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Maximum tokens to return (default: 4000).",
				},
				"include_summaries": map[string]any{
					"type":        "boolean",
					"description": "Include session summaries (default: true).",
				},
				"include_decisions": map[string]any{
					"type":        "boolean",
					"description": "Prioritize decisions (default: true).",
				},
				"file_context": map[string]any{
					"type":        "string",
					"description": "Current file being worked on (for relevance boost).",
				},
			},
			Required: []string{"query"},
		},
	}, traced(tracer, "agent_context_recall", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextRecall(ctx, args)
	}))

	// Cross-Agent Coordination Tools

	server.AddTool(mcp.Tool{
		Name:        "agent_context_share",
		Description: "Share context entries with other agents.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"entry_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entry IDs to share.",
				},
				"target_agents": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Agent IDs to share with.",
				},
				"visibility": map[string]any{
					"type":        "string",
					"enum":        []string{"shared", "public"},
					"description": "Visibility level (default: shared).",
				},
			},
			Required: []string{"entry_ids", "target_agents"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextShare(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_context_query_shared",
		Description: "Query context shared by other agents. Only returns entries explicitly shared with you or marked public.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query text.",
				},
				"requesting_agent_id": map[string]any{
					"type":        "string",
					"description": "Your agent ID (for access control).",
				},
				"source_agent_id": map[string]any{
					"type":        "string",
					"description": "Specific agent to query (or empty for all shared).",
				},
				"entry_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by entry types.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results (default: 10).",
				},
			},
			Required: []string{"query", "requesting_agent_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextQueryShared(ctx, args)
	})

	// Summarization Tool

	server.AddTool(mcp.Tool{
		Name:        "agent_context_summarize",
		Description: "Generate a summary of session context. Useful for long-running sessions or before ending a session.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID to summarize.",
				},
			},
			Required: []string{"session_id"},
		},
	}, traced(tracer, "agent_context_summarize", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextSummarize(ctx, args)
	}))

	// Codebase Integration Tool

	server.AddTool(mcp.Tool{
		Name:        "agent_context_link_codebase",
		Description: "Link agent context to codebase-memory entries. Creates a code_context entry referencing a specific file/symbol.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID.",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "File path being referenced.",
				},
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repository ID from codebase-memory.",
				},
				"symbol": map[string]any{
					"type":        "string",
					"description": "Symbol name (function, class, etc.).",
				},
				"note": map[string]any{
					"type":        "string",
					"description": "Context note about this code.",
				},
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Tags for categorization.",
				},
			},
			Required: []string{"session_id", "file_path"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextLinkCodebase(ctx, args)
	})

	// Statistics Tool

	server.AddTool(mcp.Tool{
		Name:        "agent_context_stats",
		Description: "Get statistics about agent context storage.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by agent ID.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter by session ID.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextStats(ctx, args)
	})

	// =========================================================================
	// Enhanced Recall Tool
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_context_recall_enhanced",
		Description: "Unified recall across context entries, memory hierarchy, and knowledge graph. Queries all backends in parallel with round-robin result merging. Each result includes recall_source attribution.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "What are you trying to do? (used for relevance).",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter to specific agent.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter to specific session.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter to specific namespace.",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Maximum tokens to return (default: 4000).",
				},
				"include_summaries": map[string]any{
					"type":        "boolean",
					"description": "Include session summaries (default: true).",
				},
				"include_decisions": map[string]any{
					"type":        "boolean",
					"description": "Prioritize decisions (default: true).",
				},
				"file_context": map[string]any{
					"type":        "string",
					"description": "Current file being worked on (for relevance boost).",
				},
				"symbol_context": map[string]any{
					"type":        "string",
					"description": "Current symbol for relevance boost.",
				},
				"recency_weight": map[string]any{
					"type":        "number",
					"description": "Weight for recency (0.0-1.0, default: 0.2).",
				},
				"include_tasks": map[string]any{
					"type":        "boolean",
					"description": "Include active tasks (default: true).",
				},
				"cross_agent": map[string]any{
					"type":        "boolean",
					"description": "Search across all agents/sessions (default: false).",
				},
				"include_memory": map[string]any{
					"type":        "boolean",
					"description": "Include memory hierarchy results (default: true).",
				},
				"include_graph": map[string]any{
					"type":        "boolean",
					"description": "Include knowledge graph entity results (default: true).",
				},
				"scope": map[string]any{
					"type":        "array",
					"description": "Restrict to specific backends: \"context\", \"memory\", \"graph\". Empty = all.",
					"items":       map[string]any{"type": "string", "enum": []string{"context", "memory", "graph"}},
				},
			},
			Required: []string{"query"},
		},
	}, traced(tracer, "agent_context_recall_enhanced", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEnhancedRecall(ctx, args)
	}))
}
