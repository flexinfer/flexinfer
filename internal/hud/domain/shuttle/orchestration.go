// Package shuttle implements the shuttle domain -- dispatch
// recommendations, capacity monitoring, policy management, and conflict
// preflight checks.
package shuttle

import (
	"net/http"
)

// ShuttleDomain registers shuttle policy and dispatch routes.
type ShuttleDomain struct {
	deps Deps
}

// New creates a new ShuttleDomain backed by the given Deps interface.
func New(deps Deps) *ShuttleDomain {
	return &ShuttleDomain{deps: deps}
}

// Name returns "shuttle".
func (d *ShuttleDomain) Name() string { return "shuttle" }

// RegisterRoutes wires the shuttle endpoints to the ServeMux.
func (d *ShuttleDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/shuttle/status", mw(d.handleStatus))
	mux.HandleFunc("GET /api/shuttle/capacity", mw(d.handleCapacity))
	mux.HandleFunc("GET /api/shuttle/recommendations", mw(d.handleRecommendations))
	mux.HandleFunc("POST /api/shuttle/dispatch", mw(d.handleDispatch))
	mux.HandleFunc("GET /api/shuttle/policies", mw(d.handleGetPolicies))
	mux.HandleFunc("PUT /api/shuttle/policies", mw(d.handleUpdatePolicies))
	mux.HandleFunc("POST /api/shuttle/preflight", mw(d.handlePreflight))
}
