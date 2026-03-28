package agentcontext

import (
	"sync"
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
