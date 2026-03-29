package agentcontext

import (
	"time"
)

// =========================================================================
// Memory Hierarchy Types
// =========================================================================

// MemoryTier defines the tier of memory storage
type MemoryTier string

const (
	// Working memory: Immediate context, always available, most detailed
	MemoryTierWorking MemoryTier = "working"
	// Short-term memory: Recent sessions, summarized, expires after days
	MemoryTierShortTerm MemoryTier = "short_term"
	// Long-term memory: Important decisions and learnings, highly compressed, persists indefinitely
	MemoryTierLongTerm MemoryTier = "long_term"
)

// MemoryItemStatus defines the status of a memory item
type MemoryItemStatus string

const (
	MemoryItemStatusActive     MemoryItemStatus = "active"
	MemoryItemStatusCompressed MemoryItemStatus = "compressed"
	MemoryItemStatusArchived   MemoryItemStatus = "archived"
	MemoryItemStatusExpired    MemoryItemStatus = "expired"
)

// ImportanceLevel defines how important a memory item is
type ImportanceLevel string

const (
	ImportanceLevelLow      ImportanceLevel = "low"
	ImportanceLevelMedium   ImportanceLevel = "medium"
	ImportanceLevelHigh     ImportanceLevel = "high"
	ImportanceLevelCritical ImportanceLevel = "critical"
)

// MemoryItem represents an item in the memory hierarchy
type MemoryItem struct {
	ID              string           `json:"id"`
	Tier            MemoryTier       `json:"tier"`
	Status          MemoryItemStatus `json:"status"`
	Importance      ImportanceLevel  `json:"importance"`
	ImportanceScore float64          `json:"importance_score"` // 0.0-1.0

	// Content
	Title   string `json:"title"`
	Content string `json:"content"`
	Summary string `json:"summary,omitempty"` // Compressed version

	// Original context entry reference
	SourceEntryID string    `json:"source_entry_id,omitempty"`
	SourceType    EntryType `json:"source_type,omitempty"`

	// Categorization
	Category  string   `json:"category,omitempty"` // decision, finding, pattern, error, etc.
	Tags      []string `json:"tags,omitempty"`
	Namespace string   `json:"namespace,omitempty"`

	// Provenance
	SessionID string `json:"session_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`

	// Lifecycle
	CreatedAt      time.Time  `json:"created_at"`
	LastAccessedAt time.Time  `json:"last_accessed_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	CompressedAt   *time.Time `json:"compressed_at,omitempty"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty"`

	// Access tracking
	AccessCount int `json:"access_count"`

	// Token tracking
	OriginalTokens   int `json:"original_tokens"`
	CompressedTokens int `json:"compressed_tokens,omitempty"`

	// Relationships to other memory items
	RelatedIDs []string `json:"related_ids,omitempty"`
	ParentID   string   `json:"parent_id,omitempty"` // For merged items
	ChildIDs   []string `json:"child_ids,omitempty"` // Items merged into this

	// Custom metadata
	Metadata map[string]any `json:"metadata,omitempty"`

	// Embedding for similarity search
	Embedding []float32 `json:"embedding,omitempty"`
}

// RetentionPolicy defines how memory items are retained and compressed
type RetentionPolicy struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Tier-specific settings
	Tier MemoryTier `json:"tier"`

	// TTL settings (in hours)
	DefaultTTL       int `json:"default_ttl_hours,omitempty"`        // 0 = no expiry
	MinImportanceTTL int `json:"min_importance_ttl_hours,omitempty"` // TTL for low importance items

	// Compression settings
	CompressAfterHours int     `json:"compress_after_hours,omitempty"`
	CompressionRatio   float64 `json:"compression_ratio,omitempty"` // Target ratio (e.g., 0.2 = 20% of original)
	MergeThreshold     float64 `json:"merge_threshold,omitempty"`   // Similarity threshold for merging (0.0-1.0)

	// Promotion/demotion settings
	PromotionThreshold   float64 `json:"promotion_threshold,omitempty"`    // Min importance to promote
	DemotionThreshold    float64 `json:"demotion_threshold,omitempty"`     // Max importance to demote
	AccessCountThreshold int     `json:"access_count_threshold,omitempty"` // Min accesses to prevent demotion

	// Capacity limits
	MaxItems  int `json:"max_items,omitempty"`
	MaxTokens int `json:"max_tokens,omitempty"`

	// Deduplication
	DedupeEnabled    bool    `json:"dedupe_enabled"`
	DedupeSimilarity float64 `json:"dedupe_similarity,omitempty"` // Similarity threshold for dedup

	// Categories to include/exclude
	IncludeCategories []string `json:"include_categories,omitempty"`
	ExcludeCategories []string `json:"exclude_categories,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CompressionJob represents a pending or completed compression operation
type CompressionJob struct {
	ID        string     `json:"id"`
	Tier      MemoryTier `json:"tier"`
	Status    string     `json:"status"` // pending, running, completed, failed
	ItemCount int        `json:"item_count"`

	// Results
	OriginalTokens   int `json:"original_tokens"`
	CompressedTokens int `json:"compressed_tokens"`
	MergedCount      int `json:"merged_count"`
	ArchivedCount    int `json:"archived_count"`
	ExpiredCount     int `json:"expired_count"`

	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// MemoryHierarchyStats contains statistics about memory usage
type MemoryHierarchyStats struct {
	// Per-tier stats
	WorkingMemory   MemoryTierStats `json:"working_memory"`
	ShortTermMemory MemoryTierStats `json:"short_term_memory"`
	LongTermMemory  MemoryTierStats `json:"long_term_memory"`

	// Overall stats
	TotalItems       int     `json:"total_items"`
	TotalTokens      int     `json:"total_tokens"`
	CompressionRatio float64 `json:"compression_ratio"`

	// Recent activity
	ItemsAddedLast24h      int `json:"items_added_last_24h"`
	ItemsCompressedLast24h int `json:"items_compressed_last_24h"`
	ItemsExpiredLast24h    int `json:"items_expired_last_24h"`
}

// MemoryTierStats contains statistics for a single memory tier
type MemoryTierStats struct {
	Tier          MemoryTier     `json:"tier"`
	ItemCount     int            `json:"item_count"`
	TokenCount    int            `json:"token_count"`
	AvgImportance float64        `json:"avg_importance"`
	OldestItem    *time.Time     `json:"oldest_item,omitempty"`
	NewestItem    *time.Time     `json:"newest_item,omitempty"`
	ByCategory    map[string]int `json:"by_category,omitempty"`
	ByImportance  map[string]int `json:"by_importance,omitempty"`
}

// MemoryRecallRequest represents a request to recall from memory hierarchy
type MemoryRecallRequest struct {
	Query         string       `json:"query"`
	Tiers         []MemoryTier `json:"tiers,omitempty"` // Empty = all tiers
	Categories    []string     `json:"categories,omitempty"`
	Tags          []string     `json:"tags,omitempty"`
	Namespace     string       `json:"namespace,omitempty"`
	SessionID     string       `json:"session_id,omitempty"`
	AgentID       string       `json:"agent_id,omitempty"`
	TokenBudget   int          `json:"token_budget,omitempty"`
	MinImportance float64      `json:"min_importance,omitempty"`
	Limit         int          `json:"limit,omitempty"`
}

// MemoryRecallResult contains the result of a memory recall
type MemoryRecallResult struct {
	Items       []MemoryItem   `json:"items"`
	TotalTokens int            `json:"total_tokens"`
	ByTier      map[string]int `json:"by_tier"`
	Truncated   bool           `json:"truncated"` // True if results were limited by token budget
}
