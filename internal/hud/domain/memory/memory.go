package memory

import (
	"net/http"
)

// MemoryDomain registers memory management routes.
type MemoryDomain struct {
	deps Deps
}

// New creates a new MemoryDomain backed by the given Deps interface.
func New(deps Deps) *MemoryDomain {
	return &MemoryDomain{deps: deps}
}

// Name returns "memory".
func (d *MemoryDomain) Name() string { return "memory" }

// RegisterRoutes wires the memory endpoints to the ServeMux.
func (d *MemoryDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/memory/stats", mw(d.handleMemoryStats))
	mux.HandleFunc("POST /api/memory/{id}/promote", mw(d.handleMemoryPromote))
	mux.HandleFunc("POST /api/memory/{id}/demote", mw(d.handleMemoryDemote))
	mux.HandleFunc("GET /api/memory/items", mw(d.handleMemoryItems))
	mux.HandleFunc("POST /api/memory", mw(d.handleMemoryAdd))
	mux.HandleFunc("DELETE /api/memory/{id}", mw(d.handleMemoryDelete))
	mux.HandleFunc("GET /api/memory/compaction", mw(d.handleMemoryCompaction))
}
