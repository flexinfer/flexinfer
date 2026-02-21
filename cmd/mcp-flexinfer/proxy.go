package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

func (f *flexinferServer) handleProxyModels(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	proxyURL := v.String("proxy_url", f.proxyURL)

	if proxyURL == "" {
		return mcp.JSONResult(map[string]any{
			"ok":      false,
			"message": "proxy not configured; set FLEXINFER_PROXY_URL or pass proxy_url parameter",
		})
	}

	url := strings.TrimRight(proxyURL, "/") + "/v1/models"
	body, err := f.httpClient.GetJSON(ctx, url)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("proxy GET %s: %w", url, err)), nil
	}

	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return mcp.JSONResult(map[string]any{
			"ok":     true,
			"raw":    string(body),
			"format": "text",
		})
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"models": parsed,
	})
}

func (f *flexinferServer) handleProxyHealth(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	proxyURL := v.String("proxy_url", f.proxyURL)

	if proxyURL == "" {
		return mcp.JSONResult(map[string]any{
			"ok":      false,
			"message": "proxy not configured; set FLEXINFER_PROXY_URL or pass proxy_url parameter",
		})
	}

	url := strings.TrimRight(proxyURL, "/") + "/healthz"
	body, err := f.httpClient.GetJSON(ctx, url)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("proxy GET %s: %w", url, err)), nil
	}

	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return mcp.JSONResult(map[string]any{
			"ok":     true,
			"health": string(body),
			"format": "text",
		})
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"health": parsed,
	})
}
