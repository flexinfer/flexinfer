package merge

import (
	"net/http"
)

// MergeDomain registers merge orchestration endpoints for fleet merge readiness.
type MergeDomain struct {
	deps Deps
}

// New creates a new MergeDomain backed by the given Deps interface.
func New(deps Deps) *MergeDomain {
	return &MergeDomain{deps: deps}
}

// Name returns "merge".
func (d *MergeDomain) Name() string { return "merge" }

// RegisterRoutes wires the merge orchestration endpoints to the ServeMux.
func (d *MergeDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/merge-queue", mw(d.handleMergeQueue))
	mux.HandleFunc("GET /api/merge-queue/conflicts", mw(d.handleMergeConflicts))
}
