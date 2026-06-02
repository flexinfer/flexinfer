package agentloop

import (
	"net/http"
	"strconv"
)

// Proxy instrumentation headers (see internal/proxy/usage_log.go). The
// engine pins routing with HeaderCacheKey on the request and reads the rest
// off each response.
const (
	HeaderCacheKey     = "X-Flexinfer-Cache-Key"
	HeaderUpstreamMs   = "X-Flexinfer-Upstream-Ms"
	HeaderCachedTokens = "X-Flexinfer-Cached-Tokens"
	HeaderPromptTokens = "X-Flexinfer-Prompt-Tokens"
	HeaderFinishReason = "X-Flexinfer-Finish-Reason"
	// HeaderWantPrefixHit is the request opt-in that asks the proxy to scrape
	// the engine's /metrics and return HeaderPrefixHitRate. HeaderPrefixHitRate
	// is the engine-reported prefix-cache hit rate — the direct signal for
	// engines (gemma4) that omit per-request cached_tokens.
	HeaderWantPrefixHit = "X-Flexinfer-Want-Prefix-Hit"
	HeaderPrefixHitRate = "X-Flexinfer-Prefix-Cache-Hit-Rate"
)

// TurnMetrics is the per-turn signal the F4 kill-test measured, captured
// here for every loop round so a live session reports the same evidence.
type TurnMetrics struct {
	// UpstreamMs is proxy→engine→proxy time (TTFT proxy). Flat UpstreamMs
	// across rounds despite growing PromptTokens is the cache-working signal.
	UpstreamMs int64 `json:"upstream_ms"`
	// CachedTokens is nil when the engine omits prompt_tokens_details
	// (the gemma4 engine does — the fallback path the kill-test took).
	CachedTokens *int `json:"cached_tokens,omitempty"`
	// PromptTokens is the full re-sent context size for this round.
	PromptTokens int `json:"prompt_tokens"`
	// PrefixHitRatio is CachedTokens/PromptTokens when both are known.
	PrefixHitRatio *float64 `json:"prefix_hit_ratio,omitempty"`
	// PrefixCacheHitRate is the engine-reported prefix-cache hit rate the
	// proxy scrapes from /metrics (X-Flexinfer-Prefix-Cache-Hit-Rate), present
	// only when the request opted in via HeaderWantPrefixHit and the proxy
	// could read it. Unlike PrefixHitRatio (derived from per-request
	// cached_tokens) this is available even when the engine omits
	// prompt_tokens_details — closing the gemma4 gap. Engine-windowed, so it
	// is interpretable for a single prefix-consistent session.
	PrefixCacheHitRate *float64 `json:"prefix_cache_hit_rate,omitempty"`
	// FinishReason is the engine's stop reason (stop, tool_calls, length).
	FinishReason string `json:"finish_reason,omitempty"`
}

// parseTurnMetrics reads the proxy headers and reconciles them with the
// usage block from the response body. The body's prompt_tokens is
// authoritative when present; the header is the fallback. CachedTokens stays
// nil unless the header is present and parses, so a missing header is
// distinguishable from a reported zero.
func parseTurnMetrics(h http.Header, usagePromptTokens int) TurnMetrics {
	m := TurnMetrics{FinishReason: h.Get(HeaderFinishReason)}

	if ms, err := strconv.ParseInt(h.Get(HeaderUpstreamMs), 10, 64); err == nil {
		m.UpstreamMs = ms
	}

	m.PromptTokens = usagePromptTokens
	if m.PromptTokens <= 0 {
		if pt, err := strconv.Atoi(h.Get(HeaderPromptTokens)); err == nil {
			m.PromptTokens = pt
		}
	}

	if raw := h.Get(HeaderCachedTokens); raw != "" {
		if ct, err := strconv.Atoi(raw); err == nil {
			m.CachedTokens = &ct
			if m.PromptTokens > 0 {
				ratio := float64(ct) / float64(m.PromptTokens)
				m.PrefixHitRatio = &ratio
			}
		}
	}

	// Engine-reported hit rate from the proxy's /metrics scrape. This is the
	// direct signal when the engine omits cached_tokens (CachedTokens nil).
	if raw := h.Get(HeaderPrefixHitRate); raw != "" {
		if rate, err := strconv.ParseFloat(raw, 64); err == nil {
			m.PrefixCacheHitRate = &rate
		}
	}

	return m
}
