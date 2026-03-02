package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerMemoryTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	// Memory Hierarchy Tools

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_add",
		Description: "Add items to the tiered memory hierarchy. Items start in working memory and can be promoted to short-term or long-term memory.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID for the memories.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace for organization.",
				},
				"items": map[string]any{
					"type":        "array",
					"description": "Memory items to add.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title": map[string]any{
								"type":        "string",
								"description": "Short title for the memory.",
							},
							"content": map[string]any{
								"type":        "string",
								"description": "Full content of the memory.",
							},
							"tier": map[string]any{
								"type":        "string",
								"enum":        []string{"working", "short_term", "long_term"},
								"description": "Memory tier (default: working).",
							},
							"importance": map[string]any{
								"type":        "string",
								"enum":        []string{"low", "medium", "high", "critical"},
								"description": "Importance level (default: medium).",
							},
							"category": map[string]any{
								"type":        "string",
								"description": "Category for grouping (e.g., 'decision', 'insight', 'task').",
							},
							"tags": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Tags for filtering.",
							},
							"metadata": map[string]any{
								"type":        "object",
								"description": "Additional metadata.",
							},
							"related_ids": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Related memory item IDs.",
							},
						},
						"required": []string{"title", "content"},
					},
				},
			},
			Required: []string{"items"},
		},
	}, traced(tracer, "agent_memory_add", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryAdd(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_get",
		Description: "Retrieve memory items by ID.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"item_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Memory item IDs to retrieve.",
				},
			},
			Required: []string{"item_ids"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryGet(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_recall",
		Description: "[Deprecated: use agent_recall with scope='memory'] Recall memories matching criteria. Returns most relevant items within token budget, prioritized by importance and recency.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query (matches title and content).",
				},
				"tiers": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Tiers to search: working, short_term, long_term (default: all).",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter by session.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by agent.",
				},
				"categories": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by categories.",
				},
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by tags.",
				},
				"min_importance": map[string]any{
					"type":        "number",
					"description": "Minimum importance score (0-1).",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Maximum tokens to return (default: 8000).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum items (default: 100).",
				},
			},
		},
	}, traced(tracer, "agent_memory_recall", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryRecall(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_delete",
		Description: "Delete memory items.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"item_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Memory item IDs to delete.",
				},
				"confirm": map[string]any{
					"type":        "boolean",
					"description": "Must be true to confirm deletion.",
				},
			},
			Required: []string{"item_ids", "confirm"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryDelete(ctx, args)
	})

	// NOTE: agent_memory_promote, agent_memory_demote, agent_memory_compress,
	// agent_memory_merge removed in SIMP-2. Tier management is automatic via
	// background compaction. Underlying handlers remain for internal/CLI use.

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_stats",
		Description: "Get statistics about the memory hierarchy.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryStats(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_policy_get",
		Description: "Get the retention policy for a memory tier.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"tier": map[string]any{
					"type":        "string",
					"enum":        []string{"working", "short_term", "long_term"},
					"description": "Memory tier.",
				},
			},
			Required: []string{"tier"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryPolicyGet(ctx, args)
	})

	// NOTE: agent_memory_policy_set removed in SIMP-2. Policy configuration
	// is now CLI/config only. agent_memory_policy_get retained for read-only
	// introspection.

	// NOTE: agent_memory_export and agent_memory_import removed in SIMP-3.
	// Memory persistence is handled by Qdrant storage. Export/import available
	// via CLI only (loom agent memory export/import).
}
