// api_agent_context.go implements context and knowledge REST handlers.
//
// Handlers:
//   - POST /api/agent/context/add
//   - GET  /api/agent/context-inspect
//   - GET  /api/knowledge
package hud

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// handleAgentContextAdd proxies context entries to agent-context and broadcasts
// SSE events for devbox-titled entries so the sandbox panel shows live activity.
// POST /api/agent/context/add
func (a *App) handleAgentContextAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string           `json:"session_id,omitempty"`
		Entries   []map[string]any `json:"entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if len(body.Entries) == 0 {
		a.writeError(w, http.StatusBadRequest, "entries array is required", nil)
		return
	}

	// Forward to agent-context MCP server.
	if err := a.agent.ContextAdd(body.SessionID, body.Entries); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to add context entries", err)
		return
	}

	// Detect devbox events and broadcast SSE for the sandbox panel.
	for _, entry := range body.Entries {
		title, _ := entry["title"].(string)
		if len(title) > 7 && title[:7] == "devbox." {
			// Parse "devbox.<type>: <project>" format.
			rest := title[7:]
			eventType := rest
			project := ""
			if idx := strings.Index(rest, ": "); idx >= 0 {
				eventType = rest[:idx]
				project = rest[idx+2:]
			}
			content, _ := entry["content"].(string)
			a.broadcastAgentEvent("hud.sandbox.event", map[string]any{
				"type":      eventType,
				"project":   project,
				"detail":    content,
				"timestamp": time.Now().Format(time.RFC3339),
			})
		}
	}

	// Broadcast context entry addition for live UX updates.
	a.broadcastAgentEvent("agent.context.added", map[string]any{
		"session_id":  body.SessionID,
		"entry_count": len(body.Entries),
		"entry_types": extractEntryTypes(body.Entries),
		"timestamp":   time.Now().Format(time.RFC3339),
	})
	a.broadcastAgentEvent("agent.session.stats.updated", map[string]any{
		"session_id":    body.SessionID,
		"entries_added": len(body.Entries),
	})

	a.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// extractEntryTypes collects unique entry_type values from context entries.
func extractEntryTypes(entries []map[string]any) []string {
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if t, ok := e["entry_type"].(string); ok && t != "" {
			seen[t] = struct{}{}
		}
	}
	types := make([]string, 0, len(seen))
	for t := range seen {
		types = append(types, t)
	}
	return types
}

// handleAgentContextInspect returns a context budget breakdown for an agent/session.
// GET /api/agent/context-inspect?agent_id=...&session_id=...&detail=true&limit=200
func (a *App) handleAgentContextInspect(w http.ResponseWriter, r *http.Request) {
	req, err := bridge.ParseContextInspectRequest(r.URL.Query())
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	result, err := a.agent.ContextInspect(req.AgentID, req.SessionID, req.Detail, req.Limit)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "context inspect failed", err)
		return
	}
	a.writeJSON(w, http.StatusOK, result)
}

// handleKnowledge performs a cross-agent knowledge search.
// GET /api/knowledge?query=...&category=...&budget=...
func (a *App) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	if query == "" {
		query = "recent decisions and findings"
	}
	category := r.URL.Query().Get("category")
	budget := 8000
	if b := r.URL.Query().Get("budget"); b != "" {
		if parsed, err := strconv.Atoi(b); err == nil && parsed > 0 {
			budget = parsed
		}
	}

	result, err := a.agent.KnowledgeRecall(query, category, budget)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "knowledge recall failed", err)
		return
	}

	// Group entries by category for the frontend.
	grouped := make(map[string][]bridge.KnowledgeEntry)
	for _, e := range result.Entries {
		cat := e.EntryType
		if cat == "" {
			cat = "note"
		}
		// Filter by category if specified.
		if category != "" && cat != category {
			continue
		}
		grouped[cat] = append(grouped[cat], e)
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"entries":      result.Entries,
		"grouped":      grouped,
		"count":        result.Count,
		"total_tokens": result.TotalTokens,
		"token_budget": result.TokenBudget,
	})
}
