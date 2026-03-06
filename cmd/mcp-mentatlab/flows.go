// Flow operation handlers for mcp-mentatlab
package main

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"

	"go.opentelemetry.io/otel/trace"
)

func registerFlowTools(server *mcp.Server, srv *mentatlabServer, tracer trace.Tracer) {
	server.AddTool(mcp.Tool{
		Name:        "mentatlab_list_flows",
		Description: "List saved flows with pagination",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"limit":  map[string]any{"type": "integer", "description": "Page size (default: 50, max: 500)"},
				"offset": map[string]any{"type": "integer", "description": "Offset for pagination"},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_list_flows", srv.handleListFlows))

	server.AddTool(mcp.Tool{
		Name:        "mentatlab_get_flow",
		Description: "Get a flow by flow_id",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"flow_id": map[string]any{"type": "string", "description": "Flow identifier"},
			},
			Required: []string{"flow_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_get_flow", srv.handleGetFlow))

	server.AddTool(mcp.Tool{
		Name:        "mentatlab_create_flow",
		Description: "Create a new flow definition",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":        map[string]any{"type": "string", "description": "Flow name"},
				"graph":       map[string]any{"type": "object", "description": "Flow graph payload (nodes, edges)"},
				"description": map[string]any{"type": "string", "description": "Human-readable flow description"},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_create_flow", srv.handleCreateFlow))
}

// --- Flow handlers ---

func (s *mentatlabServer) handleListFlows(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	limit := v.Int("limit", 50)
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := v.Int("offset", 0)
	if offset < 0 {
		offset = 0
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := s.request(ctx, http.MethodGet, "/api/v1/flows", map[string]string{
		"limit":  strconv.Itoa(limit),
		"offset": strconv.Itoa(offset),
	}, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}

func (s *mentatlabServer) handleGetFlow(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	flowID := strings.TrimSpace(v.Required("flow_id"))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := s.request(ctx, http.MethodGet, "/api/v1/flows/"+url.PathEscape(flowID), nil, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}

func (s *mentatlabServer) handleCreateFlow(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := strings.TrimSpace(v.Required("name"))
	graph := v.Any("graph")
	description := strings.TrimSpace(v.String("description", ""))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	payload := map[string]any{
		"name": name,
	}
	if graph != nil {
		payload["graph"] = graph
	}
	if description != "" {
		payload["description"] = description
	}

	resp, err := s.request(ctx, http.MethodPost, "/api/v1/flows", nil, payload, http.StatusCreated)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}
