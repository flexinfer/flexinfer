package fleet

import (
	"encoding/json"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// handleAgentTaskUpdate updates a task's status via the lifecycle API.
func (d *FleetDomain) handleAgentTaskUpdate(w http.ResponseWriter, r *http.Request) {
	var body bridge.UpdateTaskParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.ID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "task_id is required", nil)
		return
	}

	if err := d.deps.Agent().UpdateTask(body); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to update task", err)
		return
	}

	d.deps.BroadcastAgentEvent("agent.task.update", map[string]any{
		"task_id":    body.ID,
		"agent_id":   body.AgentID,
		"session_id": body.SessionID,
		"status":     body.Status,
		"title":      body.Title,
		"resolution": body.Resolution,
	})

	if body.Status == "completed" {
		d.deps.FleetIncrementKPI("tasks_completed", 1)
	}

	go d.deps.FleetRefresh()

	d.deps.WriteJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleAgentWorkflowDefine registers a workflow definition.
func (d *FleetDomain) handleAgentWorkflowDefine(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body["name"] == nil || body["steps"] == nil {
		d.deps.WriteError(w, http.StatusBadRequest, "name and steps are required", nil)
		return
	}

	result, err := d.deps.Agent().WorkflowDefine(body)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to define workflow", err)
		return
	}

	d.deps.WriteJSON(w, http.StatusOK, result)
}

// handleAgentWorkflowDefinitions lists registered workflow definitions.
func (d *FleetDomain) handleAgentWorkflowDefinitions(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")

	defs, err := d.deps.Agent().WorkflowDefinitions(namespace)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to list workflow definitions", err)
		return
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"definitions": defs,
		"count":       len(defs),
	})
}
