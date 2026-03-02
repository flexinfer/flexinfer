package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerGraphTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	// =========================================================================
	// Knowledge Graph Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_entity_add",
		Description: "Add entities to the knowledge graph. Entities represent code elements, concepts, decisions, or other important items.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID for provenance.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID for provenance.",
				},
				"entities": map[string]any{
					"type":        "array",
					"description": "Array of entities to add.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type": map[string]any{
								"type":        "string",
								"enum":        []string{"file", "function", "class", "module", "variable", "concept", "decision", "issue", "pr", "commit", "agent", "session", "task", "error", "service", "api", "database", "config"},
								"description": "Entity type.",
							},
							"name": map[string]any{
								"type":        "string",
								"description": "Entity name.",
							},
							"description": map[string]any{
								"type":        "string",
								"description": "Entity description.",
							},
							"namespace": map[string]any{
								"type":        "string",
								"description": "Namespace for grouping.",
							},
							"file_path": map[string]any{
								"type":        "string",
								"description": "File path (for code entities).",
							},
							"line_start": map[string]any{
								"type":        "integer",
								"description": "Starting line number.",
							},
							"line_end": map[string]any{
								"type":        "integer",
								"description": "Ending line number.",
							},
							"language": map[string]any{
								"type":        "string",
								"description": "Programming language.",
							},
							"signature": map[string]any{
								"type":        "string",
								"description": "Function/method signature.",
							},
							"properties": map[string]any{
								"type":        "object",
								"description": "Additional properties.",
							},
							"tags": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Tags for filtering.",
							},
						},
						"required": []string{"type", "name"},
					},
				},
			},
			Required: []string{"entities"},
		},
	}, traced(tracer, "agent_entity_add", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEntityAdd(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name:        "agent_entity_get",
		Description: "Retrieve entities by ID.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"entity_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entity IDs to retrieve.",
				},
			},
			Required: []string{"entity_ids"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEntityGet(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_entity_find",
		Description: "Search for entities by type, namespace, or name pattern.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"type": map[string]any{
					"type":        "string",
					"enum":        []string{"file", "function", "class", "module", "variable", "concept", "decision", "issue", "pr", "commit", "agent", "session", "task", "error", "service", "api", "database", "config"},
					"description": "Filter by entity type.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"name_pattern": map[string]any{
					"type":        "string",
					"description": "Regex pattern to match entity names.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results (default: 50).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEntityFind(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_entity_delete",
		Description: "Delete entities and their relations from the graph.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"entity_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entity IDs to delete.",
				},
				"confirm": map[string]any{
					"type":        "boolean",
					"description": "Must be true to confirm deletion.",
				},
			},
			Required: []string{"entity_ids", "confirm"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEntityDelete(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_relation_add",
		Description: "Add relations between entities in the knowledge graph.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID for provenance.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID for provenance.",
				},
				"relations": map[string]any{
					"type":        "array",
					"description": "Array of relations to add.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type": map[string]any{
								"type":        "string",
								"enum":        []string{"depends_on", "implements", "extends", "calls", "imports", "defines", "contains", "references", "overrides", "caused", "resolved", "blocked_by", "triggered", "created_by", "modified_by", "discovered_by", "assigned_to", "related_to", "similar_to", "opposite_of", "part_of", "version_of", "precedes", "follows", "occurred_with"},
								"description": "Relation type.",
							},
							"source_id": map[string]any{
								"type":        "string",
								"description": "Source entity ID.",
							},
							"target_id": map[string]any{
								"type":        "string",
								"description": "Target entity ID.",
							},
							"weight": map[string]any{
								"type":        "number",
								"description": "Relation strength/confidence (default: 1.0).",
							},
							"bidirectional": map[string]any{
								"type":        "boolean",
								"description": "Create reverse relation too.",
							},
							"evidence": map[string]any{
								"type":        "string",
								"description": "Evidence supporting this relation.",
							},
							"reasoning": map[string]any{
								"type":        "string",
								"description": "Reasoning for this relation.",
							},
							"properties": map[string]any{
								"type":        "object",
								"description": "Additional properties.",
							},
						},
						"required": []string{"type", "source_id", "target_id"},
					},
				},
			},
			Required: []string{"relations"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleRelationAdd(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_relation_get",
		Description: "Get relations for an entity.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"entity_id": map[string]any{
					"type":        "string",
					"description": "Entity ID to get relations for.",
				},
				"relation_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by relation types.",
				},
				"outgoing": map[string]any{
					"type":        "boolean",
					"description": "Include outgoing relations (default: true).",
				},
				"incoming": map[string]any{
					"type":        "boolean",
					"description": "Include incoming relations (default: true).",
				},
			},
			Required: []string{"entity_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleRelationGet(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_relation_delete",
		Description: "Delete relations from the graph.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"relation_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Relation IDs to delete.",
				},
				"confirm": map[string]any{
					"type":        "boolean",
					"description": "Must be true to confirm deletion.",
				},
			},
			Required: []string{"relation_ids", "confirm"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleRelationDelete(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_graph_query",
		Description: "Query the knowledge graph. Supports pattern matching like (file)-[calls]->(function) or traversal from an entity.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Pattern to match: (type)-[relation]->(type). Types and relations are optional.",
				},
				"entity_id": map[string]any{
					"type":        "string",
					"description": "Start traversal from this entity.",
				},
				"source_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter source entity types.",
				},
				"target_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter target entity types.",
				},
				"relation_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter relation types.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"max_depth": map[string]any{
					"type":        "integer",
					"description": "Maximum traversal depth (default: 2).",
				},
				"bidirectional": map[string]any{
					"type":        "boolean",
					"description": "Follow relations in both directions.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results.",
				},
			},
		},
	}, traced(tracer, "agent_graph_query", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleGraphQuery(ctx, args)
	}))

	// NOTE: agent_graph_find_path, agent_reasoning_chain_add,
	// agent_reasoning_chain_get, agent_reasoning_chain_list removed in SIMP-4.
	// Path finding was too specialized for agent use. Reasoning chains are
	// underused; agents use context entries instead. Underlying handlers
	// remain for internal use.

	server.AddTool(mcp.Tool{
		Name:        "agent_graph_stats",
		Description: "Get statistics about the knowledge graph.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleGraphStats(ctx, args)
	})
}
