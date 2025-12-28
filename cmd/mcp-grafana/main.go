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
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
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

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if grafanaToken != "" {
		req.Header.Set("Authorization", "Bearer "+grafanaToken)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result interface{}
	if err := json.Unmarshal(body, &result); err != nil {
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
