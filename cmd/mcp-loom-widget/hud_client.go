package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// hudClient relays GET requests from widget-triggered MCP tool calls
// to the loom HUD's /api/mobile/v1 endpoints. The bearer token lives
// only in this process — by design it never enters the LLM's context
// window, so the widget cannot exfiltrate it (per Mitiga Labs disclosure,
// May 2026, where Claude Code MCP OAuth tokens were stolen via proxied
// stdio chains that had token visibility).
//
// All relay tools wrap this client; the widget asks for tool calls via
// the MCP Apps bridge, the host proxies them to this server, and we
// fetch on the widget's behalf.
type hudClient struct {
	baseURL string
	token   string
	client  *http.Client
}

// newHUDClient reads LOOM_HUD_URL and LOOM_HUD_TOKEN from the
// environment, with sensible defaults for local dev (loomd binds to
// 127.0.0.1:3333 by default; the iOS companion + mobile API expect
// the same port).
func newHUDClient() *hudClient {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("LOOM_HUD_URL")), "/")
	if base == "" {
		base = "http://127.0.0.1:3333"
	}
	return &hudClient{
		baseURL: base,
		token:   strings.TrimSpace(os.Getenv("LOOM_HUD_TOKEN")),
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// get fetches the JSON body at path (relative — e.g.
// "/api/mobile/v1/dashboard") and returns it raw. The body is not
// re-encoded so tool callers can pass the HUD response through to the
// widget unchanged. allowedPaths constrains which HUD endpoints the
// widget can reach via this server — defense in depth against a
// compromised widget arbitrary-fetching from any HUD path.
func (h *hudClient) get(ctx context.Context, path string, allowedPaths []string) ([]byte, error) {
	if !isAllowedPath(path, allowedPaths) {
		return nil, fmt.Errorf("hudClient: path %q not in allowlist", path)
	}
	u, err := url.Parse(h.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("hudClient: parse url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("hudClient: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hudClient: GET %s: %w", path, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("hudClient: read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		// Return the body in the error so the widget can render the
		// HUD's own error message (typically "unauthorized" or
		// "scope_required"); truncate to avoid massive errors.
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		return nil, fmt.Errorf("hudClient: HUD returned %d for %s: %s", resp.StatusCode, path, snippet)
	}
	return body, nil
}

// isAllowedPath is a small allowlist check. Each relay tool declares
// which HUD path it owns; the hudClient enforces that the widget never
// asks for paths outside the declared set even if the tool handler is
// somehow tricked into passing arbitrary input.
func isAllowedPath(path string, allowed []string) bool {
	for _, ok := range allowed {
		if path == ok {
			return true
		}
	}
	return false
}
