package agentcontext

import (
	"context"
	"log/slog"
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

	// --- F2: LLM-backed auto-compaction (appended) ---

	// Mode selects the compaction strategy. "extractive" (default) preserves
	// the existing behavior. "llm" routes batches through a summarizer.
	Mode string `json:"mode,omitempty"`

	// PinRawFor controls how long raw entry blobs are kept pinned after an
	// LLM synthesis step so callers can recover originals if needed.
	PinRawFor time.Duration `json:"pin_raw_for,omitempty"`

	// MaxSynthesisTokens caps the token budget for LLM-synthesized output.
	MaxSynthesisTokens int `json:"max_synthesis_tokens,omitempty"`
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
		Mode:                     "extractive",
		PinRawFor:                1 * time.Hour,
		MaxSynthesisTokens:       2048,
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

	// F2 (appended): LLM synthesis backend + raw-blob pin store.
	summarizer CompactionSummarizer
	pinned     PinnedStore
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

// =========================================================================
// F2: LLM-backed compaction wiring (appended)
// =========================================================================

// summarizer is the LLM synthesis backend used when CompactionConfig.Mode ==
// "llm". Set via SetSummarizer; nil leaves the scheduler in extractive mode.
func (cs *CompactionScheduler) SetSummarizer(s CompactionSummarizer) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.summarizer = s
}

// SetPinnedStore wires a raw-blob pin store used when LLM synthesis replaces
// a batch of entries with a single summary.
func (cs *CompactionScheduler) SetPinnedStore(p PinnedStore) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.pinned = p
}

// getSummarizer returns the configured summarizer (nil if unset).
func (cs *CompactionScheduler) getSummarizer() CompactionSummarizer {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.summarizer
}

// getPinnedStore returns the configured PinnedStore (nil if unset).
func (cs *CompactionScheduler) getPinnedStore() PinnedStore {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.pinned
}
