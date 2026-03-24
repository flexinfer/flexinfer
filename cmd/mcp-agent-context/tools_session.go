package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerSessionTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	server.AddTool(mcp.Tool{
		Name:        "agent_session_start",
		Description: "Start a new agent context session. Returns session_id for subsequent calls. Use resume_session_id to continue an existing session. After starting, check agent_handoff_inbox for pending work from other agents.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Unique agent identifier (e.g., 'claude-code-1'). Required unless AGENT_CONTEXT_DEFAULT_AGENT_ID is set.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Optional project/task namespace for grouping sessions.",
				},
				"project": map[string]any{
					"type":        "string",
					"description": "Optional canonical project identifier for orchestration correlation. Defaults from namespace or pipeline project.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Optional session description.",
				},
				"working_dir": map[string]any{
					"type":        "string",
					"description": "Optional working directory path.",
				},
				"resume_session_id": map[string]any{
					"type":        "string",
					"description": "Resume an existing session instead of creating a new one.",
				},
				"pipeline_project": map[string]any{
					"type":        "string",
					"description": "Optional GitLab project path linked to the session's CI pipeline.",
				},
				"pipeline_id": map[string]any{
					"type":        "integer",
					"description": "Optional GitLab pipeline ID linked to the session.",
				},
				"pipeline_ref": map[string]any{
					"type":        "object",
					"description": "Optional explicit CI pipeline reference for session orchestration linking.",
					"properties": map[string]any{
						"id":      map[string]any{"type": "integer"},
						"project": map[string]any{"type": "string"},
						"ref":     map[string]any{"type": "string"},
						"web_url": map[string]any{"type": "string"},
					},
				},
			},
		},
	}, traced(tracer, "agent_session_start", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleSessionStart(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name:        "agent_session_end",
		Description: "End an agent session. Optionally triggers summarization of session context. Accepts cleanup=true (default) to auto-release file claims, deregister presence, and mark worktrees as orphaned.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID to end.",
				},
				"summarize": map[string]any{
					"type":        "boolean",
					"description": "Generate session summary on end (default: true).",
				},
				"summary_async": map[string]any{
					"type":        "boolean",
					"description": "Run summarization in background and return immediately (default: false).",
				},
				"cleanup": map[string]any{
					"type":        "boolean",
					"description": "Auto-release file claims, deregister presence, and mark worktrees as orphaned (default: true).",
				},
			},
			Required: []string{"session_id"},
		},
	}, traced(tracer, "agent_session_end", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleSessionEnd(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name:        "agent_session_list",
		Description: "List sessions, optionally filtered by agent, namespace, or status. Omit agent_id to list sessions from all agents.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID to filter by. Omit to list all agents' sessions.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"active", "ended", "summarized"},
					"description": "Filter by session status.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum sessions to return (default: 20).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleSessionList(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_session_delete",
		Description: "Delete a single session by ID from both memory and Qdrant.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID to delete.",
				},
			},
			Required: []string{"session_id"},
		},
	}, traced(tracer, "agent_session_delete", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleSessionDelete(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name:        "agent_session_prune",
		Description: "Delete stale sessions matching status and age criteria. Defaults to pruning ended/summarized sessions older than 72 hours.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"max_age_hours": map[string]any{
					"type":        "integer",
					"description": "Maximum age in hours (default: 72). Sessions older than this are pruned.",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "Comma-separated status filter (default: 'ended,summarized').",
				},
				"dry_run": map[string]any{
					"type":        "boolean",
					"description": "Preview what would be pruned without deleting (default: false).",
				},
			},
		},
	}, traced(tracer, "agent_session_prune", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleSessionPrune(ctx, args)
	}))
}
