package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerFileClaimTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	// =========================================================================
	// File Claims (Advisory Locks) Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_file_claim_acquire",
		Description: "Claim a file for editing/review. Advisory lock — returns conflict info if other agents hold claims.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent identifier.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Current session ID.",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "File path to claim.",
				},
				"claim_type": map[string]any{
					"type":        "string",
					"enum":        []string{"edit", "review", "reserve"},
					"description": "Type of claim (default: 'edit').",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Why this file is being claimed.",
				},
			},
			Required: []string{"agent_id", "session_id", "file_path"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleFileClaimAcquire(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_file_claim_release",
		Description: "Release file claims. Set file_path='all' to release all claims for the agent.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent identifier.",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "File path to release, or 'all' for all claims.",
				},
			},
			Required: []string{"agent_id", "file_path"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleFileClaimRelease(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_file_claim_query",
		Description: "Check who holds claims on specific files.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"file_paths": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "File paths to check.",
				},
				"exclude_agent": map[string]any{
					"type":        "string",
					"description": "Exclude this agent from results.",
				},
			},
			Required: []string{"file_paths"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleFileClaimQuery(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_file_claim_list",
		Description: "List file claims, optionally filtered by agent or claim type.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by agent ID.",
				},
				"claim_type": map[string]any{
					"type":        "string",
					"enum":        []string{"edit", "review", "reserve"},
					"description": "Filter by claim type.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleFileClaimList(ctx, args)
	})
}
