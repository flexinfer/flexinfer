// Package coordinator implements the coordinator domain -- LLM-powered
// agent context intelligence (summarization, compression, planning).
package coordinator

import (
	"net/http"
)

// AppHandlers exposes the subset of *App methods that coordinator routes need.
type AppHandlers interface {
	HandleCoordinatorStatus(w http.ResponseWriter, r *http.Request)
	HandleCoordinatorSummarize(w http.ResponseWriter, r *http.Request)
	HandleCoordinatorCompress(w http.ResponseWriter, r *http.Request)
	HandleCoordinatorPlan(w http.ResponseWriter, r *http.Request)

	// CoordinatorMetricsHandler returns the Prometheus metrics handler for the
	// coordinator, or nil if the coordinator is not enabled.
	CoordinatorMetricsHandler() http.Handler
}

// CoordinatorDomain registers coordinator LLM intelligence endpoints.
type CoordinatorDomain struct {
	app AppHandlers
}

// New creates a new CoordinatorDomain backed by the given handler interface.
func New(app AppHandlers) *CoordinatorDomain {
	return &CoordinatorDomain{app: app}
}

// Name returns "coordinator".
func (d *CoordinatorDomain) Name() string { return "coordinator" }

// RegisterRoutes wires the coordinator endpoints to the ServeMux.
func (d *CoordinatorDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/coordinator/status", mw(d.app.HandleCoordinatorStatus))
	mux.HandleFunc("POST /api/coordinator/summarize/{session_id}", mw(d.app.HandleCoordinatorSummarize))
	mux.HandleFunc("POST /api/coordinator/compress", mw(d.app.HandleCoordinatorCompress))
	mux.HandleFunc("POST /api/coordinator/plan", mw(d.app.HandleCoordinatorPlan))

	if h := d.app.CoordinatorMetricsHandler(); h != nil {
		mux.Handle("GET /api/coordinator/metrics", h)
	}
}
