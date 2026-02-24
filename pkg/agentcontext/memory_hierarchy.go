package agentcontext

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
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

// MemoryHierarchy manages tiered memory with automatic compression and retention
type MemoryHierarchy struct {
	mu sync.RWMutex

	// Memory items by tier
	working   map[string]*MemoryItem
	shortTerm map[string]*MemoryItem
	longTerm  map[string]*MemoryItem

	// Indexes
	byNamespace map[string]map[string]bool // namespace -> set of item IDs
	byCategory  map[string]map[string]bool // category -> set of item IDs
	bySession   map[string]map[string]bool // session -> set of item IDs

	// Retention policies
	policies map[MemoryTier]*RetentionPolicy

	// Compression jobs
	compressionJobs map[string]*CompressionJob

	// Summarizer callback (for LLM-based compression)
	summarizer func(content string, maxTokens int) (string, error)

	// Deduplication
	dedupeSimilarityThreshold float64
	embedFunc                 func(text string) ([]float64, error)
}

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

// Recall retrieves items matching the request
func (mh *MemoryHierarchy) Recall(req MemoryRecallRequest) (*MemoryRecallResult, error) {
	mh.mu.RLock()
	defer mh.mu.RUnlock()

	result := &MemoryRecallResult{
		Items:  []MemoryItem{},
		ByTier: make(map[string]int),
	}

	// Determine which tiers to search
	tiers := req.Tiers
	if len(tiers) == 0 {
		tiers = []MemoryTier{MemoryTierWorking, MemoryTierShortTerm, MemoryTierLongTerm}
	}

	// Collect candidates from each tier
	var candidates []*MemoryItem
	for _, tier := range tiers {
		var tierItems map[string]*MemoryItem
		switch tier {
		case MemoryTierWorking:
			tierItems = mh.working
		case MemoryTierShortTerm:
			tierItems = mh.shortTerm
		case MemoryTierLongTerm:
			tierItems = mh.longTerm
		}

		for _, item := range tierItems {
			if mh.matchesRecallRequest(item, req) {
				candidates = append(candidates, item)
			}
		}
	}

	// Sort by importance (descending), then by last accessed (descending)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ImportanceScore != candidates[j].ImportanceScore {
			return candidates[i].ImportanceScore > candidates[j].ImportanceScore
		}
		return candidates[i].LastAccessedAt.After(candidates[j].LastAccessedAt)
	})

	// Apply token budget
	tokenBudget := req.TokenBudget
	if tokenBudget <= 0 {
		tokenBudget = 8000 // Default
	}

	totalTokens := 0
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}

	for _, item := range candidates {
		itemTokens := item.CompressedTokens
		if itemTokens == 0 {
			itemTokens = item.OriginalTokens
		}

		if totalTokens+itemTokens > tokenBudget {
			result.Truncated = true
			break
		}

		result.Items = append(result.Items, *item)
		result.ByTier[string(item.Tier)]++
		totalTokens += itemTokens

		if len(result.Items) >= limit {
			break
		}
	}

	result.TotalTokens = totalTokens
	return result, nil
}

func (mh *MemoryHierarchy) matchesRecallRequest(item *MemoryItem, req MemoryRecallRequest) bool {
	// Skip expired items
	if item.ExpiresAt != nil && item.ExpiresAt.Before(time.Now()) {
		return false
	}

	// Skip archived/expired status
	if item.Status == MemoryItemStatusExpired || item.Status == MemoryItemStatusArchived {
		return false
	}

	// Namespace filter
	if req.Namespace != "" && item.Namespace != req.Namespace {
		return false
	}

	// Session filter
	if req.SessionID != "" && item.SessionID != req.SessionID {
		return false
	}

	// Agent filter
	if req.AgentID != "" && item.AgentID != req.AgentID {
		return false
	}

	// Category filter
	if len(req.Categories) > 0 {
		found := false
		for _, cat := range req.Categories {
			if item.Category == cat {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Tag filter (map lookup for O(1) per tag)
	if len(req.Tags) > 0 {
		tagSet := make(map[string]struct{}, len(item.Tags))
		for _, t := range item.Tags {
			tagSet[t] = struct{}{}
		}
		found := false
		for _, reqTag := range req.Tags {
			if _, ok := tagSet[reqTag]; ok {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Importance filter
	if req.MinImportance > 0 && item.ImportanceScore < req.MinImportance {
		return false
	}

	// Query filter (simple substring match)
	if req.Query != "" {
		queryLower := strings.ToLower(req.Query)
		if !strings.Contains(strings.ToLower(item.Title), queryLower) &&
			!strings.Contains(strings.ToLower(item.Content), queryLower) {
			return false
		}
	}

	return true
}

// PromoteItem promotes an item to a higher tier
func (mh *MemoryHierarchy) PromoteItem(id string) error {
	mh.mu.Lock()
	defer mh.mu.Unlock()

	item := mh.findItem(id)
	if item == nil {
		return fmt.Errorf("memory item not found: %s", id)
	}

	var newTier MemoryTier
	switch item.Tier {
	case MemoryTierWorking:
		newTier = MemoryTierShortTerm
	case MemoryTierShortTerm:
		newTier = MemoryTierLongTerm
	case MemoryTierLongTerm:
		return fmt.Errorf("item is already in long-term memory")
	}

	mh.removeFromTier(item)
	item.Tier = newTier
	mh.addToTier(item)

	// Update expiry based on new tier's policy
	policy := mh.policies[newTier]
	if policy != nil && policy.DefaultTTL > 0 {
		expiry := time.Now().UTC().Add(time.Duration(policy.DefaultTTL) * time.Hour)
		item.ExpiresAt = &expiry
	} else if newTier == MemoryTierLongTerm {
		item.ExpiresAt = nil // Long-term = no expiry
	}

	return nil
}

// DemoteItem demotes an item to a lower tier
func (mh *MemoryHierarchy) DemoteItem(id string) error {
	mh.mu.Lock()
	defer mh.mu.Unlock()

	item := mh.findItem(id)
	if item == nil {
		return fmt.Errorf("memory item not found: %s", id)
	}

	var newTier MemoryTier
	switch item.Tier {
	case MemoryTierWorking:
		return fmt.Errorf("cannot demote from working memory")
	case MemoryTierShortTerm:
		newTier = MemoryTierWorking
	case MemoryTierLongTerm:
		newTier = MemoryTierShortTerm
	}

	mh.removeFromTier(item)
	item.Tier = newTier
	mh.addToTier(item)

	// Update expiry based on new tier's policy
	policy := mh.policies[newTier]
	if policy != nil && policy.DefaultTTL > 0 {
		expiry := time.Now().UTC().Add(time.Duration(policy.DefaultTTL) * time.Hour)
		item.ExpiresAt = &expiry
	}

	return nil
}

// CompressItem compresses an item's content using LLM or fallback extractive compression
func (mh *MemoryHierarchy) CompressItem(id string) error {
	mh.mu.Lock()
	item := mh.findItem(id)
	if item == nil {
		mh.mu.Unlock()
		return fmt.Errorf("memory item not found: %s", id)
	}

	if item.Status == MemoryItemStatusCompressed {
		mh.mu.Unlock()
		return nil // Already compressed
	}

	// Get policy for compression ratio
	policy := mh.policies[item.Tier]
	ratio := 0.5
	if policy != nil && policy.CompressionRatio > 0 {
		ratio = policy.CompressionRatio
	}

	summarizer := mh.summarizer
	content := item.Content
	mh.mu.Unlock()

	// Use fallback compression with LLM if available
	result := CompressWithFallback(content, ratio, summarizer)

	mh.mu.Lock()
	defer mh.mu.Unlock()

	// Re-find item after unlocking (safety check)
	item = mh.findItem(id)
	if item == nil {
		return fmt.Errorf("memory item not found: %s", id)
	}

	// Update item
	now := time.Now().UTC()
	item.Summary = result.Summary
	item.CompressedTokens = EstimateTokens(result.Summary)
	item.Status = MemoryItemStatusCompressed
	item.CompressedAt = &now

	// Store compression metadata
	if item.Metadata == nil {
		item.Metadata = make(map[string]any)
	}
	item.Metadata["compression_method"] = string(result.Method)
	item.Metadata["compression_ratio"] = result.Ratio
	if len(result.Keywords) > 0 {
		item.Metadata["keywords"] = result.Keywords
	}

	return nil
}

// MergeItems merges multiple items into one
func (mh *MemoryHierarchy) MergeItems(ids []string, newTitle string) (*MemoryItem, error) {
	if len(ids) < 2 {
		return nil, fmt.Errorf("need at least 2 items to merge")
	}

	mh.mu.Lock()
	defer mh.mu.Unlock()

	// Collect items
	var items []*MemoryItem
	var contents []string
	var highestImportance = ImportanceLevelLow
	var highestScore float64
	var allTags []string
	var tier MemoryTier

	for _, id := range ids {
		item := mh.findItem(id)
		if item == nil {
			return nil, fmt.Errorf("memory item not found: %s", id)
		}
		items = append(items, item)
		contents = append(contents, fmt.Sprintf("## %s\n%s", item.Title, item.Content))

		if item.ImportanceScore > highestScore {
			highestScore = item.ImportanceScore
			highestImportance = item.Importance
		}

		allTags = append(allTags, item.Tags...)

		if tier == "" {
			tier = item.Tier
		}
	}

	// Create merged item
	now := time.Now().UTC()
	merged := &MemoryItem{
		ID:              uuid.New().String()[:12],
		Tier:            tier,
		Status:          MemoryItemStatusActive,
		Importance:      highestImportance,
		ImportanceScore: highestScore,
		Title:           newTitle,
		Content:         strings.Join(contents, "\n\n"),
		Category:        items[0].Category,
		Tags:            uniqueStrings(allTags),
		Namespace:       items[0].Namespace,
		SessionID:       items[0].SessionID,
		AgentID:         items[0].AgentID,
		CreatedAt:       now,
		LastAccessedAt:  now,
		OriginalTokens:  EstimateTokens(strings.Join(contents, "\n\n")),
	}

	// Set child IDs
	for _, item := range items {
		merged.ChildIDs = append(merged.ChildIDs, item.ID)
	}

	// Add merged item
	mh.addToTier(merged)
	mh.indexItem(merged)

	// Archive original items
	for _, item := range items {
		item.Status = MemoryItemStatusArchived
		item.ArchivedAt = &now
		item.ParentID = merged.ID
	}

	return merged, nil
}

// RunCompression runs automatic compression based on policies
func (mh *MemoryHierarchy) RunCompression(tier MemoryTier) (*CompressionJob, error) {
	mh.mu.Lock()

	job := &CompressionJob{
		ID:        uuid.New().String()[:8],
		Tier:      tier,
		Status:    "running",
		CreatedAt: time.Now().UTC(),
	}
	started := time.Now().UTC()
	job.StartedAt = &started
	mh.compressionJobs[job.ID] = job

	policy := mh.policies[tier]
	if policy == nil {
		mh.mu.Unlock()
		return nil, fmt.Errorf("no policy for tier: %s", tier)
	}

	// Get items for this tier
	var tierItems map[string]*MemoryItem
	switch tier {
	case MemoryTierWorking:
		tierItems = mh.working
	case MemoryTierShortTerm:
		tierItems = mh.shortTerm
	case MemoryTierLongTerm:
		tierItems = mh.longTerm
	}

	var toCompress []*MemoryItem
	var toExpire []*MemoryItem
	now := time.Now().UTC()

	for _, item := range tierItems {
		// Check expiry
		if item.ExpiresAt != nil && item.ExpiresAt.Before(now) {
			toExpire = append(toExpire, item)
			continue
		}

		// Check compression eligibility
		if item.Status == MemoryItemStatusActive && policy.CompressAfterHours > 0 {
			compressThreshold := item.CreatedAt.Add(time.Duration(policy.CompressAfterHours) * time.Hour)
			if now.After(compressThreshold) {
				toCompress = append(toCompress, item)
			}
		}
	}

	job.ItemCount = len(toCompress) + len(toExpire)
	mh.mu.Unlock()

	// Process expirations
	for _, item := range toExpire {
		mh.mu.Lock()
		item.Status = MemoryItemStatusExpired
		job.ExpiredCount++
		mh.mu.Unlock()
	}

	// Process compressions
	for _, item := range toCompress {
		originalTokens := item.OriginalTokens
		if err := mh.CompressItem(item.ID); err == nil {
			mh.mu.Lock()
			job.OriginalTokens += originalTokens
			job.CompressedTokens += item.CompressedTokens
			mh.mu.Unlock()
		}
	}

	mh.mu.Lock()
	completed := time.Now().UTC()
	job.CompletedAt = &completed
	job.Status = "completed"
	mh.mu.Unlock()

	return job, nil
}

// Stats returns memory hierarchy statistics
func (mh *MemoryHierarchy) Stats() MemoryHierarchyStats {
	mh.mu.RLock()
	defer mh.mu.RUnlock()

	stats := MemoryHierarchyStats{
		WorkingMemory:   mh.tierStats(mh.working, MemoryTierWorking),
		ShortTermMemory: mh.tierStats(mh.shortTerm, MemoryTierShortTerm),
		LongTermMemory:  mh.tierStats(mh.longTerm, MemoryTierLongTerm),
	}

	stats.TotalItems = stats.WorkingMemory.ItemCount + stats.ShortTermMemory.ItemCount + stats.LongTermMemory.ItemCount
	stats.TotalTokens = stats.WorkingMemory.TokenCount + stats.ShortTermMemory.TokenCount + stats.LongTermMemory.TokenCount

	// Calculate overall compression ratio
	var originalTotal, compressedTotal int
	for _, item := range mh.working {
		originalTotal += item.OriginalTokens
		if item.CompressedTokens > 0 {
			compressedTotal += item.CompressedTokens
		} else {
			compressedTotal += item.OriginalTokens
		}
	}
	for _, item := range mh.shortTerm {
		originalTotal += item.OriginalTokens
		if item.CompressedTokens > 0 {
			compressedTotal += item.CompressedTokens
		} else {
			compressedTotal += item.OriginalTokens
		}
	}
	for _, item := range mh.longTerm {
		originalTotal += item.OriginalTokens
		if item.CompressedTokens > 0 {
			compressedTotal += item.CompressedTokens
		} else {
			compressedTotal += item.OriginalTokens
		}
	}

	if originalTotal > 0 {
		stats.CompressionRatio = float64(compressedTotal) / float64(originalTotal)
	}

	// Count recent activity
	last24h := time.Now().Add(-24 * time.Hour)
	for _, item := range mh.working {
		if item.CreatedAt.After(last24h) {
			stats.ItemsAddedLast24h++
		}
		if item.CompressedAt != nil && item.CompressedAt.After(last24h) {
			stats.ItemsCompressedLast24h++
		}
	}
	for _, item := range mh.shortTerm {
		if item.CreatedAt.After(last24h) {
			stats.ItemsAddedLast24h++
		}
		if item.CompressedAt != nil && item.CompressedAt.After(last24h) {
			stats.ItemsCompressedLast24h++
		}
	}

	return stats
}

func (mh *MemoryHierarchy) tierStats(items map[string]*MemoryItem, tier MemoryTier) MemoryTierStats {
	stats := MemoryTierStats{
		Tier:         tier,
		ByCategory:   make(map[string]int),
		ByImportance: make(map[string]int),
	}

	var totalImportance float64
	var oldest, newest time.Time

	for _, item := range items {
		if item.Status == MemoryItemStatusExpired || item.Status == MemoryItemStatusArchived {
			continue
		}

		stats.ItemCount++
		if item.CompressedTokens > 0 {
			stats.TokenCount += item.CompressedTokens
		} else {
			stats.TokenCount += item.OriginalTokens
		}
		totalImportance += item.ImportanceScore

		if item.Category != "" {
			stats.ByCategory[item.Category]++
		}
		stats.ByImportance[string(item.Importance)]++

		if oldest.IsZero() || item.CreatedAt.Before(oldest) {
			oldest = item.CreatedAt
		}
		if newest.IsZero() || item.CreatedAt.After(newest) {
			newest = item.CreatedAt
		}
	}

	if stats.ItemCount > 0 {
		stats.AvgImportance = totalImportance / float64(stats.ItemCount)
		stats.OldestItem = &oldest
		stats.NewestItem = &newest
	}

	return stats
}

// SetRetentionPolicy sets or updates a retention policy
func (mh *MemoryHierarchy) SetRetentionPolicy(policy *RetentionPolicy) {
	mh.mu.Lock()
	defer mh.mu.Unlock()

	now := time.Now().UTC()
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = now
	}
	policy.UpdatedAt = now

	mh.policies[policy.Tier] = policy
}

// GetRetentionPolicy returns the policy for a tier
func (mh *MemoryHierarchy) GetRetentionPolicy(tier MemoryTier) *RetentionPolicy {
	mh.mu.RLock()
	defer mh.mu.RUnlock()
	return mh.policies[tier]
}

// Helper functions

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

// cosineSimilarity calculates cosine similarity between two vectors
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// DeduplicationResult contains the result of a deduplication check
type DeduplicationResult struct {
	IsDuplicate bool    `json:"is_duplicate"`
	ExistingID  string  `json:"existing_id,omitempty"`
	Similarity  float64 `json:"similarity,omitempty"`
	Action      string  `json:"action"` // "add", "skip", "merge"
}

// CheckDuplicate checks if an item is a duplicate of an existing item
// Returns the existing item ID and similarity if found
func (mh *MemoryHierarchy) CheckDuplicate(item *MemoryItem, embedding []float64) DeduplicationResult {
	mh.mu.RLock()
	defer mh.mu.RUnlock()

	// Get policy for this tier
	policy := mh.policies[item.Tier]
	if policy == nil || !policy.DedupeEnabled {
		return DeduplicationResult{Action: "add"}
	}

	threshold := policy.DedupeSimilarity
	if mh.dedupeSimilarityThreshold > 0 {
		threshold = mh.dedupeSimilarityThreshold
	}
	if threshold <= 0 {
		threshold = 0.9 // Default 90% similarity
	}

	// Check items in the same tier
	var tierItems map[string]*MemoryItem
	switch item.Tier {
	case MemoryTierWorking:
		tierItems = mh.working
	case MemoryTierShortTerm:
		tierItems = mh.shortTerm
	case MemoryTierLongTerm:
		tierItems = mh.longTerm
	}

	var bestMatch *MemoryItem
	var bestSimilarity float64

	for _, existing := range tierItems {
		// Skip archived/expired items
		if existing.Status == MemoryItemStatusArchived || existing.Status == MemoryItemStatusExpired {
			continue
		}

		// Skip same namespace/session requirement for stricter dedup
		if item.Namespace != "" && existing.Namespace != item.Namespace {
			continue
		}

		// Calculate similarity
		var similarity float64

		// If we have embeddings, use cosine similarity
		if len(embedding) > 0 && len(existing.Embedding) > 0 {
			// Convert []float32 to []float64 for comparison
			existingEmbed := make([]float64, len(existing.Embedding))
			for i, v := range existing.Embedding {
				existingEmbed[i] = float64(v)
			}
			similarity = cosineSimilarity(embedding, existingEmbed)
		} else {
			// Fall back to text similarity (Jaccard on words)
			similarity = textSimilarity(item.Content, existing.Content)
		}

		if similarity >= threshold && similarity > bestSimilarity {
			bestSimilarity = similarity
			bestMatch = existing
		}
	}

	if bestMatch != nil {
		return DeduplicationResult{
			IsDuplicate: true,
			ExistingID:  bestMatch.ID,
			Similarity:  bestSimilarity,
			Action:      "skip", // Or "merge" based on business logic
		}
	}

	return DeduplicationResult{Action: "add"}
}

// textSimilarity calculates text similarity using Jaccard coefficient on words
func textSimilarity(a, b string) float64 {
	wordsA := tokenize(a)
	wordsB := tokenize(b)

	if len(wordsA) == 0 && len(wordsB) == 0 {
		return 1.0
	}
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0.0
	}

	setA := make(map[string]bool)
	for _, w := range wordsA {
		setA[strings.ToLower(w)] = true
	}

	setB := make(map[string]bool)
	for _, w := range wordsB {
		setB[strings.ToLower(w)] = true
	}

	// Calculate Jaccard coefficient
	intersection := 0
	for w := range setA {
		if setB[w] {
			intersection++
		}
	}

	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}

// AddItemWithDedup adds an item with deduplication check
// Returns the deduplication result
func (mh *MemoryHierarchy) AddItemWithDedup(item *MemoryItem, embedding []float64) (DeduplicationResult, error) {
	// Check for duplicates first
	result := mh.CheckDuplicate(item, embedding)

	if result.IsDuplicate {
		// Track dedup in metadata
		if item.Metadata == nil {
			item.Metadata = make(map[string]any)
		}
		item.Metadata["dedup_check"] = map[string]any{
			"is_duplicate": true,
			"existing_id":  result.ExistingID,
			"similarity":   result.Similarity,
			"action":       result.Action,
		}

		// If we should skip, return without adding
		if result.Action == "skip" {
			// Update access count on existing item to track interest
			mh.mu.Lock()
			existing := mh.findItem(result.ExistingID)
			if existing != nil {
				existing.AccessCount++
				existing.LastAccessedAt = time.Now().UTC()
			}
			mh.mu.Unlock()
			return result, nil
		}
	}

	// Add the item
	if err := mh.AddItem(item); err != nil {
		return result, err
	}

	// Store embedding if provided
	if len(embedding) > 0 {
		mh.mu.Lock()
		if existingItem := mh.findItem(item.ID); existingItem != nil {
			existingItem.Embedding = make([]float32, len(embedding))
			for i, v := range embedding {
				existingItem.Embedding[i] = float32(v)
			}
		}
		mh.mu.Unlock()
	}

	result.Action = "add"
	return result, nil
}

// uniqueStrings defined in service.go

// =========================================================================
// Persistence Layer (Phase 1.3)
// =========================================================================

// MemoryPersistenceConfig holds Qdrant client for memory persistence
type MemoryPersistenceConfig struct {
	MemoryQdrant *QdrantClient
	EmbedModel   string
	VectorSize   int
}

// persistedMemoryHierarchy wraps MemoryHierarchy with Qdrant persistence
type persistedMemoryHierarchy struct {
	*MemoryHierarchy
	cfg *MemoryPersistenceConfig
}

// SetPersistence configures Qdrant persistence for memory hierarchy
func (mh *MemoryHierarchy) SetPersistence(cfg *MemoryPersistenceConfig) *persistedMemoryHierarchy {
	return &persistedMemoryHierarchy{
		MemoryHierarchy: mh,
		cfg:             cfg,
	}
}

// PersistItem saves a memory item to Qdrant
func (pmh *persistedMemoryHierarchy) PersistItem(ctx context.Context, item *MemoryItem, vector []float64) error {
	if pmh.cfg == nil || pmh.cfg.MemoryQdrant == nil {
		return nil // No persistence configured
	}

	// Ensure collection exists
	vectorSize := pmh.cfg.VectorSize
	if vectorSize <= 0 {
		vectorSize = 4 // Minimal size if embeddings not configured
	}
	if err := pmh.cfg.MemoryQdrant.EnsureCollection(ctx, vectorSize); err != nil {
		return fmt.Errorf("ensure memory collection: %w", err)
	}

	// Use zero vector if not provided
	if len(vector) == 0 {
		vector = make([]float64, vectorSize)
	}

	point := Point{
		ID:      item.ID,
		Vector:  vector,
		Payload: MemoryItemToPayload(*item, pmh.cfg.EmbedModel),
	}

	if err := pmh.cfg.MemoryQdrant.Upsert(ctx, []Point{point}, true); err != nil {
		return fmt.Errorf("persist memory item: %w", err)
	}

	return nil
}

// DeletePersistedItem removes a memory item from Qdrant
func (pmh *persistedMemoryHierarchy) DeletePersistedItem(ctx context.Context, id string) error {
	if pmh.cfg == nil || pmh.cfg.MemoryQdrant == nil {
		return nil
	}
	return pmh.cfg.MemoryQdrant.Delete(ctx, []string{id})
}

// LoadMemoryFromQdrant loads all memory items from Qdrant into memory
func (pmh *persistedMemoryHierarchy) LoadMemoryFromQdrant(ctx context.Context) error {
	if pmh.cfg == nil || pmh.cfg.MemoryQdrant == nil {
		return nil
	}

	exists, err := pmh.cfg.MemoryQdrant.CollectionExists(ctx)
	if err != nil {
		return fmt.Errorf("check memory collection: %w", err)
	}
	if !exists {
		return nil
	}

	points, err := pmh.cfg.MemoryQdrant.ScrollPoints(ctx, nil, 10000, false)
	if err != nil {
		return fmt.Errorf("load memory items: %w", err)
	}

	pmh.mu.Lock()
	defer pmh.mu.Unlock()

	for _, p := range points {
		item, err := PayloadToMemoryItem(p.Payload)
		if err != nil || item == nil {
			continue
		}

		// Add to appropriate tier
		pmh.addToTier(item)
		pmh.indexItem(item)
	}

	return nil
}

// AddItemWithPersistence adds an item and persists it to Qdrant
func (pmh *persistedMemoryHierarchy) AddItemWithPersistence(ctx context.Context, item *MemoryItem, vector []float64) error {
	// Add to in-memory hierarchy first
	if err := pmh.AddItem(item); err != nil {
		return err
	}

	// Persist to Qdrant
	if err := pmh.PersistItem(ctx, item, vector); err != nil {
		// Rollback in-memory change on persistence failure
		pmh.mu.Lock()
		pmh.removeFromTier(item)
		pmh.removeFromIndexes(item)
		pmh.mu.Unlock()
		return err
	}

	return nil
}

// UpdateItemWithPersistence updates an item and persists changes
func (pmh *persistedMemoryHierarchy) UpdateItemWithPersistence(ctx context.Context, item *MemoryItem, vector []float64) error {
	if err := pmh.UpdateItem(item); err != nil {
		return err
	}
	return pmh.PersistItem(ctx, item, vector)
}

// DeleteItemWithPersistence deletes an item and removes from Qdrant
func (pmh *persistedMemoryHierarchy) DeleteItemWithPersistence(ctx context.Context, id string) error {
	if err := pmh.DeleteItem(id); err != nil {
		return err
	}
	return pmh.DeletePersistedItem(ctx, id)
}

// PromoteItemWithPersistence promotes an item and persists the change
func (pmh *persistedMemoryHierarchy) PromoteItemWithPersistence(ctx context.Context, id string) error {
	if err := pmh.PromoteItem(id); err != nil {
		return err
	}

	// Get the item to persist
	pmh.mu.RLock()
	item := pmh.findItem(id)
	pmh.mu.RUnlock()

	if item != nil {
		return pmh.PersistItem(ctx, item, nil)
	}
	return nil
}

// DemoteItemWithPersistence demotes an item and persists the change
func (pmh *persistedMemoryHierarchy) DemoteItemWithPersistence(ctx context.Context, id string) error {
	if err := pmh.DemoteItem(id); err != nil {
		return err
	}

	// Get the item to persist
	pmh.mu.RLock()
	item := pmh.findItem(id)
	pmh.mu.RUnlock()

	if item != nil {
		return pmh.PersistItem(ctx, item, nil)
	}
	return nil
}

// CompressItemWithPersistence compresses an item and persists the change
func (pmh *persistedMemoryHierarchy) CompressItemWithPersistence(ctx context.Context, id string) error {
	if err := pmh.CompressItem(id); err != nil {
		return err
	}

	// Get the item to persist
	pmh.mu.RLock()
	item := pmh.findItem(id)
	pmh.mu.RUnlock()

	if item != nil {
		return pmh.PersistItem(ctx, item, nil)
	}
	return nil
}

// MergeItemsWithPersistence merges items and persists changes
func (pmh *persistedMemoryHierarchy) MergeItemsWithPersistence(ctx context.Context, ids []string, newTitle string, vector []float64) (*MemoryItem, error) {
	merged, err := pmh.MergeItems(ids, newTitle)
	if err != nil {
		return nil, err
	}

	// Persist the merged item
	if err := pmh.PersistItem(ctx, merged, vector); err != nil {
		return merged, fmt.Errorf("persist merged item: %w", err)
	}

	// Persist archived items
	for _, id := range ids {
		pmh.mu.RLock()
		item := pmh.findItem(id)
		pmh.mu.RUnlock()
		if item != nil && item.Status == MemoryItemStatusArchived {
			if err := pmh.PersistItem(ctx, item, nil); err != nil {
				// Non-fatal
				fmt.Printf("warning: failed to persist archived item: %v\n", err)
			}
		}
	}

	return merged, nil
}

// SearchMemorySemantic performs semantic search for memory items
func (pmh *persistedMemoryHierarchy) SearchMemorySemantic(ctx context.Context, vector []float64, limit int, tier MemoryTier, namespace string) ([]*MemoryItem, error) {
	if pmh.cfg == nil || pmh.cfg.MemoryQdrant == nil {
		return nil, fmt.Errorf("no persistence configured for semantic search")
	}

	// Build filter
	var conds []any
	if tier != "" {
		conds = append(conds, Match("tier", string(tier)))
	}
	if namespace != "" {
		conds = append(conds, Match("namespace", namespace))
	}
	// Exclude expired/archived items
	conds = append(conds, map[string]any{
		"key": "status",
		"match": map[string]any{
			"any": []string{string(MemoryItemStatusActive), string(MemoryItemStatusCompressed)},
		},
	})

	filter := FilterMust(conds...)

	// Search
	type searchResult struct {
		ID      string         `json:"id"`
		Score   float64        `json:"score"`
		Payload map[string]any `json:"payload"`
	}

	path := fmt.Sprintf("/collections/%s/points/search", pmh.cfg.MemoryQdrant.collection)
	body := map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": true,
		"filter":       filter,
	}

	var resp struct {
		Result []searchResult `json:"result"`
	}
	if err := pmh.cfg.MemoryQdrant.doJSON(ctx, "POST", path, body, &resp); err != nil {
		return nil, err
	}

	items := make([]*MemoryItem, 0, len(resp.Result))
	for _, hit := range resp.Result {
		item, err := PayloadToMemoryItem(hit.Payload)
		if err != nil || item == nil {
			continue
		}
		items = append(items, item)
	}

	return items, nil
}
