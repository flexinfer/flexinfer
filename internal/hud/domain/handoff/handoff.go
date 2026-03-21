package handoff

import (
	"net/http"
)

// HandoffDomain registers handoff listing, creation, and acceptance endpoints.
type HandoffDomain struct {
	deps Deps
}

// New creates a new HandoffDomain backed by the given Deps interface.
func New(deps Deps) *HandoffDomain {
	return &HandoffDomain{deps: deps}
}

// Name returns "handoff".
func (d *HandoffDomain) Name() string { return "handoff" }

// RegisterRoutes wires the handoff endpoints to the ServeMux.
func (d *HandoffDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/handoffs", mw(d.handleHandoffList))
	mux.HandleFunc("POST /api/handoffs", mw(d.handleHandoffCreate))
	mux.HandleFunc("POST /api/handoffs/{id}/accept", mw(d.handleHandoffAccept))
}
