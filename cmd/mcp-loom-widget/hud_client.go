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
	baseURL          string
	token            string
	cfAccessClientID string // optional Cloudflare Access service-token id
	cfAccessSecret   string // optional Cloudflare Access service-token secret
	client           *http.Client
}

// newHUDClient reads connection settings from environment:
//
//   - LOOM_HUD_URL: base URL for the HUD mobile API. Defaults to
//     http://127.0.0.1:3333 (local loomd).
//   - LOOM_HUD_TOKEN: bearer token sent in the Authorization header.
//     Empty value omits the header (loopback HUDs typically trust
//     localhost without auth).
//   - LOOM_HUD_CF_ACCESS_CLIENT_ID / _SECRET: Cloudflare Access
//     service-token pair sent as CF-Access-Client-Id / -Secret
//     headers. Required when the HUD sits behind a Cloudflare Zero
//     Trust application (e.g. hud.flexinfer.ai redirects un-headered
//     requests to flexinfer.cloudflareaccess.com). Omitted when
//     either env var is empty so loopback HUDs still work unchanged.
//
// Mirrors the auth surface the iOS companion already exposes (see
// LoomCompanionKit ConnectionViewModel.cloudflareAccessClientID/Secret).
func newHUDClient() *hudClient {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("LOOM_HUD_URL")), "/")
	if base == "" {
		base = "http://127.0.0.1:3333"
	}
	return &hudClient{
		baseURL:          base,
		token:            strings.TrimSpace(os.Getenv("LOOM_HUD_TOKEN")),
		cfAccessClientID: strings.TrimSpace(os.Getenv("LOOM_HUD_CF_ACCESS_CLIENT_ID")),
		cfAccessSecret:   strings.TrimSpace(os.Getenv("LOOM_HUD_CF_ACCESS_CLIENT_SECRET")),
		// 30s timeout covers prod HUD latencies observed in practice:
		// /handoffs takes ~17s, /stream ~4s, /dashboard ~5s (numbers
		// from hud.flexinfer.ai 2026-05-17). 5s was OK for local
		// loopback but timed out every handoffs call against prod.
		// Long-term fix is HUD-side query optimisation; tracking
		// separately. Override per-deployment via LOOM_HUD_TIMEOUT
		// (Go duration string, e.g. "60s") if a slower HUD needs it.
		client: &http.Client{Timeout: hudTimeoutFromEnv(30 * time.Second)},
	}
}

// hudTimeoutFromEnv parses LOOM_HUD_TIMEOUT as a Go duration; returns
// the fallback when unset or unparseable so a typo in the env var
// doesn't silently disable the timeout.
func hudTimeoutFromEnv(fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv("LOOM_HUD_TIMEOUT"))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// applyAuthHeaders attaches the Bearer + Cloudflare Access headers to
// a request when the corresponding env vars are populated. Centralised
// so get() and post() share identical auth behaviour.
func (h *hudClient) applyAuthHeaders(req *http.Request) {
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	if h.cfAccessClientID != "" && h.cfAccessSecret != "" {
		req.Header.Set("CF-Access-Client-Id", h.cfAccessClientID)
		req.Header.Set("CF-Access-Client-Secret", h.cfAccessSecret)
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
	h.applyAuthHeaders(req)

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
	h.applyAuthHeaders(req)

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
