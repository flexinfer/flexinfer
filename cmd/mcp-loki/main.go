package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// getHTTPClient returns an HTTP client with optional TLS skip verify
func getHTTPClient() *http.Client {
	client := &http.Client{Timeout: 30 * time.Second}
	if skipVerify := os.Getenv("TLS_SKIP_VERIFY"); strings.ToLower(skipVerify) == "true" || skipVerify == "1" {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	return client
}

var (
	version        = "0.1.0"
	lokiURL        = getEnv("LOKI_URL", "http://loki.logging.svc.cluster.local:3100")
	portForward    = getEnvBool("LOKI_PORT_FORWARD", true)
	portForwardCmd *exec.Cmd
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		v = strings.ToLower(v)
		return v == "1" || v == "true" || v == "yes" || v == "on"
	}
	return fallback
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	server := mcp.NewServer("mcp-loki", version)
	server.SetInstructions("Loki log query tools")

	// Tools
	server.AddTool(mcp.Tool{
		Name:        "loki_query",
		Description: "Execute a LogQL instant query",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query":     map[string]any{"type": "string", "description": "LogQL query expression"},
				"time":      map[string]any{"type": "string", "description": "Evaluation timestamp"},
				"limit":     map[string]any{"type": "integer", "description": "Max entries"},
				"direction": map[string]any{"type": "string", "enum": []string{"forward", "backward"}},
			},
			Required: []string{"query"},
		},
	}, handleQuery)

	server.AddTool(mcp.Tool{
		Name:        "loki_query_range",
		Description: "Execute a LogQL range query over time",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query":     map[string]any{"type": "string", "description": "LogQL query expression"},
				"start":     map[string]any{"type": "string", "description": "Start timestamp"},
				"end":       map[string]any{"type": "string", "description": "End timestamp"},
				"step":      map[string]any{"type": "string", "description": "Step (e.g., 15s)"},
				"limit":     map[string]any{"type": "integer", "description": "Max entries"},
				"direction": map[string]any{"type": "string", "enum": []string{"forward", "backward"}},
			},
			Required: []string{"query", "start", "end"},
		},
	}, handleQueryRange)

	server.AddTool(mcp.Tool{
		Name:        "loki_labels",
		Description: "List log label names",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"start": map[string]any{"type": "string"},
				"end":   map[string]any{"type": "string"},
			},
		},
	}, handleLabels)

	server.AddTool(mcp.Tool{
		Name:        "loki_label_values",
		Description: "List values for a given label",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":  map[string]any{"type": "string"},
				"start": map[string]any{"type": "string"},
				"end":   map[string]any{"type": "string"},
			},
			Required: []string{"name"},
		},
	}, handleLabelValues)

	server.AddTool(mcp.Tool{
		Name:        "loki_series",
		Description: "List series (label sets) for selectors",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"match": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "List of selector matchers",
				},
				"start": map[string]any{"type": "string"},
				"end":   map[string]any{"type": "string"},
			},
			Required: []string{"match"},
		},
	}, handleSeries)

	server.AddTool(mcp.Tool{
		Name:        "loki_stats",
		Description: "Get query statistics for a LogQL query",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{"type": "string", "description": "LogQL query expression"},
				"start": map[string]any{"type": "string", "description": "Start timestamp"},
				"end":   map[string]any{"type": "string", "description": "End timestamp (defaults to now)"},
			},
			Required: []string{"query", "start"},
		},
	}, handleStats)

	server.AddTool(mcp.Tool{
		Name:        "loki_index_stats",
		Description: "Get index statistics (storage info) for a query",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{"type": "string", "description": "LogQL stream selector"},
				"start": map[string]any{"type": "string", "description": "Start timestamp"},
				"end":   map[string]any{"type": "string", "description": "End timestamp"},
			},
			Required: []string{"query"},
		},
	}, handleIndexStats)

	server.AddTool(mcp.Tool{
		Name:        "loki_detected_fields",
		Description: "Get auto-detected fields from log lines",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query":       map[string]any{"type": "string", "description": "LogQL query expression"},
				"start":       map[string]any{"type": "string", "description": "Start timestamp"},
				"end":         map[string]any{"type": "string", "description": "End timestamp"},
				"field_limit": map[string]any{"type": "integer", "description": "Max fields to return (default: 100)"},
				"line_limit":  map[string]any{"type": "integer", "description": "Max lines to analyze (default: 1000)"},
			},
			Required: []string{"query"},
		},
	}, handleDetectedFields)

	server.AddTool(mcp.Tool{
		Name:        "loki_ready",
		Description: "Check if Loki is ready and accepting queries",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleReady)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cleanup()
		os.Exit(1)
	}
	cleanup()
}

func cleanup() {
	if portForwardCmd != nil && portForwardCmd.Process != nil {
		portForwardCmd.Process.Kill()
	}
}

// Port Forwarding

func maybeStartPortForward() {
	if !portForward {
		return
	}

	u, err := url.Parse(lokiURL)
	if err != nil {
		return
	}

	host := u.Hostname()
	needsPF := strings.HasSuffix(host, ".svc.cluster.local") || strings.HasSuffix(host, ".svc") || strings.HasPrefix(host, "loki")

	if !needsPF {
		return
	}

	if portForwardCmd != nil {
		// Check if running
		if portForwardCmd.ProcessState == nil {
			return // Still running
		}
	}

	// Start port-forward
	// kubectl -n logging port-forward svc/loki 3100:3100
	cmd := exec.Command("kubectl", "-n", "logging", "port-forward", "svc/loki", "3100:3100")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err == nil {
		portForwardCmd = cmd
		lokiURL = "http://127.0.0.1:3100"
		// Give it a moment to start
		time.Sleep(500 * time.Millisecond)
	}
}

// Loki Client

func lokiRequest(endpoint string, params map[string]string) (map[string]any, error) {
	maybeStartPortForward()

	u, err := url.Parse(lokiURL + "/loki/api/v1/" + endpoint)
	if err != nil {
		return nil, err
	}

	if len(params) > 0 {
		q := u.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := getHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return result, nil
}

// Handlers

func handleQuery(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	params := make(map[string]string)
	if v, ok := args["query"].(string); ok {
		params["query"] = v
	}
	if v, ok := args["time"].(string); ok {
		params["time"] = v
	}
	if v, ok := args["limit"].(float64); ok {
		params["limit"] = fmt.Sprintf("%d", int(v))
	}
	if v, ok := args["direction"].(string); ok {
		params["direction"] = v
	}

	res, err := lokiRequest("query", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	if status, ok := res["status"].(string); ok && status == "success" {
		return mcp.JSONResult(map[string]any{
			"ok":     true,
			"result": res["data"],
		})
	}
	return mcp.JSONResult(res)
}

func handleQueryRange(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	params := make(map[string]string)
	params["step"] = "15s" // default

	if v, ok := args["query"].(string); ok {
		params["query"] = v
	}
	if v, ok := args["start"].(string); ok {
		params["start"] = v
	}
	if v, ok := args["end"].(string); ok {
		params["end"] = v
	}
	if v, ok := args["step"].(string); ok {
		params["step"] = v
	}
	if v, ok := args["limit"].(float64); ok {
		params["limit"] = fmt.Sprintf("%d", int(v))
	}
	if v, ok := args["direction"].(string); ok {
		params["direction"] = v
	}

	res, err := lokiRequest("query_range", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	if status, ok := res["status"].(string); ok && status == "success" {
		return mcp.JSONResult(map[string]any{
			"ok":     true,
			"result": res["data"],
		})
	}
	return mcp.JSONResult(res)
}

func handleLabels(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	params := make(map[string]string)
	if v, ok := args["start"].(string); ok {
		params["start"] = v
	}
	if v, ok := args["end"].(string); ok {
		params["end"] = v
	}

	res, err := lokiRequest("labels", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	if status, ok := res["status"].(string); ok && status == "success" {
		return mcp.JSONResult(map[string]any{
			"ok":     true,
			"labels": res["data"],
		})
	}
	return mcp.JSONResult(res)
}

func handleLabelValues(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	name, _ := args["name"].(string)
	params := make(map[string]string)
	if v, ok := args["start"].(string); ok {
		params["start"] = v
	}
	if v, ok := args["end"].(string); ok {
		params["end"] = v
	}

	// URL encode label name in path
	endpoint := fmt.Sprintf("label/%s/values", url.PathEscape(name))
	res, err := lokiRequest(endpoint, params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	if status, ok := res["status"].(string); ok && status == "success" {
		return mcp.JSONResult(map[string]any{
			"ok":     true,
			"values": res["data"],
		})
	}
	return mcp.JSONResult(res)
}

func handleSeries(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	// series endpoint uses match[]=... which is tricky with map[string]string
	// We need to construct query manually or handle it specially in lokiRequest
	// For now, let's just support single match or hack it into the URL in lokiRequest if we passed a url.Values

	// Actually, let's just modify lokiRequest to take url.Values or handle it here

	maybeStartPortForward()

	u, err := url.Parse(lokiURL + "/loki/api/v1/series")
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	q := u.Query()

	if match, ok := args["match"].([]any); ok {
		for _, m := range match {
			if s, ok := m.(string); ok {
				q.Add("match[]", s)
			}
		}
	} else if match, ok := args["match"].(string); ok {
		q.Add("match[]", match)
	}

	if v, ok := args["start"].(string); ok {
		q.Set("start", v)
	}
	if v, ok := args["end"].(string); ok {
		q.Set("end", v)
	}

	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := getHTTPClient().Do(req)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("unmarshal response: %w", err)), nil
	}

	if status, ok := result["status"].(string); ok && status == "success" {
		return mcp.JSONResult(map[string]any{
			"ok":     true,
			"series": result["data"],
		})
	}
	return mcp.JSONResult(result)
}

func handleStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	params := make(map[string]string)
	if v, ok := args["query"].(string); ok {
		params["query"] = v
	}
	if v, ok := args["start"].(string); ok {
		params["start"] = v
	}
	if v, ok := args["end"].(string); ok {
		params["end"] = v
	} else {
		// Default to now
		params["end"] = fmt.Sprintf("%d", time.Now().UnixNano())
	}

	res, err := lokiRequest("index/stats", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"stats": res,
	})
}

func handleIndexStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	params := make(map[string]string)
	if v, ok := args["query"].(string); ok {
		params["query"] = v
	}
	if v, ok := args["start"].(string); ok {
		params["start"] = v
	}
	if v, ok := args["end"].(string); ok {
		params["end"] = v
	}

	res, err := lokiRequest("index/stats", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"stats": res,
	})
}

func handleDetectedFields(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	params := make(map[string]string)
	if v, ok := args["query"].(string); ok {
		params["query"] = v
	}
	if v, ok := args["start"].(string); ok {
		params["start"] = v
	}
	if v, ok := args["end"].(string); ok {
		params["end"] = v
	}
	if v, ok := args["field_limit"].(float64); ok {
		params["field_limit"] = fmt.Sprintf("%d", int(v))
	}
	if v, ok := args["line_limit"].(float64); ok {
		params["line_limit"] = fmt.Sprintf("%d", int(v))
	}

	res, err := lokiRequest("detected_fields", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	if status, ok := res["status"].(string); ok && status == "success" {
		return mcp.JSONResult(map[string]any{
			"ok":     true,
			"fields": res["data"],
		})
	}
	return mcp.JSONResult(res)
}

func handleReady(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	maybeStartPortForward()

	u, err := url.Parse(lokiURL + "/ready")
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := getHTTPClient().Do(req)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	ready := resp.StatusCode == 200

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"ready":    ready,
		"status":   resp.StatusCode,
		"response": string(body),
	})
}
