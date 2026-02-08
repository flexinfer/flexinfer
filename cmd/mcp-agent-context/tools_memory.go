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
		Description: "Recall memories matching criteria. Returns most relevant items within token budget, prioritized by importance and recency.",
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

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_promote",
		Description: "Promote memory items to a higher tier (working -> short_term -> long_term).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"item_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Memory item IDs to promote.",
				},
			},
			Required: []string{"item_ids"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryPromote(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_demote",
		Description: "Demote memory items to a lower tier (long_term -> short_term -> working).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"item_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Memory item IDs to demote.",
				},
			},
			Required: []string{"item_ids"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryDemote(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_compress",
		Description: "Compress memory items to reduce token usage. Can compress specific items or run tier-wide compression.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"item_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Specific item IDs to compress.",
				},
				"tier": map[string]any{
					"type":        "string",
					"enum":        []string{"working", "short_term", "long_term"},
					"description": "Run compression on entire tier.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryCompress(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_merge",
		Description: "Merge multiple memory items into a single item. Original items are archived.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"item_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "IDs of items to merge (minimum 2).",
				},
				"new_title": map[string]any{
					"type":        "string",
					"description": "Title for the merged item.",
				},
			},
			Required: []string{"item_ids"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryMerge(ctx, args)
	})

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

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_policy_set",
		Description: "Update the retention policy for a memory tier.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"tier": map[string]any{
					"type":        "string",
					"enum":        []string{"working", "short_term", "long_term"},
					"description": "Memory tier to configure.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Policy name.",
				},
				"default_ttl_hours": map[string]any{
					"type":        "integer",
					"description": "Default time-to-live in hours (0 = no expiry).",
				},
				"compress_after_hours": map[string]any{
					"type":        "integer",
					"description": "Hours before auto-compression.",
				},
				"compression_ratio": map[string]any{
					"type":        "number",
					"description": "Target compression ratio (0-1).",
				},
				"merge_threshold": map[string]any{
					"type":        "number",
					"description": "Similarity threshold for auto-merge (0-1).",
				},
				"promotion_threshold": map[string]any{
					"type":        "number",
					"description": "Importance threshold for promotion (0-1).",
				},
				"demotion_threshold": map[string]any{
					"type":        "number",
					"description": "Importance threshold for demotion (0-1).",
				},
				"access_count_threshold": map[string]any{
					"type":        "integer",
					"description": "Access count for auto-promotion.",
				},
				"max_items": map[string]any{
					"type":        "integer",
					"description": "Maximum items in tier.",
				},
				"max_tokens": map[string]any{
					"type":        "integer",
					"description": "Maximum tokens in tier.",
				},
				"dedupe_enabled": map[string]any{
					"type":        "boolean",
					"description": "Enable deduplication.",
				},
				"dedupe_similarity": map[string]any{
					"type":        "number",
					"description": "Similarity threshold for deduplication (0-1).",
				},
			},
			Required: []string{"tier"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryPolicySet(ctx, args)
	})

	// =========================================================================
	// Memory Export/Import Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_export",
		Description: "Export memory to universal JSON format. Supports filtering by namespace, session, tags, and time range.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID for export metadata.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter by session.",
				},
				"format": map[string]any{
					"type":        "string",
					"enum":        []string{"loom", "mem0", "supermemory"},
					"description": "Export format (default: 'loom').",
				},
				"tiers": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by memory tiers (working, short_term, long_term).",
				},
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by tags.",
				},
				"include_graph": map[string]any{
					"type":        "boolean",
					"description": "Include knowledge graph (default: true).",
				},
				"include_workflows": map[string]any{
					"type":        "boolean",
					"description": "Include workflows (default: false).",
				},
				"include_embeddings": map[string]any{
					"type":        "boolean",
					"description": "Include embedding vectors (default: false).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryExport(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_import",
		Description: "Import memory from universal JSON format.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"data": map[string]any{
					"type":        "object",
					"description": "Universal memory format data to import.",
				},
				"conflict_strategy": map[string]any{
					"type":        "string",
					"enum":        []string{"skip", "overwrite", "merge"},
					"description": "How to handle ID conflicts (default: 'skip').",
				},
				"id_prefix": map[string]any{
					"type":        "string",
					"description": "Prefix for imported IDs to avoid collisions.",
				},
				"target_tier": map[string]any{
					"type":        "string",
					"description": "Override tier for imported memories.",
				},
				"target_namespace": map[string]any{
					"type":        "string",
					"description": "Override namespace for imported items.",
				},
				"import_graph": map[string]any{
					"type":        "boolean",
					"description": "Import knowledge graph (default: true).",
				},
				"import_workflows": map[string]any{
					"type":        "boolean",
					"description": "Import workflows (default: false).",
				},
				"regenerate_embeddings": map[string]any{
					"type":        "boolean",
					"description": "Regenerate embeddings for imported items (default: false).",
				},
			},
			Required: []string{"data"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryImport(ctx, args)
	})
}
