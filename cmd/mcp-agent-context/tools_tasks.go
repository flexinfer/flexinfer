package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerTaskTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	// =========================================================================
	// Task Tracking Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_task_add",
		Description: "Add tasks/todos discovered during agent sessions. Tasks are prioritized in context recall.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID to add tasks to.",
				},
				"tasks": map[string]any{
					"type":        "array",
					"description": "Array of tasks to add.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title": map[string]any{
								"type":        "string",
								"description": "Short task title.",
							},
							"context": map[string]any{
								"type":        "string",
								"description": "Additional context about the task.",
							},
							"priority": map[string]any{
								"type":        "string",
								"enum":        []string{"low", "medium", "high", "critical"},
								"description": "Task priority (default: medium).",
							},
							"file_path": map[string]any{
								"type":        "string",
								"description": "Related file path.",
							},
							"line_number": map[string]any{
								"type":        "integer",
								"description": "Related line number.",
							},
							"tags": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Tags for categorization.",
							},
							"blocked_by": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "IDs of tasks blocking this one.",
							},
						},
						"required": []string{"title"},
					},
				},
			},
			Required: []string{"session_id", "tasks"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleTaskAdd(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_task_update",
		Description: "Update task status or resolution.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task ID to update.",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"pending", "in_progress", "completed", "blocked"},
					"description": "New task status.",
				},
				"resolution": map[string]any{
					"type":        "string",
					"description": "Resolution description (for completed tasks).",
				},
			},
			Required: []string{"task_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleTaskUpdate(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_task_list",
		Description: "List tasks with filtering options.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter by session ID.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by agent ID.",
				},
				"status": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by status(es).",
				},
				"include_completed": map[string]any{
					"type":        "boolean",
					"description": "Include completed tasks (default: false).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum tasks to return (default: 50).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleTaskList(ctx, args)
	})
}
