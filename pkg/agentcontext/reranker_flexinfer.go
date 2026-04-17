package agentcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

// =========================================================================
// FlexInfer rerank backend
//
// Calls a cross-encoder rerank proxy deployed alongside flexinfer. The
// canonical endpoint shape (borrowed from the TEI / flexinfer-rerank
// contract) is:
//
//   POST {base}/v1/rerank
//   {
//     "model": "<model>",
//     "query": "<string>",
//     "documents": ["...", "..."],
//     "top_k": <int>                  // optional
//   }
//   -> 200 {"results":[{"index":<int>,"relevance_score":<float>}, ...]}
//
// If the deployed proxy does not expose /v1/rerank (404), we surface that
// via the "unavailable" rerank_status and leave ordering untouched so
// callers never fail. An embedding-cosine fallback is left for a future
// slice — this keeps the slice surgical per the spec.
// =========================================================================

// flexInferRerankRequest matches the proxy JSON contract documented above.
type flexInferRerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopK      int      `json:"top_k,omitempty"`
}

// flexInferRerankResult is a single rerank score row.
type flexInferRerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// flexInferRerankResponse wraps the 200 response body.
type flexInferRerankResponse struct {
	Results []flexInferRerankResult `json:"results"`
}

// FlexInferReranker calls the flexinfer rerank proxy.
type FlexInferReranker struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
	timeout    time.Duration
	logger     *slog.Logger
	backendTag string
}

// newFlexInferReranker constructs a FlexInferReranker from config.
func newFlexInferReranker(cfg RerankerConfig, logger *slog.Logger) *FlexInferReranker {
	model := cfg.Model
	if model == "" {
		model = "bge-reranker-v2-m3"
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "http://localhost:8080"
	}
	return &FlexInferReranker{
		baseURL:    base,
		apiKey:     cfg.APIKey,
		model:      model,
		httpClient: &http.Client{Timeout: timeout},
		timeout:    timeout,
		logger:     logger.With("component", "agentcontext-reranker", "backend", "flexinfer"),
		backendTag: string(RerankerKindFlexInfer),
	}
}

// Backend implements Reranker.
func (r *FlexInferReranker) Backend() string { return r.backendTag }

// Rerank implements Reranker. Never returns a non-nil error to the caller
// when the network path fails — instead it annotates entries with
// rerank_status and leaves ordering unchanged so recall degrades softly.
func (r *FlexInferReranker) Rerank(ctx context.Context, query string, entries []ContextEntry) ([]ContextEntry, error) {
	metrics := GetMetrics()
	metrics.RerankRequests.Add(1)

	if len(entries) == 0 || strings.TrimSpace(query) == "" {
		return entries, nil
	}

	start := time.Now()
	scores, err := r.callRerank(ctx, query, entries)
	elapsed := time.Since(start)
	metrics.RecordRerankLatency(r.backendTag, elapsed.Microseconds())

	if err != nil {
		status := classifyRerankError(err)
		switch status {
		case "timeout":
			metrics.RerankTimeouts.Add(1)
		default:
			metrics.RerankErrors.Add(1)
		}
		r.logger.Warn("flexinfer rerank failed; returning entries unchanged",
			"status", status, "error", err, "latency_us", elapsed.Microseconds())
		return annotateRerankStatus(cloneEntries(entries), status), nil
	}

	return applyRerankScores(entries, scores), nil
}

// callRerank posts to POST {base}/v1/rerank and returns the parsed scores.
func (r *FlexInferReranker) callRerank(ctx context.Context, query string, entries []ContextEntry) ([]flexInferRerankResult, error) {
	docs := make([]string, len(entries))
	for i, e := range entries {
		docs[i] = entryDocText(e)
	}

	body, err := json.Marshal(flexInferRerankRequest{
		Model:     r.model,
		Query:     query,
		Documents: docs,
		TopK:      len(entries),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal rerank request: %w", err)
	}

	url := r.baseURL + "/v1/rerank"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build rerank request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("rerank http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed flexInferRerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode rerank response: %w", err)
	}
	return parsed.Results, nil
}

// entryDocText returns the text representation of an entry that is passed
// to the cross-encoder. Title + content works for most entry types.
func entryDocText(e ContextEntry) string {
	if e.Title == "" {
		return e.Content
	}
	if e.Content == "" {
		return e.Title
	}
	return e.Title + "\n" + e.Content
}

// applyRerankScores reorders entries by descending relevance_score. Any
// indices omitted from the response are appended at the end preserving
// their original relative order so we never drop entries.
func applyRerankScores(entries []ContextEntry, scores []flexInferRerankResult) []ContextEntry {
	if len(scores) == 0 {
		return entries
	}

	// Filter scores whose index is in-range; downstream sort is stable.
	valid := make([]flexInferRerankResult, 0, len(scores))
	seen := make(map[int]bool, len(scores))
	for _, s := range scores {
		if s.Index < 0 || s.Index >= len(entries) || seen[s.Index] {
			continue
		}
		seen[s.Index] = true
		valid = append(valid, s)
	}

	sort.SliceStable(valid, func(i, j int) bool {
		return valid[i].RelevanceScore > valid[j].RelevanceScore
	})

	reordered := make([]ContextEntry, 0, len(entries))
	for _, s := range valid {
		reordered = append(reordered, entries[s.Index])
	}
	// Preserve entries the backend forgot about.
	for i := range entries {
		if !seen[i] {
			reordered = append(reordered, entries[i])
		}
	}
	return reordered
}

// classifyRerankError maps a transport error into a short rerank_status.
func classifyRerankError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if strings.Contains(err.Error(), "Client.Timeout") ||
		strings.Contains(err.Error(), "i/o timeout") ||
		strings.Contains(err.Error(), "context deadline exceeded") {
		return "timeout"
	}
	if strings.Contains(err.Error(), "http 404") {
		return "unavailable"
	}
	return "error"
}

// cloneEntries returns a shallow copy of entries so we can safely annotate
// Metadata without mutating caller state.
func cloneEntries(in []ContextEntry) []ContextEntry {
	out := make([]ContextEntry, len(in))
	copy(out, in)
	for i := range out {
		if out[i].Metadata == nil {
			continue
		}
		md := make(map[string]any, len(out[i].Metadata))
		for k, v := range out[i].Metadata {
			md[k] = v
		}
		out[i].Metadata = md
	}
	return out
}
