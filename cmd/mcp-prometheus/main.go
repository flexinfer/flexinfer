// mcp-prometheus is a fast Prometheus MCP server written in Go.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var version = "1.0.0"

type promServer struct {
	url        string
	httpClient *http.Client
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	promURL := os.Getenv("PROMETHEUS_URL")
	if promURL == "" {
		promURL = "http://prometheus.monitoring.svc.cluster.local:9090"
	}

	prom := &promServer{
		url: promURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

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
	}, prom.handleQuery)

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
	}, prom.handleQueryRange)

	// list_metrics
	server.AddTool(mcp.Tool{
		Name:        "list_metrics",
		Description: "List all available metric names",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"match": map[string]any{
					"type":        "string",
					"description": "Filter pattern (regex)",
				},
			},
		},
	}, prom.handleListMetrics)

	// list_labels
	server.AddTool(mcp.Tool{
		Name:        "list_labels",
		Description: "List all label names",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, prom.handleListLabels)

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
	}, prom.handleLabelValues)

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
	}, prom.handleListTargets)

	// list_alerts
	server.AddTool(mcp.Tool{
		Name:        "list_alerts",
		Description: "List active alerts",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, prom.handleListAlerts)

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
	}, prom.handleListRules)

	// runtime_info
	server.AddTool(mcp.Tool{
		Name:        "runtime_info",
		Description: "Get Prometheus runtime information",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, prom.handleRuntimeInfo)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
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

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("prometheus API error %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func getStringArg(args map[string]any, key, defaultVal string) string {
	if v, ok := args[key].(string); ok && v != "" {
		return v
	}
	return defaultVal
}

func (p *promServer) handleQuery(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := getStringArg(args, "query", "")
	evalTime := getStringArg(args, "time", "")

	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	params := url.Values{}
	params.Set("query", query)
	if evalTime != "" {
		params.Set("time", evalTime)
	}

	result, err := p.request(ctx, "/api/v1/query", params)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (p *promServer) handleQueryRange(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := getStringArg(args, "query", "")
	start := getStringArg(args, "start", "")
	end := getStringArg(args, "end", "")
	step := getStringArg(args, "step", "1m")

	if query == "" || start == "" {
		return nil, fmt.Errorf("query and start are required")
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
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (p *promServer) handleListMetrics(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	match := getStringArg(args, "match", "")

	params := url.Values{}
	if match != "" {
		params.Set("match[]", match)
	}

	result, err := p.request(ctx, "/api/v1/label/__name__/values", params)
	if err != nil {
		return nil, err
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
	label := getStringArg(args, "label", "")

	if label == "" {
		return nil, fmt.Errorf("label is required")
	}

	result, err := p.request(ctx, fmt.Sprintf("/api/v1/label/%s/values", label), nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (p *promServer) handleListTargets(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	state := getStringArg(args, "state", "active")

	params := url.Values{}
	if state != "any" {
		params.Set("state", state)
	}

	result, err := p.request(ctx, "/api/v1/targets", params)
	if err != nil {
		return nil, err
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
	ruleType := getStringArg(args, "type", "")

	params := url.Values{}
	if ruleType != "" {
		params.Set("type", ruleType)
	}

	result, err := p.request(ctx, "/api/v1/rules", params)
	if err != nil {
		return nil, err
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
