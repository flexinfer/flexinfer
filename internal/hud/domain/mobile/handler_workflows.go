package mobile

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func (d *MobileDomain) handleMobileWorkflows(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	mon := d.deps.Monitors()
	limit := parseMobileLimit(r, DefaultLimit, MaxLimit)
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	agentFilter := strings.TrimSpace(r.URL.Query().Get("agent_id"))

	workflows := mon.Workflow.Workflows()
	if workflows == nil {
		workflows = []bridge.WorkflowInfo{}
	}

	filtered := make([]bridge.WorkflowInfo, 0, len(workflows))
	pendingApprovals := 0
	for _, wf := range workflows {
		status := normalizeMobileWorkflowStatus(wf.Status)
		if statusFilter != "" && status != statusFilter {
			continue
		}
		if agentFilter != "" && !d.mobileWorkflowMatchesAgent(wf.ID, agentFilter) {
			continue
		}
		if status == "waiting_approval" {
			pendingApprovals++
		}
		filtered = append(filtered, wf)
	}

	sortSliceStable(filtered, func(i, j int) bool {
		ti := parseMobileTime(filtered[i].CreatedAt)
		tj := parseMobileTime(filtered[j].CreatedAt)
		if ti.Equal(tj) {
			return filtered[i].ID < filtered[j].ID
		}
		return ti.After(tj)
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	result := make([]workflowDTO, len(filtered))
	for i, wf := range filtered {
		result[i] = mapMobileWorkflow(wf)
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"workflows":         result,
		"pending_approvals": pendingApprovals,
	})
}

func (d *MobileDomain) handleMobileWorkflowDetail(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	workflowID := strings.TrimSpace(r.PathValue("workflow_id"))
	if workflowID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "workflow_id is required")
		return
	}

	detail, err := d.deps.Monitors().Workflow.Detail(workflowID)
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to load workflow detail")
		return
	}

	stepNames := make(map[string]string, len(detail.Steps))
	for _, step := range detail.Steps {
		stepNames[step.ID] = step.Name
	}
	events := make([]workflowEventDTO, 0, len(detail.Events))
	for _, evt := range detail.Events {
		entry := workflowEventDTO{
			ID:        evt.ID,
			EventType: evt.EventType,
			Timestamp: evt.Timestamp,
			StepID:    evt.StepID,
		}
		if entry.StepID != "" {
			entry.StepName = stepNames[entry.StepID]
		}
		if len(evt.Details) > 0 {
			if msg, ok := evt.Details["message"].(string); ok {
				entry.Details = strings.TrimSpace(msg)
			}
			if entry.Details == "" {
				if raw, marshalErr := json.Marshal(evt.Details); marshalErr == nil {
					entry.Details = string(raw)
				}
			}
		}
		events = append(events, entry)
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"workflow": map[string]any{
			"id":           detail.ID,
			"name":         detail.Name,
			"status":       normalizeMobileWorkflowStatus(detail.Status),
			"current_step": detail.CurrentStep,
			"progress":     detail.Progress,
			"started_at":   chooseFirstNonEmpty(detail.StartedAt, detail.CreatedAt),
			"completed_at": detail.CompletedAt,
			"error":        detail.Error,
			"steps":        mapMobileWorkflowSteps(detail.Steps),
		},
		"events": events,
	})
}

func (d *MobileDomain) handleMobileWorkflowApprove(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "MISSING_WORKFLOW_ID", "workflow_id is required")
		return
	}

	var body struct {
		StepID string `json:"step_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		d.writeMobileError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if body.StepID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "MISSING_STEP_ID", "step_id is required")
		return
	}

	if err := d.deps.Monitors().Workflow.ApproveStep(workflowID, body.StepID); err != nil {
		d.writeMobileError(w, http.StatusInternalServerError, "APPROVE_FAILED", err.Error())
		return
	}

	d.deps.Logger().Info("workflow approved via mobile", "workflow_id", workflowID, "step_id", body.StepID)
	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"workflow_id": workflowID,
		"step_id":     body.StepID,
		"action":      "approved",
	})
}

func (d *MobileDomain) handleMobileWorkflowReject(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	workflowID := r.PathValue("workflow_id")
	if workflowID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "MISSING_WORKFLOW_ID", "workflow_id is required")
		return
	}

	var body struct {
		StepID string `json:"step_id"`
		Reason string `json:"reason,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		d.writeMobileError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if body.StepID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "MISSING_STEP_ID", "step_id is required")
		return
	}

	if err := d.deps.Monitors().Workflow.RejectStep(workflowID, body.StepID); err != nil {
		d.writeMobileError(w, http.StatusInternalServerError, "REJECT_FAILED", err.Error())
		return
	}

	d.deps.Logger().Info("workflow rejected via mobile", "workflow_id", workflowID, "step_id", body.StepID)
	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"workflow_id": workflowID,
		"step_id":     body.StepID,
		"action":      "rejected",
	})
}

// mobileWorkflowMatchesAgent checks if a workflow involves the given agent.
func (d *MobileDomain) mobileWorkflowMatchesAgent(workflowID, agentID string) bool {
	workflowID = strings.TrimSpace(workflowID)
	agentID = strings.TrimSpace(agentID)
	if workflowID == "" || agentID == "" {
		return true
	}
	detail, err := d.deps.Monitors().Workflow.Detail(workflowID)
	if err != nil {
		return false
	}
	lowerAgent := strings.ToLower(agentID)
	if strings.Contains(strings.ToLower(detail.Name), lowerAgent) {
		return true
	}
	for _, event := range detail.Events {
		if strings.Contains(strings.ToLower(event.StepID), lowerAgent) {
			return true
		}
		for _, value := range event.Details {
			if strings.Contains(strings.ToLower(strings.TrimSpace(toMobileText(value))), lowerAgent) {
				return true
			}
		}
	}
	return false
}
