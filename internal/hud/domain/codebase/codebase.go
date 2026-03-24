// Package codebase implements the codebase domain -- codebase index status,
// semantic and text search, and index management through the HUD.
package codebase

import (
	"net/http"
)

// CodebaseDomain registers codebase index status, search, and management routes.
type CodebaseDomain struct {
	deps Deps
}

// New creates a new CodebaseDomain backed by the given Deps interface.
func New(deps Deps) *CodebaseDomain {
	return &CodebaseDomain{deps: deps}
}

// Name returns "codebase".
func (d *CodebaseDomain) Name() string { return "codebase" }

// RegisterRoutes wires the codebase endpoints to the ServeMux.
func (d *CodebaseDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/codebase/status", mw(d.handleStatus))
	mux.HandleFunc("GET /api/codebase/search", mw(d.handleSearch))
	mux.HandleFunc("GET /api/codebase/text-search", mw(d.handleTextSearch))
	mux.HandleFunc("POST /api/codebase/index", mw(d.handleIndex))
	mux.HandleFunc("GET /api/codebase/index/{job_id}", mw(d.handleIndexPoll))
}
