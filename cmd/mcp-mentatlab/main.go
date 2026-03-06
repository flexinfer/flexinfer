package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
)

var version = "0.2.0"

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
	server.SetInstructions("MentatLab orchestrator MCP server. Tools cover run lifecycle (create, start, list, get, cancel, clone), agent management (list, get, register), flow management (list, get, create), gate operations (approve, reject), and health diagnostics.")

	registerRunTools(server, srv, tracer)
	registerAgentTools(server, srv, tracer)
	registerFlowTools(server, srv, tracer)
	registerDiagnosticTools(server, srv, tracer)

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
