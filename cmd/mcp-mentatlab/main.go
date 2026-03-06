package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "0.1.0"

type mentatlabServer struct {
	baseURL          string
	token            string
	maxResponseBytes int
	httpClient       *httpclient.Client
}

type mentatlabResponse struct {
	StatusCode int
	Data       any
	Truncated  bool
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-mentatlab", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-mentatlab")

	srv, err := newMentatlabServerFromEnv()
	if err != nil {
		return err
	}

	logger.Info("starting server", "name", "mcp-mentatlab", "version", version, "base_url", srv.baseURL)

	server := mcp.NewServer("mcp-mentatlab", version)
	server.SetInstructions("MentatLab orchestrator MCP server. Tools cover run lifecycle operations: create, start/run, list, get, and cancel.")

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

	return server.Run(ctx)
}

func newMentatlabServerFromEnv() (*mentatlabServer, error) {
	baseURL := strings.TrimSpace(env.StringWithFallbacks("MENTATLAB_BASE_URL", "ORCHESTRATOR_BASE_URL"))
	if baseURL == "" {
		return nil, mcperror.NotConfigured("MENTATLAB_BASE_URL", "set MENTATLAB_BASE_URL (or ORCHESTRATOR_BASE_URL) to the orchestrator API base URL")
	}

	timeoutSeconds := env.Int("MENTATLAB_TIMEOUT_SECONDS", 30)
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	maxBytes := env.Int("MENTATLAB_MAX_RESPONSE_BYTES", 2*1024*1024)
	if maxBytes <= 0 {
		maxBytes = 2 * 1024 * 1024
	}
	token := strings.TrimSpace(env.StringWithFallbacks("MENTATLAB_API_TOKEN", "MENTATLAB_BEARER_TOKEN"))

	httpCfg := httpclient.Config{
		Timeout:          time.Duration(timeoutSeconds) * time.Second,
		MaxRetries:       2,
		RetryBaseDelay:   100 * time.Millisecond,
		RetryMaxDelay:    2 * time.Second,
		MaxResponseBytes: maxBytes,
	}

	return &mentatlabServer{
		baseURL:          strings.TrimSuffix(baseURL, "/"),
		token:            token,
		maxResponseBytes: maxBytes,
		httpClient:       httpclient.New(httpCfg),
	}, nil
}

func (s *mentatlabServer) request(ctx context.Context, method, path string, query map[string]string, payload any, expectedStatuses ...int) (*mentatlabResponse, error) {
	normalizedPath := strings.TrimSpace(path)
	if normalizedPath == "" {
		return nil, mcperror.InvalidParam("path", "must not be empty")
	}
	if !strings.HasPrefix(normalizedPath, "/") {
		normalizedPath = "/" + normalizedPath
	}

	u, err := url.Parse(s.baseURL + normalizedPath)
	if err != nil {
		return nil, mcperror.InvalidParam("path", fmt.Sprintf("invalid path: %v", err))
	}
	if len(query) > 0 {
		q := u.Query()
		for key, value := range query {
			if strings.TrimSpace(value) == "" {
				continue
			}
			q.Set(key, value)
		}
		u.RawQuery = q.Encode()
	}

	var bodyReader *bytes.Reader
	if payload == nil {
		bodyReader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, mcperror.InvalidParam("payload", fmt.Sprintf("must be JSON serializable: %v", err))
		}
		bodyReader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(strings.TrimSpace(method)), u.String(), bodyReader)
	if err != nil {
		return nil, mcperror.WrapAPI("MentatLab", err)
	}

	req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.8")
	req.Header.Set("User-Agent", "mcp-mentatlab/"+version)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, mcperror.WrapAPI("MentatLab", err)
	}
	defer resp.Body.Close()

	rawBody, truncated, err := httpclient.ReadBodyWithLimit(resp.Body, s.maxResponseBytes)
	if err != nil {
		return nil, mcperror.WrapAPI("MentatLab", err)
	}

	if !statusExpected(resp.StatusCode, expectedStatuses) {
		return nil, mcperror.APIError("MentatLab", resp.StatusCode, string(rawBody))
	}

	parsed := parseBody(resp.Header.Get("Content-Type"), rawBody)
	return &mentatlabResponse{
		StatusCode: resp.StatusCode,
		Data:       parsed,
		Truncated:  truncated,
	}, nil
}

func statusExpected(statusCode int, expected []int) bool {
	if len(expected) == 0 {
		return statusCode >= 200 && statusCode < 300
	}
	for _, code := range expected {
		if code == statusCode {
			return true
		}
	}
	return false
}

func parseBody(contentType string, raw []byte) any {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return map[string]any{}
	}

	looksJSON := strings.Contains(strings.ToLower(contentType), "json") || trimmed[0] == '{' || trimmed[0] == '['
	if looksJSON {
		var parsed any
		if err := json.Unmarshal(trimmed, &parsed); err == nil {
			return parsed
		}
	}

	return string(trimmed)
}

func mcpSuccess(resp *mentatlabResponse) (*mcp.CallToolResult, error) {
	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"status_code": resp.StatusCode,
		"truncated":   resp.Truncated,
		"data":        resp.Data,
	})
}

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
