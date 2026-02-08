package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerCompactionTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	// =========================================================================
	// Compaction Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_compaction_status",
		Description: "Get compaction scheduler status and last run statistics.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleCompactionStatus(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_compaction_trigger",
		Description: "Manually trigger a compaction cycle. Returns statistics about what was processed.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, traced(tracer, "agent_compaction_trigger", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleCompactionTrigger(ctx, args)
	}))
}
