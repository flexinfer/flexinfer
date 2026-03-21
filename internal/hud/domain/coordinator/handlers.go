package coordinator

import (
	"encoding/json"
	"errors"
	"net/http"

	hudcoord "github.com/crb2nu/loom/internal/hud/coordinator"
)

// handleCoordinatorStatus returns the coordinator's current status.
// GET /api/coordinator/status
func (d *CoordinatorDomain) handleCoordinatorStatus(w http.ResponseWriter, _ *http.Request) {
	coord := d.deps.Coordinator()
	if coord == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, coord.Status())
}

// handleCoordinatorSummarize triggers on-demand session summarization.
// POST /api/coordinator/summarize/{session_id}
func (d *CoordinatorDomain) handleCoordinatorSummarize(w http.ResponseWriter, r *http.Request) {
	coord := d.deps.Coordinator()
	if coord == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "coordinator is not enabled", nil)
		return
	}

	sessionID := r.PathValue("session_id")
	if sessionID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing session_id", nil)
		return
	}

	result, err := coord.SummarizeSession(r.Context(), sessionID)
	if err != nil {
		d.deps.WriteError(w, coordinatorErrStatus(err), "summarization failed", err)
		return
	}

	d.deps.WriteJSON(w, http.StatusOK, result)
}

// handleCoordinatorCompress triggers an on-demand memory compression cycle.
// POST /api/coordinator/compress
func (d *CoordinatorDomain) handleCoordinatorCompress(w http.ResponseWriter, r *http.Request) {
	coord := d.deps.Coordinator()
	if coord == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "coordinator is not enabled", nil)
		return
	}

	result, err := coord.RunCompression(r.Context())
	if err != nil {
		d.deps.WriteError(w, coordinatorErrStatus(err), "compression failed", err)
		return
	}
	if result == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]string{"status": "nothing_to_compress"})
		return
	}

	d.deps.WriteJSON(w, http.StatusOK, result)
}

// handleCoordinatorPlan generates a workflow plan from a natural language goal.
// POST /api/coordinator/plan
func (d *CoordinatorDomain) handleCoordinatorPlan(w http.ResponseWriter, r *http.Request) {
	coord := d.deps.Coordinator()
	if coord == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "coordinator is not enabled", nil)
		return
	}

	var body struct {
		Goal      string `json:"goal"`
		Namespace string `json:"namespace"`
		Register  bool   `json:"register"` // If true, also register the workflow.
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Goal == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "goal is required", nil)
		return
	}

	plan, err := coord.PlanWorkflow(r.Context(), body.Goal, body.Namespace)
	if err != nil {
		d.deps.WriteError(w, coordinatorErrStatus(err), "planning failed", err)
		return
	}

	response := map[string]any{
		"plan": plan,
	}

	// Optionally register the plan as a workflow definition.
	if body.Register {
		defID, regErr := coord.RegisterPlan(r.Context(), plan, body.Namespace)
		if regErr != nil {
			d.deps.Logger().Warn("failed to register planned workflow", "error", regErr)
			response["register_error"] = regErr.Error()
		} else {
			response["definition_id"] = defID
		}
	}

	d.deps.BroadcastAgentEvent("coordinator.plan.complete", map[string]any{
		"workflow_name": plan.Name,
		"step_count":    len(plan.Steps),
	})

	d.deps.WriteJSON(w, http.StatusOK, response)
}

// coordinatorErrStatus returns 503 for ErrUnavailable, 502 otherwise.
func coordinatorErrStatus(err error) int {
	if errors.Is(err, hudcoord.ErrUnavailable) {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}
