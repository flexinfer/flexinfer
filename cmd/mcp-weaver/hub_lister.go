package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/crb2nu/loom/pkg/weaver"
)

// HubToolLister implements weaver.ToolLister by calling the MCP gateway's
// loom/tools endpoint over HTTP. This replaces the daemon-local tool cache
// for the standalone binary.
type HubToolLister struct {
	hubURL string
	http   *http.Client
}

// NewHubToolLister creates a lister that fetches tools from the MCP gateway.
func NewHubToolLister(hubURL string) *HubToolLister {
	return &HubToolLister{
		hubURL: hubURL,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// hubToolsResponse mirrors the JSON structure returned by the gateway's
// loom/tools endpoint.
type hubToolsResponse struct {
	Tools []hubToolEntry `json:"tools"`
}

type hubToolEntry struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Server      string         `json:"server"`
}

// ListTools fetches the full tool inventory from the MCP gateway.
func (h *HubToolLister) ListTools() ([]weaver.ToolInfo, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.hubURL+"/loom/tools", nil)
	if err != nil {
		return nil, fmt.Errorf("hub lister: create request: %w", err)
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hub lister: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("hub lister: status %d: %s", resp.StatusCode, body)
	}

	var result hubToolsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("hub lister: decode: %w", err)
	}

	tools := make([]weaver.ToolInfo, len(result.Tools))
	for i, t := range result.Tools {
		tools[i] = weaver.ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Server:      t.Server,
		}
	}
	return tools, nil
}
