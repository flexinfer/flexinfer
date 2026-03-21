// Package coordinator implements the coordinator domain -- LLM-powered
// agent context intelligence (summarization, compression, planning).
package coordinator

import (
	"net/http"
)

// CoordinatorDomain registers coordinator LLM intelligence endpoints.
type CoordinatorDomain struct {
	deps Deps
}

// New creates a new CoordinatorDomain backed by the given Deps implementation.
func New(deps Deps) *CoordinatorDomain {
	return &CoordinatorDomain{deps: deps}
}

// Name returns "coordinator".
func (d *CoordinatorDomain) Name() string { return "coordinator" }

// RegisterRoutes wires the coordinator endpoints to the ServeMux.
func (d *CoordinatorDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/coordinator/status", mw(d.handleCoordinatorStatus))
	mux.HandleFunc("POST /api/coordinator/summarize/{session_id}", mw(d.handleCoordinatorSummarize))
	mux.HandleFunc("POST /api/coordinator/compress", mw(d.handleCoordinatorCompress))
	mux.HandleFunc("POST /api/coordinator/plan", mw(d.handleCoordinatorPlan))

	if m := d.deps.CoordinatorMetrics(); m != nil {
		mux.Handle("GET /api/coordinator/metrics", m.Handler())
	}
}
