package hud

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/coordinator"
)

// handleCoordinatorStatus returns the coordinator's current status.
// GET /api/coordinator/status
func (a *App) handleCoordinatorStatus(w http.ResponseWriter, _ *http.Request) {
	if a.coordinator == nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	a.writeJSON(w, http.StatusOK, a.coordinator.Status())
}

// handleCoordinatorSummarize triggers on-demand session summarization.
// POST /api/coordinator/summarize/{session_id}
func (a *App) handleCoordinatorSummarize(w http.ResponseWriter, r *http.Request) {
	if a.coordinator == nil {
		a.writeError(w, http.StatusServiceUnavailable, "coordinator is not enabled", nil)
		return
	}

	sessionID := r.PathValue("session_id")
	if sessionID == "" {
		a.writeError(w, http.StatusBadRequest, "missing session_id", nil)
		return
	}

	result, err := a.coordinator.SummarizeSession(r.Context(), sessionID)
	if err != nil {
		a.writeError(w, coordinatorErrStatus(err), "summarization failed", err)
		return
	}

	a.writeJSON(w, http.StatusOK, result)
}

// handleCoordinatorCompress triggers an on-demand memory compression cycle.
// POST /api/coordinator/compress
func (a *App) handleCoordinatorCompress(w http.ResponseWriter, r *http.Request) {
	if a.coordinator == nil {
		a.writeError(w, http.StatusServiceUnavailable, "coordinator is not enabled", nil)
		return
	}

	result, err := a.coordinator.RunCompression(r.Context())
	if err != nil {
		a.writeError(w, coordinatorErrStatus(err), "compression failed", err)
		return
	}
	if result == nil {
		a.writeJSON(w, http.StatusOK, map[string]string{"status": "nothing_to_compress"})
		return
	}

	a.writeJSON(w, http.StatusOK, result)
}

// handleCoordinatorPlan generates a workflow plan from a natural language goal.
// POST /api/coordinator/plan
func (a *App) handleCoordinatorPlan(w http.ResponseWriter, r *http.Request) {
	if a.coordinator == nil {
		a.writeError(w, http.StatusServiceUnavailable, "coordinator is not enabled", nil)
		return
	}

	var body struct {
		Goal      string `json:"goal"`
		Namespace string `json:"namespace"`
		Register  bool   `json:"register"` // If true, also register the workflow.
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Goal == "" {
		a.writeError(w, http.StatusBadRequest, "goal is required", nil)
		return
	}

	plan, err := a.coordinator.PlanWorkflow(r.Context(), body.Goal, body.Namespace)
	if err != nil {
		a.writeError(w, coordinatorErrStatus(err), "planning failed", err)
		return
	}

	response := map[string]any{
		"plan": plan,
	}

	// Optionally register the plan as a workflow definition.
	if body.Register {
		defID, err := a.coordinator.RegisterPlan(r.Context(), plan, body.Namespace)
		if err != nil {
			a.logger.Warn("failed to register planned workflow", "error", err)
			response["register_error"] = err.Error()
		} else {
			response["definition_id"] = defID
		}
	}

	a.broadcastAgentEvent("coordinator.plan.complete", map[string]any{
		"workflow_name": plan.Name,
		"step_count":    len(plan.Steps),
	})

	a.writeJSON(w, http.StatusOK, response)
}

// coordinatorErrStatus returns 503 for ErrUnavailable, 502 otherwise.
func coordinatorErrStatus(err error) int {
	if errors.Is(err, coordinator.ErrUnavailable) {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}
