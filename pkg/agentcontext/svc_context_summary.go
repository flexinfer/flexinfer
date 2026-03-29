package agentcontext

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// --- Summarization ---

func (cs *ContextSvc) Summarize(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := cs.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	if err := cs.GenerateSummary(ctx, session); err != nil {
		return mcp.ErrorResult(fmt.Errorf("generate summary: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"summarized": true,
	})
}

func (cs *ContextSvc) GenerateSummary(ctx context.Context, session *Session) error {
	entries, err := cs.qdrant.Get(CollContext).Scroll(ctx, FilterMust(
		Match("session_id", session.ID),
	), 1000)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		return nil
	}

	var findings []string
	var decisions []string
	filesSet := make(map[string]bool)

	for _, e := range entries {
		if e.FilePath != "" {
			filesSet[e.FilePath] = true
		}
		switch e.EntryType {
		case EntryTypeFinding:
			findings = append(findings, e.Title)
		case EntryTypeDecision:
			decisions = append(decisions, e.Title)
		}
	}

	var files []string
	for f := range filesSet {
		files = append(files, f)
	}

	var summaryParts []string
	summaryParts = append(summaryParts, fmt.Sprintf("Session: %s", session.ID))
	summaryParts = append(summaryParts, fmt.Sprintf("Agent: %s", session.AgentID))
	summaryParts = append(summaryParts, fmt.Sprintf("Entries: %d", len(entries)))

	if len(findings) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Key findings: %s", strings.Join(findings, "; ")))
	}
	if len(decisions) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Decisions: %s", strings.Join(decisions, "; ")))
	}
	if len(files) > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("Files: %s", strings.Join(files, ", ")))
	}

	summaryContent := strings.Join(summaryParts, "\n")

	titleSuffix := session.ID
	if len(titleSuffix) > 8 {
		titleSuffix = titleSuffix[:8]
	}
	summaryEntry := ContextEntry{
		ID:            GenerateID(session.AgentID, session.ID, "summary", time.Now()),
		SchemaVersion: SchemaVersion,
		AgentID:       session.AgentID,
		SessionID:     session.ID,
		Namespace:     session.Namespace,
		EntryType:     EntryTypeSummary,
		Timestamp:     time.Now(),
		Title:         fmt.Sprintf("Session Summary: %s", titleSuffix),
		Content:       summaryContent,
		ContentHash:   ContentHashFunc(summaryContent),
		TokenCount:    EstimateTokens(summaryContent),
		Visibility:    cs.cfg.DefaultVisibility,
		Metadata: map[string]any{
			"key_findings":  findings,
			"key_decisions": decisions,
			"files_touched": files,
			"entry_count":   len(entries),
		},
	}

	cs.metrics.EmbeddingRequests.Add(1)
	vector, err := cs.embed.EmbedQuery(ctx, summaryEntry.Title+" "+summaryEntry.Content)
	if err != nil {
		cs.metrics.EmbeddingErrors.Add(1)
		return err
	}
	if len(vector) > 0 {
		*cs.vectorSize = len(vector)
	}
	if *cs.vectorSize <= 0 {
		return fmt.Errorf("unknown vector size")
	}
	if err := cs.qdrant.Get(CollContext).EnsureCollection(ctx, *cs.vectorSize); err != nil {
		return err
	}

	point := Point{
		ID:      summaryEntry.ID,
		Vector:  vector,
		Payload: EntryToPayload(summaryEntry, cs.cfg.EmbedModel),
	}

	if err := cs.qdrant.Get(CollContext).Upsert(ctx, []Point{point}, true); err != nil {
		return err
	}

	// Update session entry stats for the summary entry.
	if cs.addSessionEntryStats != nil {
		cs.addSessionEntryStats(session, 1, summaryEntry.TokenCount)
	}

	// Update session timestamp via callback
	if cs.markSessionSummarized != nil {
		cs.markSessionSummarized(session, time.Now())
	}

	return nil
}

func (cs *ContextSvc) maybeAutoSummarize(ctx context.Context, session *Session) {
	if cs.readSessionStats == nil {
		return
	}

	entryCount, totalTokens, lastSummary := cs.readSessionStats(session)

	shouldSummarize := false

	if entryCount >= cs.cfg.SummarizeEntryThreshold {
		if lastSummary == nil || entryCount >= cs.cfg.SummarizeEntryThreshold {
			shouldSummarize = true
		}
	}

	if totalTokens >= cs.cfg.SummarizeTokenThreshold {
		shouldSummarize = true
	}

	if lastSummary != nil {
		minutesSince := int(time.Since(*lastSummary).Minutes())
		if minutesSince >= cs.cfg.SummarizeMinuteThreshold && entryCount > 10 {
			shouldSummarize = true
		}
	}

	if shouldSummarize {
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if err := cs.GenerateSummary(bg, session); err != nil {
				cs.logger.Warn("auto-summarize failed",
					"session_id", session.ID,
					"error", err,
				)
			}
		}()
	}
}

// --- Stats ---

func (cs *ContextSvc) Stats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.String("agent_id", "")
	sessionID := v.String("session_id", "")
	namespace := v.String("namespace", "")

	var conds []any
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}
	if sessionID != "" {
		conds = append(conds, Match("session_id", sessionID))
	}
	if namespace != "" {
		conds = append(conds, Match("namespace", namespace))
	}

	var filter map[string]any
	if len(conds) > 0 {
		filter = FilterMust(conds...)
	}

	count, err := cs.qdrant.Get(CollContext).Count(ctx, filter)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("count: %w", err)), nil
	}

	entries, _ := cs.qdrant.Get(CollContext).Scroll(ctx, filter, 1000)
	totalTokens := 0
	byType := make(map[string]int)
	for _, e := range entries {
		totalTokens += e.TokenCount
		byType[string(e.EntryType)]++
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"entry_count":  count,
		"total_tokens": totalTokens,
		"by_type":      byType,
		"metrics":      cs.metrics.Snapshot(),
	})
}
