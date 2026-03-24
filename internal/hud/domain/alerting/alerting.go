// Package alerting implements the alerting domain -- pipeline alerting and
// auto-fix endpoints for the HUD.
package alerting

import (
	"net/http"
)

// AlertingDomain registers pipeline alerting and auto-fix endpoints.
type AlertingDomain struct {
	deps Deps
}

// New creates a new AlertingDomain backed by the given Deps implementation.
func New(deps Deps) *AlertingDomain {
	return &AlertingDomain{deps: deps}
}

// Name returns "alerting".
func (d *AlertingDomain) Name() string { return "alerting" }

// RegisterRoutes wires the alerting endpoints to the ServeMux.
func (d *AlertingDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	// Alert endpoints.
	mux.HandleFunc("GET /api/alerts", mw(d.handleListAlerts))
	mux.HandleFunc("GET /api/alerts/rules", mw(d.handleListRules))
	mux.HandleFunc("PUT /api/alerts/rules", mw(d.handleUpdateRules))
	mux.HandleFunc("POST /api/alerts/{id}/ack", mw(d.handleAckAlert))

	// Diagnosis endpoint.
	mux.HandleFunc("POST /api/alerts/diagnose", mw(d.handleDiagnose))

	// Auto-fix endpoints.
	mux.HandleFunc("GET /api/autofix/proposals", mw(d.handleListProposals))
	mux.HandleFunc("POST /api/autofix/proposals/{id}/approve", mw(d.handleApproveProposal))
	mux.HandleFunc("POST /api/autofix/proposals/{id}/reject", mw(d.handleRejectProposal))
	mux.HandleFunc("GET /api/autofix/executions", mw(d.handleListExecutions))
	mux.HandleFunc("GET /api/autofix/executions/{id}", mw(d.handleGetExecution))

	// Mobile API endpoints.
	mux.HandleFunc("GET /api/mobile/v1/alerts", mw(d.handleMobileListAlerts))
	mux.HandleFunc("POST /api/mobile/v1/alerts/{id}/ack", mw(d.handleMobileAckAlert))
	mux.HandleFunc("GET /api/mobile/v1/autofix/proposals", mw(d.handleMobileListProposals))
	mux.HandleFunc("POST /api/mobile/v1/autofix/proposals/{id}/approve", mw(d.handleMobileApproveProposal))
	mux.HandleFunc("POST /api/mobile/v1/autofix/proposals/{id}/reject", mw(d.handleMobileRejectProposal))
}
