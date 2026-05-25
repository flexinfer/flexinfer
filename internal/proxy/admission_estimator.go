package proxy

import (
	"encoding/json"
	"unicode/utf8"
)

// admissionMaxBodyBytes caps the body size the estimator will parse. Above
// this size we return ok=false and let the runtime decide. Real-world chat
// payloads fit comfortably below 256 KB; bodies bigger than that are almost
// certainly already over the lane's context window and forwarding to the
// runtime (which will reject in <1s for vLLM, longer for llama.cpp) is
// acceptable when the proxy can't make a cheap decision.
const admissionMaxBodyBytes = 256 * 1024

// estimatePromptTokensFromBody parses a JSON OpenAI-style chat-completion or
// completion request body and returns a conservative upper-bound token estimate
// for the prompt content plus a flag indicating whether the body was parseable.
//
// The estimator is intentionally cheap:
//  1. Single json.Unmarshal pass into map[string]any.
//  2. A short walk over messages[].content (chat) or prompt (completions).
//  3. A per-message byte/rune budget with simple heuristics.
//
// It is NOT a tokenizer. The contract is:
//
//   - For typical English/code chat payloads, the estimate is within roughly
//     [0.85 ×, 1.30 ×] of the runtime's reported prompt_tokens.
//   - For non-Latin payloads (Chinese, base64 blobs) it deliberately
//     over-counts; this is acceptable because admission control is supposed
//     to be conservative.
//
// Returns (0, false) if the body is empty, too large, not JSON, or otherwise
// unusable. Callers MUST treat (0, false) as "do not enforce" — let the
// runtime decide.
func estimatePromptTokensFromBody(body []byte) (int, bool) {
	if len(body) == 0 || len(body) > admissionMaxBodyBytes {
		return 0, false
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, false
	}

	total := 0
	matched := false

	// Chat-completions: messages[].content
	if msgs, ok := data["messages"].([]any); ok {
		matched = true
		// Per-message chat-template overhead: rough OpenAI cookbook number
		// (3 tokens per message for role markers + separators).
		const perMessageOverhead = 4
		// Conversation-level overhead: priming + assistant turn marker.
		const conversationOverhead = 3
		total += conversationOverhead
		for _, raw := range msgs {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			total += perMessageOverhead
			if content, ok := m["content"].(string); ok {
				total += estimateTokensFromString(content)
				continue
			}
			// Vision / multimodal: content is an array of parts.
			// We sum over text parts; images get a fixed overhead.
			if parts, ok := m["content"].([]any); ok {
				for _, partRaw := range parts {
					part, ok := partRaw.(map[string]any)
					if !ok {
						continue
					}
					if text, ok := part["text"].(string); ok {
						total += estimateTokensFromString(text)
					}
					if _, ok := part["image_url"].(map[string]any); ok {
						// Fixed approximation: most providers bill ~85 tokens
						// for "low detail" and up to ~765 for "high". We use a
						// conservative middle figure.
						total += 256
					}
				}
			}
		}
	}

	// /v1/completions: top-level "prompt"
	if prompt, ok := data["prompt"].(string); ok {
		matched = true
		total += estimateTokensFromString(prompt)
	} else if prompts, ok := data["prompt"].([]any); ok {
		matched = true
		for _, p := range prompts {
			if s, ok := p.(string); ok {
				total += estimateTokensFromString(s)
			}
		}
	}

	if !matched {
		return 0, false
	}
	return total, true
}

// estimateTokensFromString returns a conservative token-count estimate for the
// given string using a single rune scan.
//
// Heuristic: tokens ≈ ceil(bytes / 3.5) for ASCII-ish payloads, plus extra
// penalty for high-codepoint (likely CJK) runes which tend to map to 1
// token per rune in modern BPE tokenizers.
//
// The 3.5 constant is more conservative than OpenAI's "~4 chars/token" rule of
// thumb and is intentional — admission is supposed to err on the refusing
// side when the prompt is close to the ceiling, not generously forward to
// the runtime to fail at 30s.
func estimateTokensFromString(s string) int {
	if s == "" {
		return 0
	}

	asciiBytes := 0
	highRunes := 0
	// Single pass over the bytes. Avoid utf8.RuneCountInString because we want
	// to bucket ASCII vs high-codepoint differently.
	i := 0
	for i < len(s) {
		b := s[i]
		if b < 0x80 {
			asciiBytes++
			i++
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		if size <= 0 {
			// Invalid encoding; advance one byte and count as ASCII to avoid
			// looping forever.
			asciiBytes++
			i++
			continue
		}
		highRunes++
		i += size
	}

	// ASCII portion: ceil(bytes / 3.5) → bytes*2/7, rounded up.
	asciiTokens := (asciiBytes*2 + 6) / 7
	// High-codepoint runes are conservatively 1 token each (CJK in modern BPE
	// tokenizers usually maps each character to 1–2 tokens).
	return asciiTokens + highRunes
}

// extractMaxTokensFromBody returns the requested max_tokens value, or
// `defaultMaxTokens` when the field is absent/non-positive. The second return
// is true when the field was present and parseable.
//
// Re-parses the body; callers can avoid the double parse by sharing the
// already-decoded map, but for now we keep the estimator and the max-tokens
// extractor independent so they can be tested separately.
func extractMaxTokensFromBody(body []byte, defaultMaxTokens int) (int, bool) {
	if len(body) == 0 {
		return defaultMaxTokens, false
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return defaultMaxTokens, false
	}
	raw, ok := data["max_tokens"]
	if !ok {
		return defaultMaxTokens, false
	}
	n, ok := toPositiveInt(raw)
	if !ok || n <= 0 {
		return defaultMaxTokens, false
	}
	return n, true
}
