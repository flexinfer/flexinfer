package agentcontext

import (
	"math"
	"strings"
	"time"
)

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
