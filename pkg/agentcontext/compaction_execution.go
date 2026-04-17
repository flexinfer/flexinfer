package agentcontext

import (
	"context"
	"time"
)

// =========================================================================
// Compaction Execution: Core compaction, sweeping, and promotion logic
// =========================================================================

// runCompaction performs the actual compaction
func (cs *CompactionScheduler) runCompaction(ctx context.Context) (*CompactionStats, error) {
	startTime := time.Now()
	stats := CompactionStats{
		StartTime: startTime,
		TierStats: make(map[string]TierCompactionStats),
	}

	// F2: If LLM mode is selected and a summarizer is configured, try the
	// LLM path first. On error we fall through to the existing extractive
	// logic and emit a fallback metric.
	if cs.config.Mode == "llm" {
		if summarizer := cs.getSummarizer(); summarizer != nil {
			if ok := cs.runLLMCompaction(ctx, summarizer, &stats); ok {
				stats.Duration = time.Since(startTime)
				cs.mu.Lock()
				cs.lastRun = startTime
				cs.runCount++
				cs.lastRunStats = stats
				cs.mu.Unlock()
				return &stats, nil
			}
			// Fall through to extractive path on LLM failure.
			if cs.metrics != nil {
				cs.metrics.CompactionFallbacks.Add(1)
			}
		}
	}

	// Get initial state
	hierStats := cs.hierarchy.Stats()
	stats.TokensBefore = int64(hierStats.TotalTokens)

	// Record initial tier states
	stats.TierStats["working"] = TierCompactionStats{
		ItemsBefore:    hierStats.WorkingMemory.ItemCount,
		CapacityBefore: cs.calculateCapacity(hierStats.WorkingMemory.ItemCount, hierStats.WorkingMemory.TokenCount),
	}
	stats.TierStats["short_term"] = TierCompactionStats{
		ItemsBefore:    hierStats.ShortTermMemory.ItemCount,
		CapacityBefore: cs.calculateCapacity(hierStats.ShortTermMemory.ItemCount, hierStats.ShortTermMemory.TokenCount),
	}
	stats.TierStats["long_term"] = TierCompactionStats{
		ItemsBefore:    hierStats.LongTermMemory.ItemCount,
		CapacityBefore: cs.calculateCapacity(hierStats.LongTermMemory.ItemCount, hierStats.LongTermMemory.TokenCount),
	}

	// Phase 0: TTL expiry sweep and auto-promotion/demotion
	expired := cs.sweepExpiredItems(ctx)
	stats.ItemsExpired = expired

	promoted := cs.runPromotionDemotion(ctx)
	stats.ItemsPromoted = promoted

	// Process each tier
	processedCount := 0

	// Phase 1: Compress old items in working memory
	workingCompressed, workingDemoted, workingErrors := cs.compactTier(ctx, MemoryTierWorking, &processedCount)
	stats.ItemsCompressed += workingCompressed
	stats.ItemsDemoted += workingDemoted
	stats.Errors += workingErrors

	// Phase 2: Compress old items in short-term memory
	shortTermCompressed, shortTermDemoted, shortTermErrors := cs.compactTier(ctx, MemoryTierShortTerm, &processedCount)
	stats.ItemsCompressed += shortTermCompressed
	stats.ItemsDemoted += shortTermDemoted
	stats.Errors += shortTermErrors

	// Phase 3: Archive old items in long-term memory
	longTermCompressed, longTermArchived, longTermErrors := cs.compactTier(ctx, MemoryTierLongTerm, &processedCount)
	stats.ItemsCompressed += longTermCompressed
	stats.ItemsArchived += longTermArchived
	stats.Errors += longTermErrors

	stats.ItemsProcessed = processedCount

	// Get final state
	finalStats := cs.hierarchy.Stats()
	stats.TokensAfter = int64(finalStats.TotalTokens)
	stats.TokensSaved = stats.TokensBefore - stats.TokensAfter

	// Update final tier states
	if ts, ok := stats.TierStats["working"]; ok {
		ts.ItemsAfter = finalStats.WorkingMemory.ItemCount
		ts.CapacityAfter = cs.calculateCapacity(finalStats.WorkingMemory.ItemCount, finalStats.WorkingMemory.TokenCount)
		ts.ItemsCompressed = workingCompressed
		ts.ItemsDemoted = workingDemoted
		stats.TierStats["working"] = ts
	}
	if ts, ok := stats.TierStats["short_term"]; ok {
		ts.ItemsAfter = finalStats.ShortTermMemory.ItemCount
		ts.CapacityAfter = cs.calculateCapacity(finalStats.ShortTermMemory.ItemCount, finalStats.ShortTermMemory.TokenCount)
		ts.ItemsCompressed = shortTermCompressed
		ts.ItemsDemoted = shortTermDemoted
		stats.TierStats["short_term"] = ts
	}
	if ts, ok := stats.TierStats["long_term"]; ok {
		ts.ItemsAfter = finalStats.LongTermMemory.ItemCount
		ts.CapacityAfter = cs.calculateCapacity(finalStats.LongTermMemory.ItemCount, finalStats.LongTermMemory.TokenCount)
		ts.ItemsCompressed = longTermCompressed
		stats.TierStats["long_term"] = ts
	}

	stats.Duration = time.Since(startTime)

	// Update scheduler state
	cs.mu.Lock()
	cs.lastRun = startTime
	cs.runCount++
	if stats.Errors > 0 {
		cs.errorCount++
	}
	cs.lastRunStats = stats
	cs.mu.Unlock()

	// Update metrics
	if cs.metrics != nil {
		cs.metrics.CompressionJobs.Add(1)
		cs.metrics.CompressionTokensSaved.Add(stats.TokensSaved)
	}

	return &stats, nil
}

// compactTier compresses and optionally demotes items in a tier
func (cs *CompactionScheduler) compactTier(ctx context.Context, tier MemoryTier, processedCount *int) (compressed, demoted, errors int) {
	if *processedCount >= cs.config.MaxItemsPerRun {
		return 0, 0, 0
	}

	// Get items from this tier using Recall
	recallReq := MemoryRecallRequest{
		Tiers: []MemoryTier{tier},
		Limit: cs.config.MaxItemsPerRun * 2, // Get more than needed for filtering
	}
	recallResult, err := cs.hierarchy.Recall(recallReq)
	if err != nil || len(recallResult.Items) == 0 {
		return 0, 0, 0
	}

	items := recallResult.Items

	// Calculate how many items to process based on current capacity
	stats := cs.hierarchy.Stats()
	var tierStats MemoryTierStats
	switch tier {
	case MemoryTierWorking:
		tierStats = stats.WorkingMemory
	case MemoryTierShortTerm:
		tierStats = stats.ShortTermMemory
	case MemoryTierLongTerm:
		tierStats = stats.LongTermMemory
	}

	currentCapacity := cs.calculateCapacity(tierStats.ItemCount, tierStats.TokenCount)

	// Only process if above threshold
	var threshold float64
	switch tier {
	case MemoryTierWorking:
		threshold = cs.config.WorkingMemoryThreshold
	case MemoryTierShortTerm:
		threshold = cs.config.ShortTermMemoryThreshold
	case MemoryTierLongTerm:
		threshold = cs.config.LongTermMemoryThreshold
	}

	if currentCapacity < threshold {
		return 0, 0, 0
	}

	// Calculate target items to process
	targetCapacity := cs.config.TargetCapacity
	reductionNeeded := (currentCapacity - targetCapacity) / currentCapacity
	targetItems := int(float64(len(items)) * reductionNeeded)
	if targetItems < 1 {
		targetItems = 1
	}
	if targetItems > cs.config.MaxItemsPerRun-*processedCount {
		targetItems = cs.config.MaxItemsPerRun - *processedCount
	}

	// Sort items by age (oldest first) and priority (lowest first)
	sortedItems := cs.sortItemsForCompaction(items)

	// Process items
	for i := 0; i < targetItems && i < len(sortedItems); i++ {
		item := sortedItems[i]
		*processedCount++

		// Skip if too new
		if time.Since(item.CreatedAt) < cs.config.MinAgeBeforeCompaction {
			continue
		}

		// Decide action based on tier and compression count
		compressionCount := 0
		if item.Metadata != nil {
			if cc, ok := item.Metadata["compression_count"].(float64); ok {
				compressionCount = int(cc)
			}
		}

		if compressionCount >= cs.config.SummarizationDepth {
			// Already compressed enough, demote or archive
			if tier == MemoryTierLongTerm {
				// Archive (remove from active memory)
				if err := cs.deleteItem(ctx, item.ID); err != nil {
					cs.logger.Warn("compaction: failed to archive item", "item_id", item.ID, "error", err)
					errors++
				}
				demoted++ // Using demoted as "archived" for long-term
			} else {
				// Demote to next tier
				if err := cs.demoteItem(ctx, item.ID); err == nil {
					demoted++
				} else {
					errors++
				}
			}
		} else {
			// Compress the item
			if cs.compressFunc != nil {
				compressedContent, err := cs.compressFunc(ctx, item.Content)
				if err != nil {
					errors++
					continue
				}

				// Update item with compressed content
				item.Content = compressedContent
				if item.Metadata == nil {
					item.Metadata = make(map[string]any)
				}
				item.Metadata["compression_count"] = compressionCount + 1
				item.Metadata["last_compressed"] = time.Now().Format(time.RFC3339)
				item.Metadata["original_tokens"] = item.OriginalTokens
				item.CompressedTokens = estimateTokenCount(compressedContent)
				now := time.Now()
				item.CompressedAt = &now

				// Update in hierarchy
				if err := cs.updateItem(ctx, &item); err != nil {
					cs.logger.Warn("compaction: failed to update compressed item", "item_id", item.ID, "error", err)
					errors++
				}
				compressed++
			}
		}
	}

	return compressed, demoted, errors
}

// sweepExpiredItems removes items that have exceeded their TTL
func (cs *CompactionScheduler) sweepExpiredItems(ctx context.Context) int {
	if cs.hierarchy == nil {
		return 0
	}

	expired := 0
	now := time.Now()

	for _, tier := range []MemoryTier{MemoryTierWorking, MemoryTierShortTerm, MemoryTierLongTerm} {
		recallReq := MemoryRecallRequest{
			Tiers: []MemoryTier{tier},
			Limit: 10000,
		}
		result, err := cs.hierarchy.Recall(recallReq)
		if err != nil || len(result.Items) == 0 {
			continue
		}

		for _, item := range result.Items {
			if item.ExpiresAt != nil && now.After(*item.ExpiresAt) {
				if err := cs.deleteItem(ctx, item.ID); err != nil {
					cs.logger.Warn("TTL sweep: failed to delete expired item", "item_id", item.ID, "error", err)
				}
				expired++
			}
		}
	}
	return expired
}

// runPromotionDemotion auto-promotes and auto-demotes items based on access patterns
func (cs *CompactionScheduler) runPromotionDemotion(ctx context.Context) int {
	if cs.hierarchy == nil {
		return 0
	}

	promoted := 0

	// Auto-promote: working -> short_term (access count >= 3 and importance >= 0.7)
	workingReq := MemoryRecallRequest{
		Tiers: []MemoryTier{MemoryTierWorking},
		Limit: 10000,
	}
	workingResult, err := cs.hierarchy.Recall(workingReq)
	if err == nil {
		for _, item := range workingResult.Items {
			if item.AccessCount >= 3 && item.ImportanceScore >= 0.7 {
				if err := cs.promoteItem(ctx, item.ID); err == nil {
					promoted++
				}
			}
		}
	}

	// Auto-demote: short_term -> working (importance < demotion threshold and no access in 48h)
	shortTermReq := MemoryRecallRequest{
		Tiers: []MemoryTier{MemoryTierShortTerm},
		Limit: 10000,
	}
	shortTermResult, err := cs.hierarchy.Recall(shortTermReq)
	if err == nil {
		demotionThreshold := 0.3
		for _, item := range shortTermResult.Items {
			if item.ImportanceScore < demotionThreshold && time.Since(item.LastAccessedAt) > 48*time.Hour {
				if err := cs.demoteItem(ctx, item.ID); err != nil {
					cs.logger.Warn("auto-demotion: failed to demote item", "item_id", item.ID, "error", err)
				}
			}
		}
	}

	return promoted
}

// CompactSession compresses memory items older than 30 minutes within a
// specific session. This is useful for long-running sessions that accumulate
// lots of context and can be called independently from the global tier-based
// compaction cycle.
func (cs *CompactionScheduler) CompactSession(ctx context.Context, sessionID string) (*CompactionStats, error) {
	if cs.hierarchy == nil {
		return nil, nil
	}

	startTime := time.Now()
	stats := CompactionStats{
		StartTime: startTime,
		TierStats: make(map[string]TierCompactionStats),
	}

	// Recall all items for this session across all tiers.
	recallReq := MemoryRecallRequest{
		SessionID: sessionID,
		Limit:     10000,
	}
	result, err := cs.hierarchy.Recall(recallReq)
	if err != nil {
		return nil, err
	}
	if len(result.Items) == 0 {
		stats.Duration = time.Since(startTime)
		return &stats, nil
	}

	stats.TokensBefore = int64(result.TotalTokens)
	ageThreshold := 30 * time.Minute

	for _, item := range result.Items {
		if time.Since(item.CreatedAt) < ageThreshold {
			continue
		}
		stats.ItemsProcessed++

		if cs.compressFunc == nil {
			continue
		}

		compressedContent, err := cs.compressFunc(ctx, item.Content)
		if err != nil {
			stats.Errors++
			continue
		}

		item.Content = compressedContent
		if item.Metadata == nil {
			item.Metadata = make(map[string]any)
		}

		compressionCount := 0
		if cc, ok := item.Metadata["compression_count"].(float64); ok {
			compressionCount = int(cc)
		}
		item.Metadata["compression_count"] = compressionCount + 1
		item.Metadata["last_compressed"] = time.Now().Format(time.RFC3339)
		item.Metadata["original_tokens"] = item.OriginalTokens
		item.CompressedTokens = estimateTokenCount(compressedContent)
		now := time.Now()
		item.CompressedAt = &now

		if err := cs.updateItem(ctx, &item); err != nil {
			cs.logger.Warn("session compaction: failed to update item",
				"item_id", item.ID, "session_id", sessionID, "error", err)
			stats.Errors++
			continue
		}
		stats.ItemsCompressed++
	}

	// Recalculate token savings.
	finalResult, err := cs.hierarchy.Recall(recallReq)
	if err == nil {
		stats.TokensAfter = int64(finalResult.TotalTokens)
	}
	stats.TokensSaved = stats.TokensBefore - stats.TokensAfter
	stats.Duration = time.Since(startTime)

	if cs.metrics != nil {
		cs.metrics.CompressionJobs.Add(1)
		cs.metrics.CompressionTokensSaved.Add(stats.TokensSaved)
	}

	return &stats, nil
}

// =========================================================================
// F2: LLM-backed compaction path (appended)
// =========================================================================

// runLLMCompaction collects recent working-tier items, asks the summarizer to
// synthesize a condensed summary, pins the raw entry IDs via PinnedStore, and
// replaces the originals with a single summary item. Returns true on success,
// false on error (caller falls back to extractive path).
func (cs *CompactionScheduler) runLLMCompaction(
	ctx context.Context,
	summarizer CompactionSummarizer,
	stats *CompactionStats,
) bool {
	if cs.hierarchy == nil {
		return false
	}

	// Gather a batch from working memory -- most recent tier with the
	// highest churn. Keeps the LLM call scoped and bounded.
	recallReq := MemoryRecallRequest{
		Tiers: []MemoryTier{MemoryTierWorking},
		Limit: cs.config.MaxItemsPerRun,
	}
	result, err := cs.hierarchy.Recall(recallReq)
	if err != nil || len(result.Items) == 0 {
		return false
	}

	// Map MemoryItems into the ContextEntry shape the summarizer expects.
	entries := make([]ContextEntry, 0, len(result.Items))
	entryIDs := make([]string, 0, len(result.Items))
	tokensBefore := 0
	for _, item := range result.Items {
		entries = append(entries, ContextEntry{
			ID:        item.ID,
			EntryType: item.SourceType,
			Title:     item.Title,
			Content:   item.Content,
			Timestamp: item.CreatedAt,
		})
		entryIDs = append(entryIDs, item.ID)
		tokensBefore += item.OriginalTokens
	}
	stats.TokensBefore = int64(tokensBefore)

	summary, err := summarizer.Summarize(ctx, entries)
	if err != nil || summary == "" {
		cs.logger.Warn("llm compaction: summarize failed, falling back",
			"error", err, "batch_size", len(entries))
		return false
	}

	// Pin raw entry IDs for the configured retention window so callers can
	// still retrieve originals before they're fully reclaimed.
	pinnedUntil := time.Now().Add(cs.config.PinRawFor)
	if store := cs.getPinnedStore(); store != nil {
		if perr := store.Pin(ctx, entryIDs, pinnedUntil); perr != nil {
			cs.logger.Warn("llm compaction: pin failed",
				"error", perr, "ids", len(entryIDs))
		}
	}

	// Replace originals with a single summary item.
	summaryItem := &MemoryItem{
		ID:             "llm-summary-" + time.Now().UTC().Format("20060102T150405.000000000"),
		Tier:           MemoryTierShortTerm,
		Status:         MemoryItemStatusCompressed,
		Title:          "LLM compaction summary",
		Content:        summary,
		Summary:        summary,
		Category:       "summary",
		CreatedAt:      time.Now(),
		LastAccessedAt: time.Now(),
		OriginalTokens: tokensBefore,
		Metadata: map[string]any{
			"compaction_strategy": "llm",
			"pinned_entry_ids":    entryIDs,
			"pinned_until":        pinnedUntil.Format(time.RFC3339),
		},
	}
	if addErr := cs.hierarchy.AddItem(summaryItem); addErr != nil {
		cs.logger.Warn("llm compaction: add summary item failed", "error", addErr)
		return false
	}

	// Remove originals (best-effort; log but don't abort on delete errors).
	for _, id := range entryIDs {
		if delErr := cs.deleteItem(ctx, id); delErr != nil {
			cs.logger.Warn("llm compaction: delete original failed",
				"error", delErr, "id", id)
			stats.Errors++
		}
	}

	stats.ItemsProcessed = len(entryIDs)
	stats.ItemsCompressed = len(entryIDs)
	stats.TokensAfter = int64(estimateTokenCount(summary))
	stats.TokensSaved = stats.TokensBefore - stats.TokensAfter

	if cs.metrics != nil {
		cs.metrics.CompressionJobs.Add(1)
		cs.metrics.CompressionTokensSaved.Add(stats.TokensSaved)
	}
	return true
}
