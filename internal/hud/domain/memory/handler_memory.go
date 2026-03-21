package memory

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// StatsPayload converts bridge MemoryStatsResult into the canonical JSON shape
// served to web and mobile clients. This is the single source of truth — SSE,
// REST, and mobile handlers all call this.
func StatsPayload(stats *bridge.MemoryStatsResult) map[string]any {
	tierJSON := func(t bridge.MemoryTierStats) map[string]any {
		return map[string]any{"items": t.Items, "tokens": t.Tokens}
	}
	payload := map[string]any{
		"working_memory":    tierJSON(stats.WorkingMemory),
		"short_term_memory": tierJSON(stats.ShortTermMemory),
		"long_term_memory":  tierJSON(stats.LongTermMemory),
		"total_items":       stats.TotalItems,
		"total_tokens":      stats.TotalTokens,
	}
	if stats.CompressionRatio > 0 || stats.ItemsCompressedLast24h > 0 {
		payload["compression"] = map[string]any{
			"ratio":            stats.CompressionRatio,
			"compressed_items": stats.ItemsCompressedLast24h,
			"tokens_saved":     int(float64(stats.TotalTokens) * (1 - stats.CompressionRatio)),
			"added_24h":        stats.ItemsAddedLast24h,
			"compressed_24h":   stats.ItemsCompressedLast24h,
		}
	}
	return payload
}

// handleMemoryStats returns memory hierarchy stats from the memory monitor.
func (d *MemoryDomain) handleMemoryStats(w http.ResponseWriter, _ *http.Request) {
	stats := d.deps.MemoryMonitor().Stats()
	if stats == nil {
		directStats, err := d.deps.Agent().MemoryStats()
		if err != nil {
			d.deps.WriteError(w, http.StatusBadGateway, "failed to get memory stats", err)
			return
		}
		stats = directStats
	}
	d.deps.WriteJSON(w, http.StatusOK, StatsPayload(stats))
}

// handleMemoryPromote promotes a memory item via the monitor.
func (d *MemoryDomain) handleMemoryPromote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing memory item id", nil)
		return
	}
	if err := d.deps.MemoryMonitor().Promote(id); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to promote memory", err)
		return
	}
	d.deps.BroadcastAgentEvent("hud.memory.promote", map[string]any{"id": id})
	d.deps.WriteJSON(w, http.StatusOK, map[string]string{"status": "promoted"})
}

// handleMemoryDemote demotes a memory item via the monitor.
func (d *MemoryDomain) handleMemoryDemote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing memory item id", nil)
		return
	}
	if err := d.deps.MemoryMonitor().Demote(id); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to demote memory", err)
		return
	}
	d.deps.BroadcastAgentEvent("hud.memory.demote", map[string]any{"id": id})
	d.deps.WriteJSON(w, http.StatusOK, map[string]string{"status": "demoted"})
}

// handleMemoryItems returns memory items filtered by tier/query.
func (d *MemoryDomain) handleMemoryItems(w http.ResponseWriter, r *http.Request) {
	tier := r.URL.Query().Get("tier")
	query := r.URL.Query().Get("query")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	items, err := d.deps.Agent().MemoryRecall(tier, query, limit)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to recall memory", err)
		return
	}
	result := make([]map[string]any, len(items))
	for i, it := range items {
		result[i] = map[string]any{
			"id":            it.ID,
			"title":         it.Title,
			"content":       it.Content,
			"tier":          it.Tier,
			"importance":    it.Importance,
			"tokens":        it.Tokens,
			"status":        it.Status,
			"category":      it.Category,
			"accessed_at":   it.AccessedAt,
			"last_accessed": it.LastAccessed,
		}
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"items": result})
}

// handleMemoryAdd adds a new memory item.
func (d *MemoryDomain) handleMemoryAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title      string `json:"title"`
		Content    string `json:"content"`
		Tier       string `json:"tier"`
		Importance string `json:"importance"`
		Category   string `json:"category"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Title == "" || body.Content == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "title and content are required", nil)
		return
	}
	if err := d.deps.Agent().MemoryAdd(body.Title, body.Content, body.Tier, body.Importance, body.Category); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to add memory", err)
		return
	}
	go d.deps.MemoryMonitor().Refresh()
	d.deps.BroadcastAgentEvent("hud.memory.add", map[string]any{
		"title": body.Title,
		"tier":  body.Tier,
	})
	d.deps.WriteJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

// handleMemoryDelete deletes a memory item.
func (d *MemoryDomain) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing memory item id", nil)
		return
	}
	if err := d.deps.Agent().MemoryDelete(id); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to delete memory", err)
		return
	}
	go d.deps.MemoryMonitor().Refresh()
	d.deps.BroadcastAgentEvent("hud.memory.delete", map[string]any{"id": id})
	d.deps.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleMemoryCompaction returns compaction status.
func (d *MemoryDomain) handleMemoryCompaction(w http.ResponseWriter, _ *http.Request) {
	info, err := d.deps.Agent().CompactionStatus()
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to get compaction status", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, info)
}
