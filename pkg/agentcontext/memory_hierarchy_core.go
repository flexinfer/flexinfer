package agentcontext

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/crb2nu/loom/pkg/env"
)

// Retention policy defaults for each memory tier.
// Operators can override key values via environment variables.
const (
	// Working memory defaults.
	defaultWorkingTTLHours         = 24
	defaultWorkingCompressAfter    = 4
	defaultWorkingCompressionRatio = 0.5
	defaultWorkingMaxItems         = 1000
	defaultWorkingMaxTokens        = 100000
	defaultWorkingDedupeSimilarity = 0.9

	// Short-term memory defaults.
	defaultShortTermTTLHours           = 168 // 7 days
	defaultShortTermCompressAfter      = 24
	defaultShortTermCompressionRatio   = 0.3
	defaultShortTermMergeThreshold     = 0.8
	defaultShortTermPromotionThreshold = 0.7
	defaultShortTermDemotionThreshold  = 0.3
	defaultShortTermMaxItems           = 5000
	defaultShortTermMaxTokens          = 200000
	defaultShortTermDedupeSimilarity   = 0.85

	// Long-term memory defaults.
	defaultLongTermTTLHours             = 0 // no expiry
	defaultLongTermCompressionRatio     = 0.2
	defaultLongTermMergeThreshold       = 0.9
	defaultLongTermAccessCountThreshold = 3
	defaultLongTermMaxItems             = 10000
	defaultLongTermMaxTokens            = 500000
	defaultLongTermDedupeSimilarity     = 0.95
)

// NewMemoryHierarchy creates a new memory hierarchy
func NewMemoryHierarchy() *MemoryHierarchy {
	mh := &MemoryHierarchy{
		working:         make(map[string]*MemoryItem),
		shortTerm:       make(map[string]*MemoryItem),
		longTerm:        make(map[string]*MemoryItem),
		byNamespace:     make(map[string]map[string]bool),
		byCategory:      make(map[string]map[string]bool),
		bySession:       make(map[string]map[string]bool),
		policies:        make(map[MemoryTier]*RetentionPolicy),
		compressionJobs: make(map[string]*CompressionJob),
	}

	// Set default policies. Key operational values (TTL, capacity) are
	// overridable via AGENT_MEMORY_* environment variables.
	mh.policies[MemoryTierWorking] = &RetentionPolicy{
		ID:                 "default-working",
		Name:               "Default Working Memory",
		Tier:               MemoryTierWorking,
		DefaultTTL:         env.Int("AGENT_MEMORY_WORKING_TTL_HOURS", defaultWorkingTTLHours),
		CompressAfterHours: env.Int("AGENT_MEMORY_WORKING_COMPRESS_AFTER", defaultWorkingCompressAfter),
		CompressionRatio:   defaultWorkingCompressionRatio,
		MaxItems:           env.Int("AGENT_MEMORY_WORKING_MAX_ITEMS", defaultWorkingMaxItems),
		MaxTokens:          env.Int("AGENT_MEMORY_WORKING_MAX_TOKENS", defaultWorkingMaxTokens),
		DedupeEnabled:      true,
		DedupeSimilarity:   defaultWorkingDedupeSimilarity,
	}

	mh.policies[MemoryTierShortTerm] = &RetentionPolicy{
		ID:                 "default-short-term",
		Name:               "Default Short-Term Memory",
		Tier:               MemoryTierShortTerm,
		DefaultTTL:         env.Int("AGENT_MEMORY_SHORT_TERM_TTL_HOURS", defaultShortTermTTLHours),
		CompressAfterHours: env.Int("AGENT_MEMORY_SHORT_TERM_COMPRESS_AFTER", defaultShortTermCompressAfter),
		CompressionRatio:   defaultShortTermCompressionRatio,
		MergeThreshold:     defaultShortTermMergeThreshold,
		PromotionThreshold: defaultShortTermPromotionThreshold,
		DemotionThreshold:  defaultShortTermDemotionThreshold,
		MaxItems:           env.Int("AGENT_MEMORY_SHORT_TERM_MAX_ITEMS", defaultShortTermMaxItems),
		MaxTokens:          env.Int("AGENT_MEMORY_SHORT_TERM_MAX_TOKENS", defaultShortTermMaxTokens),
		DedupeEnabled:      true,
		DedupeSimilarity:   defaultShortTermDedupeSimilarity,
	}

	mh.policies[MemoryTierLongTerm] = &RetentionPolicy{
		ID:                   "default-long-term",
		Name:                 "Default Long-Term Memory",
		Tier:                 MemoryTierLongTerm,
		DefaultTTL:           env.IntWithZero("AGENT_MEMORY_LONG_TERM_TTL_HOURS", defaultLongTermTTLHours),
		CompressionRatio:     defaultLongTermCompressionRatio,
		MergeThreshold:       defaultLongTermMergeThreshold,
		AccessCountThreshold: defaultLongTermAccessCountThreshold,
		MaxItems:             env.Int("AGENT_MEMORY_LONG_TERM_MAX_ITEMS", defaultLongTermMaxItems),
		MaxTokens:            env.Int("AGENT_MEMORY_LONG_TERM_MAX_TOKENS", defaultLongTermMaxTokens),
		DedupeEnabled:        true,
		DedupeSimilarity:     defaultLongTermDedupeSimilarity,
	}

	return mh
}

// SetSummarizer sets the callback for summarizing content
func (mh *MemoryHierarchy) SetSummarizer(fn func(content string, maxTokens int) (string, error)) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	mh.summarizer = fn
}

// SetEmbedFunc sets the callback for generating embeddings (for deduplication)
func (mh *MemoryHierarchy) SetEmbedFunc(fn func(text string) ([]float64, error)) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	mh.embedFunc = fn
}

// SetDedupeSimilarityThreshold sets the similarity threshold for deduplication
func (mh *MemoryHierarchy) SetDedupeSimilarityThreshold(threshold float64) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	mh.dedupeSimilarityThreshold = threshold
}

// AddItem adds an item to the memory hierarchy
func (mh *MemoryHierarchy) AddItem(item *MemoryItem) error {
	if item.Title == "" {
		return fmt.Errorf("title is required")
	}
	if item.Content == "" {
		return fmt.Errorf("content is required")
	}

	mh.mu.Lock()
	defer mh.mu.Unlock()

	// Generate ID if not provided
	if item.ID == "" {
		item.ID = uuid.New().String()[:12]
	}

	// Set defaults
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.LastAccessedAt = now

	if item.Tier == "" {
		item.Tier = MemoryTierWorking
	}

	if item.Status == "" {
		item.Status = MemoryItemStatusActive
	}

	if item.Importance == "" {
		item.Importance = ImportanceLevelMedium
	}

	// Calculate importance score from level
	if item.ImportanceScore == 0 {
		item.ImportanceScore = importanceLevelToScore(item.Importance)
	}

	// Calculate tokens
	if item.OriginalTokens == 0 {
		item.OriginalTokens = EstimateTokens(item.Content)
	}

	// Set expiry based on policy
	policy := mh.policies[item.Tier]
	if policy != nil && policy.DefaultTTL > 0 && item.ExpiresAt == nil {
		expiry := now.Add(time.Duration(policy.DefaultTTL) * time.Hour)
		item.ExpiresAt = &expiry
	}

	// Add to appropriate tier
	mh.addToTier(item)

	// Update indexes
	mh.indexItem(item)

	return nil
}

func (mh *MemoryHierarchy) addToTier(item *MemoryItem) {
	switch item.Tier {
	case MemoryTierWorking:
		mh.working[item.ID] = item
	case MemoryTierShortTerm:
		mh.shortTerm[item.ID] = item
	case MemoryTierLongTerm:
		mh.longTerm[item.ID] = item
	}
}

func (mh *MemoryHierarchy) removeFromTier(item *MemoryItem) {
	switch item.Tier {
	case MemoryTierWorking:
		delete(mh.working, item.ID)
	case MemoryTierShortTerm:
		delete(mh.shortTerm, item.ID)
	case MemoryTierLongTerm:
		delete(mh.longTerm, item.ID)
	}
}

func (mh *MemoryHierarchy) indexItem(item *MemoryItem) {
	// Index by namespace
	ns := item.Namespace
	if ns == "" {
		ns = "_default"
	}
	if mh.byNamespace[ns] == nil {
		mh.byNamespace[ns] = make(map[string]bool)
	}
	mh.byNamespace[ns][item.ID] = true

	// Index by category
	if item.Category != "" {
		if mh.byCategory[item.Category] == nil {
			mh.byCategory[item.Category] = make(map[string]bool)
		}
		mh.byCategory[item.Category][item.ID] = true
	}

	// Index by session
	if item.SessionID != "" {
		if mh.bySession[item.SessionID] == nil {
			mh.bySession[item.SessionID] = make(map[string]bool)
		}
		mh.bySession[item.SessionID][item.ID] = true
	}
}

func (mh *MemoryHierarchy) removeFromIndexes(item *MemoryItem) {
	ns := item.Namespace
	if ns == "" {
		ns = "_default"
	}
	delete(mh.byNamespace[ns], item.ID)
	if item.Category != "" {
		delete(mh.byCategory[item.Category], item.ID)
	}
	if item.SessionID != "" {
		delete(mh.bySession[item.SessionID], item.ID)
	}
}

// GetItem retrieves an item by ID and updates access tracking
func (mh *MemoryHierarchy) GetItem(id string) (*MemoryItem, error) {
	mh.mu.Lock()
	defer mh.mu.Unlock()

	item := mh.findItem(id)
	if item == nil {
		return nil, fmt.Errorf("memory item not found: %s", id)
	}

	// Update access tracking
	item.LastAccessedAt = time.Now().UTC()
	item.AccessCount++

	return item, nil
}

func (mh *MemoryHierarchy) findItem(id string) *MemoryItem {
	if item, ok := mh.working[id]; ok {
		return item
	}
	if item, ok := mh.shortTerm[id]; ok {
		return item
	}
	if item, ok := mh.longTerm[id]; ok {
		return item
	}
	return nil
}

// UpdateItem updates an existing item
func (mh *MemoryHierarchy) UpdateItem(item *MemoryItem) error {
	mh.mu.Lock()
	defer mh.mu.Unlock()

	existing := mh.findItem(item.ID)
	if existing == nil {
		return fmt.Errorf("memory item not found: %s", item.ID)
	}

	// If tier changed, move between tiers
	if existing.Tier != item.Tier {
		mh.removeFromTier(existing)
		mh.addToTier(item)
	} else {
		// Update in place
		switch item.Tier {
		case MemoryTierWorking:
			mh.working[item.ID] = item
		case MemoryTierShortTerm:
			mh.shortTerm[item.ID] = item
		case MemoryTierLongTerm:
			mh.longTerm[item.ID] = item
		}
	}

	// Update indexes if category/namespace/session changed
	mh.removeFromIndexes(existing)
	mh.indexItem(item)

	return nil
}

// DeleteItem removes an item from memory
func (mh *MemoryHierarchy) DeleteItem(id string) error {
	mh.mu.Lock()
	defer mh.mu.Unlock()

	item := mh.findItem(id)
	if item == nil {
		return fmt.Errorf("memory item not found: %s", id)
	}

	mh.removeFromTier(item)
	mh.removeFromIndexes(item)

	return nil
}

// importanceLevelToScore converts an importance level to a numeric score.
func importanceLevelToScore(level ImportanceLevel) float64 {
	switch level {
	case ImportanceLevelCritical:
		return 1.0
	case ImportanceLevelHigh:
		return 0.75
	case ImportanceLevelMedium:
		return 0.5
	case ImportanceLevelLow:
		return 0.25
	default:
		return 0.5
	}
}
