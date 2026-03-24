package mobile

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

const mobileWorkflowDeprecationMessage = "Workflow approvals are deprecated in the mobile surface; use Loom tasks and pipelines instead."

type mobileWorkflowListItemDTO struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name,omitempty"`
	Status             string  `json:"status"`
	CurrentStep        string  `json:"current_step,omitempty"`
	Progress           float64 `json:"progress"`
	StartedAt          string  `json:"started_at"`
	CompletedAt        string  `json:"completed_at,omitempty"`
	Error              string  `json:"error,omitempty"`
	Deprecated         bool    `json:"deprecated,omitempty"`
	DeprecationMessage string  `json:"deprecation_message,omitempty"`
}

type mobileWorkflowsResponseDTO struct {
	Workflows                  []mobileWorkflowListItemDTO `json:"workflows"`
	PendingApprovals           int                         `json:"pending_approvals"`
	DeprecatedPendingApprovals int                         `json:"deprecated_pending_approvals,omitempty"`
	ActiveWorkflows            int                         `json:"active_workflows,omitempty"`
	Deprecated                 bool                        `json:"deprecated,omitempty"`
	DeprecationMessage         string                      `json:"deprecation_message,omitempty"`
}

type mobileWorkflowDetailDTO struct {
	ID                 string           `json:"id"`
	Name               string           `json:"name,omitempty"`
	Status             string           `json:"status"`
	CurrentStep        string           `json:"current_step,omitempty"`
	Progress           float64          `json:"progress"`
	StartedAt          string           `json:"started_at"`
	CompletedAt        string           `json:"completed_at,omitempty"`
	Error              string           `json:"error,omitempty"`
	Steps              []map[string]any `json:"steps"`
	Deprecated         bool             `json:"deprecated,omitempty"`
	DeprecationMessage string           `json:"deprecation_message,omitempty"`
}

type mobileWorkflowDetailResponseDTO struct {
	Workflow           mobileWorkflowDetailDTO `json:"workflow"`
	Events             []workflowEventDTO      `json:"events"`
	Deprecated         bool                    `json:"deprecated,omitempty"`
	DeprecationMessage string                  `json:"deprecation_message,omitempty"`
}

type mobileWorkflowMutationResponseDTO struct {
	WorkflowID         string `json:"workflow_id"`
	StepID             string `json:"step_id"`
	Action             string `json:"action"`
	Deprecated         bool   `json:"deprecated,omitempty"`
	DeprecationMessage string `json:"deprecation_message,omitempty"`
}

func buildMobileWorkflowListItem(wf bridge.WorkflowInfo) mobileWorkflowListItemDTO {
	item := mobileWorkflowListItemDTO{
		ID:          wf.ID,
		Name:        wf.Name,
		Status:      normalizeMobileWorkflowStatus(wf.Status),
		CurrentStep: wf.CurrentStep,
		Progress:    wf.Progress,
		StartedAt:   wf.CreatedAt,
		Error:       wf.Error,
	}
	if item.Status == "waiting_approval" {
		item.Deprecated = true
		item.DeprecationMessage = mobileWorkflowDeprecationMessage
	}
	return item
}

func buildMobileWorkflowsResponse(workflows []bridge.WorkflowInfo, limit int, statusFilter, agentFilter string, matchesAgent func(string, string) bool) mobileWorkflowsResponseDTO {
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
		if agentFilter != "" && matchesAgent != nil && !matchesAgent(wf.ID, agentFilter) {
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

	result := make([]mobileWorkflowListItemDTO, len(filtered))
	for i, wf := range filtered {
		result[i] = buildMobileWorkflowListItem(wf)
	}

	activeWorkflows := len(filtered) - pendingApprovals
	if activeWorkflows < 0 {
		activeWorkflows = 0
	}

	return mobileWorkflowsResponseDTO{
		Workflows:                  result,
		PendingApprovals:           0,
		DeprecatedPendingApprovals: pendingApprovals,
		ActiveWorkflows:            activeWorkflows,
		Deprecated:                 true,
		DeprecationMessage:         mobileWorkflowDeprecationMessage,
	}
}

func buildMobileWorkflowDetailResponse(detail *bridge.WorkflowDetail) mobileWorkflowDetailResponseDTO {
	if detail == nil {
		return mobileWorkflowDetailResponseDTO{
			Deprecated:         true,
			DeprecationMessage: mobileWorkflowDeprecationMessage,
		}
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

	return mobileWorkflowDetailResponseDTO{
		Workflow: mobileWorkflowDetailDTO{
			ID:                 detail.ID,
			Name:               detail.Name,
			Status:             normalizeMobileWorkflowStatus(detail.Status),
			CurrentStep:        detail.CurrentStep,
			Progress:           detail.Progress,
			StartedAt:          chooseFirstNonEmpty(detail.StartedAt, detail.CreatedAt),
			CompletedAt:        detail.CompletedAt,
			Error:              detail.Error,
			Steps:              mapMobileWorkflowSteps(detail.Steps),
			Deprecated:         true,
			DeprecationMessage: mobileWorkflowDeprecationMessage,
		},
		Events:             events,
		Deprecated:         true,
		DeprecationMessage: mobileWorkflowDeprecationMessage,
	}
}

func mobileWorkflowMutationResponse(workflowID, stepID, action string) mobileWorkflowMutationResponseDTO {
	return mobileWorkflowMutationResponseDTO{
		WorkflowID:         workflowID,
		StepID:             stepID,
		Action:             action,
		Deprecated:         true,
		DeprecationMessage: mobileWorkflowDeprecationMessage,
	}
}

func (d *MobileDomain) handleMobileWorkflows(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	mon := d.deps.Monitors()
	limit := parseMobileLimit(r, DefaultLimit, MaxLimit)
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	agentFilter := strings.TrimSpace(r.URL.Query().Get("agent_id"))

	if mon.Workflow == nil {
		d.writeMobileJSON(w, http.StatusOK, buildMobileWorkflowsResponse(nil, limit, statusFilter, agentFilter, nil))
		return
	}
	workflows := mon.Workflow.Workflows()
	if workflows == nil {
		workflows = []bridge.WorkflowInfo{}
	}

	response := buildMobileWorkflowsResponse(workflows, limit, statusFilter, agentFilter, d.mobileWorkflowMatchesAgent)
	d.writeMobileJSON(w, http.StatusOK, response)
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

	mon := d.deps.Monitors()
	if mon.Workflow == nil {
		d.writeMobileError(w, http.StatusServiceUnavailable, "upstream_error", "workflow monitor is unavailable")
		return
	}

	detail, err := mon.Workflow.Detail(workflowID)
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to load workflow detail")
		return
	}

	d.writeMobileJSON(w, http.StatusOK, buildMobileWorkflowDetailResponse(detail))
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

	mon := d.deps.Monitors()
	if mon.Workflow == nil {
		d.writeMobileError(w, http.StatusServiceUnavailable, "upstream_error", "workflow monitor is unavailable")
		return
	}

	if err := mon.Workflow.ApproveStep(workflowID, body.StepID); err != nil {
		d.writeMobileError(w, http.StatusInternalServerError, "APPROVE_FAILED", err.Error())
		return
	}

	d.deps.Logger().Info("workflow approved via mobile", "workflow_id", workflowID, "step_id", body.StepID)
	d.writeMobileJSON(w, http.StatusOK, mobileWorkflowMutationResponse(workflowID, body.StepID, "approved"))
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

	mon := d.deps.Monitors()
	if mon.Workflow == nil {
		d.writeMobileError(w, http.StatusServiceUnavailable, "upstream_error", "workflow monitor is unavailable")
		return
	}

	if err := mon.Workflow.RejectStep(workflowID, body.StepID); err != nil {
		d.writeMobileError(w, http.StatusInternalServerError, "REJECT_FAILED", err.Error())
		return
	}

	d.deps.Logger().Info("workflow rejected via mobile", "workflow_id", workflowID, "step_id", body.StepID)
	d.writeMobileJSON(w, http.StatusOK, mobileWorkflowMutationResponse(workflowID, body.StepID, "rejected"))
}

// mobileWorkflowMatchesAgent checks if a workflow involves the given agent.
func (d *MobileDomain) mobileWorkflowMatchesAgent(workflowID, agentID string) bool {
	workflowID = strings.TrimSpace(workflowID)
	agentID = strings.TrimSpace(agentID)
	if workflowID == "" || agentID == "" {
		return true
	}
	mon := d.deps.Monitors()
	if mon.Workflow == nil {
		return false
	}
	detail, err := mon.Workflow.Detail(workflowID)
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
