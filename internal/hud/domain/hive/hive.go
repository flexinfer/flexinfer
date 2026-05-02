package hive

import (
	"net/http"
	"net/url"
	"strings"
)

// Domain registers the hive-proxy routes.
type Domain struct {
	deps  Deps
	proxy *operatorProxy
}

// New creates a Domain. The proxy is constructed lazily on the first
// request; if Deps.HiveConfig().BaseURL is unset the proxy stays nil and
// every handler returns 503.
func New(deps Deps) *Domain {
	d := &Domain{deps: deps}
	if cfg := deps.HiveConfig(); cfg.BaseURL != "" {
		if u, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/")); err == nil {
			d.proxy = newOperatorProxy(u, cfg.AdminToken, deps.Logger())
		}
	}
	return d
}

// Name satisfies domain.Domain.
func (d *Domain) Name() string { return "hive" }

// RegisterRoutes wires the hive endpoints to the ServeMux. We register
// each route by exact path/method rather than a single subtree so the
// frontend gets the right HTTP semantics (GET reads stay open; POST
// mutations require the HUD admin token).
//
// The proxy itself rewrites the URL to the operator's path and adds the
// operator's admin bearer; the HUD admin gate above just authenticates
// the caller against the HUD.
func (d *Domain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	// Reads — no admin gate.
	mux.HandleFunc("GET /api/hive/status", mw(d.handleProxyGet))
	mux.HandleFunc("GET /api/hive/policy", mw(d.handleProxyGet))
	mux.HandleFunc("GET /api/hive/kpis", mw(d.handleProxyGet))
	mux.HandleFunc("GET /api/hive/council/runs", mw(d.handleProxyGet))
	mux.HandleFunc("GET /api/hive/council/runs/{id}", mw(d.handleProxyGet))
	mux.HandleFunc("GET /api/hive/pipeline/runs", mw(d.handleProxyGet))
	mux.HandleFunc("GET /api/hive/pipeline/runs/{id}", mw(d.handleProxyGet))
	mux.HandleFunc("GET /api/hive/backlog", mw(d.handleProxyGet))
	mux.HandleFunc("GET /api/hive/backlog/{id}", mw(d.handleProxyGet))
	mux.HandleFunc("GET /api/hive/eval/scores", mw(d.handleProxyGet))

	// Squads (Phase 2 slice 2.5). Read endpoints proxy through without
	// an admin gate so the HUD's Squads panel can poll them. The
	// route-test path is admin-gated to mirror the operator's own gate.
	mux.HandleFunc("GET /api/hive/squads", mw(d.handleProxyGet))
	mux.HandleFunc("GET /api/hive/squads/{name}", mw(d.handleProxyGet))
	mux.HandleFunc("GET /api/hive/squads/{name}/memory", mw(d.handleProxyGet))
	mux.HandleFunc("GET /api/hive/squads/{name}/outcomes", mw(d.handleProxyGet))

	// Audit (Phase 3 slice 3.5). Read endpoints feed the HUD Audit
	// panel; admin POST /run is gated identically to the operator's
	// own admin gate.
	mux.HandleFunc("GET /api/hive/audit/findings", mw(d.handleProxyGet))
	mux.HandleFunc("GET /api/hive/audit/findings/{id}", mw(d.handleProxyGet))

	// Mutations — gated by the HUD's existing admin-token check before
	// the operator's own admin gate. Two layers of auth keep stray
	// browser tabs from triggering the autonomy loop.
	mux.HandleFunc("POST /api/hive/council/run", mw(d.handleProxyAdminPost))
	mux.HandleFunc("POST /api/hive/council/dryrun", mw(d.handleProxyAdminPost))
	mux.HandleFunc("POST /api/hive/pipeline/runs/{backlog_id}/start", mw(d.handleProxyAdminPost))
	mux.HandleFunc("POST /api/hive/pipeline/runs/{id}/pause", mw(d.handleProxyAdminPost))
	mux.HandleFunc("POST /api/hive/pipeline/runs/{id}/resume", mw(d.handleProxyAdminPost))
	mux.HandleFunc("POST /api/hive/pipeline/runs/{id}/escalate", mw(d.handleProxyAdminPost))
	mux.HandleFunc("POST /api/hive/backlog", mw(d.handleProxyAdminPost))
	mux.HandleFunc("POST /api/hive/backlog/sync", mw(d.handleProxyAdminPost))
	mux.HandleFunc("POST /api/hive/eval/run-cross", mw(d.handleProxyAdminPost))
	mux.HandleFunc("POST /api/hive/squads/{name}/route-test", mw(d.handleProxyAdminPost))
	mux.HandleFunc("POST /api/hive/audit/run", mw(d.handleProxyAdminPost))
}

func (d *Domain) handleProxyGet(w http.ResponseWriter, r *http.Request) {
	if d.proxy == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "loom-hive operator not configured", nil)
		return
	}
	d.proxy.ServeHTTP(w, r)
}

func (d *Domain) handleProxyAdminPost(w http.ResponseWriter, r *http.Request) {
	if d.proxy == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "loom-hive operator not configured", nil)
		return
	}
	if !d.deps.RequireAdminToken(w, r) {
		return
	}
	d.proxy.ServeHTTP(w, r)
}
