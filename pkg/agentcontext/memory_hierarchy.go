package agentcontext

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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

	// Set default policies
	mh.policies[MemoryTierWorking] = &RetentionPolicy{
		ID:                 "default-working",
		Name:               "Default Working Memory",
		Tier:               MemoryTierWorking,
		DefaultTTL:         24,  // 24 hours
		CompressAfterHours: 4,   // Compress after 4 hours
		CompressionRatio:   0.5, // 50% of original
		MaxItems:           1000,
		MaxTokens:          100000,
		DedupeEnabled:      true,
		DedupeSimilarity:   0.9,
	}

	mh.policies[MemoryTierShortTerm] = &RetentionPolicy{
		ID:                 "default-short-term",
		Name:               "Default Short-Term Memory",
		Tier:               MemoryTierShortTerm,
		DefaultTTL:         168, // 7 days
		CompressAfterHours: 24,
		CompressionRatio:   0.3,
		MergeThreshold:     0.8,
		PromotionThreshold: 0.7,
		DemotionThreshold:  0.3,
		MaxItems:           5000,
		MaxTokens:          200000,
		DedupeEnabled:      true,
		DedupeSimilarity:   0.85,
	}

	mh.policies[MemoryTierLongTerm] = &RetentionPolicy{
		ID:                   "default-long-term",
		Name:                 "Default Long-Term Memory",
		Tier:                 MemoryTierLongTerm,
		DefaultTTL:           0, // No expiry
		CompressionRatio:     0.2,
		MergeThreshold:       0.9,
		AccessCountThreshold: 3,
		MaxItems:             10000,
		MaxTokens:            500000,
		DedupeEnabled:        true,
		DedupeSimilarity:     0.95,
	}

	return mh
}

// SetSummarizer sets the callback for summarizing content
func (mh *MemoryHierarchy) SetSummarizer(fn func(content string, maxTokens int) (string, error)) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	mh.summarizer = fn
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

	// Tag filter
	if len(req.Tags) > 0 {
		found := false
		for _, reqTag := range req.Tags {
			for _, itemTag := range item.Tags {
				if itemTag == reqTag {
					found = true
					break
				}
			}
			if found {
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

// CompressItem compresses an item's content
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
	mh.mu.Unlock()

	// Generate summary
	var summary string
	if summarizer != nil {
		targetTokens := int(float64(item.OriginalTokens) * ratio)
		var err error
		summary, err = summarizer(item.Content, targetTokens)
		if err != nil {
			return fmt.Errorf("compression failed: %w", err)
		}
	} else {
		// Simple truncation as fallback
		targetChars := int(float64(len(item.Content)) * ratio)
		if targetChars < len(item.Content) {
			summary = item.Content[:targetChars] + "..."
		} else {
			summary = item.Content
		}
	}

	mh.mu.Lock()
	defer mh.mu.Unlock()

	// Update item
	now := time.Now().UTC()
	item.Summary = summary
	item.CompressedTokens = EstimateTokens(summary)
	item.Status = MemoryItemStatusCompressed
	item.CompressedAt = &now

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

// uniqueStrings defined in service.go
