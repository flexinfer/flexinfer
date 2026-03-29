package agentcontext

import (
	"sort"
	"time"
)

// =========================================================================
// Compaction Strategy: Scoring, Sorting, and Policy Types
// =========================================================================

// sortItemsForCompaction sorts items for compaction (oldest and lowest priority first)
func (cs *CompactionScheduler) sortItemsForCompaction(items []MemoryItem) []MemoryItem {
	sorted := make([]MemoryItem, len(items))
	copy(sorted, items)

	// Build title-prefix frequency map for duplicate detection.
	titlePrefixCounts := buildTitlePrefixCounts(sorted)

	// Pre-compute scores to avoid redundant calls during sort
	scores := make([]float64, len(sorted))
	for i := range sorted {
		scores[i] = cs.compactionScore(sorted[i], titlePrefixCounts)
	}

	sort.Slice(sorted, func(i, j int) bool {
		return scores[i] > scores[j]
	})

	return sorted
}

// buildTitlePrefixCounts returns a map of title prefix -> count for duplicate
// content detection. Prefix is the first 30 characters of the title.
func buildTitlePrefixCounts(items []MemoryItem) map[string]int {
	counts := make(map[string]int, len(items))
	for _, item := range items {
		prefix := titlePrefix(item.Title)
		if prefix != "" {
			counts[prefix]++
		}
	}
	return counts
}

// titlePrefix returns the first 30 characters of a title for grouping.
func titlePrefix(title string) string {
	if len(title) > 30 {
		return title[:30]
	}
	return title
}

// compactionScore calculates a score for compaction priority (higher = compact first).
// It considers age, access recency, importance, token size, entry type, and
// duplicate content detection.
func (cs *CompactionScheduler) compactionScore(item MemoryItem, titlePrefixCounts map[string]int) float64 {
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

	// Base score from original factors
	score := ageScore*0.3 + accessScore*0.3 + priorityScore*0.2 + tokenScore*0.2

	// Entry type compaction boost
	score += entryTypeCompactionBoost(item.Category)

	// Duplicate content detection: items sharing a title prefix with others
	// are more likely redundant and should compact sooner.
	if titlePrefixCounts != nil {
		prefix := titlePrefix(item.Title)
		if prefix != "" && titlePrefixCounts[prefix] > 1 {
			score += 0.3
		}
	}

	return score
}

// entryTypeCompactionBoost returns a compaction score modifier based on
// entry type. Positive values make items compact sooner; negative values
// make them compact later (i.e., they are preserved longer).
func entryTypeCompactionBoost(entryType string) float64 {
	switch EntryType(entryType) {
	case EntryTypeFileRead:
		return 0.2
	case EntryTypeDecision:
		return -0.3
	default:
		return 0.0
	}
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
