// Package sandbox implements the sandbox domain -- devbox container
// management endpoints for the HUD dashboard.
package sandbox

import (
	"context"
	"net/http"
)

// AppHandlers exposes the subset of *App methods that sandbox routes need.
type AppHandlers interface {
	HandleSandbox(w http.ResponseWriter, r *http.Request)
	HandleSandboxPolicy(w http.ResponseWriter, r *http.Request)
	HandleSandboxStart(w http.ResponseWriter, r *http.Request)
	HandleSandboxStop(w http.ResponseWriter, r *http.Request)
}

// SandboxDomain registers devbox sandbox management endpoints.
type SandboxDomain struct {
	app AppHandlers
}

// New creates a new SandboxDomain backed by the given handler interface.
func New(app AppHandlers) *SandboxDomain {
	return &SandboxDomain{app: app}
}

// Name returns "sandbox".
func (d *SandboxDomain) Name() string { return "sandbox" }

// RegisterRoutes wires the sandbox endpoints to the ServeMux.
func (d *SandboxDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/sandbox", mw(d.app.HandleSandbox))
	mux.HandleFunc("GET /api/sandbox/policy", mw(d.app.HandleSandboxPolicy))
	mux.HandleFunc("POST /api/sandbox/start", mw(d.app.HandleSandboxStart))
	mux.HandleFunc("POST /api/sandbox/stop", mw(d.app.HandleSandboxStop))
}

// Start is a no-op; sandbox monitor lifecycle is managed by *App.
func (d *SandboxDomain) Start(_ context.Context) error { return nil }

// Stop is a no-op.
func (d *SandboxDomain) Stop() error { return nil }
