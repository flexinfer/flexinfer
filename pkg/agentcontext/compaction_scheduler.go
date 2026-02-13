package agentcontext

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// =========================================================================
// Automatic Context Compaction (Phase 3.4)
// =========================================================================

// CompactionConfig configures automatic compaction behavior
type CompactionConfig struct {
	// Enable automatic compaction
	Enabled bool `json:"enabled"`

	// Check interval for compaction triggers
	CheckInterval time.Duration `json:"check_interval"`

	// Capacity thresholds (0.0 - 1.0) that trigger compaction
	WorkingMemoryThreshold   float64 `json:"working_memory_threshold"`
	ShortTermMemoryThreshold float64 `json:"short_term_memory_threshold"`
	LongTermMemoryThreshold  float64 `json:"long_term_memory_threshold"`

	// Target capacity after compaction (0.0 - 1.0)
	TargetCapacity float64 `json:"target_capacity"`

	// Minimum age before considering for compaction
	MinAgeBeforeCompaction time.Duration `json:"min_age_before_compaction"`

	// Progressive summarization settings
	SummarizationDepth int `json:"summarization_depth"` // How many times to summarize before archiving

	// Max items to process per compaction run
	MaxItemsPerRun int `json:"max_items_per_run"`
}

// DefaultCompactionConfig returns sensible defaults
func DefaultCompactionConfig() CompactionConfig {
	return CompactionConfig{
		Enabled:                  true,
		CheckInterval:            5 * time.Minute,
		WorkingMemoryThreshold:   0.95,
		ShortTermMemoryThreshold: 0.90,
		LongTermMemoryThreshold:  0.85,
		TargetCapacity:           0.70,
		MinAgeBeforeCompaction:   1 * time.Hour,
		SummarizationDepth:       3,
		MaxItemsPerRun:           100,
	}
}

// CompactionScheduler manages automatic context compaction
type CompactionScheduler struct {
	mu sync.RWMutex

	config    CompactionConfig
	hierarchy *MemoryHierarchy
	persisted *persistedMemoryHierarchy // optional: when set, mutations write to Qdrant
	metrics   *Metrics
	logger    *slog.Logger

	// Compression function (LLM or fallback)
	compressFunc func(ctx context.Context, content string) (string, error)

	// State
	running    bool
	stopCh     chan struct{}
	lastRun    time.Time
	runCount   int64
	errorCount int64

	// Stats from last run
	lastRunStats CompactionStats
}

// CompactionStats contains statistics from a compaction run
type CompactionStats struct {
	StartTime       time.Time                      `json:"start_time"`
	Duration        time.Duration                  `json:"duration"`
	ItemsProcessed  int                            `json:"items_processed"`
	ItemsCompressed int                            `json:"items_compressed"`
	ItemsDemoted    int                            `json:"items_demoted"`
	ItemsArchived   int                            `json:"items_archived"`
	ItemsPromoted   int                            `json:"items_promoted"`
	ItemsExpired    int                            `json:"items_expired"`
	TokensBefore    int64                          `json:"tokens_before"`
	TokensAfter     int64                          `json:"tokens_after"`
	TokensSaved     int64                          `json:"tokens_saved"`
	Errors          int                            `json:"errors"`
	TierStats       map[string]TierCompactionStats `json:"tier_stats"`
}

// TierCompactionStats contains per-tier statistics
type TierCompactionStats struct {
	ItemsBefore     int     `json:"items_before"`
	ItemsAfter      int     `json:"items_after"`
	CapacityBefore  float64 `json:"capacity_before"`
	CapacityAfter   float64 `json:"capacity_after"`
	ItemsCompressed int     `json:"items_compressed"`
	ItemsDemoted    int     `json:"items_demoted"`
}

// NewCompactionScheduler creates a new compaction scheduler
func NewCompactionScheduler(
	config CompactionConfig,
	hierarchy *MemoryHierarchy,
	compressFunc func(ctx context.Context, content string) (string, error),
	logger *slog.Logger,
) *CompactionScheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &CompactionScheduler{
		config:       config,
		hierarchy:    hierarchy,
		compressFunc: compressFunc,
		metrics:      GetMetrics(),
		logger:       logger,
		stopCh:       make(chan struct{}),
	}
}

// SetPersistence configures Qdrant-backed persistence for compaction mutations.
func (cs *CompactionScheduler) SetPersistence(pmh *persistedMemoryHierarchy) {
	cs.persisted = pmh
}

// deleteItem deletes via persisted hierarchy if available, else in-memory.
func (cs *CompactionScheduler) deleteItem(ctx context.Context, id string) error {
	if cs.persisted != nil {
		return cs.persisted.DeleteItemWithPersistence(ctx, id)
	}
	return cs.hierarchy.DeleteItem(id)
}

// demoteItem demotes via persisted hierarchy if available, else in-memory.
func (cs *CompactionScheduler) demoteItem(ctx context.Context, id string) error {
	if cs.persisted != nil {
		return cs.persisted.DemoteItemWithPersistence(ctx, id)
	}
	return cs.hierarchy.DemoteItem(id)
}

// promoteItem promotes via persisted hierarchy if available, else in-memory.
func (cs *CompactionScheduler) promoteItem(ctx context.Context, id string) error {
	if cs.persisted != nil {
		return cs.persisted.PromoteItemWithPersistence(ctx, id)
	}
	return cs.hierarchy.PromoteItem(id)
}

// updateItem updates via persisted hierarchy if available, else in-memory.
func (cs *CompactionScheduler) updateItem(ctx context.Context, item *MemoryItem) error {
	if cs.persisted != nil {
		return cs.persisted.UpdateItemWithPersistence(ctx, item, nil)
	}
	return cs.hierarchy.UpdateItem(item)
}

// Start begins the automatic compaction scheduler
func (cs *CompactionScheduler) Start(ctx context.Context) error {
	cs.mu.Lock()
	if cs.running {
		cs.mu.Unlock()
		return nil
	}
	cs.running = true
	cs.stopCh = make(chan struct{})
	cs.mu.Unlock()

	go cs.runLoop(ctx)
	return nil
}

// Stop stops the compaction scheduler
func (cs *CompactionScheduler) Stop() {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if !cs.running {
		return
	}

	close(cs.stopCh)
	cs.running = false
}

// IsRunning returns whether the scheduler is active
func (cs *CompactionScheduler) IsRunning() bool {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.running
}

// GetLastRunStats returns stats from the most recent compaction run
func (cs *CompactionScheduler) GetLastRunStats() CompactionStats {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.lastRunStats
}

// TriggerCompaction manually triggers a compaction run
func (cs *CompactionScheduler) TriggerCompaction(ctx context.Context) (*CompactionStats, error) {
	return cs.runCompaction(ctx)
}

// runLoop is the main scheduler loop
func (cs *CompactionScheduler) runLoop(ctx context.Context) {
	ticker := time.NewTicker(cs.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-cs.stopCh:
			return
		case <-ticker.C:
			if cs.shouldCompact() {
				stats, err := cs.runCompaction(ctx)
				if err != nil {
					cs.logger.Warn("compaction run failed", "error", err)
				} else if stats != nil {
					cs.logger.Info("compaction run completed",
						"items_processed", stats.ItemsProcessed,
						"items_compressed", stats.ItemsCompressed,
						"items_demoted", stats.ItemsDemoted,
						"tokens_saved", stats.TokensSaved,
						"errors", stats.Errors,
						"duration", stats.Duration,
					)
				}
			}
		}
	}
}

// shouldCompact checks if compaction is needed based on capacity thresholds
func (cs *CompactionScheduler) shouldCompact() bool {
	if !cs.config.Enabled {
		return false
	}

	stats := cs.hierarchy.Stats()

	// Calculate capacity as ratio of current items/tokens to limits
	// Using item count as proxy for capacity
	workingCapacity := cs.calculateCapacity(stats.WorkingMemory.ItemCount, stats.WorkingMemory.TokenCount)
	shortTermCapacity := cs.calculateCapacity(stats.ShortTermMemory.ItemCount, stats.ShortTermMemory.TokenCount)
	longTermCapacity := cs.calculateCapacity(stats.LongTermMemory.ItemCount, stats.LongTermMemory.TokenCount)

	// Check each tier against its threshold
	if workingCapacity >= cs.config.WorkingMemoryThreshold {
		return true
	}
	if shortTermCapacity >= cs.config.ShortTermMemoryThreshold {
		return true
	}
	if longTermCapacity >= cs.config.LongTermMemoryThreshold {
		return true
	}

	return false
}

// calculateCapacity estimates tier capacity based on item and token counts
// Returns a value between 0.0 and 1.0
func (cs *CompactionScheduler) calculateCapacity(itemCount, tokenCount int) float64 {
	return cs.calculateCapacityForTier(itemCount, tokenCount, "")
}

// calculateCapacityForTier estimates capacity using retention policy limits when available
func (cs *CompactionScheduler) calculateCapacityForTier(itemCount, tokenCount int, tier MemoryTier) float64 {
	maxItems := 1000
	maxTokens := 500000

	// Use retention policy limits if available
	if tier != "" && cs.hierarchy != nil {
		if policy := cs.hierarchy.GetRetentionPolicy(tier); policy != nil {
			if policy.MaxItems > 0 {
				maxItems = policy.MaxItems
			}
			if policy.MaxTokens > 0 {
				maxTokens = policy.MaxTokens
			}
		}
	}

	itemCapacity := float64(itemCount) / float64(maxItems)
	tokenCapacity := float64(tokenCount) / float64(maxTokens)

	// Use the higher of the two
	if itemCapacity > tokenCapacity {
		return itemCapacity
	}
	return tokenCapacity
}

// runCompaction performs the actual compaction
func (cs *CompactionScheduler) runCompaction(ctx context.Context) (*CompactionStats, error) {
	startTime := time.Now()
	stats := CompactionStats{
		StartTime: startTime,
		TierStats: make(map[string]TierCompactionStats),
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

// sortItemsForCompaction sorts items for compaction (oldest and lowest priority first)
func (cs *CompactionScheduler) sortItemsForCompaction(items []MemoryItem) []MemoryItem {
	sorted := make([]MemoryItem, len(items))
	copy(sorted, items)

	// Pre-compute scores to avoid redundant calls during sort
	scores := make([]float64, len(sorted))
	for i := range sorted {
		scores[i] = cs.compactionScore(sorted[i])
	}

	sort.Slice(sorted, func(i, j int) bool {
		return scores[i] > scores[j]
	})

	return sorted
}

// compactionScore calculates a score for compaction priority (higher = compact first)
func (cs *CompactionScheduler) compactionScore(item MemoryItem) float64 {
	// Age factor (older = higher score)
	ageDays := time.Since(item.CreatedAt).Hours() / 24
	ageScore := ageDays / 30.0 // Normalize to roughly 0-1 for a month

	// Access recency factor (less recent = higher score)
	accessDays := time.Since(item.LastAccessedAt).Hours() / 24
	accessScore := accessDays / 30.0

	// Priority factor (lower importance = higher score)
	priorityScore := 1.0 - item.ImportanceScore

	// Token size factor (larger = higher score)
	tokenCount := item.OriginalTokens
	if item.CompressedTokens > 0 {
		tokenCount = item.CompressedTokens
	}
	tokenScore := float64(tokenCount) / 1000.0

	// Combine factors
	return ageScore*0.3 + accessScore*0.3 + priorityScore*0.2 + tokenScore*0.2
}

// estimateTokenCount estimates tokens from content length
func estimateTokenCount(content string) int {
	// Rough estimate: ~4 characters per token
	return len(content) / 4
}

// CompactionPolicy defines rules for automatic compaction
type CompactionPolicy struct {
	// Tier-specific policies
	WorkingPolicy   TierCompactionPolicy `json:"working_policy"`
	ShortTermPolicy TierCompactionPolicy `json:"short_term_policy"`
	LongTermPolicy  TierCompactionPolicy `json:"long_term_policy"`
}

// TierCompactionPolicy defines compaction rules for a single tier
type TierCompactionPolicy struct {
	// Maximum age before forced compaction
	MaxAge time.Duration `json:"max_age"`

	// Maximum items before compaction triggers
	MaxItems int `json:"max_items"`

	// Maximum tokens before compaction triggers
	MaxTokens int `json:"max_tokens"`

	// Compression strategy
	CompressionStrategy string `json:"compression_strategy"` // "summarize", "extract", "hybrid"

	// Action when fully compressed
	FullyCompressedAction string `json:"fully_compressed_action"` // "demote", "archive", "keep"
}

// DefaultCompactionPolicy returns sensible defaults
func DefaultCompactionPolicy() CompactionPolicy {
	return CompactionPolicy{
		WorkingPolicy: TierCompactionPolicy{
			MaxAge:                24 * time.Hour,
			MaxItems:              100,
			MaxTokens:             50000,
			CompressionStrategy:   "summarize",
			FullyCompressedAction: "demote",
		},
		ShortTermPolicy: TierCompactionPolicy{
			MaxAge:                7 * 24 * time.Hour,
			MaxItems:              500,
			MaxTokens:             200000,
			CompressionStrategy:   "hybrid",
			FullyCompressedAction: "demote",
		},
		LongTermPolicy: TierCompactionPolicy{
			MaxAge:                30 * 24 * time.Hour,
			MaxItems:              2000,
			MaxTokens:             500000,
			CompressionStrategy:   "extract",
			FullyCompressedAction: "archive",
		},
	}
}

// SchedulerStatus contains the current status of the compaction scheduler
type SchedulerStatus struct {
	Running      bool             `json:"running"`
	LastRun      time.Time        `json:"last_run,omitempty"`
	RunCount     int64            `json:"run_count"`
	ErrorCount   int64            `json:"error_count"`
	Config       CompactionConfig `json:"config"`
	LastRunStats *CompactionStats `json:"last_run_stats,omitempty"`
	NextCheckIn  time.Duration    `json:"next_check_in,omitempty"`
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

	// Auto-promote: working → short_term (access count >= 3 and importance >= 0.7)
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

	// Auto-demote: short_term → working (importance < demotion threshold and no access in 48h)
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

// Status returns the current scheduler status
func (cs *CompactionScheduler) Status() SchedulerStatus {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	status := SchedulerStatus{
		Running:    cs.running,
		LastRun:    cs.lastRun,
		RunCount:   cs.runCount,
		ErrorCount: cs.errorCount,
		Config:     cs.config,
	}

	if cs.runCount > 0 {
		stats := cs.lastRunStats
		status.LastRunStats = &stats
	}

	if cs.running && !cs.lastRun.IsZero() {
		nextCheck := cs.lastRun.Add(cs.config.CheckInterval)
		if nextCheck.After(time.Now()) {
			status.NextCheckIn = time.Until(nextCheck)
		}
	}

	return status
}
