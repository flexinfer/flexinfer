package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerPresenceTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	// =========================================================================
	// Agent Presence Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_presence_register",
		Description: "Register an agent's presence. Announces the agent is active and available for coordination. Call this at the start of your work session for multi-agent discovery.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Unique agent identifier.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Current session ID.",
				},
				"agent_type": map[string]any{
					"type":        "string",
					"description": "Agent type (e.g., 'claude-code', 'codex', 'gemini').",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "What the agent is working on.",
				},
				"heartbeat_ttl_seconds": map[string]any{
					"type":        "integer",
					"description": "Heartbeat TTL in seconds (default: 120). Agent is considered offline after this.",
				},
			},
			Required: []string{"agent_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandlePresenceRegister(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_presence_heartbeat",
		Description: "Send a heartbeat to keep presence alive. Auto-registers agent if not yet registered (resilient to missed initial registration). Returns file conflicts if active_files overlap with other agents.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent identifier.",
				},
				"agent_type": map[string]any{
					"type":        "string",
					"description": "Agent type (e.g., 'claude-code', 'codex'). Used for auto-registration if agent is not yet registered.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Current session ID. Used for auto-registration if agent is not yet registered.",
				},
				"active_files": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Files currently being edited/viewed.",
				},
				"current_task": map[string]any{
					"type":        "string",
					"description": "Current task description.",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Current git branch.",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"active", "idle"},
					"description": "Optional presence status update: 'active' or 'idle'.",
				},
			},
			Required: []string{"agent_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandlePresenceHeartbeat(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_presence_list",
		Description: "List active agents. Discover what other agents are working on.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"include_offline": map[string]any{
					"type":        "boolean",
					"description": "Include agents whose heartbeat has expired (default: false).",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandlePresenceList(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_presence_deregister",
		Description: "Deregister an agent's presence. Clean exit releasing resources.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent identifier.",
				},
				"release_claims": map[string]any{
					"type":        "boolean",
					"description": "Release all file claims (default: true).",
				},
				"release_worktrees": map[string]any{
					"type":        "boolean",
					"description": "Mark assigned worktrees as orphaned (default: false).",
				},
			},
			Required: []string{"agent_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandlePresenceDeregister(ctx, args)
	})
}
