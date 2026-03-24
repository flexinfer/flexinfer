package context

import (
	"net/http"
)

// ContextDomain registers context health and budget management routes.
type ContextDomain struct {
	deps Deps
}

// New creates a new ContextDomain backed by the given Deps interface.
func New(deps Deps) *ContextDomain {
	return &ContextDomain{deps: deps}
}

// Name returns "context".
func (d *ContextDomain) Name() string { return "context" }

// RegisterRoutes wires the context health endpoints to the ServeMux.
func (d *ContextDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/context/health", mw(d.handleContextHealth))
	mux.HandleFunc("GET /api/context/health/{agent_id}", mw(d.handleContextHealthAgent))
	mux.HandleFunc("POST /api/context/compact/{session_id}", mw(d.handleContextCompact))
	mux.HandleFunc("GET /api/context/budget", mw(d.handleContextBudget))
	mux.HandleFunc("PUT /api/context/budget/{agent_id}", mw(d.handleContextBudgetSet))
}
