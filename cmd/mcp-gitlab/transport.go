package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/poll"
	"github.com/crb2nu/loom/pkg/strutil"
)

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
		// Try array.
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

func backoffDelay(attempt int, max time.Duration) time.Duration {
	delay := time.Duration(1<<attempt) * time.Second
	if delay > max {
		return max
	}
	return delay
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	// Server errors (5xx) are transient.
	if mcperror.IsServerError(err) {
		return true
	}
	// Network errors could be transient.
	errStr := err.Error()
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "temporary failure")
}
