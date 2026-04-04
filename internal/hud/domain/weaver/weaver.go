package weaver

import (
	"net/http"
)

// WeaverDomain registers FlexInfer weaver query endpoints.
type WeaverDomain struct {
	deps Deps
}

// New creates a new WeaverDomain backed by the given Deps implementation.
func New(deps Deps) *WeaverDomain {
	return &WeaverDomain{deps: deps}
}

// Name returns "weaver".
func (d *WeaverDomain) Name() string { return "weaver" }

// RegisterRoutes wires the weaver endpoints to the ServeMux.
func (d *WeaverDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /api/weaver/status", mw(d.handleStatus))
	mux.HandleFunc("GET /api/weaver/domains", mw(d.handleDomains))
	mux.HandleFunc("GET /api/weaver/history", mw(d.handleHistory))
	mux.HandleFunc("GET /api/weaver/metrics", mw(d.handleMetrics))
}
