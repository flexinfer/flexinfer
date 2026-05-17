package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/env"
)

// iccClient is a thin HTTP wrapper around the ICC backend. The MCP server
// is a trusted-context caller: it sends Origin + X-Requested-With +
// Content-Type and the backend accepts that. HMAC signing is a future
// hardening slice (intentionally not implemented here).
type iccClient struct {
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger
}

// defaultICCTimeout is used when ICC_TIMEOUT_SECONDS is unset / invalid.
const defaultICCTimeout = 30 * time.Second

// newICCClient reads ICC_* env vars and returns a configured client. It
// never fails — missing ICC_BASE_URL is fine at startup so tools that do
// not need ICC (e.g. icc_format_slack_paste, icc_lint_notes) still work
// without the backend reachable. Network-backed tools call
// ensureConfigured() at call time and fail loud if the base URL is empty.
func newICCClient(logger *slog.Logger) *iccClient {
	// ICC_BASE_URL is the canonical Slice C-2 name. ICC_API_URL is the
	// historical Slice B name; keep it as a fallback so existing
	// deployments don't need to flip env vars in lockstep with the
	// MCP-server roll-out.
	base := strings.TrimRight(strings.TrimSpace(env.StringWithFallbacks("ICC_BASE_URL", "ICC_API_URL")), "/")
	timeout := time.Duration(env.Int("ICC_TIMEOUT_SECONDS", int(defaultICCTimeout/time.Second))) * time.Second
	if timeout <= 0 {
		timeout = defaultICCTimeout
	}
	return &iccClient{
		baseURL:    base,
		httpClient: &http.Client{Timeout: timeout},
		logger:     logger,
	}
}

// ensureConfigured returns an error if ICC_BASE_URL is empty so callers
// fail loud rather than silently no-op against a missing backend.
func (c *iccClient) ensureConfigured() error {
	if c == nil || c.baseURL == "" {
		return errors.New("ICC not configured: set ICC_BASE_URL")
	}
	return nil
}

// post sends a JSON POST to <baseURL>/<path> with the three required
// headers (Content-Type, X-Requested-With, Origin) and returns the raw
// status code + body bytes. Callers branch on shape (e.g. demote's
// URL-templated path needs raw bytes, postJSON's typed wrapper would
// erase the artifact id from the path).
func (c *iccClient) post(ctx context.Context, path string, body any) (int, []byte, error) {
	if err := c.ensureConfigured(); err != nil {
		return 0, nil, err
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return 0, nil, fmt.Errorf("encode request body: %w", err)
	}

	url := c.baseURL + ensureLeadingSlash(path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "integration-command-center")
	req.Header.Set("Origin", c.baseURL)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}

// iccEnvelope is the standard ICC response envelope: {"ok": bool,
// "result": <payload>} on success and {"error": "<message>"} on failure.
type iccEnvelope[T any] struct {
	OK     bool   `json:"ok"`
	Result T      `json:"result"`
	Error  string `json:"error"`
}

// postJSON is a convenience wrapper around post that decodes the
// response envelope into a typed result. It returns the raw status code
// so callers can still distinguish 201 (fresh) from 200 (idempotent)
// when the response body carries the distinction implicitly.
func postJSON[T any](ctx context.Context, c *iccClient, path string, body any) (int, T, error) {
	var zero T
	status, raw, err := c.post(ctx, path, body)
	if err != nil {
		return status, zero, err
	}

	// 2xx → decode envelope and return result.
	if status >= 200 && status < 300 {
		var env iccEnvelope[T]
		if len(raw) == 0 {
			return status, zero, fmt.Errorf("ICC returned empty body (status=%d)", status)
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return status, zero, fmt.Errorf("decode response: %w; body=%s", err, string(raw))
		}
		return status, env.Result, nil
	}

	// Non-2xx → try to pull a structured error message out of the body.
	var env iccEnvelope[json.RawMessage]
	if json.Unmarshal(raw, &env) == nil && env.Error != "" {
		return status, zero, fmt.Errorf("ICC %d: %s", status, env.Error)
	}
	return status, zero, fmt.Errorf("ICC %d: %s", status, strings.TrimSpace(string(raw)))
}

func ensureLeadingSlash(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}
