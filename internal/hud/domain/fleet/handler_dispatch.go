package fleet

import (
	"encoding/json"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// handleAgentDispatch dispatches a task to a specific agent from the HUD.
func (d *FleetDomain) handleAgentDispatch(w http.ResponseWriter, r *http.Request) {
	var body bridge.DispatchTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	params, err := body.ToParams()
	if err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	result, err := d.deps.Agent().DispatchTask(params)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to dispatch task", err)
		return
	}

	d.deps.BroadcastAgentEvent("agent.task.dispatched", map[string]any{
		"target_agent_id": params.TargetAgentID,
		"title":           params.Title,
		"priority":        params.Priority,
	})

	go d.deps.FleetRefresh()

	d.deps.WriteJSON(w, http.StatusOK, result)
}

// handleClaimRelease force-releases a file claim for an agent.
func (d *FleetDomain) handleClaimRelease(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	filePath := r.PathValue("file_path")
	if agentID == "" || filePath == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "agent_id and file_path are required", nil)
		return
	}

	if err := d.deps.Agent().ReleaseFileClaim(agentID, filePath); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to release claim", err)
		return
	}

	d.deps.BroadcastAgentEvent("hud.claim.released", map[string]any{
		"agent_id":  agentID,
		"file_path": filePath,
	})

	go d.deps.FleetRefresh()

	d.deps.WriteJSON(w, http.StatusOK, map[string]string{"status": "released"})
}
