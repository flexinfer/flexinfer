// Package orchestration implements the orchestration domain -- dispatch
// recommendations, capacity monitoring, policy management, and conflict
// preflight checks.
package orchestration

import (
	"net/http"
)

// OrchestrationDomain registers orchestration policy and dispatch routes.
type OrchestrationDomain struct {
	deps Deps
}

// New creates a new OrchestrationDomain backed by the given Deps interface.
func New(deps Deps) *OrchestrationDomain {
	return &OrchestrationDomain{deps: deps}
}

// Name returns "orchestration".
func (d *OrchestrationDomain) Name() string { return "orchestration" }

// RegisterRoutes wires the orchestration endpoints to the ServeMux.
func (d *OrchestrationDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/orchestration/status", mw(d.handleStatus))
	mux.HandleFunc("GET /api/orchestration/capacity", mw(d.handleCapacity))
	mux.HandleFunc("GET /api/orchestration/recommendations", mw(d.handleRecommendations))
	mux.HandleFunc("POST /api/orchestration/dispatch", mw(d.handleDispatch))
	mux.HandleFunc("GET /api/orchestration/policies", mw(d.handleGetPolicies))
	mux.HandleFunc("PUT /api/orchestration/policies", mw(d.handleUpdatePolicies))
	mux.HandleFunc("POST /api/orchestration/preflight", mw(d.handlePreflight))
}
