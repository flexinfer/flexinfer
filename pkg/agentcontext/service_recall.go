package agentcontext

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// scopeIncludes returns true if the given source is within the recall scope.
// An empty scope means all sources are included.
func scopeIncludes(scope RecallScope, src RecallSource) bool {
	if len(scope) == 0 {
		return true
	}
	for _, s := range scope {
		if s == src {
			return true
		}
	}
	return false
}

// enhancedRecallContext performs unified recall across context, memory, and graph backends.
// Returns entries with source attribution and per-source result counts.
func (s *Service) enhancedRecallContext(ctx context.Context, opts EnhancedRecallOptions) ([]ContextEntry, map[string]int, error) {
	var results []ContextEntry
	seen := make(map[string]bool)
	remainingBudget := opts.TokenBudget
	sourceCounts := map[string]int{
		string(RecallSourceContext): 0,
		string(RecallSourceMemory):  0,
		string(RecallSourceGraph):   0,
	}

	includeContext := scopeIncludes(opts.Scope, RecallSourceContext)
	includeMemory := opts.IncludeMemory && scopeIncludes(opts.Scope, RecallSourceMemory)
	includeGraph := opts.IncludeGraph && scopeIncludes(opts.Scope, RecallSourceGraph)

	// When cross_agent is true, clear agent/session filters so all entries
	// across all sessions are searched.
	agentID := opts.AgentID
	sessionID := opts.SessionID
	if opts.CrossAgent {
		agentID = ""
		sessionID = ""
	}

	// Helper: tag entry with source attribution and add to results.
	addEntry := func(entry ContextEntry, src RecallSource) bool {
		if entry.ID != "" && seen[entry.ID] {
			return false
		}
		if remainingBudget < entry.TokenCount {
			return false
		}
		if entry.Metadata == nil {
			entry.Metadata = make(map[string]any)
		}
		entry.Metadata["recall_source"] = string(src)
		results = append(results, entry)
		if entry.ID != "" {
			seen[entry.ID] = true
		}
		remainingBudget -= entry.TokenCount
		sourceCounts[string(src)]++
		return true
	}

	// --- Context backend phases (sequential, cheap filter queries) ---

	if includeContext {
		// Phase 1: Active tasks
		if opts.IncludeTasks && remainingBudget > 0 {
			tasks, _ := s.getActiveTasks(ctx, agentID, sessionID, 5)
			for _, task := range tasks {
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
				addEntry(entry, RecallSourceContext)
			}
		}

		// Phase 2: Recent decisions
		if opts.IncludeDecisions && remainingBudget > 0 {
			decisions, _ := s.ctxSvc.getRecentByType(ctx, agentID, sessionID, EntryTypeDecision, 5)
			for _, d := range decisions {
				addEntry(d, RecallSourceContext)
			}
		}

		// Phase 3: Session summaries
		if opts.IncludeSummaries && remainingBudget > 0 {
			summaries, _ := s.ctxSvc.getRecentByType(ctx, agentID, sessionID, EntryTypeSummary, 3)
			for _, sum := range summaries {
				addEntry(sum, RecallSourceContext)
			}
		}

		// Phase 4: Symbol-context boosting
		if opts.SymbolContext != "" && remainingBudget > 500 {
			symbolEntries, _ := s.getEntriesForSymbol(ctx, agentID, opts.SymbolContext, 5)
			for _, se := range symbolEntries {
				addEntry(se, RecallSourceContext)
			}
		}
	}

	// --- Parallel semantic searches across all enabled backends ---

	if remainingBudget > 500 && opts.Query != "" {
		s.metrics.EmbeddingRequests.Add(1)
		vector, err := s.embed.EmbedQuery(ctx, opts.Query)
		if err != nil {
			s.metrics.EmbeddingErrors.Add(1)
		}
		if err == nil {
			type backendResult struct {
				entries []ContextEntry
				source  RecallSource
			}

			var wg sync.WaitGroup
			ch := make(chan backendResult, 3)

			// Context backend: semantic search with recency weighting
			if includeContext {
				wg.Add(1)
				go func() {
					defer wg.Done()
					entries := s.contextSemanticSearch(ctx, vector, agentID, sessionID, opts.RecencyWeight)
					ch <- backendResult{entries: entries, source: RecallSourceContext}
				}()
			}

			// Memory backend: memory hierarchy recall
			if includeMemory && s.memoryHierarchy != nil {
				wg.Add(1)
				go func() {
					defer wg.Done()
					entries := s.memoryRecallToEntries(opts, agentID, sessionID)
					ch <- backendResult{entries: entries, source: RecallSourceMemory}
				}()
			}

			// Graph backend: knowledge graph entity search
			if includeGraph && s.persistedKnowledgeGraph != nil {
				wg.Add(1)
				go func() {
					defer wg.Done()
					entries := s.graphSearchToEntries(ctx, vector, opts.Namespace)
					ch <- backendResult{entries: entries, source: RecallSourceGraph}
				}()
			}

			go func() {
				wg.Wait()
				close(ch)
			}()

			// Collect all backend results, interleave by source for fairness.
			var allBackendResults []backendResult
			for br := range ch {
				allBackendResults = append(allBackendResults, br)
			}

			// Merge: round-robin across backends to ensure each gets representation.
			maxLen := 0
			for _, br := range allBackendResults {
				if len(br.entries) > maxLen {
					maxLen = len(br.entries)
				}
			}
			for i := 0; i < maxLen && remainingBudget > 0; i++ {
				for _, br := range allBackendResults {
					if i < len(br.entries) {
						addEntry(br.entries[i], br.source)
					}
				}
			}
		}
	}

	// --- Context backend: file-context and annotation boosting ---

	if includeContext {
		// Phase 6: File-context boosting
		if opts.FileContext != "" && remainingBudget > 200 {
			fileEntries, _ := s.ctxSvc.getEntriesForFile(ctx, agentID, opts.FileContext, 5)
			for _, fe := range fileEntries {
				addEntry(fe, RecallSourceContext)
			}
		}

		// Phase 7: Code annotations for current file
		if opts.FileContext != "" && remainingBudget > 100 {
			annotations, _ := s.ctxSvc.GetAnnotationsForFile(ctx, agentID, opts.FileContext, 5)
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
				addEntry(entry, RecallSourceContext)
			}
		}
	}

	// Re-sort all results by priority score for better token budget utilization.
	// This ensures high-value entries (decisions, active tasks) surface first
	// regardless of which phase collected them.
	results = prioritySortEntries(results)

	// Re-apply budget constraint after reordering: entries may have been
	// collected within budget but reordering can push lower-priority items
	// past the limit when higher-priority items are promoted.
	results = trimToBudget(results, opts.TokenBudget)

	return results, sourceCounts, nil
}

// recallPriorityScore returns a priority score for a recall entry.
// Higher scores surface first. The score combines an entry-type weight
// with a recency boost.
func recallPriorityScore(entry ContextEntry) float64 {
	// Entry-type base weight
	weight := entryTypePriorityWeight(entry.EntryType)

	// Recency boost: last hour 1.5x, last 24h 1.2x, older 1.0x
	age := time.Since(entry.Timestamp)
	switch {
	case age <= 1*time.Hour:
		weight *= 1.5
	case age <= 24*time.Hour:
		weight *= 1.2
	}

	return weight
}

// entryTypePriorityWeight returns the base priority weight for an entry type.
func entryTypePriorityWeight(et EntryType) float64 {
	switch et {
	case EntryTypeDecision:
		return 1.0
	case EntryTypeTask:
		return 0.9
	case EntryTypeSummary:
		return 0.85
	case EntryTypeAnnotation:
		return 0.7
	case EntryTypeFinding:
		return 0.6
	case "entity":
		return 0.5
	case EntryTypeFileRead:
		return 0.3
	default:
		return 0.4
	}
}

// prioritySortEntries sorts entries by descending priority score.
func prioritySortEntries(entries []ContextEntry) []ContextEntry {
	scores := make([]float64, len(entries))
	for i := range entries {
		scores[i] = recallPriorityScore(entries[i])
	}
	sort.Slice(entries, func(i, j int) bool {
		return scores[i] > scores[j]
	})
	return entries
}

// trimToBudget removes trailing entries that exceed the token budget.
func trimToBudget(entries []ContextEntry, budget int) []ContextEntry {
	used := 0
	for i, e := range entries {
		used += e.TokenCount
		if used > budget && budget > 0 {
			return entries[:i]
		}
	}
	return entries
}

// contextSemanticSearch runs the context-backend vector search with recency weighting.
func (s *Service) contextSemanticSearch(ctx context.Context, vector []float64, agentID, sessionID string, recencyWeight float64) []ContextEntry {
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

	searchResults, _ := s.qdrant.Get(CollContext).Search(ctx, vector, filter, 30, true)

	now := time.Now()
	for i := range searchResults {
		age := now.Sub(searchResults[i].Entry.Timestamp)
		ageHours := age.Hours()
		recencyBoost := 1.0 / (1.0 + (ageHours / 24.0 * recencyWeight))
		searchResults[i].Score *= (1.0 + recencyBoost*recencyWeight)
	}
	sort.Slice(searchResults, func(i, j int) bool {
		return searchResults[i].Score > searchResults[j].Score
	})

	entries := make([]ContextEntry, 0, len(searchResults))
	for _, sr := range searchResults {
		entries = append(entries, sr.Entry)
	}
	return entries
}

// allocateBudget distributes a total token budget across enabled backends.
// Context gets 50%, memory gets 30%, graph gets 20%. If a backend is
// disabled, its share is redistributed equally to the remaining backends.
func allocateBudget(total int, contextEnabled, memoryEnabled, graphEnabled bool) (contextBudget, memoryBudget, graphBudget int) {
	type share struct {
		enabled bool
		pct     float64
		out     *int
	}
	shares := []share{
		{contextEnabled, 0.50, &contextBudget},
		{memoryEnabled, 0.30, &memoryBudget},
		{graphEnabled, 0.20, &graphBudget},
	}

	enabledCount := 0
	disabledPct := 0.0
	for _, s := range shares {
		if s.enabled {
			enabledCount++
		} else {
			disabledPct += s.pct
		}
	}
	if enabledCount == 0 {
		return 0, 0, 0
	}

	redistribution := disabledPct / float64(enabledCount)
	for _, s := range shares {
		if s.enabled {
			*s.out = int(float64(total) * (s.pct + redistribution))
		}
	}
	return
}

// memoryRecallToEntries converts memory hierarchy recall results to ContextEntry.
func (s *Service) memoryRecallToEntries(opts EnhancedRecallOptions, agentID, sessionID string) []ContextEntry {
	if s.memoryHierarchy == nil {
		return nil
	}

	_, memBudget, _ := allocateBudget(
		opts.TokenBudget,
		true, // context is always enabled when we reach this path
		true, // memory is enabled (we are in memoryRecallToEntries)
		opts.IncludeGraph,
	)
	if memBudget < 256 {
		memBudget = 256
	}

	req := MemoryRecallRequest{
		Query:       opts.Query,
		Namespace:   opts.Namespace,
		SessionID:   sessionID,
		AgentID:     agentID,
		TokenBudget: memBudget,
		Limit:       10,
	}

	result, err := s.memoryHierarchy.Recall(req)
	if err != nil || result == nil {
		return nil
	}

	entries := make([]ContextEntry, 0, len(result.Items))
	for _, item := range result.Items {
		content := item.Content
		if item.Summary != "" {
			content = item.Summary
		}
		entry := ContextEntry{
			ID:         item.ID,
			AgentID:    item.AgentID,
			SessionID:  item.SessionID,
			EntryType:  EntryType(item.Category),
			Title:      fmt.Sprintf("[memory:%s] %s", item.Tier, item.Title),
			Content:    content,
			Namespace:  item.Namespace,
			Timestamp:  item.CreatedAt,
			Tags:       item.Tags,
			TokenCount: item.OriginalTokens,
			Metadata: map[string]any{
				"memory_tier":      string(item.Tier),
				"importance":       string(item.Importance),
				"importance_score": item.ImportanceScore,
			},
		}
		if entry.TokenCount == 0 {
			entry.TokenCount = item.CompressedTokens
		}
		entries = append(entries, entry)
	}
	return entries
}

// graphSearchToEntries converts knowledge graph entity search results to ContextEntry.
func (s *Service) graphSearchToEntries(ctx context.Context, vector []float64, namespace string) []ContextEntry {
	if s.persistedKnowledgeGraph == nil {
		return nil
	}
	entities, err := s.persistedKnowledgeGraph.SearchEntitiesSemantic(ctx, vector, 10, "", namespace)
	if err != nil {
		return nil
	}

	entries := make([]ContextEntry, 0, len(entities))
	for _, entity := range entities {
		content := entity.Description
		if entity.Signature != "" {
			content = entity.Signature + "\n" + content
		}

		// Estimate tokens from content length (~4 chars per token).
		tokenEst := len(content) / 4
		if tokenEst < 10 {
			tokenEst = 10
		}

		entry := ContextEntry{
			ID:         entity.ID,
			AgentID:    entity.AgentID,
			SessionID:  entity.SessionID,
			EntryType:  "entity",
			Title:      fmt.Sprintf("[%s] %s", entity.Type, entity.Name),
			Content:    content,
			FilePath:   entity.FilePath,
			LineStart:  entity.LineStart,
			LineEnd:    entity.LineEnd,
			Namespace:  entity.Namespace,
			Timestamp:  entity.CreatedAt,
			TokenCount: tokenEst,
			Metadata: map[string]any{
				"entity_type": string(entity.Type),
				"language":    entity.Language,
			},
		}
		entries = append(entries, entry)
	}
	return entries
}

// HandleUnifiedRecall is the single recall entry point that queries both context
// and memory backends. It supersedes HandleContextRecall, HandleEnhancedRecall,
// and HandleMemoryRecall. The "scope" parameter controls which backends are queried:
// "context" (default), "memory", or "all".
func (s *Service) HandleUnifiedRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
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
	scope := v.String("scope", "context")
	includeMemoryEntries := v.Bool("include_memory", false)
	includeGraphEntries := v.Bool("include_graph", false)
	// Memory-specific filters (used when scope includes memory).
	memoryTiers := v.StringSlice("memory_tiers")
	memoryCategories := v.StringSlice("memory_categories")
	memoryTags := v.StringSlice("memory_tags")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	includeContext := scope == "context" || scope == "all"
	includeMemory := scope == "memory" || scope == "all"
	includeGraph := scope == "graph" || scope == "all"

	resp := map[string]any{
		"ok":           true,
		"token_budget": tokenBudget,
		"scope":        scope,
	}

	totalTokens := 0
	totalCount := 0

	// --- Context backend ---
	if includeContext {
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
			IncludeMemory: includeMemoryEntries,
			IncludeGraph:  includeGraphEntries,
		}

		entries, _, err := s.enhancedRecallContext(ctx, opts)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("recall context: %w", err)), nil
		}

		for _, e := range entries {
			totalTokens += e.TokenCount
		}
		totalCount += len(entries)
		resp["entries"] = entries
		resp["context_count"] = len(entries)
	}

	// --- Memory backend ---
	// When context recall already merged memory entries (legacy enhanced path),
	// avoid emitting a second memory-only section.
	mergeMemoryIntoEntries := includeContext && includeMemoryEntries
	if includeMemory && !mergeMemoryIntoEntries {
		memBudget := tokenBudget - totalTokens
		if memBudget < 256 {
			memBudget = 256
		}

		req := MemoryRecallRequest{
			Query:       query,
			Namespace:   namespace,
			SessionID:   sessionID,
			AgentID:     agentID,
			TokenBudget: memBudget,
			Limit:       50,
			Categories:  memoryCategories,
			Tags:        memoryTags,
		}
		for _, t := range memoryTiers {
			if t != "" {
				req.Tiers = append(req.Tiers, MemoryTier(t))
			}
		}

		result, err := s.memoryHierarchy.Recall(req)
		if err == nil && len(result.Items) > 0 {
			items := make([]map[string]any, len(result.Items))
			for i, item := range result.Items {
				items[i] = memoryItemToMap(&item)
			}
			totalTokens += result.TotalTokens
			totalCount += len(items)
			resp["memory_items"] = items
			resp["memory_count"] = len(items)
		}
	}

	// --- Graph backend ---
	// When scope is "graph" or "all", query the knowledge graph for matching entities.
	// Uses FindEntities with the query text as a name pattern for text-based matching.
	if includeGraph && s.knowledgeGraph != nil {
		entities := s.knowledgeGraph.FindEntities("", namespace, query, 20)
		if len(entities) > 0 {
			graphEntities := make([]map[string]any, len(entities))
			for i, e := range entities {
				graphEntities[i] = entityToMap(e)
			}
			totalCount += len(graphEntities)
			resp["graph_entities"] = graphEntities
			resp["graph_count"] = len(graphEntities)
		}
	}

	resp["count"] = totalCount
	resp["total_tokens"] = totalTokens

	s.metrics.RecallRequests.Add(1)
	if totalCount > 0 {
		s.metrics.RecallHits.Add(1)
	} else {
		s.metrics.RecallMisses.Add(1)
	}
	if totalTokens >= tokenBudget {
		s.metrics.RecallTruncated.Add(1)
	}

	return mcp.JSONResult(resp)
}

// HandleDeprecatedContextRecall wraps HandleUnifiedRecall with a deprecation notice.
func (s *Service) HandleDeprecatedContextRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	args["scope"] = "context"
	return s.HandleUnifiedRecall(ctx, args)
}

// HandleDeprecatedEnhancedRecall normalizes legacy enhanced recall args and
// routes to the unified recall path.
func (s *Service) HandleDeprecatedEnhancedRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	normalized := make(map[string]any, len(args)+3)
	for k, v := range args {
		normalized[k] = v
	}

	if raw, ok := normalized["scope"]; ok {
		scope, hasContext, hasMemory, hasGraph := normalizeLegacyEnhancedScope(raw)
		if scope == "" {
			scope = "context"
		}
		normalized["scope"] = scope
		if _, exists := normalized["include_memory"]; !exists {
			normalized["include_memory"] = hasMemory
		}
		if _, exists := normalized["include_graph"]; !exists {
			normalized["include_graph"] = hasGraph
		}
		if !hasContext && hasGraph {
			// Graph results are emitted from the context recall path.
			normalized["scope"] = "context"
		}
	} else {
		normalized["scope"] = "all"
		if _, exists := normalized["include_memory"]; !exists {
			normalized["include_memory"] = true
		}
		if _, exists := normalized["include_graph"]; !exists {
			normalized["include_graph"] = true
		}
	}

	return s.HandleUnifiedRecall(ctx, normalized)
}

// HandleDeprecatedMemoryRecall wraps HandleUnifiedRecall with a deprecation notice.
func (s *Service) HandleDeprecatedMemoryRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	args["scope"] = "memory"
	return s.HandleUnifiedRecall(ctx, args)
}

func normalizeLegacyEnhancedScope(raw any) (scope string, hasContext, hasMemory, hasGraph bool) {
	add := func(s string) {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "context":
			hasContext = true
		case "memory":
			hasMemory = true
		case "graph":
			hasGraph = true
		}
	}

	switch v := raw.(type) {
	case string:
		add(v)
	case []string:
		for _, s := range v {
			add(s)
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				add(s)
			}
		}
	}

	switch {
	case hasMemory && !hasContext && !hasGraph:
		return "memory", hasContext, hasMemory, hasGraph
	case hasContext && !hasMemory:
		return "context", hasContext, hasMemory, hasGraph
	case hasGraph && !hasContext && !hasMemory:
		return "context", hasContext, hasMemory, hasGraph
	case hasContext || hasMemory || hasGraph:
		return "all", hasContext, hasMemory, hasGraph
	default:
		return "", false, false, false
	}
}

func (s *Service) getEntriesForSymbol(ctx context.Context, agentID, symbol string, limit int) ([]ContextEntry, error) {
	vector, err := s.embed.EmbedQuery(ctx, symbol)
	if err != nil {
		return nil, err
	}

	var filter map[string]any
	if agentID != "" {
		filter = FilterMust(Match("agent_id", agentID))
	}

	results, err := s.qdrant.Get(CollContext).Search(ctx, vector, filter, limit, true)
	if err != nil {
		return nil, err
	}

	entries := make([]ContextEntry, 0, len(results))
	for _, r := range results {
		entries = append(entries, r.Entry)
	}
	return entries, nil
}
