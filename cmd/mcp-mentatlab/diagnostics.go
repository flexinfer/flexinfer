// Diagnostic handlers for mcp-mentatlab
package main

import (
	"context"
	"net/http"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcpotel"

	"go.opentelemetry.io/otel/trace"
)

func registerDiagnosticTools(server *mcp.Server, srv *mentatlabServer, tracer trace.Tracer) {
	server.AddTool(mcp.Tool{
		Name:        "mentatlab_health",
		Description: "Check MentatLab orchestrator health status",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_health", srv.handleHealth))
}

func (s *mentatlabServer) handleHealth(ctx context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
	resp, err := s.request(ctx, http.MethodGet, "/health", nil, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}
