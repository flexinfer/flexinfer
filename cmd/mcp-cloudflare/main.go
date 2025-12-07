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
	"strconv"
	"syscall"
	"time"

	"github.com/crb2nu/loom/pkg/mcp"
)

var (
	version       = "0.1.0"
	cfAPIToken    = os.Getenv("CF_API_TOKEN")
	cfAccountID   = os.Getenv("CF_ACCOUNT_ID")
	cfAPIBase     = getEnv("CF_API_BASE", "https://api.cloudflare.com")
	cfHTTPTimeout = getEnvDuration("CF_HTTP_TIMEOUT", 30*time.Second)
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return time.Duration(f * float64(time.Second))
		}
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

	server := mcp.NewServer("mcp-cloudflare", version)
	server.SetInstructions("Cloudflare API tools")

	// Tools
	server.AddTool(mcp.Tool{
		Name:        "cf_verify_token",
		Description: "Verify Cloudflare API token status and scopes",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, handleVerifyToken)

	server.AddTool(mcp.Tool{
		Name:        "cf_list_zones",
		Description: "List Cloudflare DNS zones",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"per_page": map[string]any{"type": "integer", "description": "Items per page (default 50)"},
				"page":     map[string]any{"type": "integer", "description": "Page number (default 1)"},
			},
		},
	}, handleListZones)

	server.AddTool(mcp.Tool{
		Name:        "cf_list_tunnels",
		Description: "List Cloudflare tunnels for the configured account",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"per_page": map[string]any{"type": "integer", "description": "Items per page (default 50)"},
				"page":     map[string]any{"type": "integer", "description": "Page number (default 1)"},
			},
		},
	}, handleListTunnels)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// Cloudflare Client

func cfRequest(method, path string, params map[string]string) (map[string]any, error) {
	if cfAPIToken == "" {
		return nil, fmt.Errorf("CF_API_TOKEN is required")
	}

	u, err := url.Parse(cfAPIBase + "/client/v4" + path)
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

	req, err := http.NewRequest(method, u.String(), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfAPIToken)
	req.Header.Set("User-Agent", "mcp-cloudflare/0.1")

	client := &http.Client{Timeout: cfHTTPTimeout}
	resp, err := client.Do(req)
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

	if success, ok := result["success"].(bool); !ok || !success {
		// Try to extract errors
		if errs, ok := result["errors"].([]any); ok && len(errs) > 0 {
			return nil, fmt.Errorf("api error: %v", errs)
		}
		return nil, fmt.Errorf("api request failed: %s", string(body))
	}

	return result, nil
}

// Handlers

func handleVerifyToken(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	res, err := cfRequest("GET", "/user/tokens/verify", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	data := res["result"].(map[string]any)
	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"token_id": data["id"],
		"status":   data["status"],
		"scopes":   data["policies"],
	})
}

func handleListZones(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	params := make(map[string]string)
	if v, ok := args["per_page"].(float64); ok {
		params["per_page"] = fmt.Sprintf("%d", int(v))
	}
	if v, ok := args["page"].(float64); ok {
		params["page"] = fmt.Sprintf("%d", int(v))
	}

	res, err := cfRequest("GET", "/zones", params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	zones := res["result"].([]any)
	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"count": len(zones),
		"zones": zones,
	})
}

func handleListTunnels(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if cfAccountID == "" {
		return mcp.ErrorResult(fmt.Errorf("CF_ACCOUNT_ID is required for tunnels")), nil
	}

	params := make(map[string]string)
	if v, ok := args["per_page"].(float64); ok {
		params["per_page"] = fmt.Sprintf("%d", int(v))
	}
	if v, ok := args["page"].(float64); ok {
		params["page"] = fmt.Sprintf("%d", int(v))
	}

	path := fmt.Sprintf("/accounts/%s/tunnels", cfAccountID)
	res, err := cfRequest("GET", path, params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	tunnels := res["result"].([]any)
	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"count":   len(tunnels),
		"tunnels": tunnels,
	})
}
