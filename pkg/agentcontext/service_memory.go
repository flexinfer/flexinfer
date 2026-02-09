package agentcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// GetMemoryHierarchy returns the memory hierarchy for direct access
func (s *Service) GetMemoryHierarchy() *MemoryHierarchy {
	return s.memoryHierarchy
}

// HandleMemoryAdd adds items to the memory hierarchy
func (s *Service) HandleMemoryAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.String("session_id", "")
	agentID := v.String("agent_id", s.cfg.DefaultAgentID)
	namespace := v.String("namespace", s.cfg.DefaultNamespace)
	itemsRaw := v.RequiredAny("items")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	itemsArr, ok := itemsRaw.([]any)
	if !ok || len(itemsArr) == 0 {
		return mcp.ErrorResult(fmt.Errorf("items array is required")), nil
	}

	var addedIDs []string
	for i, itemRaw := range itemsArr {
		itemMap, ok := itemRaw.(map[string]any)
		if !ok {
			return mcp.ErrorResult(fmt.Errorf("item %d must be an object", i)), nil
		}

		item := &MemoryItem{
			ID:         toString(itemMap["id"]),
			Tier:       MemoryTier(toString(itemMap["tier"])),
			Importance: ImportanceLevel(toString(itemMap["importance"])),
			Title:      toString(itemMap["title"]),
			Content:    toString(itemMap["content"]),
			Category:   toString(itemMap["category"]),
			Namespace:  namespace,
			SessionID:  sessionID,
			AgentID:    agentID,
		}

		// Parse tags
		if tags, ok := itemMap["tags"].([]any); ok {
			for _, t := range tags {
				if ts := toString(t); ts != "" {
					item.Tags = append(item.Tags, ts)
				}
			}
		}

		// Parse metadata
		if metadata, ok := itemMap["metadata"].(map[string]any); ok {
			item.Metadata = metadata
		}

		// Parse related_ids
		if related, ok := itemMap["related_ids"].([]any); ok {
			for _, r := range related {
				if rs := toString(r); rs != "" {
					item.RelatedIDs = append(item.RelatedIDs, rs)
				}
			}
		}

		if err := s.persistedMemoryHierarchy.AddItemWithPersistence(ctx, item, nil); err != nil {
			return mcp.ErrorResult(fmt.Errorf("failed to add item %d: %w", i, err)), nil
		}

		addedIDs = append(addedIDs, item.ID)
	}

	// Update tier-specific metrics
	for _, itemRaw := range itemsArr {
		if im, ok := itemRaw.(map[string]any); ok {
			tokens := int64(EstimateTokens(toString(im["content"])))
			switch MemoryTier(toString(im["tier"])) {
			case MemoryTierWorking:
				s.metrics.WorkingMemoryItems.Add(1)
				s.metrics.WorkingMemoryTokens.Add(tokens)
			case MemoryTierLongTerm:
				s.metrics.LongTermMemoryItems.Add(1)
				s.metrics.LongTermMemoryTokens.Add(tokens)
			default: // short_term or unspecified
				s.metrics.ShortTermMemoryItems.Add(1)
				s.metrics.ShortTermMemoryTokens.Add(tokens)
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(addedIDs),
		"item_ids": addedIDs,
	})
}

// HandleMemoryGet retrieves memory items by ID
func (s *Service) HandleMemoryGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	itemIDs := v.RequiredStringSlice("item_ids")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var items []map[string]any
	for _, id := range itemIDs {
		item, err := s.memoryHierarchy.GetItem(id)
		if err != nil {
			continue // Skip not found
		}
		items = append(items, memoryItemToMap(item))
	}

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"count": len(items),
		"items": items,
	})
}

// HandleMemoryRecall recalls memories matching criteria
func (s *Service) HandleMemoryRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.String("query", "")
	namespace := v.String("namespace", "")
	sessionID := v.String("session_id", "")
	agentID := v.String("agent_id", "")
	tokenBudget := v.Int("token_budget", 8000)
	limit := v.Int("limit", 100)
	minImportance := v.Float("min_importance", 0)
	categories := v.StringSlice("categories")
	tags := v.StringSlice("tags")

	req := MemoryRecallRequest{
		Query:         query,
		Namespace:     namespace,
		SessionID:     sessionID,
		AgentID:       agentID,
		TokenBudget:   tokenBudget,
		Limit:         limit,
		MinImportance: minImportance,
		Categories:    categories,
		Tags:          tags,
	}

	// Parse tiers
	if tiers, ok := args["tiers"].([]any); ok {
		for _, t := range tiers {
			if ts, ok := t.(string); ok && ts != "" {
				req.Tiers = append(req.Tiers, MemoryTier(ts))
			}
		}
	}

	result, err := s.memoryHierarchy.Recall(req)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	items := make([]map[string]any, len(result.Items))
	for i, item := range result.Items {
		items[i] = memoryItemToMap(&item)
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"count":        len(items),
		"items":        items,
		"by_tier":      result.ByTier,
		"total_tokens": result.TotalTokens,
		"truncated":    result.Truncated,
	})
}

// HandleMemoryDelete deletes memory items
func (s *Service) HandleMemoryDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	itemIDs := v.RequiredStringSlice("item_ids")
	confirm := v.Bool("confirm", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if !confirm {
		return mcp.ErrorResult(fmt.Errorf("confirm must be true to delete items")), nil
	}

	var deleted []string
	for _, id := range itemIDs {
		if err := s.persistedMemoryHierarchy.DeleteItemWithPersistence(ctx, id); err == nil {
			deleted = append(deleted, id)
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"deleted": deleted,
	})
}

// HandleMemoryPromote promotes items to a higher tier
func (s *Service) HandleMemoryPromote(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	itemIDs := v.RequiredStringSlice("item_ids")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var promoted []string
	var errors []string
	for _, id := range itemIDs {
		if err := s.persistedMemoryHierarchy.PromoteItemWithPersistence(ctx, id); err == nil {
			promoted = append(promoted, id)
		} else {
			errors = append(errors, fmt.Sprintf("%s: %v", id, err))
		}
	}

	result := map[string]any{
		"ok":       true,
		"promoted": promoted,
	}
	if len(errors) > 0 {
		result["errors"] = errors
	}

	return mcp.JSONResult(result)
}

// HandleMemoryDemote demotes items to a lower tier
func (s *Service) HandleMemoryDemote(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	itemIDs := v.RequiredStringSlice("item_ids")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var demoted []string
	var errors []string
	for _, id := range itemIDs {
		if err := s.persistedMemoryHierarchy.DemoteItemWithPersistence(ctx, id); err == nil {
			demoted = append(demoted, id)
		} else {
			errors = append(errors, fmt.Sprintf("%s: %v", id, err))
		}
	}

	result := map[string]any{
		"ok":      true,
		"demoted": demoted,
	}
	if len(errors) > 0 {
		result["errors"] = errors
	}

	return mcp.JSONResult(result)
}

// HandleMemoryCompress compresses items to reduce token usage
func (s *Service) HandleMemoryCompress(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	// Check if we're compressing specific items or running tier-wide compression
	itemIDs := v.StringSlice("item_ids")
	tierStr := v.String("tier", "")

	tier := MemoryTier(tierStr)

	if len(itemIDs) > 0 {
		// Compress specific items
		var compressed []string
		var errors []string
		for _, id := range itemIDs {
			if err := s.memoryHierarchy.CompressItem(id); err == nil {
				compressed = append(compressed, id)
			} else {
				errors = append(errors, fmt.Sprintf("%s: %v", id, err))
			}
		}

		result := map[string]any{
			"ok":         true,
			"compressed": compressed,
		}
		if len(errors) > 0 {
			result["errors"] = errors
		}
		return mcp.JSONResult(result)
	}

	if tier != "" {
		// Run tier-wide compression
		job, err := s.memoryHierarchy.RunCompression(tier)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		return mcp.JSONResult(map[string]any{
			"ok":                true,
			"job_id":            job.ID,
			"tier":              job.Tier,
			"item_count":        job.ItemCount,
			"expired_count":     job.ExpiredCount,
			"original_tokens":   job.OriginalTokens,
			"compressed_tokens": job.CompressedTokens,
			"status":            job.Status,
		})
	}

	return mcp.ErrorResult(fmt.Errorf("either item_ids or tier is required")), nil
}

// HandleMemoryMerge merges multiple items into one
func (s *Service) HandleMemoryMerge(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	itemIDs := v.RequiredStringSlice("item_ids")
	newTitle := v.String("new_title", "Merged Memory")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if len(itemIDs) < 2 {
		return mcp.ErrorResult(fmt.Errorf("at least 2 item_ids are required to merge")), nil
	}

	merged, err := s.memoryHierarchy.MergeItems(itemIDs, newTitle)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":              true,
		"merged_item_id":  merged.ID,
		"merged_item":     memoryItemToMap(merged),
		"source_item_ids": itemIDs,
	})
}

// HandleMemoryStats returns memory hierarchy statistics
func (s *Service) HandleMemoryStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	stats := s.memoryHierarchy.Stats()

	return mcp.JSONResult(map[string]any{
		"ok":                        true,
		"total_items":               stats.TotalItems,
		"total_tokens":              stats.TotalTokens,
		"compression_ratio":         stats.CompressionRatio,
		"items_added_last_24h":      stats.ItemsAddedLast24h,
		"items_compressed_last_24h": stats.ItemsCompressedLast24h,
		"working_memory": map[string]any{
			"item_count":     stats.WorkingMemory.ItemCount,
			"token_count":    stats.WorkingMemory.TokenCount,
			"avg_importance": stats.WorkingMemory.AvgImportance,
			"by_category":    stats.WorkingMemory.ByCategory,
			"by_importance":  stats.WorkingMemory.ByImportance,
		},
		"short_term_memory": map[string]any{
			"item_count":     stats.ShortTermMemory.ItemCount,
			"token_count":    stats.ShortTermMemory.TokenCount,
			"avg_importance": stats.ShortTermMemory.AvgImportance,
			"by_category":    stats.ShortTermMemory.ByCategory,
			"by_importance":  stats.ShortTermMemory.ByImportance,
		},
		"long_term_memory": map[string]any{
			"item_count":     stats.LongTermMemory.ItemCount,
			"token_count":    stats.LongTermMemory.TokenCount,
			"avg_importance": stats.LongTermMemory.AvgImportance,
			"by_category":    stats.LongTermMemory.ByCategory,
			"by_importance":  stats.LongTermMemory.ByImportance,
		},
	})
}

// HandleMemoryPolicyGet returns retention policy for a tier
func (s *Service) HandleMemoryPolicyGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	tierStr := v.Required("tier")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	tier := MemoryTier(tierStr)

	policy := s.memoryHierarchy.GetRetentionPolicy(tier)
	if policy == nil {
		return mcp.ErrorResult(fmt.Errorf("no policy for tier: %s", tier)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok": true,
		"policy": map[string]any{
			"id":                     policy.ID,
			"name":                   policy.Name,
			"tier":                   string(policy.Tier),
			"default_ttl_hours":      policy.DefaultTTL,
			"compress_after_hours":   policy.CompressAfterHours,
			"compression_ratio":      policy.CompressionRatio,
			"merge_threshold":        policy.MergeThreshold,
			"promotion_threshold":    policy.PromotionThreshold,
			"demotion_threshold":     policy.DemotionThreshold,
			"access_count_threshold": policy.AccessCountThreshold,
			"max_items":              policy.MaxItems,
			"max_tokens":             policy.MaxTokens,
			"dedupe_enabled":         policy.DedupeEnabled,
			"dedupe_similarity":      policy.DedupeSimilarity,
		},
	})
}

// HandleMemoryPolicySet updates retention policy for a tier
func (s *Service) HandleMemoryPolicySet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	tierStr := v.Required("tier")
	name := v.String("name", "")
	ttl := v.Int("default_ttl_hours", 0)
	compress := v.Int("compress_after_hours", 0)
	ratio := v.Float("compression_ratio", 0)
	merge := v.Float("merge_threshold", 0)
	promo := v.Float("promotion_threshold", 0)
	demo := v.Float("demotion_threshold", 0)
	access := v.Int("access_count_threshold", 0)
	maxItems := v.Int("max_items", 0)
	maxTokens := v.Int("max_tokens", 0)
	dedupeEnabled := v.Bool("dedupe_enabled", true)
	dedupeSim := v.Float("dedupe_similarity", 0)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	tier := MemoryTier(tierStr)

	// Get existing policy or create new
	policy := s.memoryHierarchy.GetRetentionPolicy(tier)
	if policy == nil {
		policy = &RetentionPolicy{
			ID:   fmt.Sprintf("custom-%s", tier),
			Tier: tier,
		}
	}

	// Update fields if provided
	if name != "" {
		policy.Name = name
	}
	if ttl > 0 {
		policy.DefaultTTL = ttl
	}
	if compress > 0 {
		policy.CompressAfterHours = compress
	}
	if ratio > 0 {
		policy.CompressionRatio = ratio
	}
	if merge > 0 {
		policy.MergeThreshold = merge
	}
	if promo > 0 {
		policy.PromotionThreshold = promo
	}
	if demo > 0 {
		policy.DemotionThreshold = demo
	}
	if access > 0 {
		policy.AccessCountThreshold = access
	}
	if maxItems > 0 {
		policy.MaxItems = maxItems
	}
	if maxTokens > 0 {
		policy.MaxTokens = maxTokens
	}
	if _, ok := args["dedupe_enabled"]; ok {
		policy.DedupeEnabled = dedupeEnabled
	}
	if dedupeSim > 0 {
		policy.DedupeSimilarity = dedupeSim
	}

	s.memoryHierarchy.SetRetentionPolicy(policy)

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"tier":    string(tier),
		"message": "Retention policy updated",
	})
}

// Helper function for memory items
func memoryItemToMap(item *MemoryItem) map[string]any {
	m := map[string]any{
		"id":               item.ID,
		"tier":             string(item.Tier),
		"status":           string(item.Status),
		"importance":       string(item.Importance),
		"importance_score": item.ImportanceScore,
		"title":            item.Title,
		"content":          item.Content,
		"category":         item.Category,
		"original_tokens":  item.OriginalTokens,
		"access_count":     item.AccessCount,
		"created_at":       item.CreatedAt.Format(time.RFC3339Nano),
		"last_accessed_at": item.LastAccessedAt.Format(time.RFC3339Nano),
	}

	if item.Summary != "" {
		m["summary"] = item.Summary
		m["compressed_tokens"] = item.CompressedTokens
	}
	if item.Namespace != "" {
		m["namespace"] = item.Namespace
	}
	if item.SessionID != "" {
		m["session_id"] = item.SessionID
	}
	if item.AgentID != "" {
		m["agent_id"] = item.AgentID
	}
	if len(item.Tags) > 0 {
		m["tags"] = item.Tags
	}
	if item.Metadata != nil {
		m["metadata"] = item.Metadata
	}
	if len(item.RelatedIDs) > 0 {
		m["related_ids"] = item.RelatedIDs
	}
	if item.ExpiresAt != nil {
		m["expires_at"] = item.ExpiresAt.Format(time.RFC3339Nano)
	}
	if item.CompressedAt != nil {
		m["compressed_at"] = item.CompressedAt.Format(time.RFC3339Nano)
	}

	return m
}

// HandleMemoryExport exports memory to universal JSON format
func (s *Service) HandleMemoryExport(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.String("agent_id", "")
	namespace := v.String("namespace", "")
	sessionID := v.String("session_id", "")
	format := v.String("format", "loom")
	includeGraph := v.Bool("include_graph", true)
	includeWorkflows := v.Bool("include_workflows", false)
	includeEmbeddings := v.Bool("include_embeddings", false)
	tiers := v.StringSlice("tiers")
	tags := v.StringSlice("tags")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	opts := ExportOptions{
		IncludeMemories:   true,
		IncludeGraph:      includeGraph,
		IncludeWorkflows:  includeWorkflows,
		IncludeEmbeddings: includeEmbeddings,
		MemoryTiers:       tiers,
		SessionID:         sessionID,
		Namespace:         namespace,
		Format:            format,
		AgentID:           agentID,
		Tags:              tags,
	}

	data, err := s.memoryExporter.Export(opts)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("export: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"export": data,
		"stats":  data.Stats,
	})
}

// HandleMemoryImport imports memory from universal JSON format
func (s *Service) HandleMemoryImport(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	conflictStrategy := v.String("conflict_strategy", "skip")
	idPrefix := v.String("id_prefix", "")
	targetTier := v.String("target_tier", "")
	targetNamespace := v.String("target_namespace", "")
	importGraph := v.Bool("import_graph", true)
	importWorkflows := v.Bool("import_workflows", false)
	regenerateEmbeddings := v.Bool("regenerate_embeddings", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Get the data payload
	dataRaw, ok := args["data"]
	if !ok {
		return mcp.ErrorResult(fmt.Errorf("data is required")), nil
	}

	// Marshal and unmarshal to get a proper UniversalMemoryFormat
	dataBytes, err := json.Marshal(dataRaw)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("invalid data format: %w", err)), nil
	}

	opts := ImportOptions{
		ImportMemories:       true,
		ImportGraph:          importGraph,
		ImportWorkflows:      importWorkflows,
		ConflictStrategy:     conflictStrategy,
		IDPrefix:             idPrefix,
		TargetTier:           targetTier,
		TargetNamespace:      targetNamespace,
		RegenerateEmbeddings: regenerateEmbeddings,
	}

	result, err := s.memoryImporter.ImportFromJSON(dataBytes, opts)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("import: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":                 true,
		"memories_imported":  result.MemoriesImported,
		"memories_skipped":   result.MemoriesSkipped,
		"entities_imported":  result.EntitiesImported,
		"entities_skipped":   result.EntitiesSkipped,
		"relations_imported": result.RelationsImported,
		"relations_skipped":  result.RelationsSkipped,
		"workflows_imported": result.WorkflowsImported,
		"workflows_skipped":  result.WorkflowsSkipped,
		"errors":             result.Errors,
	})
}
