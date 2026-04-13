package agentcontext

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

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

// RunCompression runs automatic compression based on policies.
// Sets the compacting flag so concurrent recall queries can avoid
// reading partially compressed items.
func (mh *MemoryHierarchy) RunCompression(tier MemoryTier) (*CompressionJob, error) {
	mh.compacting.Add(1)
	defer mh.compacting.Add(-1)

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
