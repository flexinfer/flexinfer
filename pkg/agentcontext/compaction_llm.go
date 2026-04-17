package agentcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// =========================================================================
// LLM-backed auto-compaction (F2)
//
// This file introduces a CompactionSummarizer interface used by the
// compaction scheduler when CompactionConfig.Mode == "llm". The default
// extractive path continues to work unchanged; LLM synthesis is opt-in and
// degrades gracefully to extractive compaction on error.
// =========================================================================

// CompactionSummarizer synthesizes a condensed summary from a batch of raw
// context entries. Implementations should be side-effect free with respect to
// the underlying memory hierarchy.
type CompactionSummarizer interface {
	Summarize(ctx context.Context, entries []ContextEntry) (string, error)
}

// coordinatorSummarizer is a thin HTTP adapter that POSTs to the coordinator
// domain's compress endpoint and expects a JSON body of the shape:
//
//	{"summary": "..."}
//
// Contract is intentionally minimal; see follow-up note in the commit message
// about wiring the exact coordinator schema once its request/response shape is
// finalized (internal/hud/domain/coordinator/coordinator.go:26).
type coordinatorSummarizer struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewCoordinatorSummarizer returns a CompactionSummarizer backed by the
// coordinator HTTP endpoint. If client is nil, a defensive default with a
// short timeout is used.
func NewCoordinatorSummarizer(baseURL, token string, client *http.Client) CompactionSummarizer {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &coordinatorSummarizer{
		baseURL: baseURL,
		token:   token,
		client:  client,
	}
}

// Summarize POSTs the raw entries to the coordinator and returns the summary.
func (c *coordinatorSummarizer) Summarize(ctx context.Context, entries []ContextEntry) (string, error) {
	if c.baseURL == "" {
		return "", fmt.Errorf("coordinator summarizer: empty base URL")
	}

	// Keep the outbound payload small and stable.
	type outEntry struct {
		ID        string    `json:"id"`
		EntryType EntryType `json:"entry_type,omitempty"`
		Title     string    `json:"title,omitempty"`
		Content   string    `json:"content"`
		Timestamp time.Time `json:"timestamp,omitempty"`
	}
	outEntries := make([]outEntry, 0, len(entries))
	for _, e := range entries {
		outEntries = append(outEntries, outEntry{
			ID:        e.ID,
			EntryType: e.EntryType,
			Title:     e.Title,
			Content:   e.Content,
			Timestamp: e.Timestamp,
		})
	}

	body, err := json.Marshal(struct {
		Entries []outEntry `json:"entries"`
	}{Entries: outEntries})
	if err != nil {
		return "", fmt.Errorf("coordinator summarizer: marshal: %w", err)
	}

	url := c.baseURL + "/api/coordinator/compress"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("coordinator summarizer: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("coordinator summarizer: do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("coordinator summarizer: status %d: %s", resp.StatusCode, string(snippet))
	}

	var parsed struct {
		Summary string `json:"summary"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("coordinator summarizer: decode: %w", err)
	}
	if parsed.Summary == "" {
		return "", fmt.Errorf("coordinator summarizer: empty summary")
	}
	return parsed.Summary, nil
}
