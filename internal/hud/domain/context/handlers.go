package context

import (
	"encoding/json"
	"net/http"
)

// handleContextHealth returns the full context health snapshot.
func (d *ContextDomain) handleContextHealth(w http.ResponseWriter, _ *http.Request) {
	mon := d.deps.ContextHealthMonitor()
	if mon == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "context health monitor not available", nil)
		return
	}
	snap := mon.Snapshot()
	d.deps.WriteJSON(w, http.StatusOK, snap)
}

// handleContextHealthAgent returns context health for a specific agent.
func (d *ContextDomain) handleContextHealthAgent(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	if agentID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing agent_id", nil)
		return
	}

	mon := d.deps.ContextHealthMonitor()
	if mon == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "context health monitor not available", nil)
		return
	}

	health := mon.AgentHealth(agentID)
	if health == nil {
		d.deps.WriteError(w, http.StatusNotFound, "agent not found or no active session", nil)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, health)
}

// handleContextCompact triggers manual compaction for a session.
func (d *ContextDomain) handleContextCompact(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	if sessionID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing session_id", nil)
		return
	}

	mon := d.deps.ContextHealthMonitor()
	if mon == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "context health monitor not available", nil)
		return
	}

	if err := mon.TriggerCompaction(r.Context(), sessionID); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "compaction failed", err)
		return
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]string{
		"status":     "compaction_triggered",
		"session_id": sessionID,
	})
}

// handleContextBudget returns the budget overview for all agents.
func (d *ContextDomain) handleContextBudget(w http.ResponseWriter, _ *http.Request) {
	mon := d.deps.ContextHealthMonitor()
	if mon == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "context health monitor not available", nil)
		return
	}

	snap := mon.Snapshot()

	type agentBudget struct {
		AgentID           string  `json:"agent_id"`
		TokenBudget       int     `json:"token_budget"`
		TokensUsed        int     `json:"tokens_used"`
		BudgetUtilization float64 `json:"budget_utilization"`
		CompactionNeeded  bool    `json:"compaction_needed"`
	}

	budgets := make([]agentBudget, len(snap.Agents))
	for i, a := range snap.Agents {
		budgets[i] = agentBudget{
			AgentID:           a.AgentID,
			TokenBudget:       a.TokenBudget,
			TokensUsed:        a.TokensUsed,
			BudgetUtilization: a.BudgetUtilization,
			CompactionNeeded:  a.CompactionNeeded,
		}
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"agents":       budgets,
		"total_budget": snap.TotalBudget,
		"total_used":   snap.TotalUsed,
	})
}

// handleContextBudgetSet sets a custom token budget for an agent.
func (d *ContextDomain) handleContextBudgetSet(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	if agentID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing agent_id", nil)
		return
	}

	var body struct {
		TokenBudget int `json:"token_budget"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.TokenBudget <= 0 {
		d.deps.WriteError(w, http.StatusBadRequest, "token_budget must be positive", nil)
		return
	}

	mon := d.deps.ContextHealthMonitor()
	if mon == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "context health monitor not available", nil)
		return
	}

	mon.SetBudgetOverride(agentID, body.TokenBudget)

	d.deps.Logger().Info("context: budget override set",
		"agent_id", agentID,
		"token_budget", body.TokenBudget,
	)

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"agent_id":     agentID,
		"token_budget": body.TokenBudget,
		"status":       "updated",
	})
}
