// app_routes_observability.go contains analytics and observability HTTP handlers.
//
// These handlers serve KPI aggregates, activity timelines, cache stats, LLM
// cost tracking, RBAC configuration, OTel status, and tunnel connections.
package hud

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// --- Command center API handlers ---

// handleKPIs returns daily aggregate KPI metrics for the overview panel.
// GET /api/kpis
func (a *App) handleKPIs(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()

	kpis := a.fleetMonitor.KPIs()

	a.writeJSON(w, http.StatusOK, map[string]any{
		"sessions_today":        kpis.SessionsToday,
		"tokens_today":          kpis.TokensToday,
		"tasks_completed_today": kpis.TasksCompletedToday,
		"active_agents":         snap.ActiveAgents,
		"pending_approvals":     snap.PendingApprovals,
		"file_conflicts":        kpis.FileConflicts,
		"conflict_details":      kpis.ConflictDetails,
	})
}

// handleTimeline returns chronological agent lifecycle events from the ring buffer.
// GET /api/timeline?since=<RFC3339>&limit=<int>
func (a *App) handleTimeline(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	eventType := strings.TrimSpace(r.URL.Query().Get("event_type"))

	var entries []TimelineEntry
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			entries = a.eventLog.Since(t, 0)
		} else {
			entries = a.eventLog.All(0)
		}
	} else {
		entries = a.eventLog.All(0)
	}

	entries = filterTimelineEntries(entries, agentID, eventType)
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	if entries == nil {
		entries = []TimelineEntry{}
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"count":   len(entries),
	})
}

func filterTimelineEntries(entries []TimelineEntry, agentID, eventType string) []TimelineEntry {
	agentID = strings.TrimSpace(agentID)
	eventType = strings.TrimSpace(eventType)
	if agentID == "" && eventType == "" {
		return entries
	}

	filtered := make([]TimelineEntry, 0, len(entries))
	for _, entry := range entries {
		if agentID != "" && entry.AgentID != agentID {
			continue
		}
		if eventType != "" && entry.EventType != eventType {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func (a *App) handleCacheStats(w http.ResponseWriter, _ *http.Request) {
	raw, err := a.client.Call("loom/cache/stats", nil)
	if err != nil {
		// Fallback to local HUD cache stats if daemon doesn't support cache RPC.
		a.logger.Debug("cache stats RPC failed, returning local cache", "error", err)
		a.writeJSON(w, http.StatusOK, map[string]any{
			"entries":  a.cache.Len(),
			"hit_rate": 0.0,
		})
		return
	}
	var result bridge.CacheStatsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		a.logger.Debug("cache stats unmarshal failed", "error", err)
		a.writeJSON(w, http.StatusOK, map[string]any{
			"entries":  a.cache.Len(),
			"hit_rate": 0.0,
		})
		return
	}
	hitRate := 0.0
	if result.TotalHits > 0 && result.Entries > 0 {
		hitRate = float64(result.TotalHits) / float64(result.TotalHits+int64(result.Entries))
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"entries":    result.Entries,
		"size_bytes": result.SizeBytes,
		"max_bytes":  result.MaxBytes,
		"hit_rate":   hitRate,
		"enabled":    result.Enabled,
	})
}

func (a *App) handleCost(w http.ResponseWriter, _ *http.Request) {
	snap := a.costMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, snap)
}

// fetchRBACConfig fetches RBAC configuration from the daemon with graceful
// degradation. Returns a zero-value result (enabled=false) on any error.
func (a *App) fetchRBACConfig() bridge.RBACConfigResult {
	raw, err := a.client.Call("loom/rbac-config", nil)
	if err != nil {
		a.logger.Debug("rbac-config call failed", "error", err)
		return bridge.RBACConfigResult{}
	}
	var result bridge.RBACConfigResult
	if err := json.Unmarshal(raw, &result); err != nil {
		a.logger.Debug("rbac-config unmarshal failed", "error", err)
		return bridge.RBACConfigResult{}
	}
	return result
}

// fetchOTelStatus fetches OTel observability status from the daemon with
// graceful degradation. Returns a zero-value result on any error.
func (a *App) fetchOTelStatus() bridge.OTelStatusResult {
	raw, err := a.client.Call("loom/otel-status", nil)
	if err != nil {
		a.logger.Debug("otel-status call failed", "error", err)
		return bridge.OTelStatusResult{}
	}
	var result bridge.OTelStatusResult
	if err := json.Unmarshal(raw, &result); err != nil {
		a.logger.Debug("otel-status unmarshal failed", "error", err)
		return bridge.OTelStatusResult{}
	}
	return result
}

func (a *App) handleRBAC(w http.ResponseWriter, _ *http.Request) {
	result := a.fetchRBACConfig()
	a.writeJSON(w, http.StatusOK, result)
}

func (a *App) handleOTel(w http.ResponseWriter, _ *http.Request) {
	result := a.fetchOTelStatus()
	a.writeJSON(w, http.StatusOK, result)
}

type traceAPIEntry struct {
	Timestamp     string `json:"timestamp"`
	AgentID       string `json:"agent_id,omitempty"`
	AgentType     string `json:"agent_type,omitempty"`
	Server        string `json:"server"`
	Tool          string `json:"tool"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	Target        string `json:"target,omitempty"`
	Cached        bool   `json:"cached,omitempty"`
	PipelineStage string `json:"pipeline_stage,omitempty"`
	DurationMs    int64  `json:"duration_ms"`
	RouteMs       int64  `json:"route_ms,omitempty"`
	BuildMs       int64  `json:"build_ms,omitempty"`
	ExecuteMs     int64  `json:"execute_ms,omitempty"`
	SendMs        int64  `json:"send_ms,omitempty"`
	RecvMs        int64  `json:"recv_ms,omitempty"`
}

type auditTraceData struct {
	Enabled bool            `json:"enabled"`
	Path    string          `json:"path,omitempty"`
	Count   int             `json:"count"`
	Limit   int             `json:"limit"`
	Summary map[string]any  `json:"summary"`
	Traces  []traceAPIEntry `json:"traces"`
}

func (a *App) handleTraces(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	traces, err := a.fetchAuditTraces(limit)
	if err != nil {
		a.logger.Debug("audit-traces call failed", "error", err)
		a.writeJSON(w, http.StatusOK, map[string]any{
			"enabled": false,
			"limit":   limit,
			"count":   0,
			"summary": map[string]any{},
			"traces":  []traceAPIEntry{},
		})
		return
	}

	a.writeJSON(w, http.StatusOK, traces)
}

func (a *App) fetchAuditTraces(limit int) (auditTraceData, error) {
	raw, err := a.client.Call("loom/audit-traces", map[string]any{"limit": limit})
	if err != nil {
		return auditTraceData{}, err
	}

	var result struct {
		Enabled bool            `json:"enabled"`
		Path    string          `json:"path,omitempty"`
		Count   int             `json:"count"`
		Limit   int             `json:"limit"`
		Summary json.RawMessage `json:"summary"`
		Traces  []struct {
			Timestamp     time.Time `json:"timestamp"`
			AgentID       string    `json:"agent_id,omitempty"`
			AgentType     string    `json:"agent_type,omitempty"`
			Server        string    `json:"server"`
			Tool          string    `json:"tool"`
			Status        string    `json:"status"`
			Error         string    `json:"error,omitempty"`
			Target        string    `json:"target,omitempty"`
			Cached        bool      `json:"cached,omitempty"`
			PipelineStage string    `json:"pipeline_stage,omitempty"`
			DurationMs    int64     `json:"duration_ms"`
			RouteMs       int64     `json:"route_ms,omitempty"`
			BuildMs       int64     `json:"build_ms,omitempty"`
			ExecuteMs     int64     `json:"execute_ms,omitempty"`
			SendMs        int64     `json:"send_ms,omitempty"`
			RecvMs        int64     `json:"recv_ms,omitempty"`
		} `json:"traces"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return auditTraceData{}, err
	}

	entries := make([]traceAPIEntry, 0, len(result.Traces))
	for _, trace := range result.Traces {
		entries = append(entries, traceAPIEntry{
			Timestamp:     trace.Timestamp.Format(time.RFC3339Nano),
			AgentID:       trace.AgentID,
			AgentType:     trace.AgentType,
			Server:        trace.Server,
			Tool:          trace.Tool,
			Status:        trace.Status,
			Error:         trace.Error,
			Target:        trace.Target,
			Cached:        trace.Cached,
			PipelineStage: trace.PipelineStage,
			DurationMs:    trace.DurationMs,
			RouteMs:       trace.RouteMs,
			BuildMs:       trace.BuildMs,
			ExecuteMs:     trace.ExecuteMs,
			SendMs:        trace.SendMs,
			RecvMs:        trace.RecvMs,
		})
	}
	if entries == nil {
		entries = []traceAPIEntry{}
	}

	var summary map[string]any
	if len(result.Summary) > 0 {
		_ = json.Unmarshal(result.Summary, &summary)
	}
	if summary == nil {
		summary = map[string]any{}
	}

	return auditTraceData{
		Enabled: result.Enabled,
		Path:    result.Path,
		Count:   result.Count,
		Limit:   result.Limit,
		Summary: summary,
		Traces:  entries,
	}, nil
}

func (a *App) handleTunnels(w http.ResponseWriter, _ *http.Request) {
	raw, err := a.client.Call("loom/tunnels", nil)
	if err != nil {
		// Fallback to empty if daemon doesn't support tunnels yet.
		a.logger.Debug("tunnels RPC failed, returning empty", "error", err)
		a.writeJSON(w, http.StatusOK, map[string]any{"tunnels": []any{}, "count": 0})
		return
	}
	var result bridge.TunnelsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		a.logger.Debug("tunnels unmarshal failed", "error", err)
		a.writeJSON(w, http.StatusOK, map[string]any{"tunnels": []any{}, "count": 0})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"tunnels":   result.Tunnels,
		"count":     result.Total,
		"connected": result.Connected,
	})
}
