package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerAnnotationTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	// =========================================================================
	// Code Annotation Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_code_annotate",
		Description: "[Deprecated: use agent_context_add with entry_type='annotation'] Create a code annotation attached to a specific file location.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID.",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "File path to annotate.",
				},
				"line_start": map[string]any{
					"type":        "integer",
					"description": "Starting line number.",
				},
				"line_end": map[string]any{
					"type":        "integer",
					"description": "Ending line number (optional).",
				},
				"annotation_type": map[string]any{
					"type":        "string",
					"enum":        []string{"todo", "fixme", "note", "question", "important", "bug", "perf"},
					"description": "Type of annotation (default: note).",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Annotation content.",
				},
				"symbol": map[string]any{
					"type":        "string",
					"description": "Related symbol name.",
				},
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repository ID for codebase-memory integration.",
				},
			},
			Required: []string{"session_id", "file_path", "line_start", "content"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleAnnotationAdd(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_code_annotations_get",
		Description: "[Deprecated: use agent_context_search with entry_type='annotation'] Get code annotations for a file or range.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "File path to get annotations for.",
				},
				"line_start": map[string]any{
					"type":        "integer",
					"description": "Filter by line range start.",
				},
				"line_end": map[string]any{
					"type":        "integer",
					"description": "Filter by line range end.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by agent ID.",
				},
				"annotation_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by annotation types.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum annotations to return (default: 50).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleAnnotationsGet(ctx, args)
	})
}
