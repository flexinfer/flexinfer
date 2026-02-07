// mcp-prometheus is a fast Prometheus MCP server written in Go.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/strutil"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "1.0.0"

type promServer struct {
	url        string
	httpClient *httpclient.Client
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-prometheus", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-prometheus")

	promURL := os.Getenv("PROMETHEUS_URL")
	if promURL == "" {
		promURL = "http://prometheus.monitoring.svc.cluster.local:9090"
	}

	prom := &promServer{
		url:        promURL,
		httpClient: httpclient.NewDefault(),
	}

	logger.Info("starting server", "name", "mcp-prometheus", "version", version, "url", promURL)

	server := mcp.NewServer("mcp-prometheus", version)
	server.SetInstructions("Fast Go-native Prometheus MCP server. Query metrics, alerts, and targets.")

	// query
	server.AddTool(mcp.Tool{
		Name:        "query",
		Description: "Execute an instant PromQL query",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "PromQL query expression",
				},
				"time": map[string]any{
					"type":        "string",
					"description": "Evaluation timestamp (RFC3339 or Unix timestamp). Defaults to current time.",
				},
			},
			Required: []string{"query"},
		},
	}, mcpotel.TracedToolHandler(tracer, "query", prom.handleQuery))

	// query_range
	server.AddTool(mcp.Tool{
		Name:        "query_range",
		Description: "Execute a range PromQL query",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "PromQL query expression",
				},
				"start": map[string]any{
					"type":        "string",
					"description": "Start time (RFC3339 or Unix timestamp)",
				},
				"end": map[string]any{
					"type":        "string",
					"description": "End time (RFC3339 or Unix timestamp). Defaults to now.",
				},
				"step": map[string]any{
					"type":        "string",
					"description": "Query resolution step (e.g., '15s', '1m', '1h'). Defaults to '1m'.",
				},
			},
			Required: []string{"query", "start"},
		},
	}, mcpotel.TracedToolHandler(tracer, "query_range", prom.handleQueryRange))

	// list_metrics
	server.AddTool(mcp.Tool{
		Name:        "list_metrics",
		Description: "List all available metric names",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"match": map[string]any{
					"type":        "string",
					"description": "Filter pattern (regex). Applied client-side to filter metric names.",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "list_metrics", prom.handleListMetrics))

	// list_labels
	server.AddTool(mcp.Tool{
		Name:        "list_labels",
		Description: "List all label names",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, mcpotel.TracedToolHandler(tracer, "list_labels", prom.handleListLabels))

	// label_values
	server.AddTool(mcp.Tool{
		Name:        "label_values",
		Description: "Get values for a specific label",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"label": map[string]any{
					"type":        "string",
					"description": "Label name",
				},
			},
			Required: []string{"label"},
		},
	}, mcpotel.TracedToolHandler(tracer, "label_values", prom.handleLabelValues))

	// list_targets
	server.AddTool(mcp.Tool{
		Name:        "list_targets",
		Description: "List scrape targets and their status",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"state": map[string]any{
					"type":        "string",
					"description": "Filter by state: active, dropped, any. Defaults to 'active'.",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "list_targets", prom.handleListTargets))

	// list_alerts
	server.AddTool(mcp.Tool{
		Name:        "list_alerts",
		Description: "List active alerts",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, mcpotel.TracedToolHandler(tracer, "list_alerts", prom.handleListAlerts))

	// list_rules
	server.AddTool(mcp.Tool{
		Name:        "list_rules",
		Description: "List alerting and recording rules",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"type": map[string]any{
					"type":        "string",
					"description": "Filter by type: alert, record. Defaults to all.",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "list_rules", prom.handleListRules))

	// runtime_info
	server.AddTool(mcp.Tool{
		Name:        "runtime_info",
		Description: "Get Prometheus runtime information",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, mcpotel.TracedToolHandler(tracer, "runtime_info", prom.handleRuntimeInfo))

	return server.Run(ctx)
}

func (p *promServer) request(ctx context.Context, path string, params url.Values) (map[string]any, error) {
	reqURL := p.url + path
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.httpClient.HTTP().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	maxBytes := env.Int("PROMETHEUS_MAX_RESPONSE_BYTES", 5*1024*1024)
	body, truncated, err := httpclient.ReadBodyWithLimit(resp.Body, maxBytes)
	if err != nil {
		return nil, err
	}
	if truncated && resp.StatusCode < 400 {
		return nil, fmt.Errorf("prometheus response exceeded %d bytes (set PROMETHEUS_MAX_RESPONSE_BYTES to increase; narrow query/range or increase step)", maxBytes)
	}

	if resp.StatusCode >= 400 {
		return nil, mcperror.APIError("Prometheus", resp.StatusCode, strutil.BodySnippet(body, 4096))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return result, nil
}

func (p *promServer) handleQuery(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	evalTime := v.String("time", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("query", query)
	if evalTime != "" {
		params.Set("time", evalTime)
	}

	result, err := p.request(ctx, "/api/v1/query", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

func (p *promServer) handleQueryRange(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	start := v.Required("start")
	end := v.String("end", "")
	step := v.String("step", "1m")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("start", start)
	if end != "" {
		params.Set("end", end)
	} else {
		params.Set("end", time.Now().Format(time.RFC3339))
	}
	params.Set("step", step)

	result, err := p.request(ctx, "/api/v1/query_range", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

func (p *promServer) handleListMetrics(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	matchPattern := v.String("match", "")

	// Fetch all metric names (no server-side filter - Prometheus match[] expects
	// series selectors like {job="prometheus"}, not regex patterns)
	result, err := p.request(ctx, "/api/v1/label/__name__/values", nil)
	if err != nil {
		return nil, err
	}

	// If match pattern provided, filter client-side with regex
	if matchPattern != "" {
		re, err := regexp.Compile(matchPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex pattern: %w", err)
		}
		if data, ok := result["data"].([]any); ok {
			filtered := make([]any, 0, len(data)/4) // preallocate conservatively
			for _, name := range data {
				if s, ok := name.(string); ok && re.MatchString(s) {
					filtered = append(filtered, name)
				}
			}
			result["data"] = filtered
		}
	}

	return mcp.JSONResult(result)
}

func (p *promServer) handleListLabels(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := p.request(ctx, "/api/v1/labels", nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (p *promServer) handleLabelValues(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	label := v.Required("label")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := p.request(ctx, fmt.Sprintf("/api/v1/label/%s/values", label), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

func (p *promServer) handleListTargets(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	state := v.Enum("state", "active", "active", "dropped", "any")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	if state != "any" {
		params.Set("state", state)
	}

	result, err := p.request(ctx, "/api/v1/targets", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

func (p *promServer) handleListAlerts(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := p.request(ctx, "/api/v1/alerts", nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (p *promServer) handleListRules(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	ruleType := v.String("type", "")

	params := url.Values{}
	if ruleType != "" {
		params.Set("type", ruleType)
	}

	result, err := p.request(ctx, "/api/v1/rules", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

func (p *promServer) handleRuntimeInfo(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := p.request(ctx, "/api/v1/status/runtimeinfo", nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}
