// mcp-alertmanager is an MCP server for Alertmanager operations.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "1.0.0"

type alertmanagerServer struct {
	url    string
	client *httpclient.Client
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	amURL := os.Getenv("ALERTMANAGER_URL")
	if amURL == "" {
		amURL = "http://alertmanager.monitoring.svc.cluster.local:9093"
	}
	amURL = strings.TrimSuffix(amURL, "/")

	am := &alertmanagerServer{
		url:    amURL,
		client: httpclient.NewDefault(),
	}

	logger.Info("starting server", "name", "mcp-alertmanager", "version", version, "url", amURL)

	server := mcp.NewServer("mcp-alertmanager", version)
	server.SetInstructions("Alertmanager MCP server. Manage alerts and silences.")

	// list_alerts
	server.AddTool(mcp.Tool{
		Name:        "am_list_alerts",
		Description: "List active alerts with labels and annotations",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"filter": map[string]any{
					"type":        "string",
					"description": "Filter alerts by label matcher (e.g., 'severity=critical')",
				},
				"silenced": map[string]any{
					"type":        "boolean",
					"description": "Include silenced alerts (default: false)",
				},
				"inhibited": map[string]any{
					"type":        "boolean",
					"description": "Include inhibited alerts (default: false)",
				},
			},
		},
	}, am.handleListAlerts)

	// list_silences
	server.AddTool(mcp.Tool{
		Name:        "am_list_silences",
		Description: "List current silences",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"filter": map[string]any{
					"type":        "string",
					"description": "Filter silences by label matcher",
				},
			},
		},
	}, am.handleListSilences)

	// create_silence
	server.AddTool(mcp.Tool{
		Name:        "am_create_silence",
		Description: "Create a new silence for alerts matching the given matchers",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"matchers": map[string]any{
					"type":        "array",
					"description": "Label matchers (e.g., [{\"name\": \"alertname\", \"value\": \"HighCPU\", \"isRegex\": false}])",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":    map[string]any{"type": "string"},
							"value":   map[string]any{"type": "string"},
							"isRegex": map[string]any{"type": "boolean"},
							"isEqual": map[string]any{"type": "boolean"},
						},
					},
				},
				"duration": map[string]any{
					"type":        "string",
					"description": "Silence duration (e.g., '2h', '30m', '1d'). Default: 1h",
				},
				"comment": map[string]any{
					"type":        "string",
					"description": "Reason for the silence",
				},
				"createdBy": map[string]any{
					"type":        "string",
					"description": "Who created the silence",
				},
			},
			Required: []string{"matchers", "comment"},
		},
	}, am.handleCreateSilence)

	// delete_silence
	server.AddTool(mcp.Tool{
		Name:        "am_delete_silence",
		Description: "Delete (expire) a silence by ID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Silence ID to delete",
				},
			},
			Required: []string{"id"},
		},
	}, am.handleDeleteSilence)

	// status
	server.AddTool(mcp.Tool{
		Name:        "am_status",
		Description: "Get Alertmanager cluster status and configuration",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, am.handleStatus)

	return server.Run(ctx)
}

// API request helper
func (s *alertmanagerServer) request(ctx context.Context, method, path string, body io.Reader) (map[string]any, error) {
	url := s.url + "/api/v2" + path

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	maxBytes := getEnvInt("ALERTMANAGER_MAX_RESPONSE_BYTES", 5*1024*1024)
	respBody, truncated, err := readBodyWithLimit(resp.Body, maxBytes)
	if err != nil {
		return nil, err
	}
	if truncated && resp.StatusCode < 400 {
		return nil, fmt.Errorf("alertmanager response exceeded %d bytes (set ALERTMANAGER_MAX_RESPONSE_BYTES to increase)", maxBytes)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bodySnippet(respBody))
	}

	// Handle empty responses
	if len(respBody) == 0 {
		return map[string]any{"ok": true}, nil
	}

	// Try to parse as JSON
	var result any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Wrap arrays in a map
	if arr, ok := result.([]any); ok {
		return map[string]any{"data": arr}, nil
	}
	if m, ok := result.(map[string]any); ok {
		return m, nil
	}
	return map[string]any{"data": result}, nil
}

func getEnvInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func bodySnippet(body []byte) string {
	const max = 4 * 1024
	truncated := false
	if len(body) > max {
		body = body[:max]
		truncated = true
	}
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "<empty response body>"
	}
	if truncated {
		return s + "…"
	}
	return s
}

func readBodyWithLimit(r io.Reader, maxBytes int) ([]byte, bool, error) {
	if maxBytes <= 0 {
		b, err := io.ReadAll(r)
		return b, false, err
	}

	b, err := io.ReadAll(io.LimitReader(r, int64(maxBytes+1)))
	if err != nil {
		return nil, false, err
	}
	if len(b) > maxBytes {
		return b[:maxBytes], true, nil
	}
	return b, false, nil
}

func (s *alertmanagerServer) handleListAlerts(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	filter := v.String("filter", "")
	silenced := v.Bool("silenced", false)
	inhibited := v.Bool("inhibited", false)

	path := "/alerts"
	q := url.Values{}
	if filter != "" {
		q.Set("filter", filter)
	}
	if silenced {
		q.Set("silenced", "true")
	}
	if inhibited {
		q.Set("inhibited", "true")
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	result, err := s.request(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	alerts, _ := result["data"].([]any)
	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"count":  len(alerts),
		"alerts": alerts,
	})
}

func (s *alertmanagerServer) handleListSilences(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	filter := v.String("filter", "")

	path := "/silences"
	if filter != "" {
		path += "?filter=" + url.QueryEscape(filter)
	}

	result, err := s.request(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	silences, _ := result["data"].([]any)

	// Filter to only active silences
	active := make([]any, 0)
	for _, s := range silences {
		if m, ok := s.(map[string]any); ok {
			if status, ok := m["status"].(map[string]any); ok {
				if state, _ := status["state"].(string); state == "active" {
					active = append(active, s)
				}
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(active),
		"silences": active,
	})
}

func (s *alertmanagerServer) handleCreateSilence(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	comment := v.Required("comment")
	createdBy := v.String("createdBy", "mcp-alertmanager")
	durationStr := v.String("duration", "1h")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Parse duration
	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("invalid duration: %w", err)), nil
	}

	// Get matchers
	matchers, ok := args["matchers"].([]any)
	if !ok || len(matchers) == 0 {
		return mcp.ErrorResult(fmt.Errorf("matchers is required")), nil
	}

	// Build silence payload
	now := time.Now()
	silence := map[string]any{
		"matchers":  matchers,
		"startsAt":  now.Format(time.RFC3339),
		"endsAt":    now.Add(duration).Format(time.RFC3339),
		"createdBy": createdBy,
		"comment":   comment,
	}

	payload, _ := json.Marshal(silence)
	result, err := s.request(ctx, "POST", "/silences", strings.NewReader(string(payload)))
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"message":   "silence created",
		"silenceId": result["silenceID"],
		"endsAt":    now.Add(duration).Format(time.RFC3339),
	})
}

func (s *alertmanagerServer) handleDeleteSilence(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	id := v.Required("id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	_, err := s.request(ctx, "DELETE", "/silence/"+id, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"message": "silence deleted",
		"id":      id,
	})
}

func (s *alertmanagerServer) handleStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := s.request(ctx, "GET", "/status", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"status": result,
	})
}
