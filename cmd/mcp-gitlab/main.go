// mcp-gitlab is a fast GitLab MCP server written in Go.
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
	"strconv"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/poll"
	"github.com/crb2nu/loom/pkg/strutil"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "1.0.0"

type gitlabServer struct {
	token      string
	apiURL     string
	httpClient *httpclient.Client
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-gitlab", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-gitlab")

	token := env.StringWithFallbacks("GITLAB_PERSONAL_ACCESS_TOKEN", "GITLAB_TOKEN")
	apiURL := strings.TrimSuffix(env.String("GITLAB_API_URL", "https://gitlab.com/api/v4"), "/")

	gl := &gitlabServer{
		token:      token,
		apiURL:     apiURL,
		httpClient: httpclient.NewDefault(),
	}

	logger.Info("starting server", "name", "mcp-gitlab", "version", version, "api_url", apiURL)

	server := mcp.NewServer("mcp-gitlab", version)
	server.SetInstructions("Fast Go-native GitLab MCP server. Supports projects, issues, merge requests, and more.")

	// Register all tools
	registerRepositoryTools(server, gl, tracer)
	registerIssueTools(server, gl, tracer)
	registerMergeRequestTools(server, gl, tracer)
	registerPipelineTools(server, gl, tracer)

	// verify_token
	server.AddTool(mcp.Tool{
		Name:        "verify_token",
		Description: "Verify GitLab API token status and scopes",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, mcpotel.TracedToolHandler(tracer, "verify_token", gl.handleVerifyToken))

	return server.Run(ctx)
}

// Token verification handler

func (g *gitlabServer) handleVerifyToken(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(g.token) == "" {
		return nil, mcperror.NotConfigured("GITLAB_PERSONAL_ACCESS_TOKEN", "set via environment variable or GITLAB_TOKEN")
	}
	if strings.Contains(g.token, "${") {
		return nil, mcperror.InvalidParam("token", "appears to be unexpanded - check your Loom secrets/keychain resolution")
	}

	result := map[string]any{
		"ok":      false,
		"api_url": g.apiURL,
		"token": map[string]any{
			"present": true,
		},
	}

	// Best-effort: PAT metadata (scopes, expiry). Not all GitLab versions expose this endpoint.
	if tok, err := g.request(ctx, "GET", "/personal_access_tokens/self", nil); err == nil {
		// Never return the actual token; the endpoint doesn't include it anyway, but keep future-proof.
		delete(tok, "token")
		result["personal_access_token"] = tok
	} else if err != nil {
		if mcpErr, ok := err.(*mcperror.Error); ok && mcpErr.Code == mcperror.CodeNotFound {
			// Older GitLab instances may not support this endpoint; fall back to /user.
		} else {
			// If it's not a 404, bubble up (401/403/5xx/etc).
			return nil, err
		}
	}

	user, err := g.request(ctx, "GET", "/user", nil)
	if err != nil {
		return nil, err
	}
	result["user"] = user
	result["ok"] = true
	return mcp.JSONResult(result)
}

// HTTP request helpers

func (g *gitlabServer) request(ctx context.Context, method, path string, body any) (map[string]any, error) {
	reqURL := g.apiURL + path

	var reqBodyBytes []byte
	if body != nil {
		var err error
		reqBodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}

	headers := map[string]string{
		"Accept": "application/json",
	}
	if len(reqBodyBytes) > 0 {
		headers["Content-Type"] = "application/json"
	}
	respBody, _, err := g.doRequest(ctx, method, reqURL, reqBodyBytes, headers)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Try array
		var arr []any
		if err := json.Unmarshal(respBody, &arr); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
		return map[string]any{"items": arr}, nil
	}

	return result, nil
}

func (g *gitlabServer) requestList(ctx context.Context, path string) ([]any, error) {
	items, _, err := g.requestListWithMeta(ctx, path)
	return items, err
}

func (g *gitlabServer) requestListWithMeta(ctx context.Context, path string) ([]any, map[string]any, error) {
	reqURL := g.apiURL + path

	respBody, respHeaders, err := g.doRequest(ctx, "GET", reqURL, nil, map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, nil, err
	}

	var result []any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, nil, fmt.Errorf("parse response: %w", err)
	}

	return result, parsePaginationHeaders(respHeaders), nil
}

func (g *gitlabServer) doRequest(ctx context.Context, method, reqURL string, body []byte, headers map[string]string) ([]byte, http.Header, error) {
	const (
		maxAttempts       = 3
		maxErrorBodyBytes = 8192
		maxRetryDelay     = 10 * time.Second
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var reqBody io.Reader
		if len(body) > 0 {
			reqBody = bytes.NewReader(body)
		}

		req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
		if err != nil {
			return nil, nil, err
		}

		req.Header.Set("Accept", "*/*")
		req.Header.Set("User-Agent", "mcp-gitlab/"+version)
		for k, v := range headers {
			if k != "" && v != "" {
				req.Header.Set(k, v)
			}
		}
		if g.token != "" {
			req.Header.Set("PRIVATE-TOKEN", g.token)
		}

		resp, err := g.httpClient.HTTP().Do(req)
		if err != nil {
			if attempt < maxAttempts-1 && isTransientError(err) {
				if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
					return nil, nil, sleepErr
				}
				continue
			}
			return nil, nil, err
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		respHeaders := resp.Header.Clone()
		if readErr != nil {
			if attempt < maxAttempts-1 && isTransientError(readErr) {
				if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
					return nil, respHeaders, sleepErr
				}
				continue
			}
			return nil, respHeaders, readErr
		}

		if resp.StatusCode == 429 && attempt < maxAttempts-1 {
			delay := parseRetryAfter(respHeaders.Get("Retry-After"))
			if delay <= 0 {
				delay = backoffDelay(attempt, maxRetryDelay)
			}
			if delay > maxRetryDelay {
				delay = maxRetryDelay
			}
			if sleepErr := poll.WaitWithContext(ctx, delay); sleepErr != nil {
				return nil, respHeaders, sleepErr
			}
			continue
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 && attempt < maxAttempts-1 {
			if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
				return nil, respHeaders, sleepErr
			}
			continue
		}

		if resp.StatusCode >= 400 {
			return nil, respHeaders, mcperror.APIError("GitLab", resp.StatusCode, strutil.TruncateNoEllipsis(string(respBody), maxErrorBodyBytes))
		}

		return respBody, respHeaders, nil
	}

	return nil, nil, fmt.Errorf("request failed after retries")
}

func (g *gitlabServer) doRequestLimited(ctx context.Context, method, reqURL string, body []byte, headers map[string]string, maxBytes int) ([]byte, *http.Response, bool, error) {
	const (
		maxAttempts       = 3
		maxErrorBodyBytes = 8192
		maxRetryDelay     = 10 * time.Second
	)

	if maxBytes <= 0 {
		return nil, nil, false, fmt.Errorf("maxBytes must be > 0")
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var reqBody io.Reader
		if len(body) > 0 {
			reqBody = bytes.NewReader(body)
		}

		req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
		if err != nil {
			return nil, nil, false, err
		}

		req.Header.Set("Accept", "*/*")
		req.Header.Set("User-Agent", "mcp-gitlab/"+version)
		for k, v := range headers {
			if k != "" && v != "" {
				req.Header.Set(k, v)
			}
		}
		if g.token != "" {
			req.Header.Set("PRIVATE-TOKEN", g.token)
		}

		resp, err := g.httpClient.HTTP().Do(req)
		if err != nil {
			if attempt < maxAttempts-1 && isTransientError(err) {
				if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
					return nil, nil, false, sleepErr
				}
				continue
			}
			return nil, nil, false, err
		}

		limited, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes+1)))
		_ = resp.Body.Close()
		if readErr != nil {
			if attempt < maxAttempts-1 && isTransientError(readErr) {
				if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
					return nil, resp, false, sleepErr
				}
				continue
			}
			return nil, resp, false, readErr
		}

		if resp.StatusCode == 429 && attempt < maxAttempts-1 {
			delay := parseRetryAfter(resp.Header.Get("Retry-After"))
			if delay <= 0 {
				delay = backoffDelay(attempt, maxRetryDelay)
			}
			if delay > maxRetryDelay {
				delay = maxRetryDelay
			}
			if sleepErr := poll.WaitWithContext(ctx, delay); sleepErr != nil {
				return nil, resp, false, sleepErr
			}
			continue
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 && attempt < maxAttempts-1 {
			if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
				return nil, resp, false, sleepErr
			}
			continue
		}

		truncated := len(limited) > maxBytes
		if truncated {
			limited = limited[:maxBytes]
		}

		if resp.StatusCode >= 400 {
			return nil, resp, truncated, mcperror.APIError("GitLab", resp.StatusCode, strutil.TruncateNoEllipsis(string(limited), maxErrorBodyBytes))
		}

		return limited, resp, truncated, nil
	}

	return nil, nil, false, fmt.Errorf("request failed after retries")
}

func (g *gitlabServer) doRequestTail(ctx context.Context, method, reqURL string, headers map[string]string, maxBytes int) ([]byte, *http.Response, int, error) {
	const (
		maxAttempts   = 3
		maxRetryDelay = 10 * time.Second
	)

	if maxBytes <= 0 {
		maxBytes = 200_000
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, reqURL, nil)
		if err != nil {
			return nil, nil, 0, err
		}

		req.Header.Set("Accept", "*/*")
		req.Header.Set("User-Agent", "mcp-gitlab/"+version)
		for k, v := range headers {
			if k != "" && v != "" {
				req.Header.Set(k, v)
			}
		}
		if g.token != "" {
			req.Header.Set("PRIVATE-TOKEN", g.token)
		}

		resp, err := g.httpClient.HTTP().Do(req)
		if err != nil {
			if attempt < maxAttempts-1 && isTransientError(err) {
				if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
					return nil, nil, 0, sleepErr
				}
				continue
			}
			return nil, nil, 0, err
		}

		if resp.StatusCode == 429 && attempt < maxAttempts-1 {
			_ = resp.Body.Close()
			delay := parseRetryAfter(resp.Header.Get("Retry-After"))
			if delay <= 0 {
				delay = backoffDelay(attempt, maxRetryDelay)
			}
			if delay > maxRetryDelay {
				delay = maxRetryDelay
			}
			if sleepErr := poll.WaitWithContext(ctx, delay); sleepErr != nil {
				return nil, nil, 0, sleepErr
			}
			continue
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 && attempt < maxAttempts-1 {
			_ = resp.Body.Close()
			if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
				return nil, nil, 0, sleepErr
			}
			continue
		}

		tail, totalRead, readErr := readTail(resp.Body, maxBytes)
		_ = resp.Body.Close()
		if readErr != nil {
			if attempt < maxAttempts-1 && isTransientError(readErr) {
				if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
					return nil, resp, totalRead, sleepErr
				}
				continue
			}
			return nil, resp, totalRead, readErr
		}

		if resp.StatusCode >= 400 {
			return nil, resp, totalRead, mcperror.APIError("GitLab", resp.StatusCode, strutil.TruncateNoEllipsis(string(tail), 8192))
		}

		return tail, resp, totalRead, nil
	}

	return nil, nil, 0, fmt.Errorf("request failed after retries")
}

func (g *gitlabServer) fetchJobTraceTail(ctx context.Context, project string, jobID int, tailLines int, maxBytes int) (string, string, bool, error) {
	if tailLines <= 0 {
		tailLines = 200
	}
	if maxBytes <= 0 {
		maxBytes = 200_000
	}

	path := fmt.Sprintf("/projects/%s/jobs/%d/trace", encodeProject(project), jobID)
	reqURL := g.apiURL + path

	b, resp, totalRead, err := g.doRequestTail(ctx, "GET", reqURL, map[string]string{"Accept": "text/plain"}, maxBytes) //nolint:bodyclose // body closed inside doRequestTail
	if err != nil {
		return "", "", false, err
	}

	contentType := ""
	if resp != nil {
		contentType = resp.Header.Get("Content-Type")
	}

	truncated := totalRead > maxBytes
	trace := string(b)
	lines := strings.Split(trace, "\n")
	if tailLines > 0 && len(lines) > tailLines {
		truncated = true
		lines = lines[len(lines)-tailLines:]
		trace = strings.Join(lines, "\n")
	}

	return trace, contentType, truncated, nil
}

// Utility functions

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	secs, err := strconv.Atoi(v)
	if err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	// Retry-After also supports HTTP date format.
	if t, parseErr := http.ParseTime(v); parseErr == nil {
		delay := time.Until(t)
		if delay <= 0 {
			return 0
		}
		return delay
	}
	return 0
}

func backoffDelay(attempt int, max time.Duration) time.Duration {
	delay := time.Duration(1<<attempt) * time.Second
	if delay > max {
		return max
	}
	return delay
}

func parsePaginationHeaders(headers http.Header) map[string]any {
	if headers == nil {
		return nil
	}
	out := map[string]any{}
	for _, kv := range []struct {
		key string
		dst string
	}{
		{"X-Page", "page"},
		{"X-Per-Page", "per_page"},
		{"X-Next-Page", "next_page"},
		{"X-Prev-Page", "prev_page"},
		{"X-Total-Pages", "total_pages"},
		{"X-Total", "total"},
	} {
		v := strings.TrimSpace(headers.Get(kv.key))
		if v == "" {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil {
			out[kv.dst] = n
		} else {
			out[kv.dst] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func readTail(r io.Reader, maxBytes int) ([]byte, int, error) {
	if maxBytes <= 0 {
		return nil, 0, fmt.Errorf("maxBytes must be > 0")
	}

	ring := make([]byte, maxBytes)
	buf := make([]byte, 32*1024)
	pos := 0
	filled := 0
	total := 0

	for {
		n, err := r.Read(buf)
		if n > 0 {
			total += n
			if n >= maxBytes {
				copy(ring, buf[n-maxBytes:n])
				pos = 0
				filled = maxBytes
			} else {
				end := pos + n
				if end <= maxBytes {
					copy(ring[pos:end], buf[:n])
				} else {
					first := maxBytes - pos
					copy(ring[pos:], buf[:first])
					copy(ring[:end-maxBytes], buf[first:n])
				}
				pos = end % maxBytes
				if filled < maxBytes {
					filled += n
					if filled > maxBytes {
						filled = maxBytes
					}
				}
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, total, err
		}
	}

	if filled == 0 {
		return []byte{}, total, nil
	}

	if filled < maxBytes {
		return ring[:filled], total, nil
	}

	// pos is the start of the oldest data.
	out := make([]byte, 0, maxBytes)
	out = append(out, ring[pos:]...)
	out = append(out, ring[:pos]...)
	return out, total, nil
}

func encodeProject(project string) string {
	return url.PathEscape(project)
}

func encodeArtifactPath(artifactPath string) string {
	parts := strings.Split(strings.TrimPrefix(artifactPath, "/"), "/")
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		escaped = append(escaped, url.PathEscape(part))
	}
	return strings.Join(escaped, "/")
}

func validatePositiveIntParam(field string, value int) *mcp.CallToolResult {
	if value <= 0 {
		return mcp.ErrorResult(mcperror.InvalidParam(field, "must be greater than 0"))
	}
	return nil
}

func parseOptionalPositiveIntSliceArg(args map[string]any, field string) ([]int, error) {
	raw, ok := args[field]
	if !ok || raw == nil {
		return nil, nil
	}

	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array of integers")
	}

	out := make([]int, 0, len(values))
	for i, item := range values {
		n, ok := toInt(item)
		if !ok {
			return nil, fmt.Errorf("item %d must be an integer", i)
		}
		if n <= 0 {
			return nil, fmt.Errorf("item %d must be greater than 0", i)
		}
		out = append(out, n)
	}

	return out, nil
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case float32:
		if n != float32(int(n)) {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case int32:
		return int(n), true
	case uint:
		return int(n), true
	case uint64:
		return int(n), true
	case uint32:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func normalizePerPage(perPage int, defaultVal int) int {
	return validate.NormalizePerPage(perPage, defaultVal, 100)
}

func normalizePage(page int) int {
	return validate.NormalizePage(page)
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	// Server errors (5xx) are transient
	if mcperror.IsServerError(err) {
		return true
	}
	// Network errors could be transient
	errStr := err.Error()
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "temporary failure")
}

func isTextContent(contentType string, data []byte) bool {
	if strings.HasPrefix(contentType, "text/") {
		return true
	}
	if strings.Contains(contentType, "json") ||
		strings.Contains(contentType, "xml") ||
		strings.Contains(contentType, "yaml") {
		return true
	}
	// Check first 512 bytes for text
	checkLen := len(data)
	if checkLen > 512 {
		checkLen = 512
	}
	for i := 0; i < checkLen; i++ {
		b := data[i]
		// Allow printable ASCII, tabs, newlines, carriage returns
		if b < 32 && b != '\t' && b != '\n' && b != '\r' {
			return false
		}
		if b > 126 && b < 160 {
			return false
		}
	}
	return true
}

func encodeBase64(data []byte) string {
	const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var result strings.Builder
	result.Grow((len(data)*4 + 2) / 3)

	for i := 0; i < len(data); i += 3 {
		var n uint32
		remaining := len(data) - i

		n = uint32(data[i]) << 16
		if remaining > 1 {
			n |= uint32(data[i+1]) << 8
		}
		if remaining > 2 {
			n |= uint32(data[i+2])
		}

		result.WriteByte(base64Chars[(n>>18)&0x3f])
		result.WriteByte(base64Chars[(n>>12)&0x3f])
		if remaining > 1 {
			result.WriteByte(base64Chars[(n>>6)&0x3f])
		} else {
			result.WriteByte('=')
		}
		if remaining > 2 {
			result.WriteByte(base64Chars[n&0x3f])
		} else {
			result.WriteByte('=')
		}
	}

	return result.String()
}

// Ensure validate package is used (referenced by handler files)
var _ = validate.NewArgs
