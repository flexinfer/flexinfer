package main

import (
	"bytes"
	"context"
	"encoding/json"
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

// safeIDPattern is enforced on every interpolated path segment. Handoff
// IDs from the HUD are uuid-like (alphanumerics + dash + underscore);
// anything else is rejected before the URL is built so the widget can
// never construct a path like /api/mobile/v1/handoffs/../secrets/accept.
const safeIDChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"

func isSafeID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		if !strings.ContainsRune(safeIDChars, rune(id[i])) {
			return false
		}
	}
	return true
}

// post sends a JSON body to a templated HUD path. pathTemplate uses
// {placeholder} segments which are substituted from the substitutions
// map after a strict isSafeID check on every value. The templated
// path (with placeholders) must be in allowedTemplates; the resolved
// concrete path is then constructed safely. body is encoded as JSON;
// pass nil for an empty body.
//
// This is the mutating sibling of get(). Path templates rather than
// fixed strings let allowlisted endpoints carry an id segment without
// surrendering the defense-in-depth.
func (h *hudClient) post(ctx context.Context, pathTemplate string, substitutions map[string]string, body any, allowedTemplates []string) ([]byte, error) {
	if !isAllowedPath(pathTemplate, allowedTemplates) {
		return nil, fmt.Errorf("hudClient: path template %q not in allowlist", pathTemplate)
	}
	resolved := pathTemplate
	for key, value := range substitutions {
		if !isSafeID(value) {
			return nil, fmt.Errorf("hudClient: substitution %q has unsafe value %q", key, value)
		}
		resolved = strings.ReplaceAll(resolved, "{"+key+"}", value)
	}
	if strings.Contains(resolved, "{") {
		// All template placeholders must have been substituted; a
		// stray "{name}" indicates a programming error in the caller.
		return nil, fmt.Errorf("hudClient: unsubstituted placeholder in %q", resolved)
	}

	u, err := url.Parse(h.baseURL + resolved)
	if err != nil {
		return nil, fmt.Errorf("hudClient: parse url: %w", err)
	}

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("hudClient: marshal body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), reqBody)
	if err != nil {
		return nil, fmt.Errorf("hudClient: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hudClient: POST %s: %w", resolved, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("hudClient: read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		return nil, fmt.Errorf("hudClient: HUD returned %d for %s: %s", resp.StatusCode, resolved, snippet)
	}
	return respBody, nil
}
