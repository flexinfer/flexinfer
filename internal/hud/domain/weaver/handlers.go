package weaver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxQueryBodyBytes caps the POST /api/weaver/query body so a malformed
// caller can't pin the HUD process on a giant unbounded read. A real
// query payload is well under 64 KB; the cap is generous for prompts
// with embedded code blocks.
const maxQueryBodyBytes = 256 * 1024

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

	// Try lifetime metrics endpoint first.
	raw, err := br.Call("loom/weaver/metrics", nil)
	if err == nil {
		var metrics map[string]any
		if json.Unmarshal(raw, &metrics) == nil {
			d.deps.WriteJSON(w, http.StatusOK, metrics)
			return
		}
	}

	// Fallback: derive from history.
	raw, err = br.Call("loom/weaver/history", nil)
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

// handleQuery proxies POST /api/weaver/query to the daemon's
// loom/weaver/query JSON-RPC. Used by in-cluster callers (the Mills
// operator) that want the routed multi-domain weaver dispatch without
// embedding the Router themselves.
//
// Request body (all fields optional except query):
//
//	{
//	  "query":             "<prompt>",
//	  "domains":           ["codebase", "ci-pipeline"],   // optional
//	  "max_tokens":        1024,                           // optional
//	  "agent_id":          "loom-mills-operator",          // optional
//	  "session_id":        "<parent agent-context session>", // optional
//	  "parent_session_id": "<proxy session>"               // optional
//	}
//
// Response: pkg/weaver.QueryResult JSON, surfaced verbatim from the
// daemon.
func (d *WeaverDomain) handleQuery(w http.ResponseWriter, r *http.Request) {
	br := d.deps.WeaverBridge()
	if br == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "weaver bridge not configured", nil)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxQueryBodyBytes+1))
	if err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "read request body", err)
		return
	}
	if len(body) > maxQueryBodyBytes {
		d.deps.WriteError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("request body exceeds %d bytes", maxQueryBodyBytes), nil)
		return
	}

	var params struct {
		Query           string   `json:"query"`
		Domains         []string `json:"domains,omitempty"`
		MaxTokens       int      `json:"max_tokens,omitempty"`
		AgentID         string   `json:"agent_id,omitempty"`
		SessionID       string   `json:"session_id,omitempty"`
		ParentSessionID string   `json:"parent_session_id,omitempty"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &params); err != nil {
			d.deps.WriteError(w, http.StatusBadRequest, "invalid JSON", err)
			return
		}
	}
	params.Query = strings.TrimSpace(params.Query)
	if params.Query == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "query is required", nil)
		return
	}

	// Forward to the daemon. The bridge's Call uses (method, params),
	// so we hand it the same struct shape the daemon's
	// handleWeaverQuery decodes — daemon-side validation is the source
	// of truth for required fields.
	raw, err := br.Call("loom/weaver/query", params)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "weaver query failed", err)
		return
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		d.deps.WriteError(w, http.StatusInternalServerError, "failed to parse weaver result", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, result)
}
