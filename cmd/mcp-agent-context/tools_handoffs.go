package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerHandoffTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	// =========================================================================
	// Agent Handoff Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_handoff_create",
		Description: "Create a handoff package for another agent. Packages session context with instructions.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Source session ID.",
				},
				"target_agent_id": map[string]any{
					"type":        "string",
					"description": "Target agent to hand off to.",
				},
				"handoff_type": map[string]any{
					"type":        "string",
					"enum":        []string{"full", "selective", "summary_only"},
					"description": "Type of handoff (default: summary_only).",
				},
				"entry_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entry IDs for selective handoff.",
				},
				"instructions": map[string]any{
					"type":        "string",
					"description": "Instructions for the target agent.",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Maximum tokens in handoff (default: 8000).",
				},
			},
			Required: []string{"session_id", "target_agent_id"},
		},
	}, traced(tracer, "agent_handoff_create", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleHandoffCreate(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name:        "agent_handoff_accept",
		Description: "Accept and import a handoff from another agent.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"handoff_id": map[string]any{
					"type":        "string",
					"description": "Handoff ID to accept.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Your session ID.",
				},
				"import_entries": map[string]any{
					"type":        "boolean",
					"description": "Import full entries (default: false, summary only).",
				},
			},
			Required: []string{"handoff_id", "session_id"},
		},
	}, traced(tracer, "agent_handoff_accept", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleHandoffAccept(ctx, args)
	}))

	// =========================================================================
	// Handoff Inbox Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_handoff_inbox",
		Description: "List pending handoffs for an agent. The agent's 'inbox' for receiving work from other agents.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID to check inbox for.",
				},
				"include_viewed": map[string]any{
					"type":        "boolean",
					"description": "Include already-viewed handoffs (default: false).",
				},
			},
			Required: []string{"agent_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleHandoffInbox(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_handoff_reject",
		Description: "Reject a handoff with an optional reason.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"handoff_id": map[string]any{
					"type":        "string",
					"description": "Handoff ID to reject.",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Reason for rejection.",
				},
			},
			Required: []string{"handoff_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleHandoffReject(ctx, args)
	})
}
