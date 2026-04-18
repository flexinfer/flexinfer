package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/modelmeta"
)

// defaultPromptReserveTokens is the number of tokens reserved for the prompt
// when clamping an inbound max_tokens against the model's context window.
// Chosen generously: 512 leaves room for short chat prompts while still giving
// the client close-to-max generation budget.
const defaultPromptReserveTokens = 512

// clampMaxTokensInBody inspects a JSON request body and, when max_tokens would
// overflow the target model's context window, rewrites it in place so at least
// promptReserveTokens of prompt budget remain. Returns the (possibly unchanged)
// body, the original requested max_tokens when a change was made (-1 otherwise),
// and the clamped value (-1 when unchanged).
//
// Only acts when:
//   - body parses as a JSON object,
//   - body contains an integer "max_tokens" field > 0,
//   - ContextWindow > 0 (we know the cap),
//   - max_tokens > ContextWindow - promptReserveTokens.
//
// Returns body unchanged on any error so the existing fallback path through
// vLLM's own validation still fires.
func clampMaxTokensInBody(body []byte, limits modelmeta.TokenLimits, promptReserveTokens int) ([]byte, int, int) {
	if len(body) == 0 || limits.ContextWindow <= 0 {
		return body, -1, -1
	}
	if promptReserveTokens < 1 {
		promptReserveTokens = defaultPromptReserveTokens
	}

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return body, -1, -1
	}

	raw, ok := data["max_tokens"]
	if !ok {
		return body, -1, -1
	}

	orig, ok := toPositiveInt(raw)
	if !ok || orig <= 0 {
		return body, -1, -1
	}

	cap := limits.ContextWindow - promptReserveTokens
	if cap < 1 {
		cap = 1
	}
	if orig <= cap {
		return body, -1, -1
	}

	data["max_tokens"] = cap
	modified, err := json.Marshal(data)
	if err != nil {
		return body, -1, -1
	}
	// Preserve original body if marshalling somehow produces identical bytes.
	if bytes.Equal(modified, body) {
		return body, -1, -1
	}
	return modified, orig, cap
}

// toPositiveInt extracts an integer from an arbitrary JSON-decoded value. JSON
// numbers decode to float64 via encoding/json, so we accept both that and the
// bare int cases that other call sites might feed in.
func toPositiveInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n < 0 || n > 2147483647 {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		if n < 0 || n > 2147483647 {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

// fetchTokenLimits loads the target Model v1alpha2 and returns its resolved
// token limits, or a zero-value limits struct if the Model cannot be read.
func (p *Proxy) fetchTokenLimits(ctx context.Context, modelName string) modelmeta.TokenLimits {
	m := &aiv1alpha2.Model{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m); err != nil {
		return modelmeta.TokenLimits{}
	}
	return modelmeta.ResolveTokenLimits(&m.Spec)
}

// maybeClampMaxTokens is the routing-side adapter used in the request forward
// path. It consults the model's token limits and clamps max_tokens in the JSON
// body if necessary, logging and incrementing the clamp counter on change.
func (p *Proxy) maybeClampMaxTokens(ctx context.Context, modelName string, body []byte) []byte {
	if len(body) == 0 || modelName == "" {
		return body
	}
	limits := p.fetchTokenLimits(ctx, modelName)
	clamped, orig, to := clampMaxTokensInBody(body, limits, defaultPromptReserveTokens)
	if orig < 0 || to < 0 {
		return body
	}
	slog.InfoContext(ctx, "clamped max_tokens",
		"model", modelName,
		"from", orig,
		"to", to,
		"context_window", limits.ContextWindow,
		"prompt_reserve_tokens", defaultPromptReserveTokens,
		"reason", "context_window_headroom",
	)
	maxTokensClampedTotal.WithLabelValues(modelName, "context_window_headroom").Inc()
	return clamped
}
