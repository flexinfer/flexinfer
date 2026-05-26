package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/flexinfer/flexinfer/internal/proxy/spec_decode"
)

// httpBackend implements the bench's Draft+Verify contract on top of
// OpenAI-compatible /v1/completions endpoints (vLLM and llama.cpp both
// satisfy this).
//
// Draft hits `draftURL` and asks the small model for N candidate tokens
// with logprobs. Verify hits `verifyURL` with prompt+candidate_text and
// uses vLLM's `prompt_logprobs` extension to extract, in a single forward
// pass, both the verifier's argmax and the logprob it assigned to the
// draft's candidate at each position. That single-call shape is what
// makes the speculative-decoding speedup math meaningful — N candidates
// are scored for ~one decode step of verifier cost.
//
// Token IDs in this bench are synthetic: we hash the token's decoded
// text. The bench's AcceptGreedy compares Token.ID for equality, so as
// long as Draft and Verify both derive IDs from the same text via the
// same hash, identical text matches and produces accept; different text
// produces reject. This sidesteps the wire-protocol limitation that
// OpenAI completions doesn't expose model-native token IDs by default.
type httpBackend struct {
	httpClient    *http.Client
	draftURL      string
	verifyURL     string
	draftModel    string
	verifyModel   string
	promptTopK    int
	requestLogger func(stage, method, url string, payload, response []byte)

	// promptTokenCache memoises the prompt-only token count per prompt
	// string so the Verify path can locate candidate positions in the
	// returned prompt_logprobs array without re-querying on every round.
	promptTokenCache map[string]int
}

// httpBackendConfig is the operator-facing knob bag. promptTopK
// determines how many top tokens the verifier returns per position; the
// draft's candidate may or may not appear in that window — when it
// doesn't we fall back to a very-low logprob, which under
// modified-rejection guarantees the position is rejected (correct
// behavior: if the verifier wouldn't pick the candidate in its top-K, it
// almost certainly won't accept it).
type httpBackendConfig struct {
	httpClient  *http.Client
	draftURL    string
	verifyURL   string
	draftModel  string
	verifyModel string
	promptTopK  int
}

func newHTTPBackend(cfg httpBackendConfig) (*httpBackend, error) {
	if cfg.draftURL == "" {
		return nil, fmt.Errorf("draft-url is required for backend=http")
	}
	if cfg.verifyURL == "" {
		return nil, fmt.Errorf("verify-url is required for backend=http")
	}
	if cfg.draftModel == "" {
		return nil, fmt.Errorf("draft-model is required for backend=http")
	}
	if cfg.verifyModel == "" {
		return nil, fmt.Errorf("verify-model is required for backend=http")
	}
	if cfg.promptTopK <= 0 {
		cfg.promptTopK = 20
	}
	client := cfg.httpClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	return &httpBackend{
		httpClient:       client,
		draftURL:         cfg.draftURL,
		verifyURL:        cfg.verifyURL,
		draftModel:       cfg.draftModel,
		verifyModel:      cfg.verifyModel,
		promptTopK:       cfg.promptTopK,
		promptTokenCache: make(map[string]int),
	}, nil
}

// completionsRequest is the subset of the OpenAI completions schema the
// bench actually populates. Optional vLLM-specific fields (prompt_logprobs)
// are included as pointers so we can omit them when not needed.
type completionsRequest struct {
	Model          string  `json:"model"`
	Prompt         string  `json:"prompt"`
	MaxTokens      int     `json:"max_tokens"`
	Temperature    float64 `json:"temperature"`
	Logprobs       *int    `json:"logprobs,omitempty"`
	PromptLogprobs *int    `json:"prompt_logprobs,omitempty"`
	Stream         bool    `json:"stream"`
}

// completionsResponse captures only the fields we read; the real schema
// has many more we don't care about, so decoding is forgiving.
type completionsResponse struct {
	Choices []struct {
		Text     string             `json:"text"`
		Logprobs *completionLogprob `json:"logprobs"`
		// prompt_logprobs is per-position; each position is either null
		// (BOS) or a map of token_id_str → details. We decode into a
		// generic shape because vLLM uses string keys for ints.
		PromptLogprobs []map[string]promptLogprobEntry `json:"prompt_logprobs"`
	} `json:"choices"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
}

type completionLogprob struct {
	Tokens        []string  `json:"tokens"`
	TokenLogprobs []float64 `json:"token_logprobs"`
}

type promptLogprobEntry struct {
	Logprob      float64 `json:"logprob"`
	Rank         int     `json:"rank"`
	DecodedToken string  `json:"decoded_token"`
}

// tokenIDFromText hashes the decoded token text into a stable non-zero
// 31-bit int that the bench's accept rule can compare for equality. The
// zero value is reserved by spec_decode.Token to mean "no token", so we
// bump 0 → 1.
func tokenIDFromText(s string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	v := int(h.Sum32() & 0x7fffffff)
	if v == 0 {
		v = 1
	}
	return v
}

// Draft asks the draft endpoint for up to n candidate tokens following
// `prompt`. Temperature is fixed at 0 so the draft is deterministic for
// a given prompt; the modified-rejection accept rule still gets useful
// logprobs because vLLM returns the argmax logprob even at T=0.
func (b *httpBackend) Draft(ctx context.Context, prompt string, n int) ([]spec_decode.Token, error) {
	if n <= 0 {
		return nil, nil
	}
	logp := 1
	req := completionsRequest{
		Model:       b.draftModel,
		Prompt:      prompt,
		MaxTokens:   n,
		Temperature: 0,
		Logprobs:    &logp,
	}
	var resp completionsResponse
	if err := b.postJSON(ctx, "draft", b.draftURL, req, &resp); err != nil {
		return nil, fmt.Errorf("draft post: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("draft: empty choices array")
	}
	lp := resp.Choices[0].Logprobs
	if lp == nil || len(lp.Tokens) == 0 {
		return nil, fmt.Errorf("draft: response has no logprobs.tokens (set logprobs=1 supported by backend?)")
	}
	if len(lp.TokenLogprobs) != len(lp.Tokens) {
		return nil, fmt.Errorf("draft: logprob/token length mismatch (%d vs %d)",
			len(lp.TokenLogprobs), len(lp.Tokens))
	}
	out := make([]spec_decode.Token, 0, len(lp.Tokens))
	for i, text := range lp.Tokens {
		out = append(out, spec_decode.Token{
			ID:      tokenIDFromText(text),
			Text:    text,
			Logprob: lp.TokenLogprobs[i],
		})
	}
	return out, nil
}

// Verify runs ONE verifier forward pass on prompt+concat(candidates) and
// extracts per-position (argmax, draft_candidate_logprob) from
// prompt_logprobs.
//
// Position alignment: vLLM returns prompt_logprobs of length equal to
// the total prompt token count (BOS at index 0 is always null). We need
// to know how many positions belong to the prompt vs the candidates so
// we can take the tail correctly. We get that by caching usage.prompt_tokens
// from a prompt-only call on first sight of each prompt.
func (b *httpBackend) Verify(
	ctx context.Context,
	prompt string,
	candidates []spec_decode.Token,
) ([]spec_decode.Logprob, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	promptOnlyN, err := b.countPromptTokens(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("verify: count prompt tokens: %w", err)
	}

	var combined strings.Builder
	combined.WriteString(prompt)
	for _, c := range candidates {
		combined.WriteString(c.Text)
	}

	pl := b.promptTopK
	maxTok := 1
	req := completionsRequest{
		Model:          b.verifyModel,
		Prompt:         combined.String(),
		MaxTokens:      maxTok,
		Temperature:    0,
		PromptLogprobs: &pl,
	}
	var resp completionsResponse
	if err := b.postJSON(ctx, "verify", b.verifyURL, req, &resp); err != nil {
		return nil, fmt.Errorf("verify post: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("verify: empty choices array")
	}
	pls := resp.Choices[0].PromptLogprobs
	if len(pls) == 0 {
		return nil, fmt.Errorf("verify: prompt_logprobs missing (backend does not support vLLM extension)")
	}

	candidatePositions := len(pls) - promptOnlyN
	if candidatePositions <= 0 {
		return nil, fmt.Errorf("verify: combined prompt had %d tokens, prompt-only had %d — no candidate positions to score",
			len(pls), promptOnlyN)
	}
	// Take the first len(candidates) candidate positions; if retokenisation
	// merged or split a candidate, we may have fewer real positions than
	// drafted. Return a shorter slice — the spec_decode accept rule
	// already handles len(verify) < len(draft).
	usable := candidatePositions
	if usable > len(candidates) {
		usable = len(candidates)
	}

	out := make([]spec_decode.Logprob, usable)
	for i := 0; i < usable; i++ {
		pos := promptOnlyN + i
		entry := pls[pos]
		out[i] = extractLogprob(entry, candidates[i])
	}
	return out, nil
}

// Decode runs a plain /v1/completions call against the verifier for
// the baseline run. One round trip generates up to maxTokens tokens —
// this is what spec-decode is trying to beat on wall-clock tok/s.
func (b *httpBackend) Decode(ctx context.Context, prompt string, maxTokens int) ([]spec_decode.Token, error) {
	if maxTokens <= 0 {
		return nil, nil
	}
	logp := 1
	req := completionsRequest{
		Model:       b.verifyModel,
		Prompt:      prompt,
		MaxTokens:   maxTokens,
		Temperature: 0,
		Logprobs:    &logp,
	}
	var resp completionsResponse
	if err := b.postJSON(ctx, "baseline-decode", b.verifyURL, req, &resp); err != nil {
		return nil, fmt.Errorf("baseline decode: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("baseline decode: empty choices")
	}
	lp := resp.Choices[0].Logprobs
	if lp == nil || len(lp.Tokens) == 0 {
		return nil, fmt.Errorf("baseline decode: response has no logprobs.tokens")
	}
	out := make([]spec_decode.Token, 0, len(lp.Tokens))
	for i, text := range lp.Tokens {
		var lpv float64
		if i < len(lp.TokenLogprobs) {
			lpv = lp.TokenLogprobs[i]
		}
		out = append(out, spec_decode.Token{
			ID:      tokenIDFromText(text),
			Text:    text,
			Logprob: lpv,
		})
	}
	return out, nil
}

// extractLogprob picks the verifier's argmax (rank=1 entry) and the
// logprob it assigned to the draft's candidate at this position. If the
// draft candidate is not present in the returned top-K, we synthesise a
// very-negative logprob so AcceptModifiedRejection rejects with
// probability ~1. AcceptGreedy still works correctly because it only
// compares argmax.ID vs draft.ID.
func extractLogprob(entry map[string]promptLogprobEntry, draft spec_decode.Token) spec_decode.Logprob {
	var argmax spec_decode.Token
	draftLP := -math.MaxFloat32 // effectively -inf for AcceptModifiedRejection

	for _, e := range entry {
		if e.Rank == 1 {
			argmax = spec_decode.Token{
				ID:      tokenIDFromText(e.DecodedToken),
				Text:    e.DecodedToken,
				Logprob: e.Logprob,
			}
		}
		if e.DecodedToken == draft.Text {
			draftLP = e.Logprob
		}
	}
	return spec_decode.Logprob{
		Argmax:                argmax,
		DraftCandidateLogprob: draftLP,
	}
}

// countPromptTokens hits the verifier with the prompt alone and reads
// usage.prompt_tokens. It is cheap (max_tokens=1, no logprobs) but we
// still memoise so a 10-round Coordinate loop on the same prompt makes
// the call exactly once.
func (b *httpBackend) countPromptTokens(ctx context.Context, prompt string) (int, error) {
	if n, ok := b.promptTokenCache[prompt]; ok {
		return n, nil
	}
	req := completionsRequest{
		Model:       b.verifyModel,
		Prompt:      prompt,
		MaxTokens:   1,
		Temperature: 0,
	}
	var resp completionsResponse
	if err := b.postJSON(ctx, "verify-count", b.verifyURL, req, &resp); err != nil {
		return 0, err
	}
	if resp.Usage.PromptTokens <= 0 {
		return 0, fmt.Errorf("verify-count: usage.prompt_tokens missing or zero (got %d)", resp.Usage.PromptTokens)
	}
	b.promptTokenCache[prompt] = resp.Usage.PromptTokens
	return resp.Usage.PromptTokens, nil
}

// postJSON marshals payload, POSTs to url, decodes a JSON response into
// out. On non-2xx it returns an error containing a tail of the response
// body so failures are diagnosable without re-running.
func (b *httpBackend) postJSON(
	ctx context.Context,
	stage, url string,
	payload, out any,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := b.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http do: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if b.requestLogger != nil {
		b.requestLogger(stage, http.MethodPost, url, body, respBody)
	}

	if httpResp.StatusCode/100 != 2 {
		// Trim very long error bodies so logs stay readable.
		tail := respBody
		if len(tail) > 512 {
			tail = tail[:512]
		}
		return fmt.Errorf("%s: status %d: %s", stage, httpResp.StatusCode, string(tail))
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		tail := respBody
		if len(tail) > 256 {
			tail = tail[:256]
		}
		return fmt.Errorf("decode response: %w (body=%s)", err, string(tail))
	}
	return nil
}
