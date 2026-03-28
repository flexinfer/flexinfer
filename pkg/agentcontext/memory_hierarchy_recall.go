package agentcontext

import (
	"sort"
	"strings"
	"time"
)

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
