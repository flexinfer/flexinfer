// Package aimodels implements the aimodels domain — read-only HUD
// surface for inspecting the pkg/aimodels role resolver state. Lets
// operators see which model is bound to which role (weaver-router,
// weaver-subagent, mills-judge, coordinator-default, autofix) and how
// often fallbacks have fired.
//
// See .loom/111-product-spec-weaver-qwen3-integration-2026-05-08.md
// (HUD-002).
package aimodels

import (
	"net/http"
)

// AIModelsDomain registers /api/aimodels/* endpoints.
type AIModelsDomain struct {
	deps Deps
}

// New creates a new AIModelsDomain.
func New(deps Deps) *AIModelsDomain {
	return &AIModelsDomain{deps: deps}
}

// Name returns "aimodels".
func (d *AIModelsDomain) Name() string { return "aimodels" }

// RegisterRoutes mounts the read-only role inspection endpoints.
func (d *AIModelsDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/aimodels/roles", mw(d.handleRoles))
}
