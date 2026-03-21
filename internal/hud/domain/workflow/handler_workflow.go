package workflow

import (
	"encoding/json"
	"net/http"
)

// handleWorkflowList returns the workflow list from the workflow monitor.
func (d *WorkflowDomain) handleWorkflowList(w http.ResponseWriter, _ *http.Request) {
	workflows := d.deps.WorkflowMonitor().Workflows()
	result := make([]map[string]any, len(workflows))
	for i, wf := range workflows {
		result[i] = map[string]any{
			"id":           wf.ID,
			"name":         wf.Name,
			"status":       wf.Status,
			"current_step": wf.CurrentStep,
			"started_at":   wf.CreatedAt,
			"progress":     wf.Progress,
		}
		if wf.Error != "" {
			result[i]["error"] = wf.Error
		}
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"workflows": result})
}

// handleWorkflowDetail returns detail for a single workflow.
func (d *WorkflowDomain) handleWorkflowDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing workflow id", nil)
		return
	}
	detail, err := d.deps.WorkflowMonitor().Detail(id)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to get workflow", err)
		return
	}

	steps := make([]map[string]any, len(detail.Steps))
	stepNames := make(map[string]string, len(detail.Steps))
	for i, s := range detail.Steps {
		stepNames[s.ID] = s.Name
		steps[i] = map[string]any{
			"id":     s.ID,
			"name":   s.Name,
			"type":   s.Type,
			"status": s.Status,
		}
	}
	events := make([]map[string]any, len(detail.Events))
	for i, e := range detail.Events {
		entry := map[string]any{
			"id":         e.ID,
			"event_type": e.EventType,
			"timestamp":  e.Timestamp,
		}
		if e.StepID != "" {
			entry["step_id"] = e.StepID
			if name := stepNames[e.StepID]; name != "" {
				entry["step_name"] = name
			}
		}
		if len(e.Details) > 0 {
			if msg, ok := e.Details["message"].(string); ok && msg != "" {
				entry["details"] = msg
			} else {
				raw, err := json.Marshal(e.Details)
				if err == nil {
					entry["details"] = string(raw)
				}
			}
		}
		events[i] = entry
	}

	result := map[string]any{
		"id":           detail.ID,
		"name":         detail.Name,
		"status":       detail.Status,
		"current_step": detail.CurrentStep,
		"progress":     detail.Progress,
		"started_at":   detail.CreatedAt,
		"steps":        steps,
		"events":       events,
	}
	if detail.StartedAt != "" {
		result["started_at"] = detail.StartedAt
	}
	if detail.CompletedAt != "" {
		result["completed_at"] = detail.CompletedAt
	}
	if detail.Error != "" {
		result["error"] = detail.Error
	}

	d.deps.WriteJSON(w, http.StatusOK, result)
}

// handleWorkflowApprove approves a workflow step.
func (d *WorkflowDomain) handleWorkflowApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing workflow id", nil)
		return
	}
	var body struct {
		StepID string `json:"step_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.StepID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing step_id", nil)
		return
	}
	if err := d.deps.WorkflowMonitor().ApproveStep(id, body.StepID); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to approve step", err)
		return
	}
	go d.deps.WorkflowMonitor().Refresh()
	d.deps.BroadcastAgentEvent("hud.workflow.approve", map[string]any{
		"workflow_id": id,
		"step_id":     body.StepID,
	})
	d.deps.WriteJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// handleWorkflowReject rejects a workflow step.
func (d *WorkflowDomain) handleWorkflowReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing workflow id", nil)
		return
	}
	var body struct {
		StepID string `json:"step_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.StepID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing step_id", nil)
		return
	}
	if err := d.deps.WorkflowMonitor().RejectStep(id, body.StepID); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to reject step", err)
		return
	}
	go d.deps.WorkflowMonitor().Refresh()
	d.deps.BroadcastAgentEvent("hud.workflow.reject", map[string]any{
		"workflow_id": id,
		"step_id":     body.StepID,
	})
	d.deps.WriteJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// handleWorkflowCancel cancels a running workflow.
func (d *WorkflowDomain) handleWorkflowCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing workflow id", nil)
		return
	}
	if err := d.deps.WorkflowMonitor().CancelWorkflow(id); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to cancel workflow", err)
		return
	}
	go d.deps.WorkflowMonitor().Refresh()
	d.deps.BroadcastAgentEvent("hud.workflow.cancel", map[string]any{
		"workflow_id": id,
	})
	d.deps.WriteJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}
