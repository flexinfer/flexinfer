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
								"enum":        []string{"file_read", "decision", "finding", "question", "note", "error", "code_context", "annotation"},
								"description": "Type of context entry. For graph durability, also used as entity type. Use 'annotation' for code annotations (replaces agent_code_annotate).",
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
								"description": "File path (for file_read entries, annotation entries, or graph entities).",
							},
							"line_start": map[string]any{
								"type":        "integer",
								"description": "Start line (for file_read and annotation entries).",
							},
							"line_end": map[string]any{
								"type":        "integer",
								"description": "End line (for file_read and annotation entries).",
							},
							"annotation_type": map[string]any{
								"type":        "string",
								"enum":        []string{"todo", "fixme", "note", "question", "important", "bug", "perf"},
								"description": "Annotation subtype (for entry_type='annotation').",
							},
							"symbol": map[string]any{
								"type":        "string",
								"description": "Related symbol name (for annotation entries).",
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

	// NOTE: agent_context_get and agent_context_delete removed in SIMP-8.
	// Retrieval is handled by agent_recall (SIMP-1). Deletion is rare and
	// available via CLI only.

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
		Description: "[Deprecated: use agent_recall] Intelligently recall relevant context for a task, optimized for token budget. Prioritizes decisions and summaries, then semantic matches.",
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
		return svc.HandleDeprecatedContextRecall(ctx, args)
	}))

	// NOTE: agent_context_share and agent_context_query_shared removed in SIMP-8.
	// Cross-agent sharing is handled by the handoff system.

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

	// NOTE: agent_context_link_codebase removed in SIMP-8. Use
	// agent_context_add with entry_type="code_context" instead.

	// NOTE: agent_context_stats removed in SIMP-8. Statistics available
	// via CLI only (loom agent context stats).

	// =========================================================================
	// Unified Recall Tool (replaces agent_context_recall, agent_context_recall_enhanced, agent_memory_recall)
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_recall",
		Description: "Unified recall across context and memory backends. Use scope to control which backends are queried: 'context' (default), 'memory', or 'all'. Replaces agent_context_recall, agent_context_recall_enhanced, and agent_memory_recall.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "What are you trying to do? (used for relevance).",
				},
				"scope": map[string]any{
					"type":        "string",
					"enum":        []string{"context", "memory", "all"},
					"description": "Which backends to query: 'context' (default), 'memory', or 'all'.",
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
				"memory_tiers": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Memory tiers to search: working, short_term, long_term (scope=memory or all).",
				},
				"memory_categories": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Memory categories to filter (scope=memory or all).",
				},
				"memory_tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Memory tags to filter (scope=memory or all).",
				},
			},
			Required: []string{"query"},
		},
	}, traced(tracer, "agent_recall", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleUnifiedRecall(ctx, args)
	}))

	// =========================================================================
	// Deprecated: Enhanced Recall Tool (use agent_recall instead)
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_context_recall_enhanced",
		Description: "[Deprecated: use agent_recall] Enhanced recall with task priority, symbol context, recency weighting, and code annotations.",
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
		return svc.HandleDeprecatedEnhancedRecall(ctx, args)
	}))
}
