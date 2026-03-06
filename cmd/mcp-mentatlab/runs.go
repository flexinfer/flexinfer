// Run lifecycle and operation handlers for mcp-mentatlab
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

func registerRunTools(server *mcp.Server, srv *mentatlabServer, tracer trace.Tracer) {
	server.AddTool(mcp.Tool{
		Name:        "mentatlab_list_runs",
		Description: "List MentatLab runs with cursor pagination support",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"limit":  map[string]any{"type": "integer", "description": "Page size (default: 50, max: 500)"},
				"cursor": map[string]any{"type": "string", "description": "Pagination cursor from previous response"},
				"owner":  map[string]any{"type": "string", "description": "Filter runs by owner"},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_list_runs", srv.handleListRuns))

	server.AddTool(mcp.Tool{
		Name:        "mentatlab_get_run",
		Description: "Get a run by run_id",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"run_id": map[string]any{"type": "string", "description": "Run identifier"},
			},
			Required: []string{"run_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_get_run", srv.handleGetRun))

	server.AddTool(mcp.Tool{
		Name:        "mentatlab_create_run",
		Description: "Create a run from an inline plan payload",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":           map[string]any{"type": "string", "description": "Run name"},
				"plan":           map[string]any{"type": "object", "description": "Run plan payload"},
				"auto_start":     map[string]any{"type": "boolean", "description": "Start execution immediately if true"},
				"webhook_url":    map[string]any{"type": "string", "description": "Optional completion webhook URL"},
				"webhook_secret": map[string]any{"type": "string", "description": "Optional webhook signing secret"},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_create_run", srv.handleCreateRun))

	server.AddTool(mcp.Tool{
		Name:        "mentatlab_start_run",
		Description: "Start a created run by run_id",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"run_id": map[string]any{"type": "string", "description": "Run identifier"},
			},
			Required: []string{"run_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_start_run", srv.handleStartRun))

	server.AddTool(mcp.Tool{
		Name:        "mentatlab_run_flow",
		Description: "Create and start a run from an existing flow_id",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"flow_id":  map[string]any{"type": "string", "description": "Flow identifier"},
				"timeout":  map[string]any{"type": "string", "description": "Optional run timeout duration (for example 5m)"},
				"owner":    map[string]any{"type": "string", "description": "Optional owner override"},
				"run_name": map[string]any{"type": "string", "description": "Optional run name override"},
			},
			Required: []string{"flow_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_run_flow", srv.handleRunFlow))

	server.AddTool(mcp.Tool{
		Name:        "mentatlab_cancel_run",
		Description: "Cancel a running run by run_id",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"run_id": map[string]any{"type": "string", "description": "Run identifier"},
			},
			Required: []string{"run_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_cancel_run", srv.handleCancelRun))

	server.AddTool(mcp.Tool{
		Name:        "mentatlab_clone_run",
		Description: "Clone an existing run, optionally starting it immediately",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"run_id":     map[string]any{"type": "string", "description": "Run identifier to clone"},
				"auto_start": map[string]any{"type": "boolean", "description": "Start the cloned run immediately if true"},
			},
			Required: []string{"run_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_clone_run", srv.handleCloneRun))

	server.AddTool(mcp.Tool{
		Name:        "mentatlab_approve_gate",
		Description: "Approve a gate node in a running DAG",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"run_id":  map[string]any{"type": "string", "description": "Run identifier"},
				"node_id": map[string]any{"type": "string", "description": "Gate node identifier"},
			},
			Required: []string{"run_id", "node_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_approve_gate", srv.handleApproveGate))

	server.AddTool(mcp.Tool{
		Name:        "mentatlab_reject_gate",
		Description: "Reject a gate node in a running DAG",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"run_id":  map[string]any{"type": "string", "description": "Run identifier"},
				"node_id": map[string]any{"type": "string", "description": "Gate node identifier"},
			},
			Required: []string{"run_id", "node_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "mentatlab_reject_gate", srv.handleRejectGate))
}

// --- Run handlers ---

func (s *mentatlabServer) handleListRuns(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	limit := v.Int("limit", 50)
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	cursor := strings.TrimSpace(v.String("cursor", ""))
	owner := strings.TrimSpace(v.String("owner", ""))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := s.request(ctx, http.MethodGet, "/api/v1/runs", map[string]string{
		"limit":  strconv.Itoa(limit),
		"cursor": cursor,
		"owner":  owner,
	}, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}

func (s *mentatlabServer) handleGetRun(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	runID := strings.TrimSpace(v.Required("run_id"))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := s.request(ctx, http.MethodGet, "/api/v1/runs/"+url.PathEscape(runID), nil, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}

func (s *mentatlabServer) handleCreateRun(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := strings.TrimSpace(v.Required("name"))
	plan := v.Any("plan")
	autoStart := v.Bool("auto_start", false)
	webhookURL := strings.TrimSpace(v.String("webhook_url", ""))
	webhookSecret := strings.TrimSpace(v.String("webhook_secret", ""))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	payload := map[string]any{
		"name":       name,
		"auto_start": autoStart,
	}
	if plan != nil {
		payload["plan"] = plan
	}
	if webhookURL != "" {
		payload["webhook_url"] = webhookURL
	}
	if webhookSecret != "" {
		payload["webhook_secret"] = webhookSecret
	}

	resp, err := s.request(ctx, http.MethodPost, "/api/v1/runs", nil, payload, http.StatusCreated)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}

func (s *mentatlabServer) handleStartRun(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	runID := strings.TrimSpace(v.Required("run_id"))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := s.request(ctx, http.MethodPost, "/api/v1/runs/"+url.PathEscape(runID)+"/start", nil, map[string]any{}, http.StatusOK)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}

func (s *mentatlabServer) handleRunFlow(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	flowID := strings.TrimSpace(v.Required("flow_id"))
	timeout := strings.TrimSpace(v.String("timeout", ""))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	payload := map[string]any{}
	if timeout != "" {
		payload["timeout"] = timeout
	}

	resp, err := s.request(ctx, http.MethodPost, "/api/v1/flows/"+url.PathEscape(flowID)+"/run", nil, payload, http.StatusCreated)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}

func (s *mentatlabServer) handleCancelRun(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	runID := strings.TrimSpace(v.Required("run_id"))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := s.request(ctx, http.MethodPost, "/api/v1/runs/"+url.PathEscape(runID)+"/cancel", nil, map[string]any{}, http.StatusOK)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}

func (s *mentatlabServer) handleCloneRun(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	runID := strings.TrimSpace(v.Required("run_id"))
	autoStart := v.Bool("auto_start", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	payload := map[string]any{}
	if autoStart {
		payload["auto_start"] = true
	}

	resp, err := s.request(ctx, http.MethodPost, "/api/v1/runs/"+url.PathEscape(runID)+"/clone", nil, payload, http.StatusCreated)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}

func (s *mentatlabServer) handleApproveGate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	runID := strings.TrimSpace(v.Required("run_id"))
	nodeID := strings.TrimSpace(v.Required("node_id"))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := "/api/v1/runs/" + url.PathEscape(runID) + "/nodes/" + url.PathEscape(nodeID) + "/approve"
	resp, err := s.request(ctx, http.MethodPost, path, nil, map[string]any{})
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}

func (s *mentatlabServer) handleRejectGate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	runID := strings.TrimSpace(v.Required("run_id"))
	nodeID := strings.TrimSpace(v.Required("node_id"))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := "/api/v1/runs/" + url.PathEscape(runID) + "/nodes/" + url.PathEscape(nodeID) + "/reject"
	resp, err := s.request(ctx, http.MethodPost, path, nil, map[string]any{})
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcpSuccess(resp)
}
