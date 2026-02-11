package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// eventEmitter sends sandbox lifecycle events to the HUD agent-context API.
type eventEmitter struct {
	hudAddr string // e.g., "localhost:3333"
	enabled bool
	client  *http.Client
	logger  *slog.Logger
}

func newEventEmitter(hudAddr string, logger *slog.Logger) *eventEmitter {
	return &eventEmitter{
		hudAddr: hudAddr,
		enabled: hudAddr != "",
		client: &http.Client{
			Timeout: 2 * time.Second,
		},
		logger: logger,
	}
}

// Emit sends a sandbox event to the HUD API. Errors are logged but not returned.
func (e *eventEmitter) Emit(ctx context.Context, eventType, project, detail string) {
	if !e.enabled {
		return
	}

	payload := map[string]any{
		"entries": []map[string]any{
			{
				"entry_type": "finding",
				"title":      fmt.Sprintf("devbox.%s: %s", eventType, project),
				"content":    detail,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		e.logger.Debug("failed to marshal event", "error", err)
		return
	}

	url := fmt.Sprintf("http://%s/api/agent/context/add", e.hudAddr)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		e.logger.Debug("failed to create request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		e.logger.Debug("failed to emit event", "event", eventType, "error", err)
		return
	}
	resp.Body.Close()
}

// handleSummary returns an aggregated status summary for HUD display.
func (m *manager) handleSummary(_ context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
	entries := m.store.List()

	var running, paused, stopped int
	projects := make([]string, 0, len(entries))
	for name, entry := range entries {
		projects = append(projects, name)
		switch entry.Status {
		case "running":
			running++
		case "paused":
			paused++
		case "stopped":
			stopped++
		}
	}

	summary := map[string]any{
		"total_sandboxes": len(entries),
		"running":         running,
		"paused":          paused,
		"stopped":         stopped,
		"projects":        projects,
		"backend":         m.cfg.backendType,
	}

	return mcp.JSONResult(summary)
}
