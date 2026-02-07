package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/portforward"
	"github.com/crb2nu/loom/pkg/strutil"
	"github.com/crb2nu/loom/pkg/validate"
)

var httpClient = httpclient.NewDefault()

var (
	version        = "0.1.0"
	lokiURL        = env.String("LOKI_URL", "http://loki.logging.svc.cluster.local:3100")
	portForward    = env.Bool("LOKI_PORT_FORWARD", true)
	portForwardCmd *exec.Cmd
	pfMu           sync.Mutex
	pfStderr       *limitedBuffer
)

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	defer cleanup()

	logger := mcplog.NewDefault()
	logger.Info("starting server", "name", "mcp-loki", "version", version, "url", lokiURL)

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

	return server.Run(ctx)
}

func cleanup() {
	pfMu.Lock()
	cmd := portForwardCmd
	portForwardCmd = nil
	pfMu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
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
	needsPF := portforward.NeedsPortForward(host, nil)

	if !needsPF {
		return
	}

	pfMu.Lock()
	if isPortForwardRunningLocked() {
		pfMu.Unlock()
		return
	}

	// Start port-forward: kubectl -n logging port-forward svc/loki 3100:3100
	cmd := exec.Command("kubectl", "-n", "logging", "port-forward", "svc/loki", "3100:3100") //nolint:noctx // background port-forward managed separately
	cmd.Stdout = io.Discard
	pfStderr = &limitedBuffer{MaxBytes: 8 * 1024}
	cmd.Stderr = pfStderr
	if err := cmd.Start(); err != nil {
		pfMu.Unlock()
		return
	}

	portForwardCmd = cmd
	pfMu.Unlock()

	go func(cmd *exec.Cmd) {
		_ = cmd.Wait()
		pfMu.Lock()
		if portForwardCmd == cmd {
			portForwardCmd = nil
		}
		pfMu.Unlock()
	}(cmd)

	if err := waitForLocalLokiReady(context.Background(), 3*time.Second); err != nil {
		pfMu.Lock()
		if portForwardCmd == cmd && cmd.Process != nil {
			_ = cmd.Process.Kill()
			portForwardCmd = nil
		}
		pfMu.Unlock()
	}
}

// Loki Client

func lokiRequest(ctx context.Context, endpoint string, params url.Values) (map[string]any, error) {
	maybeStartPortForward()

	baseURL := effectiveLokiBaseURL()
	reqURL, err := lokiAPIURL(baseURL, endpoint)
	if err != nil {
		return nil, err
	}

	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.HTTP().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	maxBytes := env.Int("LOKI_MAX_RESPONSE_BYTES", 5*1024*1024)
	body, truncated, err := httpclient.ReadBodyWithLimit(resp.Body, maxBytes)
	if err != nil {
		return nil, err
	}
	if truncated && resp.StatusCode < 400 {
		return nil, fmt.Errorf("loki response exceeded %d bytes (set LOKI_MAX_RESPONSE_BYTES to increase; try lowering limit/narrowing time range)", maxBytes)
	}

	if resp.StatusCode >= 400 {
		return nil, mcperror.APIError("Loki", resp.StatusCode, strutil.BodySnippet(body, 4096))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf(
			"loki response was not JSON (status %d, content-type %q, url %s): %s",
			resp.StatusCode,
			resp.Header.Get("Content-Type"),
			reqURL,
			strutil.BodySnippet(body, 4096),
		)
	}

	return result, nil
}

// Handlers

func handleQuery(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	timeVal := v.String("time", "")
	limit := v.IntRange("limit", 100, 1, 5000)
	direction := v.Enum("direction", "", "forward", "backward")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("query", query)
	if timeVal != "" {
		params.Set("time", timeVal)
	}
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	if direction != "" {
		params.Set("direction", direction)
	}

	res, err := lokiRequest(ctx, "query", params)
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
	v := validate.NewArgs(args)
	query := v.Required("query")
	start := v.Required("start")
	end := v.Required("end")
	step := v.String("step", "15s")
	limit := v.IntRange("limit", 100, 1, 5000)
	direction := v.Enum("direction", "", "forward", "backward")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("start", start)
	params.Set("end", end)
	params.Set("step", step)
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	if direction != "" {
		params.Set("direction", direction)
	}

	res, err := lokiRequest(ctx, "query_range", params)
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
	v := validate.NewArgs(args)
	start := v.String("start", "")
	end := v.String("end", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	if start != "" {
		params.Set("start", start)
	}
	if end != "" {
		params.Set("end", end)
	}

	res, err := lokiRequest(ctx, "labels", params)
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
	v := validate.NewArgs(args)
	name := v.Required("name")
	start := v.String("start", "")
	end := v.String("end", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	if start != "" {
		params.Set("start", start)
	}
	if end != "" {
		params.Set("end", end)
	}

	// URL encode label name in path
	endpoint := fmt.Sprintf("label/%s/values", url.PathEscape(name))
	res, err := lokiRequest(ctx, endpoint, params)
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
	v := validate.NewArgs(args)
	match := v.RequiredStringSlice("match")
	start := v.String("start", "")
	end := v.String("end", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	for _, m := range match {
		params.Add("match[]", m)
	}
	if start != "" {
		params.Set("start", start)
	}
	if end != "" {
		params.Set("end", end)
	}

	result, err := lokiRequest(ctx, "series", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
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
	v := validate.NewArgs(args)
	query := v.Required("query")
	start := v.Required("start")
	end := v.String("end", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("start", start)
	if end != "" {
		params.Set("end", end)
	} else {
		// Default to now
		params.Set("end", fmt.Sprintf("%d", time.Now().UnixNano()))
	}

	res, err := lokiRequest(ctx, "index/stats", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"stats": res,
	})
}

func handleIndexStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	start := v.String("start", "")
	end := v.String("end", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("query", query)
	if start != "" {
		params.Set("start", start)
	}
	if end != "" {
		params.Set("end", end)
	}

	res, err := lokiRequest(ctx, "index/stats", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"stats": res,
	})
}

func handleDetectedFields(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	start := v.String("start", "")
	end := v.String("end", "")
	fieldLimit := v.IntRange("field_limit", 100, 1, 1000)
	lineLimit := v.IntRange("line_limit", 1000, 1, 5000)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("query", query)
	if start != "" {
		params.Set("start", start)
	}
	if end != "" {
		params.Set("end", end)
	}
	if fieldLimit > 0 {
		params.Set("field_limit", fmt.Sprintf("%d", fieldLimit))
	}
	if lineLimit > 0 {
		params.Set("line_limit", fmt.Sprintf("%d", lineLimit))
	}

	res, err := lokiRequest(ctx, "detected_fields", params)
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

	u, err := url.Parse(effectiveLokiBaseURL())
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ready"

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := httpClient.HTTP().Do(req)
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

func effectiveLokiBaseURL() string {
	pfMu.Lock()
	defer pfMu.Unlock()
	if portForwardCmd == nil || portForwardCmd.Process == nil {
		return lokiURL
	}
	if err := portForwardCmd.Process.Signal(syscall.Signal(0)); err != nil {
		return lokiURL
	}
	return "http://127.0.0.1:3100"
}

func isPortForwardRunningLocked() bool {
	if portForwardCmd == nil || portForwardCmd.Process == nil {
		return false
	}
	return portForwardCmd.Process.Signal(syscall.Signal(0)) == nil
}

func waitForLocalLokiReady(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := httpClient.HTTP()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		// Check immediately on first iteration
		reqCtx, reqCancel := context.WithTimeout(ctx, 500*time.Millisecond)
		req, err := http.NewRequestWithContext(reqCtx, "GET", "http://127.0.0.1:3100/ready", nil)
		if err != nil {
			reqCancel()
			return err
		}
		resp, err := client.Do(req)
		reqCancel()
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}

		// Wait for next tick or context cancellation
		select {
		case <-ctx.Done():
			pfMu.Lock()
			defer pfMu.Unlock()
			if pfStderr != nil && strings.TrimSpace(pfStderr.String()) != "" {
				return fmt.Errorf("port-forward did not become ready: %s", strings.TrimSpace(pfStderr.String()))
			}
			return fmt.Errorf("port-forward did not become ready")
		case <-ticker.C:
			// Continue to next iteration
		}
	}
}

func lokiAPIURL(baseURL, endpoint string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}

	p := strings.TrimRight(base.Path, "/")
	switch {
	case strings.HasSuffix(p, "/loki/api/v1"):
		// already includes the API prefix
	case strings.HasSuffix(p, "/loki"):
		p = p + "/api/v1"
	case p == "":
		p = "/loki/api/v1"
	default:
		p = p + "/loki/api/v1"
	}

	base.Path = p + "/" + strings.TrimLeft(endpoint, "/")
	return base.String(), nil
}

type limitedBuffer struct {
	MaxBytes int
	mu       sync.Mutex
	buf      []byte
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.MaxBytes <= 0 {
		l.MaxBytes = 1024
	}

	l.buf = append(l.buf, p...)
	if len(l.buf) > l.MaxBytes {
		l.buf = l.buf[len(l.buf)-l.MaxBytes:]
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.buf)
}
