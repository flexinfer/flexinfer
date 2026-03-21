// Package workflow implements the workflow domain -- listing, detail,
// approval, rejection, and cancellation of agent workflows.
package workflow

import (
	"net/http"
)

// WorkflowDomain registers workflow management routes.
type WorkflowDomain struct {
	deps Deps
}

// New creates a new WorkflowDomain backed by the given Deps interface.
func New(deps Deps) *WorkflowDomain {
	return &WorkflowDomain{deps: deps}
}

// Name returns "workflow".
func (d *WorkflowDomain) Name() string { return "workflow" }

// RegisterRoutes wires the workflow endpoints to the ServeMux.
func (d *WorkflowDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/workflows", mw(d.handleWorkflowList))
	mux.HandleFunc("GET /api/workflows/{id}", mw(d.handleWorkflowDetail))
	mux.HandleFunc("POST /api/workflows/{id}/approve", mw(d.handleWorkflowApprove))
	mux.HandleFunc("POST /api/workflows/{id}/reject", mw(d.handleWorkflowReject))
	mux.HandleFunc("POST /api/workflows/{id}/cancel", mw(d.handleWorkflowCancel))
}
