package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// usageLogKey carries per-request metadata from the request handler into the
// upstream-response hook so the access log can correlate both halves.
type usageLogKeyType struct{}

var usageLogKey = usageLogKeyType{}

type usageLogCtx struct {
	model         string
	resolvedModel string
	path          string
	maxTokens     int // -1 when not present in body
	stream        bool
	userAgent     string
	startedAt     time.Time
}

func withUsageLogCtx(ctx context.Context, lc *usageLogCtx) context.Context {
	return context.WithValue(ctx, usageLogKey, lc)
}

func usageLogCtxFrom(ctx context.Context) *usageLogCtx {
	if v, ok := ctx.Value(usageLogKey).(*usageLogCtx); ok {
		return v
	}
	return nil
}

// parseRequestForUsageLog extracts `max_tokens` and `stream` from a JSON
// request body. Best-effort: missing or malformed fields default to (-1, false).
func parseRequestForUsageLog(body []byte) (maxTokens int, stream bool) {
	maxTokens = -1
	if len(body) == 0 {
		return
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return
	}
	if v, ok := data["max_tokens"]; ok {
		if n, ok := toPositiveInt(v); ok {
			maxTokens = n
		}
	}
	if v, ok := data["stream"]; ok {
		if b, ok := v.(bool); ok {
			stream = b
		}
	}
	return
}

// isCompletionPath returns true for OpenAI-style chat or text completion
// endpoints. Only these emit a `usage` block worth parsing.
func isCompletionPath(path string) bool {
	return strings.HasSuffix(path, "/v1/chat/completions") ||
		strings.HasSuffix(path, "/v1/completions")
}

// extractUsageFields parses an OpenAI-style response body and returns
// (prompt_tokens, completion_tokens, finish_reason, ok). ok=false when the
// body does not parse — usage and finish_reason fields default to zero/empty.
func extractUsageFields(body []byte) (promptTokens, completionTokens int, finishReason string, ok bool) {
	var resp struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, 0, "", false
	}
	if len(resp.Choices) > 0 {
		finishReason = resp.Choices[0].FinishReason
	}
	return resp.Usage.PromptTokens, resp.Usage.CompletionTokens, finishReason, true
}

// logUpstreamUsage is wired onto httputil.ReverseProxy.ModifyResponse. For
// non-streaming OpenAI completion endpoints it buffers the response body to
// extract token usage, then restores the body so the downstream client
// receives it unchanged. Streaming responses (text/event-stream) are logged
// without usage — we deliberately do not buffer SSE.
//
// Emits a single slog line per request:
//
//	level=INFO msg="request usage" event=request_usage model=... prompt_tokens=...
func (p *Proxy) logUpstreamUsage(resp *http.Response) error {
	lc := usageLogCtxFrom(resp.Request.Context())
	if lc == nil {
		return nil
	}
	durMs := time.Since(lc.startedAt).Milliseconds()

	args := []any{
		"event", "request_usage",
		"model", lc.model,
		"resolved_model", lc.resolvedModel,
		"path", lc.path,
		"status", resp.StatusCode,
		"max_tokens", lc.maxTokens,
		"stream", lc.stream,
		"user_agent", lc.userAgent,
		"duration_ms", durMs,
	}

	contentType := resp.Header.Get("Content-Type")
	parseable := resp.StatusCode >= 200 && resp.StatusCode < 300 &&
		!lc.stream &&
		strings.Contains(contentType, "application/json") &&
		isCompletionPath(lc.path)

	if parseable {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			slog.Debug("usage-log: failed to read upstream body", "error", err)
			slog.Info("request usage", args...)
			return nil
		}
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))

		if pt, ct, fr, ok := extractUsageFields(body); ok {
			args = append(args,
				"prompt_tokens", pt,
				"completion_tokens", ct,
				"finish_reason", fr,
			)
		}
	}
	slog.Info("request usage", args...)
	return nil
}
