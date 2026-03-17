// api_agent_task.go implements task and workflow REST handlers.
//
// Handlers:
//   - POST /api/agent/task-update
//   - POST /api/agent/workflow-define
//   - GET  /api/agent/workflow-definitions
package hud

import (
	"encoding/json"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// handleAgentTaskUpdate updates a task's status via the lifecycle API.
// POST /api/agent/task-update
func (a *App) handleAgentTaskUpdate(w http.ResponseWriter, r *http.Request) {
	var body bridge.UpdateTaskParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.ID == "" {
		a.writeError(w, http.StatusBadRequest, "task_id is required", nil)
		return
	}

	if err := a.agent.UpdateTask(body); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to update task", err)
		return
	}

	a.broadcastAgentEvent("agent.task.update", map[string]any{
		"task_id":    body.ID,
		"agent_id":   body.AgentID,
		"session_id": body.SessionID,
		"status":     body.Status,
		"title":      body.Title,
		"resolution": body.Resolution,
	})

	// Increment daily KPI counter for completed tasks.
	if body.Status == "completed" {
		a.fleetMonitor.IncrementKPI("tasks_completed", 1)
	}

	go a.fleetMonitor.Refresh()

	a.writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleAgentWorkflowDefine registers a workflow definition.
// POST /api/agent/workflow-define
func (a *App) handleAgentWorkflowDefine(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body["name"] == nil || body["steps"] == nil {
		a.writeError(w, http.StatusBadRequest, "name and steps are required", nil)
		return
	}

	result, err := a.agent.WorkflowDefine(body)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to define workflow", err)
		return
	}

	a.writeJSON(w, http.StatusOK, result)
}

// handleAgentWorkflowDefinitions lists registered workflow definitions.
// GET /api/agent/workflow-definitions?namespace=...
func (a *App) handleAgentWorkflowDefinitions(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")

	defs, err := a.agent.WorkflowDefinitions(namespace)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list workflow definitions", err)
		return
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"definitions": defs,
		"count":       len(defs),
	})
}
