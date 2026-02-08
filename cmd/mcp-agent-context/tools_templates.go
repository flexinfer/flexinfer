package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerTemplateTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	// =========================================================================
	// Session Template Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_template_create",
		Description: "Create a reusable session template.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Template name.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Template description.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace for the template.",
				},
				"from_session_id": map[string]any{
					"type":        "string",
					"description": "Copy from existing session.",
				},
				"entry_types_to_include": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entry types to include when copying.",
				},
				"created_by": map[string]any{
					"type":        "string",
					"description": "Creator agent ID.",
				},
			},
			Required: []string{"name"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleTemplateCreate(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_template_list",
		Description: "List available session templates.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum templates to return (default: 50).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleTemplateList(ctx, args)
	})
}
