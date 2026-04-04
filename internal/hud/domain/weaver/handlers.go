package weaver

import (
	"encoding/json"
	"net/http"
)

// handleStatus returns the weaver configuration and overall state.
func (d *WeaverDomain) handleStatus(w http.ResponseWriter, _ *http.Request) {
	br := d.deps.WeaverBridge()
	if br == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}

	raw, err := br.Call("loom/weaver/status", nil)
	if err != nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"enabled": false, "error": err.Error()})
		return
	}

	var status map[string]any
	if err := json.Unmarshal(raw, &status); err != nil {
		d.deps.WriteError(w, http.StatusInternalServerError, "failed to parse status", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, status)
}

// handleDomains returns the configured weaver domains.
func (d *WeaverDomain) handleDomains(w http.ResponseWriter, _ *http.Request) {
	br := d.deps.WeaverBridge()
	if br == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"domains": []any{}})
		return
	}

	raw, err := br.Call("loom/weaver/status", nil)
	if err != nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"domains": []any{}})
		return
	}

	var status map[string]any
	if err := json.Unmarshal(raw, &status); err != nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"domains": []any{}})
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"domains":        status["domains"],
		"router_model":   status["router_model"],
		"subagent_model": status["subagent_model"],
	})
}

// handleHistory returns the recent query history buffer.
func (d *WeaverDomain) handleHistory(w http.ResponseWriter, _ *http.Request) {
	br := d.deps.WeaverBridge()
	if br == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"entries": []any{}})
		return
	}

	raw, err := br.Call("loom/weaver/history", nil)
	if err != nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"entries": []any{}})
		return
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"entries": []any{}})
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, result)
}

// handleMetrics returns aggregated weaver metrics summary.
func (d *WeaverDomain) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	br := d.deps.WeaverBridge()
	if br == nil {
		d.deps.WriteJSON(w, http.StatusOK, emptyMetrics())
		return
	}

	// Derive metrics from history.
	raw, err := br.Call("loom/weaver/history", nil)
	if err != nil {
		d.deps.WriteJSON(w, http.StatusOK, emptyMetrics())
		return
	}

	var result struct {
		Entries []struct {
			Status    string `json:"status"`
			LatencyMs int64  `json:"latency_ms"`
			Tokens    int    `json:"total_tokens"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		d.deps.WriteJSON(w, http.StatusOK, emptyMetrics())
		return
	}

	total := len(result.Entries)
	var errors int
	var totalLatency int64
	var totalTokens int
	for _, e := range result.Entries {
		if e.Status == "error" {
			errors++
		}
		totalLatency += e.LatencyMs
		totalTokens += e.Tokens
	}

	var avgLatency float64
	var errorRate float64
	if total > 0 {
		avgLatency = float64(totalLatency) / float64(total)
		errorRate = float64(errors) / float64(total)
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"total_queries":  total,
		"avg_latency_ms": avgLatency,
		"error_rate":     errorRate,
		"total_tokens":   totalTokens,
		"error_count":    errors,
	})
}

func emptyMetrics() map[string]any {
	return map[string]any{
		"total_queries":  0,
		"avg_latency_ms": 0,
		"error_rate":     0,
		"total_tokens":   0,
		"error_count":    0,
	}
}
