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
	// requestID is the propagated/generated X-Request-ID, carried so the
	// "request usage" line correlates with the "request start" line and any
	// forward-failure WARNs for the same request.
	requestID string
	startedAt time.Time

	// wantPrefixHit is set when the inbound request opts in via the
	// X-Flexinfer-Want-Prefix-Hit header. Only then does the response hook
	// pay the extra upstream /metrics round-trip to derive the prefix-cache
	// hit rate — keeping normal traffic on the zero-cost path.
	wantPrefixHit bool
	// targetURL is the resolved upstream base URL (http://podIP:backendPort)
	// this request was forwarded to. The response hook scrapes
	// <targetURL>/metrics for the engine's prefix-cache counters.
	targetURL string
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

// extractStreamingUsage scans the trailing bytes of an OpenAI-style streaming
// (SSE) completion body for the terminal `usage` chunk and returns its
// prompt/completion token counts plus the prefix-cache detail. OpenAI emits
// this chunk only when the client sets `stream_options.include_usage`: the
// engine streams content frames with `usage: null`, then a final
// `data: {... "usage": {...}}` frame (cumulative totals) before `data: [DONE]`.
//
// The terminal chunk carries the same `prompt_tokens_details.cached_tokens`
// the non-streaming body does, so streamed traffic is just as measurable for
// prefix-cache purposes — it simply has to be read off the stream instead of
// the response headers, which are long since flushed by the time the usage
// chunk arrives. cachedTokens is -1 / cachedOK=false when the engine omits the
// detail block, matching extractUsageFieldsFull's "not reported" vs "reported
// as zero" distinction.
//
// The input may begin mid-line (it is a bounded tail of the full stream); such
// partial leading frames simply fail to parse and are skipped. The last frame
// carrying non-zero usage wins. ok=false when no usage frame is present — the
// caller then records nothing (the completionsTotal stream-share counter still
// quantifies the gap).
func extractStreamingUsage(tail []byte) (promptTokens, completionTokens, cachedTokens int, cachedOK, ok bool) {
	cachedTokens = -1
	for _, raw := range bytes.Split(tail, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		var chunk struct {
			Usage *struct {
				PromptTokens        int `json:"prompt_tokens"`
				CompletionTokens    int `json:"completion_tokens"`
				PromptTokensDetails *struct {
					CachedTokens *int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(payload, &chunk); err != nil {
			continue
		}
		if chunk.Usage == nil {
			continue
		}
		// Require a real total so an early `usage: null`-equivalent frame
		// (or a zero-filled placeholder) does not record a spurious zero.
		if chunk.Usage.PromptTokens <= 0 && chunk.Usage.CompletionTokens <= 0 {
			continue
		}
		promptTokens = chunk.Usage.PromptTokens
		completionTokens = chunk.Usage.CompletionTokens
		// Reset per winning frame so a later usage chunk without the detail
		// block does not inherit an earlier frame's cached count.
		cachedTokens, cachedOK = -1, false
		if d := chunk.Usage.PromptTokensDetails; d != nil && d.CachedTokens != nil {
			cachedTokens = *d.CachedTokens
			cachedOK = true
		}
		ok = true
	}
	return
}

// streamUsageTailCap bounds the trailing bytes retained from a streaming
// completion body so the terminal SSE usage chunk can be parsed without
// buffering the whole stream. The usage frame is a few hundred bytes; 32 KiB is
// generous headroom while staying small per in-flight request.
const streamUsageTailCap = 32 * 1024

// usageSniffingBody is a transparent pass-through wrapper around a streaming
// (SSE) completion response body. It forwards every byte to the client
// unmodified and unbuffered while retaining a bounded tail of the most recent
// bytes. On Close it parses that tail for the terminal streaming `usage` chunk
// and records the token-shape histograms once — closing the non-streaming-only
// blind spot of logUpstreamUsage without delaying the stream.
type usageSniffingBody struct {
	rc     io.ReadCloser
	model  string
	tail   []byte
	parsed bool
}

func newUsageSniffingBody(rc io.ReadCloser, model string) *usageSniffingBody {
	return &usageSniffingBody{rc: rc, model: model}
}

func (b *usageSniffingBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.appendTail(p[:n])
	}
	return n, err
}

// appendTail keeps at most streamUsageTailCap of the most recent bytes seen.
func (b *usageSniffingBody) appendTail(chunk []byte) {
	if len(chunk) >= streamUsageTailCap {
		b.tail = append(b.tail[:0], chunk[len(chunk)-streamUsageTailCap:]...)
		return
	}
	b.tail = append(b.tail, chunk...)
	if len(b.tail) > streamUsageTailCap {
		b.tail = b.tail[len(b.tail)-streamUsageTailCap:]
	}
}

func (b *usageSniffingBody) Close() error {
	b.sniff()
	return b.rc.Close()
}

// sniff parses the retained tail once and records the token-shape histograms
// and the prefix-cache counters when a usage chunk is present. Best-effort: a
// parse miss leaves both untouched.
func (b *usageSniffingBody) sniff() {
	if b.parsed {
		return
	}
	b.parsed = true
	pt, ct, cached, cachedOK, ok := extractStreamingUsage(b.tail)
	if !ok {
		return
	}
	requestPromptTokens.WithLabelValues(b.model).Observe(float64(pt))
	requestCompletionTokens.WithLabelValues(b.model).Observe(float64(ct))
	recordPrefixCacheTokens(b.model, pt, cached, cachedOK)
}

// recordPrefixCacheTokens advances the APC hit-share counters for one
// completion. Called from both the streaming sniffer and the non-streaming
// body parse so the ratio covers all observable traffic rather than only the
// non-streamed minority.
//
// No-ops when the engine did not report cached_tokens: leaving both counters
// untouched keeps numerator and denominator on the same population, so a lane
// whose engine never reports the field is absent from the ratio instead of
// reading as a flat 0% hit rate.
func recordPrefixCacheTokens(model string, promptTokens, cachedTokens int, cachedOK bool) {
	if !cachedOK || promptTokens <= 0 {
		return
	}
	observedPromptTokensTotal.WithLabelValues(model).Add(float64(promptTokens))
	if cachedTokens > 0 {
		cachedPromptTokensTotal.WithLabelValues(model).Add(float64(cachedTokens))
	}
}

// parsePrefixCacheHitRate extracts a prefix-cache hit rate in [0,1] from a
// Prometheus text-format /metrics body. It tolerates either vLLM metrics
// shape:
//
//   - a gauge `vllm:gpu_prefix_cache_hit_rate` (preferred when present), or
//   - cumulative counters `vllm:gpu_prefix_cache_hits_total` /
//     `vllm:gpu_prefix_cache_queries_total`, from which the lifetime ratio
//     hits/queries is computed.
//
// Metric base names are matched by suffix (ignoring the `gpu_` segment) so the
// non-GPU spellings (`vllm:prefix_cache_*`) also parse. Label sets are summed,
// which collapses to the single series a one-model engine pod emits. Returns
// ok=false when neither shape is present or queries is zero — the caller then
// omits the header rather than emit a misleading value.
func parsePrefixCacheHitRate(body []byte) (rate float64, ok bool) {
	var hits, queries float64
	var haveHits, haveQueries bool
	var gauge float64
	var haveGauge bool

	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Prometheus text line: `name{labels} value` or `name value`.
		// Split the value off the end; the metric identifier is everything
		// before the last whitespace-separated field.
		sp := strings.LastIndexAny(line, " \t")
		if sp < 0 {
			continue
		}
		ident := line[:sp]
		valStr := strings.TrimSpace(line[sp+1:])
		// Strip any label block to get the bare metric base name.
		base := ident
		if brace := strings.IndexByte(base, '{'); brace >= 0 {
			base = base[:brace]
		}
		base = strings.TrimSpace(base)

		switch {
		case base == "vllm:gpu_prefix_cache_hit_rate" || base == "vllm:prefix_cache_hit_rate":
			if v, err := strconv.ParseFloat(valStr, 64); err == nil {
				gauge = v
				haveGauge = true
			}
		case strings.HasSuffix(base, "prefix_cache_hits_total"):
			if v, err := strconv.ParseFloat(valStr, 64); err == nil {
				hits += v
				haveHits = true
			}
		case strings.HasSuffix(base, "prefix_cache_queries_total"):
			if v, err := strconv.ParseFloat(valStr, 64); err == nil {
				queries += v
				haveQueries = true
			}
		}
	}

	if haveGauge && gauge >= 0 {
		return gauge, true
	}
	if haveHits && haveQueries && queries > 0 {
		return hits / queries, true
	}
	return 0, false
}

// scrapePrefixCacheHitRate fetches the upstream engine's /metrics and parses
// the prefix-cache hit rate. Best-effort: any error (unreachable endpoint,
// non-200, unparseable body, metrics absent) returns ok=false so the response
// hook simply omits the header. targetURL is the upstream base
// (http://podIP:backendPort); vLLM serves /metrics on the same port as /v1.
func scrapePrefixCacheHitRate(ctx context.Context, targetURL string) (rate float64, ok bool) {
	if targetURL == "" {
		return 0, false
	}
	url := strings.TrimRight(targetURL, "/") + "/metrics"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false
	}
	resp, err := prefixHitScrapeClient.Do(req)
	if err != nil {
		slog.Debug("usage-log: prefix-hit scrape failed", "error", err, "target", targetURL)
		return 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	// /metrics bodies are small KBs; cap defensively.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, false
	}
	return parsePrefixCacheHitRate(body)
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
//	                              vLLM reports the field only when launched
//	                              with --enable-prompt-tokens-details
//	                              (Model config enablePromptTokensDetails)
//	                              AND prefix caching produced a nonzero hit,
//	                              so lanes with enablePrefixCaching: false
//	                              never emit this header by design.
//
//	                              STREAMING: this header is structurally
//	                              unavailable. Response headers are flushed
//	                              when the upstream returns 200, long before
//	                              the terminal SSE usage chunk that carries
//	                              cached_tokens. Streaming clients must read
//	                              usage.prompt_tokens_details.cached_tokens
//	                              off the final chunk instead, which requires
//	                              stream_options.include_usage. The proxy
//	                              forwards that chunk byte-for-byte and, as
//	                              of this slice, also folds it into the
//	                              flexinfer_proxy_*_prompt_tokens_total
//	                              counters so APC health is measurable for
//	                              streamed traffic server-side.
//
//	                              A MISSING header (and a null
//	                              prompt_tokens_details) means ZERO cached
//	                              tokens, not "unknown": vLLM only attaches
//	                              the detail block when cached_tokens is
//	                              nonzero. Because vLLM caches whole KV
//	                              blocks, and hybrid GDN/Mamba lanes align
//	                              the attention block up to the mamba page
//	                              size (qwen35-9b: 544 tokens), any prompt
//	                              shorter than one block reports zero by
//	                              construction. Probe APC with a repeated
//	                              prompt of at least a few blocks.
//	X-Flexinfer-Prefix-Cache-Hit-Rate
//	                            — engine-windowed prefix-cache hit rate in
//	                              [0,1], scraped from the upstream's /metrics
//	                              and emitted only when the inbound request
//	                              opts in via X-Flexinfer-Want-Prefix-Hit.
//	                              Closes the gemma4 gap where the engine
//	                              omits per-request cached_tokens: the rate
//	                              comes from vLLM's own counters instead.
//	                              Engine-windowed/cumulative, not strictly
//	                              per-request — interpretable for a single
//	                              prefix-consistent session. Best-effort:
//	                              omitted on any scrape/parse failure.
const (
	headerUpstreamMs   = "X-Flexinfer-Upstream-Ms"
	headerCachedTokens = "X-Flexinfer-Cached-Tokens"
	headerPromptTokens = "X-Flexinfer-Prompt-Tokens"
	headerFinishReason = "X-Flexinfer-Finish-Reason"
	// headerPrefixHitRate is the response header carrying the engine's
	// prefix-cache hit rate. headerWantPrefixHit is the inbound opt-in.
	headerPrefixHitRate = "X-Flexinfer-Prefix-Cache-Hit-Rate"
	headerWantPrefixHit = "X-Flexinfer-Want-Prefix-Hit"
)

// prefixHitScrapeTimeout bounds the upstream /metrics round-trip the response
// hook makes when a request opts in. It runs synchronously before the client
// receives its response, so it must stay small.
const prefixHitScrapeTimeout = 1500 * time.Millisecond

// prefixHitScrapeClient is a dedicated client for the best-effort /metrics
// scrape. Separate from the proxy transport so a slow or stuck engine metrics
// endpoint can never tie up a forwarding connection.
var prefixHitScrapeClient = &http.Client{Timeout: prefixHitScrapeTimeout}

// prefixHitOptIn reports whether an inbound X-Flexinfer-Want-Prefix-Hit header
// value requests the prefix-cache hit-rate header. Truthy values: 1/true/yes/on
// (case-insensitive). Empty or anything else is opt-out — the default zero-cost
// path with no extra /metrics round-trip.
func prefixHitOptIn(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

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

		// Coverage denominator for the token-shape histograms (which observe
		// non-streaming only). Counts every successful completion by stream
		// flag so the stream=true share quantifies the histograms' blind spot.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			shapeModel := lc.resolvedModel
			if shapeModel == "" {
				shapeModel = lc.model
			}
			completionsTotal.WithLabelValues(shapeModel, strconv.FormatBool(lc.stream)).Inc()
		}
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
		"request_id", lc.requestID,
		"duration_ms", durMs,
	}

	// Opt-in prefix-cache hit-rate header. Only paid for when the client
	// asked (X-Flexinfer-Want-Prefix-Hit) on a completion path; closes the
	// gap for engines (gemma4) that omit per-request cached_tokens by reading
	// the rate off the engine's own /metrics counters. Best-effort.
	if lc.wantPrefixHit && isCompletionPath(lc.path) {
		ctx, cancel := context.WithTimeout(resp.Request.Context(), prefixHitScrapeTimeout)
		if rate, okRate := scrapePrefixCacheHitRate(ctx, lc.targetURL); okRate {
			resp.Header.Set(headerPrefixHitRate, strconv.FormatFloat(rate, 'f', 4, 64))
			args = append(args, "prefix_cache_hit_rate", rate)
		}
		cancel()
	}

	// Streaming completions deliver token usage in a terminal SSE chunk, present
	// only when the client set stream_options.include_usage. Wrap the body in a
	// transparent tail-sniffer that records the token-shape histograms when that
	// chunk flows past — closing the non-streaming-only blind spot below without
	// buffering the stream. No-op for streams without the usage chunk (the
	// completionsTotal stream-share counter still reports the gap).
	if lc.stream && isCompletionPath(lc.path) && resp.Body != nil &&
		resp.StatusCode >= 200 && resp.StatusCode < 300 {
		shapeModel := lc.resolvedModel
		if shapeModel == "" {
			shapeModel = lc.model
		}
		resp.Body = newUsageSniffingBody(resp.Body, shapeModel)
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

			// Surface request shape to Prometheus. The usage *log* line above is
			// unreachable in the aggregator (proxy pod on an un-scraped
			// control-plane node), so these histograms are the durable,
			// scrape-reliable view of the per-lane traffic mix. Label by the
			// resolved model (the actual serving lane) to match the other proxy
			// metrics; fall back to the requested alias when unresolved.
			shapeModel := lc.resolvedModel
			if shapeModel == "" {
				shapeModel = lc.model
			}
			requestPromptTokens.WithLabelValues(shapeModel).Observe(float64(pt))
			requestCompletionTokens.WithLabelValues(shapeModel).Observe(float64(ct))
			resp.Header.Set(headerPromptTokens, strconv.Itoa(pt))
			if fr != "" {
				resp.Header.Set(headerFinishReason, fr)
			}
			if cachedOK {
				args = append(args, "cached_tokens", cached)
				resp.Header.Set(headerCachedTokens, strconv.Itoa(cached))
			}
			recordPrefixCacheTokens(shapeModel, pt, cached, cachedOK)
		}
	}
	slog.Info("request usage", args...)
	return nil
}
