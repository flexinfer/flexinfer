package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HubToolCaller implements weaver.ToolCaller by sending tools/call requests
// to the MCP gateway over HTTP. This replaces the daemon-local bridge caller
// for the standalone binary, and is passed to weaver.NewDaemonToolExecutor.
type HubToolCaller struct {
	hubURL string
	http   *http.Client
}

// NewHubToolCaller creates a caller that dispatches tool calls through the
// MCP gateway.
func NewHubToolCaller(hubURL string) *HubToolCaller {
	return &HubToolCaller{
		hubURL: hubURL,
		http: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// hubCallRequest is the JSON body sent to the gateway's tools/call endpoint.
type hubCallRequest struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// CallToolWithTimeout dispatches a tool call to the MCP gateway. The timeout
// parameter sets a per-call context deadline. The method signature satisfies
// weaver.ToolCaller.
func (h *HubToolCaller) CallToolWithTimeout(name string, args map[string]any, timeout time.Duration) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	reqBody := hubCallRequest{
		Name:      name,
		Arguments: args,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("hub caller: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.hubURL+"/tools/call", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("hub caller: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hub caller: request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB limit
	if err != nil {
		return nil, fmt.Errorf("hub caller: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hub caller: status %d: %s", resp.StatusCode, raw)
	}

	return json.RawMessage(raw), nil
}
