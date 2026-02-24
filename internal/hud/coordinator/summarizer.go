package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// SessionSummaryResult holds the output of a session summarization.
type SessionSummaryResult struct {
	SessionID    string   `json:"session_id"`
	Summary      string   `json:"summary"`
	KeyFindings  []string `json:"key_findings"`
	KeyDecisions []string `json:"key_decisions"`
	FilesTouched []string `json:"files_touched"`
	Unresolved   []string `json:"unresolved"`
	Tags         []string `json:"tags"`
}

// Summarizer handles LLM-powered session summarization.
type Summarizer struct {
	client *FlexInferClient
	agent  *bridge.AgentBridge
	config Config
	model  string // Resolved model from selectModel().
	logger *slog.Logger

	// summarized tracks session IDs that have already been summarized
	// in this process lifetime, preventing re-processing on each sweep.
	summarizedMu sync.Mutex
	summarized   map[string]struct{}
}

// NewSummarizer creates a Summarizer.
func NewSummarizer(client *FlexInferClient, agent *bridge.AgentBridge, cfg Config, logger *slog.Logger) *Summarizer {
	return &Summarizer{
		client:     client,
		agent:      agent,
		config:     cfg,
		logger:     logger.With("subsystem", "summarizer"),
		summarized: make(map[string]struct{}),
	}
}

// SummarizeSession fetches entries for a session and produces an LLM summary.
func (s *Summarizer) SummarizeSession(ctx context.Context, sessionID string) (*SessionSummaryResult, error) {
	entries, err := s.agent.SessionEntries(sessionID, 100)
	if err != nil {
		return nil, fmt.Errorf("fetch session entries: %w", err)
	}
	return s.summarizeFromEntries(ctx, sessionID, entries)
}

func (s *Summarizer) summarizeFromEntries(ctx context.Context, sessionID string, entries []bridge.ContextEntryInfo) (*SessionSummaryResult, error) {
	if len(entries) == 0 {
		return &SessionSummaryResult{
			SessionID: sessionID,
			Summary:   "Empty session — no entries recorded.",
		}, nil
	}

	// Format entries into a user message.
	userMsg := formatEntries(entries)

	model := s.model
	if model == "" {
		model = s.config.DefaultModel
	}
	raw, err := s.client.CompleteSimple(ctx, model, promptSessionSummarize, userMsg, s.config.SummarizerMaxTokens)
	if err != nil {
		// Fallback: extractive summary.
		return s.extractiveFallback(sessionID, entries), nil
	}

	// Parse the JSON response.
	result, err := parseSummaryResponse(raw)
	if err != nil {
		s.logger.Warn("failed to parse LLM summary, using extractive fallback", "error", err)
		return s.extractiveFallback(sessionID, entries), nil
	}
	result.SessionID = sessionID

	s.storeSummary(sessionID, result)

	return result, nil
}

// SweepEndedSessions finds ended sessions without summaries and summarizes
// up to maxSessions of them. This cap prevents storming the LLM backend
// when many sessions have accumulated.
func (s *Summarizer) SweepEndedSessions(ctx context.Context, maxSessions int) (int, error) {
	sessions, err := s.agent.Sessions()
	if err != nil {
		return 0, fmt.Errorf("list sessions: %w", err)
	}
	if maxSessions <= 0 {
		maxSessions = 2 // Safety default.
	}

	var count int
	for _, sess := range sessions {
		if count >= maxSessions {
			break
		}
		if sess.Status != "ended" {
			continue
		}

		// Skip sessions already summarized in this process lifetime.
		s.summarizedMu.Lock()
		_, alreadySummarized := s.summarized[sess.ID]
		s.summarizedMu.Unlock()
		if alreadySummarized {
			continue
		}

		// Fetch session entries once and reuse for both summary detection and
		// summarization input to avoid duplicate context-search calls.
		entries, err := s.agent.SessionEntries(sess.ID, 100)
		if err != nil {
			continue
		}

		// Skip empty sessions entirely — nothing useful to summarize and
		// storing an "empty session" summary creates a loop (the semantic
		// search in SessionEntries can't find it, so hasSummaryEntry stays
		// false forever).
		if len(entries) == 0 {
			continue
		}

		if hasSummaryEntry(entries) {
			continue
		}

		if _, err := s.summarizeFromEntries(ctx, sess.ID, entries); err != nil {
			s.logger.Debug("sweep summarize failed", "session_id", sess.ID, "error", err)
			continue
		}
		count++

		// Check context cancellation between sessions.
		if ctx.Err() != nil {
			return count, ctx.Err()
		}
	}
	return count, nil
}

// extractiveFallback produces a simple summary from entry titles.
func (s *Summarizer) extractiveFallback(sessionID string, entries []bridge.ContextEntryInfo) *SessionSummaryResult {
	var titles []string
	for _, e := range entries {
		if e.Entry.Title != "" {
			titles = append(titles, e.Entry.Title)
		}
	}

	summary := "Session contained " + fmt.Sprintf("%d", len(entries)) + " entries."
	if len(titles) > 0 {
		if len(titles) > 5 {
			titles = titles[:5]
		}
		summary += " Topics: " + strings.Join(titles, "; ")
	}

	return &SessionSummaryResult{
		SessionID: sessionID,
		Summary:   summary,
	}
}

// formatEntries converts context entries into a text block for LLM input.
func formatEntries(entries []bridge.ContextEntryInfo) string {
	var b strings.Builder
	for i, e := range entries {
		fmt.Fprintf(&b, "--- Entry %d ---\n", i+1)
		fmt.Fprintf(&b, "Type: %s\n", e.Entry.EntryType)
		if e.Entry.Title != "" {
			fmt.Fprintf(&b, "Title: %s\n", e.Entry.Title)
		}
		if e.Entry.Content != "" {
			content := e.Entry.Content
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			fmt.Fprintf(&b, "Content: %s\n", content)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// parseSummaryResponse parses the LLM JSON response into a SessionSummaryResult.
func parseSummaryResponse(raw string) (*SessionSummaryResult, error) {
	// Strip markdown code fences if present.
	raw = stripCodeFence(raw)

	var result SessionSummaryResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("parse summary JSON: %w", err)
	}
	if result.Summary == "" {
		return nil, fmt.Errorf("empty summary in response")
	}
	return &result, nil
}

// hasSummaryEntry checks if any entry in the list is a summary.
func hasSummaryEntry(entries []bridge.ContextEntryInfo) bool {
	for _, e := range entries {
		if e.Entry.EntryType == "summary" {
			return true
		}
		if strings.HasPrefix(e.Entry.Title, "Session Summary:") {
			return true
		}
	}
	return false
}

// stripCodeFence removes ```json ... ``` wrapping from LLM output.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}

func (s *Summarizer) storeSummary(sessionID string, result *SessionSummaryResult) {
	title := "Session Summary: " + sessionID
	content := summaryContent(result)

	if err := s.agent.ContextAdd(sessionID, []map[string]any{
		{
			"entry_type": "summary",
			"title":      title,
			"content":    content,
			"tags":       result.Tags,
		},
	}); err != nil {
		s.logger.Warn("failed to store session summary context entry", "session_id", sessionID, "error", err)
	}

	if err := s.agent.MemoryAdd(
		title,
		content,
		"long_term",
		"high",
		"summary",
	); err != nil {
		s.logger.Warn("failed to store session summary memory", "session_id", sessionID, "error", err)
	}

	// Mark session as summarized so the sweep loop skips it on future cycles.
	s.summarizedMu.Lock()
	s.summarized[sessionID] = struct{}{}
	s.summarizedMu.Unlock()
}

func summaryContent(result *SessionSummaryResult) string {
	if result == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString(result.Summary)

	if len(result.KeyFindings) > 0 {
		b.WriteString("\n\nKey findings:")
		for _, finding := range result.KeyFindings {
			b.WriteString("\n- ")
			b.WriteString(finding)
		}
	}

	if len(result.KeyDecisions) > 0 {
		b.WriteString("\n\nKey decisions:")
		for _, decision := range result.KeyDecisions {
			b.WriteString("\n- ")
			b.WriteString(decision)
		}
	}

	if len(result.Unresolved) > 0 {
		b.WriteString("\n\nUnresolved:")
		for _, item := range result.Unresolved {
			b.WriteString("\n- ")
			b.WriteString(item)
		}
	}

	if len(result.FilesTouched) > 0 {
		b.WriteString("\n\nFiles touched: ")
		b.WriteString(strings.Join(result.FilesTouched, ", "))
	}

	return b.String()
}
