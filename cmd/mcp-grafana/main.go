package main

import (
	"bytes"
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

	"github.com/crb2nu/loom/pkg/validate"
)

var (
	version        = "0.1.0"
	grafanaURL     = getEnv("GRAFANA_URL", "http://kube-prometheus-stack-grafana.monitoring.svc.cluster.local")
	grafanaToken   = os.Getenv("GRAFANA_API_TOKEN")
	portForward    = getEnvBool("GRAFANA_PORT_FORWARD", true)
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

	server := mcp.NewServer("mcp-grafana", version)
	server.SetInstructions("Grafana dashboard search and retrieval")

	// Tools
	server.AddTool(mcp.Tool{
		Name:        "grafana_search",
		Description: "Search dashboards/folders",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{"type": "string", "description": "search text"},
				"limit": map[string]any{"type": "integer", "description": "max results"},
			},
		},
	}, handleSearch)

	server.AddTool(mcp.Tool{
		Name:        "grafana_get_dashboard",
		Description: "Fetch dashboard JSON by UID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"uid": map[string]any{"type": "string", "description": "Dashboard UID"},
			},
			Required: []string{"uid"},
		},
	}, handleGetDashboard)

	// Datasource tools
	server.AddTool(mcp.Tool{
		Name:        "grafana_list_datasources",
		Description: "List configured datasources",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleListDatasources)

	server.AddTool(mcp.Tool{
		Name:        "grafana_get_datasource",
		Description: "Get details of a specific datasource",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"uid": map[string]any{"type": "string", "description": "Datasource UID"},
			},
			Required: []string{"uid"},
		},
	}, handleGetDatasource)

	// Alert tools
	server.AddTool(mcp.Tool{
		Name:        "grafana_list_alerts",
		Description: "List alerting rules",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"state": map[string]any{"type": "string", "description": "Filter by state (alerting, pending, ok, nodata, error)"},
				"limit": map[string]any{"type": "integer", "description": "Maximum number of alerts"},
			},
		},
	}, handleListAlerts)

	server.AddTool(mcp.Tool{
		Name:        "grafana_list_alert_instances",
		Description: "List current alert instances (firing alerts)",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleListAlertInstances)

	// Annotation tools
	server.AddTool(mcp.Tool{
		Name:        "grafana_list_annotations",
		Description: "List dashboard annotations",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"dashboard_uid": map[string]any{"type": "string", "description": "Filter by dashboard UID"},
				"from":          map[string]any{"type": "string", "description": "Start time (epoch ms or RFC3339)"},
				"to":            map[string]any{"type": "string", "description": "End time (epoch ms or RFC3339)"},
				"limit":         map[string]any{"type": "integer", "description": "Maximum annotations"},
			},
		},
	}, handleListAnnotations)

	server.AddTool(mcp.Tool{
		Name:        "grafana_create_annotation",
		Description: "Create an annotation",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"dashboard_uid": map[string]any{"type": "string", "description": "Dashboard UID (optional for global)"},
				"panel_id":      map[string]any{"type": "integer", "description": "Panel ID (optional)"},
				"text":          map[string]any{"type": "string", "description": "Annotation text"},
				"tags":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Tags"},
				"time":          map[string]any{"type": "integer", "description": "Start time (epoch ms, defaults to now)"},
				"time_end":      map[string]any{"type": "integer", "description": "End time for region annotation"},
			},
			Required: []string{"text"},
		},
	}, handleCreateAnnotation)

	// Folder tools
	server.AddTool(mcp.Tool{
		Name:        "grafana_list_folders",
		Description: "List dashboard folders",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleListFolders)

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

	u, err := url.Parse(grafanaURL)
	if err != nil {
		return
	}

	host := u.Hostname()
	needsPF := strings.HasSuffix(host, ".svc.cluster.local") || strings.HasSuffix(host, ".svc") || strings.HasPrefix(host, "kube-prometheus-stack-grafana")

	if !needsPF {
		return
	}

	if portForwardCmd != nil {
		if portForwardCmd.ProcessState == nil {
			return // Still running
		}
	}

	// Start port-forward
	// kubectl -n monitoring port-forward svc/kube-prometheus-stack-grafana 3000:80
	cmd := exec.Command("kubectl", "-n", "monitoring", "port-forward", "svc/kube-prometheus-stack-grafana", "3000:80")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err == nil {
		portForwardCmd = cmd
		grafanaURL = "http://127.0.0.1:3000"
		time.Sleep(500 * time.Millisecond)
	}
}

// Grafana Client

func grafanaRequest(path string, params map[string]string) (map[string]any, error) {
	return grafanaRequestWithBody("GET", path, params, nil)
}

func grafanaRequestWithBody(method, path string, params map[string]string, body any) (map[string]any, error) {
	maybeStartPortForward()

	u, err := url.Parse(grafanaURL + path)
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

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest(method, u.String(), bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if grafanaToken != "" {
		req.Header.Set("Authorization", "Bearer "+grafanaToken)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	if skipVerify := os.Getenv("TLS_SKIP_VERIFY"); strings.ToLower(skipVerify) == "true" || skipVerify == "1" {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Wrap in map for consistency if it's a list
	if asList, ok := result.([]any); ok {
		return map[string]any{"data": asList}, nil
	}
	if asMap, ok := result.(map[string]any); ok {
		return asMap, nil
	}
	return nil, fmt.Errorf("unexpected response format")
}

// Handlers

func handleSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	params := make(map[string]string)
	if v, ok := args["query"].(string); ok {
		params["query"] = v
	}
	if v, ok := args["limit"].(float64); ok {
		params["limit"] = fmt.Sprintf("%d", int(v))
	} else {
		params["limit"] = "20"
	}

	res, err := grafanaRequest("/api/search", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	data, _ := res["data"].([]any)
	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"results": data,
		"count":   len(data),
	})
}

func handleGetDashboard(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	uid, _ := args["uid"].(string)
	if uid == "" {
		return mcp.ErrorResult(fmt.Errorf("missing 'uid'")), nil
	}

	path := fmt.Sprintf("/api/dashboards/uid/%s", url.PathEscape(uid))
	res, err := grafanaRequest(path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"dashboard": res,
	})
}

// Datasource Handlers

func handleListDatasources(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	res, err := grafanaRequest("/api/datasources", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	datasources, _ := res["data"].([]any)
	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"count":       len(datasources),
		"datasources": datasources,
	})
}

func handleGetDatasource(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	uid := v.Required("uid")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/api/datasources/uid/%s", url.PathEscape(uid))
	res, err := grafanaRequest(path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"datasource": res,
	})
}

// Alert Handlers

func handleListAlerts(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	state := v.String("state", "")
	limit := v.Int("limit", 100)

	params := make(map[string]string)
	if state != "" {
		params["state"] = state
	}
	params["limit"] = fmt.Sprintf("%d", limit)

	// Grafana Alerting API (v9+)
	res, err := grafanaRequest("/api/v1/provisioning/alert-rules", params)
	if err != nil {
		// Fallback to legacy alerting if unified alerting not enabled
		res, err = grafanaRequest("/api/alerts", params)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
	}

	alerts, _ := res["data"].([]any)
	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"count":  len(alerts),
		"alerts": alerts,
	})
}

func handleListAlertInstances(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	// Get current firing alerts from Alertmanager endpoint
	res, err := grafanaRequest("/api/alertmanager/grafana/api/v2/alerts", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	alerts, _ := res["data"].([]any)
	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"count":  len(alerts),
		"alerts": alerts,
	})
}

// Annotation Handlers

func handleListAnnotations(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	dashboardUID := v.String("dashboard_uid", "")
	from := v.String("from", "")
	to := v.String("to", "")
	limit := v.Int("limit", 100)

	params := make(map[string]string)
	if dashboardUID != "" {
		params["dashboardUID"] = dashboardUID
	}
	if from != "" {
		params["from"] = from
	}
	if to != "" {
		params["to"] = to
	}
	params["limit"] = fmt.Sprintf("%d", limit)

	res, err := grafanaRequest("/api/annotations", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	annotations, _ := res["data"].([]any)
	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"count":       len(annotations),
		"annotations": annotations,
	})
}

func handleCreateAnnotation(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	text := v.Required("text")
	dashboardUID := v.String("dashboard_uid", "")
	panelID := v.Int("panel_id", 0)
	tags := v.StringSlice("tags")
	timeMs := v.Int("time", 0)
	timeEndMs := v.Int("time_end", 0)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	body := map[string]any{
		"text": text,
	}
	if dashboardUID != "" {
		body["dashboardUID"] = dashboardUID
	}
	if panelID > 0 {
		body["panelId"] = panelID
	}
	if len(tags) > 0 {
		body["tags"] = tags
	}
	if timeMs > 0 {
		body["time"] = timeMs
	}
	if timeEndMs > 0 {
		body["timeEnd"] = timeEndMs
	}

	res, err := grafanaRequestWithBody("POST", "/api/annotations", nil, body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"message":    "Annotation created",
		"annotation": res,
	})
}

// Folder Handlers

func handleListFolders(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	res, err := grafanaRequest("/api/folders", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	folders, _ := res["data"].([]any)
	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"count":   len(folders),
		"folders": folders,
	})
}
