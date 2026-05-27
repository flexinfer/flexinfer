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
	pt, ct, fr, _, _, parsed := extractUsageFieldsFull(body)
	return pt, ct, fr, parsed
}

// extractUsageFieldsFull is extractUsageFields plus the cached_tokens hint
// vLLM (and any OpenAI-spec-compliant engine) reports under
// `usage.prompt_tokens_details.cached_tokens`. When the engine omits the
// detail block, cachedTokens is -1 and cachedOK is false to distinguish
// "not reported" from "reported as zero". The F4 instrumentation slice
// gates the X-Flexinfer-Cached-Tokens response header on cachedOK so
// engines that don't report the field don't emit a misleading "0".
func extractUsageFieldsFull(body []byte) (promptTokens, completionTokens int, finishReason string, cachedTokens int, cachedOK bool, ok bool) {
	cachedTokens = -1
	var resp struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails *struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, 0, "", -1, false, false
	}
	if len(resp.Choices) > 0 {
		finishReason = resp.Choices[0].FinishReason
	}
	if resp.Usage.PromptTokensDetails != nil && resp.Usage.PromptTokensDetails.CachedTokens != nil {
		cachedTokens = *resp.Usage.PromptTokensDetails.CachedTokens
		cachedOK = true
	}
	return resp.Usage.PromptTokens, resp.Usage.CompletionTokens, finishReason, cachedTokens, cachedOK, true
}

// Response headers emitted by the proxy for the F4-instant-followup
// instrumentation slice. Clients and operators use these to interpret
// prefix-cache behavior without round-tripping to vLLM metrics.
//
//	X-Flexinfer-Upstream-Ms     — time from request received by the proxy
//	                              to upstream response headers; equals TTFT
//	                              for streaming completions and total
//	                              upstream time for non-streaming.
//	X-Flexinfer-Cached-Tokens   — vLLM-reported
//	                              usage.prompt_tokens_details.cached_tokens
//	                              for non-streaming completions; omitted
//	                              when the engine does not report it (e.g.
//	                              llama.cpp without prefix-cache stats).
const (
	headerUpstreamMs   = "X-Flexinfer-Upstream-Ms"
	headerCachedTokens = "X-Flexinfer-Cached-Tokens"
	headerPromptTokens = "X-Flexinfer-Prompt-Tokens"
	headerFinishReason = "X-Flexinfer-Finish-Reason"
)

// logUpstreamUsage is wired onto httputil.ReverseProxy.ModifyResponse. For
// non-streaming OpenAI completion endpoints it buffers the response body to
// extract token usage, then restores the body so the downstream client
// receives it unchanged. Streaming responses (text/event-stream) are logged
// without usage — we deliberately do not buffer SSE.
//
// Beyond logging, this hook emits X-Flexinfer-Upstream-Ms on every
// completion response and X-Flexinfer-Cached-Tokens when the engine
// reports prefix-cache hit tokens. These are the F4-instant-followup
// headers from the 2026-05-25 long-context brainstorm.
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

	// Surface the upstream timing on every completion response.
	// For streaming, this is TTFT — ModifyResponse fires on the upstream
	// 200 OK before the first SSE chunk is forwarded.
	if isCompletionPath(lc.path) {
		resp.Header.Set(headerUpstreamMs, strconv.FormatInt(durMs, 10))
	}

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

		if pt, ct, fr, cached, cachedOK, ok := extractUsageFieldsFull(body); ok {
			args = append(args,
				"prompt_tokens", pt,
				"completion_tokens", ct,
				"finish_reason", fr,
			)
			resp.Header.Set(headerPromptTokens, strconv.Itoa(pt))
			if fr != "" {
				resp.Header.Set(headerFinishReason, fr)
			}
			if cachedOK {
				args = append(args, "cached_tokens", cached)
				resp.Header.Set(headerCachedTokens, strconv.Itoa(cached))
			}
		}
	}
	slog.Info("request usage", args...)
	return nil
}
