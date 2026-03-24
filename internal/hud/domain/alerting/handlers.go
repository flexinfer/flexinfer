package alerting

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/crb2nu/loom/internal/hud/alerting"
)

// --- Alert endpoints ---

// handleListAlerts handles GET /api/alerts -- list recent alerts.
func (d *AlertingDomain) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	engine := d.deps.AlertEngine()
	if engine == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"alerts": []any{}})
		return
	}

	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}

	severity := r.URL.Query().Get("severity")
	alerts := engine.ListAlerts(limit)

	// Apply optional severity filter.
	if severity != "" {
		filtered := make([]alerting.Alert, 0, len(alerts))
		for _, a := range alerts {
			if a.Severity == severity {
				filtered = append(filtered, a)
			}
		}
		alerts = filtered
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
}

// handleListRules handles GET /api/alerts/rules -- list alert rules.
func (d *AlertingDomain) handleListRules(w http.ResponseWriter, _ *http.Request) {
	engine := d.deps.AlertEngine()
	if engine == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"rules": []any{}})
		return
	}

	rules := engine.ListRules()
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

// handleUpdateRules handles PUT /api/alerts/rules -- update alert rules.
func (d *AlertingDomain) handleUpdateRules(w http.ResponseWriter, r *http.Request) {
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

	engine := d.deps.AlertEngine()
	if engine == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "alert engine not configured", nil)
		return
	}

	var body struct {
		Rules []alerting.AlertRule `json:"rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	engine.UpdateRules(body.Rules)
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"updated": true, "count": len(body.Rules)})
}

// handleAckAlert handles POST /api/alerts/{id}/ack -- acknowledge an alert.
func (d *AlertingDomain) handleAckAlert(w http.ResponseWriter, r *http.Request) {
	alertID := r.PathValue("id")
	if alertID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "alert id required", nil)
		return
	}

	engine := d.deps.AlertEngine()
	if engine == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "alert engine not configured", nil)
		return
	}

	var body struct {
		AckedBy string `json:"acked_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		body.AckedBy = "hud-user"
	}
	if body.AckedBy == "" {
		body.AckedBy = "hud-user"
	}

	if err := engine.AckAlert(alertID, body.AckedBy); err != nil {
		d.deps.WriteError(w, http.StatusNotFound, err.Error(), nil)
		return
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"acked": true, "id": alertID})
}

// --- Diagnosis endpoint ---

// handleDiagnose handles POST /api/alerts/diagnose -- trigger LLM diagnosis.
func (d *AlertingDomain) handleDiagnose(w http.ResponseWriter, r *http.Request) {
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

	autofix := d.deps.AutoFixEngine()
	if autofix == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "auto-fix engine not configured", nil)
		return
	}

	var body struct {
		Project    string `json:"project"`
		PipelineID int    `json:"pipeline_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Project == "" || body.PipelineID <= 0 {
		d.deps.WriteError(w, http.StatusBadRequest, "project and pipeline_id required", nil)
		return
	}

	diag, err := autofix.DiagnoseFailure(r.Context(), body.Project, body.PipelineID)
	if err != nil {
		d.deps.WriteError(w, http.StatusInternalServerError, err.Error(), nil)
		return
	}

	// Automatically generate a proposal from the diagnosis.
	proposal, propErr := autofix.ProposeAutoFix(*diag)
	resp := map[string]any{"diagnosis": diag}
	if propErr == nil && proposal != nil {
		resp["proposal"] = proposal
	}

	d.deps.WriteJSON(w, http.StatusOK, resp)
}

// --- Auto-fix endpoints ---

// handleListProposals handles GET /api/autofix/proposals -- list proposals.
func (d *AlertingDomain) handleListProposals(w http.ResponseWriter, _ *http.Request) {
	autofix := d.deps.AutoFixEngine()
	if autofix == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"proposals": []any{}})
		return
	}

	proposals := autofix.ListProposals()
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"proposals": proposals})
}

// handleApproveProposal handles POST /api/autofix/proposals/{id}/approve.
func (d *AlertingDomain) handleApproveProposal(w http.ResponseWriter, r *http.Request) {
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

	proposalID := r.PathValue("id")
	if proposalID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "proposal id required", nil)
		return
	}

	autofix := d.deps.AutoFixEngine()
	if autofix == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "auto-fix engine not configured", nil)
		return
	}

	proposal, err := autofix.GetProposal(proposalID)
	if err != nil {
		d.deps.WriteError(w, http.StatusNotFound, err.Error(), nil)
		return
	}

	exec, err := autofix.ExecuteAutoFix(r.Context(), *proposal)
	if err != nil {
		d.deps.WriteError(w, http.StatusInternalServerError, err.Error(), nil)
		return
	}

	d.deps.WriteJSON(w, http.StatusAccepted, map[string]any{"execution": exec})
}

// handleRejectProposal handles POST /api/autofix/proposals/{id}/reject.
func (d *AlertingDomain) handleRejectProposal(w http.ResponseWriter, r *http.Request) {
	proposalID := r.PathValue("id")
	if proposalID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "proposal id required", nil)
		return
	}

	autofix := d.deps.AutoFixEngine()
	if autofix == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "auto-fix engine not configured", nil)
		return
	}

	if err := autofix.RejectProposal(proposalID); err != nil {
		d.deps.WriteError(w, http.StatusNotFound, err.Error(), nil)
		return
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"rejected": true, "proposal_id": proposalID})
}

// handleListExecutions handles GET /api/autofix/executions -- list executions.
func (d *AlertingDomain) handleListExecutions(w http.ResponseWriter, _ *http.Request) {
	autofix := d.deps.AutoFixEngine()
	if autofix == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"executions": []any{}})
		return
	}

	executions := autofix.ListExecutions()
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"executions": executions})
}

// handleGetExecution handles GET /api/autofix/executions/{id} -- execution detail.
func (d *AlertingDomain) handleGetExecution(w http.ResponseWriter, r *http.Request) {
	execID := r.PathValue("id")
	if execID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "execution id required", nil)
		return
	}

	autofix := d.deps.AutoFixEngine()
	if autofix == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "auto-fix engine not configured", nil)
		return
	}

	exec, err := autofix.GetExecution(execID)
	if err != nil {
		d.deps.WriteError(w, http.StatusNotFound, err.Error(), nil)
		return
	}

	d.deps.WriteJSON(w, http.StatusOK, exec)
}

// --- Mobile API endpoints ---

// handleMobileListAlerts handles GET /api/mobile/v1/alerts.
func (d *AlertingDomain) handleMobileListAlerts(w http.ResponseWriter, r *http.Request) {
	d.handleListAlerts(w, r)
}

// handleMobileAckAlert handles POST /api/mobile/v1/alerts/{id}/ack.
func (d *AlertingDomain) handleMobileAckAlert(w http.ResponseWriter, r *http.Request) {
	d.handleAckAlert(w, r)
}

// handleMobileListProposals handles GET /api/mobile/v1/autofix/proposals.
func (d *AlertingDomain) handleMobileListProposals(w http.ResponseWriter, r *http.Request) {
	d.handleListProposals(w, r)
}

// handleMobileApproveProposal handles POST /api/mobile/v1/autofix/proposals/{id}/approve.
func (d *AlertingDomain) handleMobileApproveProposal(w http.ResponseWriter, r *http.Request) {
	d.handleApproveProposal(w, r)
}

// handleMobileRejectProposal handles POST /api/mobile/v1/autofix/proposals/{id}/reject.
func (d *AlertingDomain) handleMobileRejectProposal(w http.ResponseWriter, r *http.Request) {
	d.handleRejectProposal(w, r)
}
