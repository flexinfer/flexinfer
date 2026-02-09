package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// TriageResult holds the classification for a single entry.
type TriageResult struct {
	EntryID     string   `json:"entry_id"`
	Importance  string   `json:"importance"`
	Categories  []string `json:"categories"`
	DuplicateOf string   `json:"duplicate_of,omitempty"`
}

// TriageBatchResult holds the results of a batch triage.
type TriageBatchResult struct {
	Count    int `json:"count"`
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
}

// Triager handles LLM-powered context entry classification.
type Triager struct {
	client *FlexInferClient
	agent  *bridge.AgentBridge
	config Config
	logger *slog.Logger
}

// NewTriager creates a Triager.
func NewTriager(client *FlexInferClient, agent *bridge.AgentBridge, cfg Config, logger *slog.Logger) *Triager {
	return &Triager{
		client: client,
		agent:  agent,
		config: cfg,
		logger: logger.With("subsystem", "triager"),
	}
}

// TriageEntries classifies a batch of context entries.
func (t *Triager) TriageEntries(ctx context.Context, entries []bridge.ContextEntryInfo) ([]TriageResult, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	userMsg := formatEntries(entries) // Reuse from summarizer.

	raw, err := t.client.CompleteSimple(ctx, t.config.DefaultModel, promptTriageEntries, userMsg, 400)
	if err != nil {
		// Fallback: mark all as medium.
		return t.fallbackTriage(entries), nil
	}

	raw = stripCodeFence(raw)
	var result struct {
		Results []TriageResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.logger.Debug("triage parse failed, using fallback", "error", err)
		return t.fallbackTriage(entries), nil
	}

	return result.Results, nil
}

// TriageRecent fetches recent un-triaged entries and classifies them.
func (t *Triager) TriageRecent(ctx context.Context) (*TriageBatchResult, error) {
	// Fetch recent entries (context stream).
	entries, err := t.agent.MemoryRecall("working", "", t.config.TriagerBatchSize)
	if err != nil {
		return nil, fmt.Errorf("recall working memory: %w", err)
	}
	if len(entries) == 0 {
		return nil, nil
	}

	// Convert MemoryItems to ContextEntryInfo for the triager.
	contextEntries := memoryToContextEntries(entries)

	results, err := t.TriageEntries(ctx, contextEntries)
	if err != nil {
		return nil, err
	}

	// Aggregate stats.
	batch := &TriageBatchResult{Count: len(results)}
	for _, r := range results {
		switch r.Importance {
		case "critical":
			batch.Critical++
		case "high":
			batch.High++
		case "medium":
			batch.Medium++
		case "low":
			batch.Low++
		}
	}

	return batch, nil
}

// fallbackTriage returns default "medium" importance for all entries.
func (t *Triager) fallbackTriage(entries []bridge.ContextEntryInfo) []TriageResult {
	results := make([]TriageResult, len(entries))
	for i, e := range entries {
		results[i] = TriageResult{
			EntryID:    e.Entry.ID,
			Importance: "medium",
		}
	}
	return results
}

// memoryToContextEntries converts MemoryItems to ContextEntryInfo for reuse.
func memoryToContextEntries(items []bridge.MemoryItem) []bridge.ContextEntryInfo {
	entries := make([]bridge.ContextEntryInfo, len(items))
	for i, item := range items {
		entries[i] = bridge.ContextEntryInfo{
			Entry: bridge.ContextEntry{
				ID:        item.ID,
				EntryType: item.Category,
				Title:     item.Title,
				Content:   item.Content,
			},
		}
	}
	return entries
}
