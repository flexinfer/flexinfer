// Package sandbox implements the sandbox domain -- devbox container
// management endpoints for the HUD dashboard.
package sandbox

import (
	"net/http"
)

// SandboxDomain registers devbox sandbox management endpoints.
type SandboxDomain struct {
	deps Deps
}

// New creates a new SandboxDomain backed by the given Deps implementation.
func New(deps Deps) *SandboxDomain {
	return &SandboxDomain{deps: deps}
}

// Name returns "sandbox".
func (d *SandboxDomain) Name() string { return "sandbox" }

// RegisterRoutes wires the sandbox endpoints to the ServeMux.
func (d *SandboxDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/sandbox", mw(d.handleSandbox))
	mux.HandleFunc("GET /api/sandbox/capabilities", mw(d.handleSandboxCapabilities))
	mux.HandleFunc("GET /api/sandbox/policy", mw(d.handleSandboxPolicy))
	mux.HandleFunc("POST /api/sandbox/start", mw(d.handleSandboxStart))
	mux.HandleFunc("POST /api/sandbox/stop", mw(d.handleSandboxStop))
	mux.HandleFunc("POST /api/sandbox/exec", mw(d.handleSandboxExec))
	mux.HandleFunc("GET /api/sandbox/exec/{exec_id}", mw(d.handleSandboxExecPoll))
	mux.HandleFunc("GET /api/labs/auth-check", mw(d.handleLabsAuthCheck))
}
