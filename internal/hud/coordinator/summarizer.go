package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

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
	logger *slog.Logger
}

// NewSummarizer creates a Summarizer.
func NewSummarizer(client *FlexInferClient, agent *bridge.AgentBridge, cfg Config, logger *slog.Logger) *Summarizer {
	return &Summarizer{
		client: client,
		agent:  agent,
		config: cfg,
		logger: logger.With("subsystem", "summarizer"),
	}
}

// SummarizeSession fetches entries for a session and produces an LLM summary.
func (s *Summarizer) SummarizeSession(ctx context.Context, sessionID string) (*SessionSummaryResult, error) {
	entries, err := s.agent.SessionEntries(sessionID, 100)
	if err != nil {
		return nil, fmt.Errorf("fetch session entries: %w", err)
	}
	if len(entries) == 0 {
		return &SessionSummaryResult{
			SessionID: sessionID,
			Summary:   "Empty session — no entries recorded.",
		}, nil
	}

	// Format entries into a user message.
	userMsg := formatEntries(entries)

	model := s.config.DefaultModel
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

	// Store the summary as a context entry.
	if err := s.agent.MemoryAdd(
		"Session Summary: "+sessionID,
		result.Summary,
		"long_term",
		"high",
		"summary",
	); err != nil {
		s.logger.Warn("failed to store session summary", "error", err)
	}

	return result, nil
}

// SweepEndedSessions finds ended sessions without summaries and summarizes them.
func (s *Summarizer) SweepEndedSessions(ctx context.Context) (int, error) {
	sessions, err := s.agent.Sessions()
	if err != nil {
		return 0, fmt.Errorf("list sessions: %w", err)
	}

	var count int
	for _, sess := range sessions {
		if sess.Status != "ended" {
			continue
		}

		// Check if we already have a summary for this session.
		entries, err := s.agent.SessionEntries(sess.ID, 5)
		if err != nil {
			continue
		}
		if hasSummaryEntry(entries) {
			continue
		}

		if _, err := s.SummarizeSession(ctx, sess.ID); err != nil {
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
