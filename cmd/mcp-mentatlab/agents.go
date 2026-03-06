// Agent operation handlers for mcp-mentatlab
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

func registerAgentTools(server *mcp.Server, srv *mentatlabServer, tracer trace.Tracer) {
	server.AddTool(mcp.Tool{
		Name:        "mentatlab_list_agents",
		Description: "List registered agents with optional filtering",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"limit":        map[string]any{"type": "integer", "description": "Page size (default: 50, max: 500)"},
				"offset":       map[string]any{"type": "integer", "description": "Offset for pagination"},
				"capabilities": map[string]any{"type": "string", "description": "Comma-separated capabilities to filter by"},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_list_agents", srv.handleListAgents))

	server.AddTool(mcp.Tool{
		Name:        "mentatlab_get_agent",
		Description: "Get an agent by agent_id",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{"type": "string", "description": "Agent identifier"},
			},
			Required: []string{"agent_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_get_agent", srv.handleGetAgent))

	server.AddTool(mcp.Tool{
		Name:        "mentatlab_register_agent",
		Description: "Register a new agent with the orchestrator",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":         map[string]any{"type": "string", "description": "Agent name"},
				"image":        map[string]any{"type": "string", "description": "Container image for K8s execution"},
				"command":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Command to run the agent"},
				"description":  map[string]any{"type": "string", "description": "Human-readable agent description"},
				"capabilities": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Agent capabilities list"},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_register_agent", srv.handleRegisterAgent))
}

// --- Agent handlers ---

func (s *mentatlabServer) handleListAgents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
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
	capabilities := strings.TrimSpace(v.String("capabilities", ""))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := s.request(ctx, http.MethodGet, "/api/v1/agents", map[string]string{
		"limit":        strconv.Itoa(limit),
		"offset":       strconv.Itoa(offset),
		"capabilities": capabilities,
	}, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}

func (s *mentatlabServer) handleGetAgent(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := strings.TrimSpace(v.Required("agent_id"))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := s.request(ctx, http.MethodGet, "/api/v1/agents/"+url.PathEscape(agentID), nil, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}

func (s *mentatlabServer) handleRegisterAgent(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := strings.TrimSpace(v.Required("name"))
	image := strings.TrimSpace(v.String("image", ""))
	command := v.Any("command")
	description := strings.TrimSpace(v.String("description", ""))
	capabilities := v.Any("capabilities")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	payload := map[string]any{
		"name": name,
	}
	if image != "" {
		payload["image"] = image
	}
	if command != nil {
		payload["command"] = command
	}
	if description != "" {
		payload["description"] = description
	}
	if capabilities != nil {
		payload["capabilities"] = capabilities
	}

	resp, err := s.request(ctx, http.MethodPost, "/api/v1/agents", nil, payload, http.StatusCreated)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}
