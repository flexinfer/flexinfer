package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// CompactionResult holds the results of a compaction cycle.
type CompactionResult struct {
	Tier            string `json:"tier"`
	CompressedCount int    `json:"compressed_count"`
	MergedCount     int    `json:"merged_count"`
	TokensSaved     int    `json:"tokens_saved"`
}

// CompressionResult holds the output of compressing a single item.
type CompressionResult struct {
	Compressed string   `json:"compressed"`
	Keywords   []string `json:"keywords"`
	Importance string   `json:"importance"`
}

// MergeSuggestion describes a group of items that should be merged.
type MergeSuggestion struct {
	IDs           []string `json:"ids"`
	Reason        string   `json:"reason"`
	MergedTitle   string   `json:"merged_title"`
	MergedContent string   `json:"merged_content"`
}

// Compressor handles LLM-powered memory compression and merging.
type Compressor struct {
	client *FlexInferClient
	agent  *bridge.AgentBridge
	config Config
	logger *slog.Logger
}

// NewCompressor creates a Compressor.
func NewCompressor(client *FlexInferClient, agent *bridge.AgentBridge, cfg Config, logger *slog.Logger) *Compressor {
	return &Compressor{
		client: client,
		agent:  agent,
		config: cfg,
		logger: logger.With("subsystem", "compressor"),
	}
}

// CompressItem compresses a single memory item's content using the LLM.
func (c *Compressor) CompressItem(ctx context.Context, item bridge.MemoryItem) (*CompressionResult, error) {
	userMsg := fmt.Sprintf("Title: %s\nTier: %s\nImportance: %s\n\nContent:\n%s",
		item.Title, item.Tier, item.Importance, item.Content)

	raw, err := c.client.CompleteSimple(ctx, c.config.DefaultModel, promptMemoryCompress, userMsg, 300)
	if err != nil {
		return nil, fmt.Errorf("compress item %s: %w", item.ID, err)
	}

	raw = stripCodeFence(raw)
	var result CompressionResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("parse compression result: %w", err)
	}

	return &result, nil
}

// SuggestMerges analyzes a batch of memory items for merge candidates.
func (c *Compressor) SuggestMerges(ctx context.Context, items []bridge.MemoryItem) ([]MergeSuggestion, error) {
	if len(items) < 2 {
		return nil, nil
	}

	var userMsg string
	for _, item := range items {
		content := item.Content
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		userMsg += fmt.Sprintf("ID: %s\nTitle: %s\nContent: %s\n\n", item.ID, item.Title, content)
	}

	raw, err := c.client.CompleteSimple(ctx, c.config.DefaultModel, promptMergeSuggestions, userMsg, 500)
	if err != nil {
		return nil, fmt.Errorf("suggest merges: %w", err)
	}

	raw = stripCodeFence(raw)
	var result struct {
		MergeGroups []MergeSuggestion `json:"merge_groups"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("parse merge suggestions: %w", err)
	}

	return result.MergeGroups, nil
}

// RunCompactionCycle checks memory stats and compresses oversized tiers.
// It limits work per cycle to avoid storming the LLM backend.
func (c *Compressor) RunCompactionCycle(ctx context.Context) (*CompactionResult, error) {
	stats, err := c.agent.MemoryStats()
	if err != nil {
		return nil, fmt.Errorf("fetch memory stats: %w", err)
	}

	// Find the most overloaded tier.
	tier, tierTokens := mostLoadedTier(stats)
	if tier == "" || tierTokens < 1000 {
		return nil, nil // Nothing to compress.
	}

	maxItems := c.config.MaxCompressItems
	if maxItems <= 0 {
		maxItems = 3 // Safety default.
	}

	items, err := c.agent.MemoryRecall(tier, "", 20)
	if err != nil {
		return nil, fmt.Errorf("recall tier %s: %w", tier, err)
	}
	if len(items) == 0 {
		return nil, nil
	}

	result := &CompactionResult{Tier: tier}

	// Compress individual oversized items — capped to avoid LLM storms.
	for _, item := range items {
		if result.CompressedCount >= maxItems {
			break
		}
		if ctx.Err() != nil {
			break
		}
		if item.Tokens < 100 || item.Content == "" {
			continue
		}

		compressed, err := c.CompressItem(ctx, item)
		if err != nil {
			c.logger.Debug("compress item failed", "id", item.ID, "error", err)
			continue
		}

		// Store compressed version and delete original.
		importance := compressed.Importance
		if importance == "" {
			importance = item.Importance
		}
		if err := c.agent.MemoryAdd(item.Title, compressed.Compressed, item.Tier, importance, item.Category); err != nil {
			c.logger.Debug("store compressed item failed", "id", item.ID, "error", err)
			continue
		}
		if err := c.agent.MemoryDelete(item.ID); err != nil {
			c.logger.Debug("delete original item failed", "id", item.ID, "error", err)
		}

		result.CompressedCount++
		result.TokensSaved += item.Tokens - len(compressed.Compressed)/4 // Rough token estimate
	}

	// Suggest merges only if we have context budget remaining and items to merge.
	if len(items) >= 3 && ctx.Err() == nil {
		merges, err := c.SuggestMerges(ctx, items)
		if err == nil {
			for _, merge := range merges {
				if len(merge.IDs) < 2 {
					continue
				}
				// Create merged item.
				if err := c.agent.MemoryAdd(merge.MergedTitle, merge.MergedContent, tier, "medium", "merged"); err != nil {
					continue
				}
				// Delete originals.
				for _, id := range merge.IDs {
					_ = c.agent.MemoryDelete(id)
				}
				result.MergedCount++
			}
		}
	}

	return result, nil
}

// mostLoadedTier returns the tier name and token count of the most loaded tier.
func mostLoadedTier(stats *bridge.MemoryStatsResult) (string, int) {
	type tierInfo struct {
		name   string
		tokens int
	}
	tiers := []tierInfo{
		{"working", stats.WorkingMemory.Tokens},
		{"short_term", stats.ShortTermMemory.Tokens},
		{"long_term", stats.LongTermMemory.Tokens},
	}

	var best tierInfo
	for _, t := range tiers {
		if t.tokens > best.tokens {
			best = t
		}
	}
	return best.name, best.tokens
}
