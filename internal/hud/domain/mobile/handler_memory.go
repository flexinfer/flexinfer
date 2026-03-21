package mobile

import (
	"net/http"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func (d *MobileDomain) handleMobileMemoryStats(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	mon := d.deps.Monitors()
	stats := mon.Memory.Stats()
	if stats == nil {
		directStats, err := d.deps.Agent().MemoryStats()
		if err != nil {
			d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to load memory stats")
			return
		}
		stats = directStats
	}

	resp := d.deps.MemoryStatsPayload(stats)
	if comp, ok := resp["compression"].(map[string]any); ok {
		comp["estimated_saved"] = comp["tokens_saved"]
	} else {
		resp["compression"] = map[string]any{
			"ratio":            stats.CompressionRatio,
			"added_24h":        stats.ItemsAddedLast24h,
			"compressed_24h":   stats.ItemsCompressedLast24h,
			"estimated_saved":  int(float64(stats.TotalTokens) * (1 - stats.CompressionRatio)),
			"compressed_items": stats.ItemsCompressedLast24h,
		}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{"stats": resp})
}

func (d *MobileDomain) handleMobileMemoryItems(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	tier, ok := normalizeMobileMemoryTier(strings.TrimSpace(r.URL.Query().Get("tier")))
	if !ok {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "tier must be one of working, short_term, long_term")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	limit := parseMobileLimit(r, DefaultLimit, MaxLimit)

	items, err := d.deps.Agent().MemoryRecall(tier, query, limit)
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to list memory items")
		return
	}
	if items == nil {
		items = []bridge.MemoryItem{}
	}

	sortSliceStable(items, func(i, j int) bool {
		ti := parseMobileTime(chooseFirstNonEmpty(items[i].LastAccessed, items[i].AccessedAt))
		tj := parseMobileTime(chooseFirstNonEmpty(items[j].LastAccessed, items[j].AccessedAt))
		if ti.Equal(tj) {
			return items[i].ID < items[j].ID
		}
		return ti.After(tj)
	})

	result := make([]memoryItemDTO, len(items))
	for i, item := range items {
		result[i] = memoryItemDTO{
			ID:           item.ID,
			Title:        item.Title,
			Content:      item.Content,
			Tier:         normalizeMobileMemoryTierOutput(item.Tier),
			Importance:   normalizeMobileImportance(item.Importance),
			ImportanceSc: item.ImportanceScore,
			Tokens:       item.Tokens,
			Status:       strings.TrimSpace(item.Status),
			Category:     strings.TrimSpace(item.Category),
			AccessedAt:   item.AccessedAt,
			LastAccessed: item.LastAccessed,
		}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"items": result,
		"tier":  tier,
	})
}

func (d *MobileDomain) handleMobileStream(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	limit := parseMobileLimit(r, DefaultLimit, MaxLimit)
	typeFilter := parseMobileTypeFilter(r.URL.Query().Get("types"))
	agentFilter := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	sessionFilter := strings.TrimSpace(r.URL.Query().Get("session_id"))

	var (
		entries []bridge.ContextEntryInfo
		err     error
	)
	if sessionFilter != "" {
		entries, err = d.deps.Agent().SessionEntries(sessionFilter, MaxLimit)
	} else {
		entries, err = d.deps.Agent().ContextStream(time.Time{}, MaxLimit)
	}
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to load context stream")
		return
	}

	result := make([]streamEntryDTO, 0, limit)
	for _, entry := range entries {
		kind := strings.TrimSpace(entry.Entry.EntryType)
		if len(typeFilter) > 0 {
			if _, ok := typeFilter[strings.ToLower(kind)]; !ok {
				continue
			}
		}
		if agentFilter != "" && !strings.EqualFold(entry.Entry.AgentID, agentFilter) {
			continue
		}
		result = append(result, streamEntryDTO{
			ID:        entry.Entry.ID,
			EntryType: kind,
			AgentID:   entry.Entry.AgentID,
			Agent:     entry.Entry.AgentID,
			Namespace: entry.Entry.Namespace,
			Title:     entry.Entry.Title,
			Content:   entry.Entry.Content,
			Timestamp: entry.Entry.Timestamp,
			Score:     entry.Score,
		})
		if len(result) >= limit {
			break
		}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{"entries": result})
}
