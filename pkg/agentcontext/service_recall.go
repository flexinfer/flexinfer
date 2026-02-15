package agentcontext

import (
	"context"
	"fmt"
	"sort"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

func (s *Service) HandleEnhancedRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	agentID := v.String("agent_id", "")
	sessionID := v.String("session_id", "")
	namespace := v.String("namespace", "")
	tokenBudget := v.Int("token_budget", s.cfg.DefaultTokenBudget)
	includeSummaries := v.Bool("include_summaries", true)
	includeDecisions := v.Bool("include_decisions", true)
	fileContext := v.String("file_context", "")
	symbolContext := v.String("symbol_context", "")
	recencyWeight := v.Float("recency_weight", s.cfg.DefaultRecencyWeight)
	includeTasks := v.Bool("include_tasks", true)
	crossAgent := v.Bool("cross_agent", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	opts := EnhancedRecallOptions{
		RecallOptions: RecallOptions{
			Query:            query,
			AgentID:          agentID,
			SessionID:        sessionID,
			Namespace:        namespace,
			TokenBudget:      tokenBudget,
			IncludeSummaries: includeSummaries,
			IncludeDecisions: includeDecisions,
			FileContext:      fileContext,
		},
		SymbolContext: symbolContext,
		RecencyWeight: recencyWeight,
		IncludeTasks:  includeTasks,
		CrossAgent:    crossAgent,
	}

	entries, err := s.enhancedRecallContext(ctx, opts)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("recall: %w", err)), nil
	}

	s.metrics.RecallRequests.Add(1)
	if len(entries) > 0 {
		s.metrics.RecallHits.Add(1)
	} else {
		s.metrics.RecallMisses.Add(1)
	}

	totalTokens := 0
	for _, e := range entries {
		totalTokens += e.TokenCount
	}

	if totalTokens >= opts.TokenBudget {
		s.metrics.RecallTruncated.Add(1)
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"entries":      entries,
		"count":        len(entries),
		"total_tokens": totalTokens,
		"token_budget": opts.TokenBudget,
	})
}

func (s *Service) enhancedRecallContext(ctx context.Context, opts EnhancedRecallOptions) ([]ContextEntry, error) {
	var results []ContextEntry
	seen := make(map[string]bool)
	remainingBudget := opts.TokenBudget

	// When cross_agent is true, clear agent/session filters so all entries
	// across all sessions are searched. Individual entries still carry their
	// source agent_id and session_id for attribution.
	agentID := opts.AgentID
	sessionID := opts.SessionID
	if opts.CrossAgent {
		agentID = ""
		sessionID = ""
	}

	// Phase 1 (NEW): Active tasks - highest priority
	if opts.IncludeTasks && remainingBudget > 0 {
		tasks, _ := s.getActiveTasks(ctx, agentID, sessionID, 5)
		for _, task := range tasks {
			// Convert task to context entry for unified return type
			entry := ContextEntry{
				ID:         task.ID,
				AgentID:    task.AgentID,
				SessionID:  task.SessionID,
				EntryType:  EntryTypeTask,
				Title:      fmt.Sprintf("[%s] %s", task.Priority, task.Title),
				Content:    task.Context,
				FilePath:   task.FilePath,
				Timestamp:  task.CreatedAt,
				Tags:       task.Tags,
				TokenCount: task.TokenCount,
				Metadata: map[string]any{
					"task_status":   string(task.Status),
					"task_priority": string(task.Priority),
					"blocked_by":    task.BlockedBy,
				},
			}
			if remainingBudget >= entry.TokenCount && !seen[entry.ID] {
				results = append(results, entry)
				seen[entry.ID] = true
				remainingBudget -= entry.TokenCount
			}
		}
	}

	// Phase 2: Recent decisions
	if opts.IncludeDecisions && remainingBudget > 0 {
		decisions, _ := s.getRecentByType(ctx, agentID, sessionID, EntryTypeDecision, 5)
		for _, d := range decisions {
			if remainingBudget >= d.TokenCount && !seen[d.ID] {
				results = append(results, d)
				seen[d.ID] = true
				remainingBudget -= d.TokenCount
			}
		}
	}

	// Phase 3: Session summaries
	if opts.IncludeSummaries && remainingBudget > 0 {
		summaries, _ := s.getRecentByType(ctx, agentID, sessionID, EntryTypeSummary, 3)
		for _, sum := range summaries {
			if remainingBudget >= sum.TokenCount && !seen[sum.ID] {
				results = append(results, sum)
				seen[sum.ID] = true
				remainingBudget -= sum.TokenCount
			}
		}
	}

	// Phase 4 (NEW): Symbol-context boosting
	if opts.SymbolContext != "" && remainingBudget > 500 {
		symbolEntries, _ := s.getEntriesForSymbol(ctx, agentID, opts.SymbolContext, 5)
		for _, se := range symbolEntries {
			if remainingBudget >= se.TokenCount && !seen[se.ID] {
				results = append(results, se)
				seen[se.ID] = true
				remainingBudget -= se.TokenCount
			}
		}
	}

	// Phase 5 (ENHANCED): Semantic search with recency weighting
	if remainingBudget > 500 && opts.Query != "" {
		s.metrics.EmbeddingRequests.Add(1)
		vector, err := s.embed.EmbedQuery(ctx, opts.Query)
		if err != nil {
			s.metrics.EmbeddingErrors.Add(1)
		}
		if err == nil {
			var conds []any
			if agentID != "" {
				conds = append(conds, Match("agent_id", agentID))
			}
			if sessionID != "" {
				conds = append(conds, Match("session_id", sessionID))
			}

			var filter map[string]any
			if len(conds) > 0 {
				filter = FilterMust(conds...)
			}

			searchResults, _ := s.contextQdrant.Search(ctx, vector, filter, 30, true)

			// Apply recency weighting
			now := time.Now()
			for i := range searchResults {
				age := now.Sub(searchResults[i].Entry.Timestamp)
				ageHours := age.Hours()
				// Decay factor: recent entries get boosted
				recencyBoost := 1.0 / (1.0 + (ageHours / 24.0 * opts.RecencyWeight))
				searchResults[i].Score *= (1.0 + recencyBoost*opts.RecencyWeight)
			}

			// Re-sort by adjusted score
			sort.Slice(searchResults, func(i, j int) bool {
				return searchResults[i].Score > searchResults[j].Score
			})

			for _, sr := range searchResults {
				if remainingBudget >= sr.Entry.TokenCount && !seen[sr.Entry.ID] {
					results = append(results, sr.Entry)
					seen[sr.Entry.ID] = true
					remainingBudget -= sr.Entry.TokenCount
				}
			}
		}
	}

	// Phase 6: File-context boosting
	if opts.FileContext != "" && remainingBudget > 200 {
		fileEntries, _ := s.getEntriesForFile(ctx, agentID, opts.FileContext, 5)
		for _, fe := range fileEntries {
			if remainingBudget >= fe.TokenCount && !seen[fe.ID] {
				results = append(results, fe)
				seen[fe.ID] = true
				remainingBudget -= fe.TokenCount
			}
		}
	}

	// Phase 7 (NEW): Code annotations for current file
	if opts.FileContext != "" && remainingBudget > 100 {
		annotations, _ := s.getAnnotationsForFile(ctx, agentID, opts.FileContext, 5)
		for _, ann := range annotations {
			entry := ContextEntry{
				ID:         ann.ID,
				AgentID:    ann.AgentID,
				SessionID:  ann.SessionID,
				EntryType:  EntryTypeAnnotation,
				Title:      fmt.Sprintf("[%s] %s:%d", ann.AnnotationType, ann.FilePath, ann.LineStart),
				Content:    ann.Content,
				FilePath:   ann.FilePath,
				LineStart:  ann.LineStart,
				LineEnd:    ann.LineEnd,
				Timestamp:  ann.CreatedAt,
				TokenCount: ann.TokenCount,
			}
			if remainingBudget >= entry.TokenCount && !seen[entry.ID] {
				results = append(results, entry)
				seen[entry.ID] = true
				remainingBudget -= entry.TokenCount
			}
		}
	}

	return results, nil
}

func (s *Service) getEntriesForSymbol(ctx context.Context, agentID, symbol string, limit int) ([]ContextEntry, error) {
	// Search for entries mentioning the symbol
	vector, err := s.embed.EmbedQuery(ctx, symbol)
	if err != nil {
		return nil, err
	}

	var filter map[string]any
	if agentID != "" {
		filter = FilterMust(Match("agent_id", agentID))
	}

	results, err := s.contextQdrant.Search(ctx, vector, filter, limit, true)
	if err != nil {
		return nil, err
	}

	entries := make([]ContextEntry, 0, len(results))
	for _, r := range results {
		entries = append(entries, r.Entry)
	}
	return entries, nil
}
