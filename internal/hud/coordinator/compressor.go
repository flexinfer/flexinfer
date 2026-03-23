package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

const (
	compactionTierTokensFloor         = 1000
	compactionPromptPressureTierFloor = 500
)

// CompactionResult holds the results of a compaction cycle.
type CompactionResult struct {
	Tier                    string `json:"tier"`
	Trigger                 string `json:"trigger,omitempty"`
	CompressedCount         int    `json:"compressed_count"`
	MergedCount             int    `json:"merged_count"`
	TokensSaved             int    `json:"tokens_saved"`
	PressureSessionID       string `json:"pressure_session_id,omitempty"`
	PressureAgentID         string `json:"pressure_agent_id,omitempty"`
	PressureNamespace       string `json:"pressure_namespace,omitempty"`
	PressureEstimatedTokens int    `json:"pressure_estimated_tokens,omitempty"`
	PromptTokensBefore      int    `json:"prompt_tokens_before,omitempty"`
	PromptTokensAfter       int    `json:"prompt_tokens_after,omitempty"`
	PromptTokensDelta       int    `json:"prompt_tokens_delta,omitempty"`
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

type compactionPressureSignal struct {
	SessionID       string
	AgentID         string
	Namespace       string
	EstimatedTokens int
}

// Compressor handles LLM-powered memory compression and merging.
type Compressor struct {
	client *FlexInferClient
	agent  *bridge.AgentBridge
	config Config
	model  string // Resolved model from selectModel().
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

	model := c.model
	if model == "" {
		model = c.config.DefaultModel
	}
	raw, err := c.client.CompleteSimple(ctx, model, promptMemoryCompress, userMsg, 300)
	if err != nil {
		return nil, fmt.Errorf("compress item %s: %w", item.ID, err)
	}

	var result CompressionResult
	if err := decodeStructuredJSON(raw, &result); err != nil {
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

	mergeModel := c.model
	if mergeModel == "" {
		mergeModel = c.config.DefaultModel
	}
	raw, err := c.client.CompleteSimple(ctx, mergeModel, promptMergeSuggestions, userMsg, 500)
	if err != nil {
		return nil, fmt.Errorf("suggest merges: %w", err)
	}

	var result struct {
		MergeGroups []MergeSuggestion `json:"merge_groups"`
	}
	if err := decodeStructuredJSON(raw, &result); err != nil {
		return nil, fmt.Errorf("parse merge suggestions: %w", err)
	}

	return result.MergeGroups, nil
}

// RunCompactionCycle checks memory stats and compresses oversized tiers.
// It limits work per cycle to avoid storming the LLM backend.
func (c *Compressor) RunCompactionCycle(ctx context.Context) (*CompactionResult, error) {
	pressureSignal, err := c.detectPromptPressure(ctx)
	if err != nil {
		c.logger.Debug("prompt pressure detection failed", "error", err)
	}

	stats, err := c.agent.MemoryStats()
	if err != nil {
		return nil, fmt.Errorf("fetch memory stats: %w", err)
	}

	// Find the most overloaded tier.
	tier, tierTokens := mostLoadedTier(stats)
	minTierTokens := compactionTierTokensFloor
	trigger := "memory_overload"
	if pressureSignal != nil {
		minTierTokens = compactionPromptPressureTierFloor
		trigger = "prompt_pressure"
	}
	if tier == "" || tierTokens < minTierTokens {
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

	result := &CompactionResult{
		Tier:    tier,
		Trigger: trigger,
	}
	if pressureSignal != nil {
		result.PressureSessionID = pressureSignal.SessionID
		result.PressureAgentID = pressureSignal.AgentID
		result.PressureNamespace = pressureSignal.Namespace
		result.PressureEstimatedTokens = pressureSignal.EstimatedTokens
		result.PromptTokensBefore = pressureSignal.EstimatedTokens
	}

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

	if err := c.recordPromptDelta(ctx, result); err != nil {
		c.logger.Debug("post-compaction prompt inspection failed",
			"session_id", result.PressureSessionID,
			"error", err,
		)
	}

	return result, nil
}

func (c *Compressor) recordPromptDelta(ctx context.Context, result *CompactionResult) error {
	if c == nil || c.agent == nil || result == nil {
		return nil
	}
	if result.PressureSessionID == "" || result.PressureAgentID == "" {
		return nil
	}
	if result.CompressedCount == 0 && result.MergedCount == 0 {
		return nil
	}
	inspect, err := c.agent.ContextInspect(result.PressureAgentID, result.PressureSessionID, false, 200)
	if err != nil {
		return err
	}
	if inspect == nil {
		return nil
	}
	updatePromptDelta(result, inspect)
	return nil
}

func updatePromptDelta(result *CompactionResult, inspect *bridge.ContextInspectResult) {
	if result == nil || inspect == nil {
		return
	}
	result.PromptTokensAfter = inspect.EstimatedTokens
	result.PromptTokensDelta = result.PromptTokensBefore - result.PromptTokensAfter
}

func (c *Compressor) detectPromptPressure(ctx context.Context) (*compactionPressureSignal, error) {
	if c == nil || c.agent == nil {
		return nil, nil
	}

	threshold := c.config.CompactionPromptTokenThreshold
	if threshold <= 0 {
		return nil, nil
	}
	maxSessions := c.config.CompactionInspectSessions
	if maxSessions <= 0 {
		maxSessions = 3
	}

	sessions, err := c.agent.Sessions()
	if err != nil {
		return nil, err
	}

	return selectPromptPressureCandidate(ctx, sessions, threshold, maxSessions, func(session bridge.SessionInfo) (*bridge.ContextInspectResult, error) {
		return c.agent.ContextInspect(session.AgentID, session.ID, false, 200)
	})
}

func selectPromptPressureCandidate(
	ctx context.Context,
	sessions []bridge.SessionInfo,
	threshold int,
	maxSessions int,
	inspect func(bridge.SessionInfo) (*bridge.ContextInspectResult, error),
) (*compactionPressureSignal, error) {
	if threshold <= 0 || maxSessions <= 0 || inspect == nil {
		return nil, nil
	}

	active := make([]bridge.SessionInfo, 0, len(sessions))
	for _, session := range sessions {
		if strings.EqualFold(strings.TrimSpace(session.Status), "active") {
			active = append(active, session)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].TotalTokens == active[j].TotalTokens {
			if active[i].EntryCount == active[j].EntryCount {
				return active[i].StartedAt > active[j].StartedAt
			}
			return active[i].EntryCount > active[j].EntryCount
		}
		return active[i].TotalTokens > active[j].TotalTokens
	})
	if len(active) > maxSessions {
		active = active[:maxSessions]
	}

	var best *compactionPressureSignal
	for _, session := range active {
		if err := ctx.Err(); err != nil {
			return best, err
		}
		inspectResult, err := inspect(session)
		if err != nil || inspectResult == nil {
			continue
		}
		if inspectResult.EstimatedTokens < threshold {
			continue
		}
		if best == nil || inspectResult.EstimatedTokens > best.EstimatedTokens {
			best = &compactionPressureSignal{
				SessionID:       session.ID,
				AgentID:         session.AgentID,
				Namespace:       session.Namespace,
				EstimatedTokens: inspectResult.EstimatedTokens,
			}
		}
	}
	return best, nil
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
