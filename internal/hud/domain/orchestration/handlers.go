package orchestration

import (
	"encoding/json"
	"net/http"

	orch "github.com/crb2nu/loom/internal/hud/orchestration"
)

// handleStatus returns the full orchestration snapshot.
func (d *OrchestrationDomain) handleStatus(w http.ResponseWriter, _ *http.Request) {
	mon := d.deps.OrchestrationMonitor()
	if mon == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "orchestration monitor not available", nil)
		return
	}
	snapshot := mon.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	d.deps.WriteJSON(w, http.StatusOK, snapshot)
}

// handleCapacity returns the current agent capacities.
func (d *OrchestrationDomain) handleCapacity(w http.ResponseWriter, _ *http.Request) {
	mon := d.deps.OrchestrationMonitor()
	if mon == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "orchestration monitor not available", nil)
		return
	}
	snapshot := mon.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"capacities":    snapshot.Capacities,
		"active_agents": snapshot.ActiveAgents,
		"system_load":   snapshot.SystemLoad,
	})
}

// handleRecommendations returns current dispatch recommendations.
func (d *OrchestrationDomain) handleRecommendations(w http.ResponseWriter, _ *http.Request) {
	mon := d.deps.OrchestrationMonitor()
	if mon == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "orchestration monitor not available", nil)
		return
	}
	snapshot := mon.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"recommendations": snapshot.Recommendations,
		"pending_tasks":   snapshot.PendingTasks,
	})
}

// handleDispatch evaluates dispatch for a specific task.
func (d *OrchestrationDomain) handleDispatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TaskID    string `json:"task_id"`
		TaskTitle string `json:"task_title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.TaskID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "task_id is required", nil)
		return
	}

	engine := d.deps.OrchestrationEngine()
	mon := d.deps.OrchestrationMonitor()
	if engine == nil || mon == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "orchestration not available", nil)
		return
	}

	snapshot := mon.Snapshot()
	agentID, reason := engine.EvaluateDispatch(body.TaskID, body.TaskTitle, snapshot.Capacities)

	w.Header().Set("Content-Type", "application/json")
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"task_id":           body.TaskID,
		"recommended_agent": agentID,
		"reason":            reason,
	})
}

// handleGetPolicies returns the current policy configuration.
func (d *OrchestrationDomain) handleGetPolicies(w http.ResponseWriter, _ *http.Request) {
	engine := d.deps.OrchestrationEngine()
	if engine == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "orchestration engine not available", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	d.deps.WriteJSON(w, http.StatusOK, engine.GetPolicy())
}

// handleUpdatePolicies replaces the policy configuration.
func (d *OrchestrationDomain) handleUpdatePolicies(w http.ResponseWriter, r *http.Request) {
	var cfg orch.PolicyConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid policy configuration", err)
		return
	}

	engine := d.deps.OrchestrationEngine()
	if engine == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "orchestration engine not available", nil)
		return
	}

	engine.UpdatePolicy(cfg)

	w.Header().Set("Content-Type", "application/json")
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"policy": engine.GetPolicy(),
	})
}

// handlePreflight checks for file claim conflicts before dispatch.
func (d *OrchestrationDomain) handlePreflight(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AgentID  string `json:"agent_id"`
		FilePath string `json:"file_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.AgentID == "" || body.FilePath == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "agent_id and file_path are required", nil)
		return
	}

	engine := d.deps.OrchestrationEngine()
	br := d.deps.OrchestrationBridge()
	if engine == nil || br == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "orchestration not available", nil)
		return
	}

	claims, err := br.FileClaimList("")
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to fetch file claims", err)
		return
	}

	warnings := engine.PreflightCheck(body.AgentID, body.FilePath, claims)
	if warnings == nil {
		warnings = []orch.ConflictWarning{}
	}

	w.Header().Set("Content-Type", "application/json")
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"agent_id":  body.AgentID,
		"file_path": body.FilePath,
		"conflicts": warnings,
		"clear":     len(warnings) == 0,
	})
}
