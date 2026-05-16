package hud

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
)

// registerRoutes sets up all HTTP routes on the given ServeMux.
//
// Domain-specific routes (fleet, spawn, mobile, coordinator, sandbox) are
// registered by domain modules via the domain registry. Core/shared routes
// (status, health, pprof, frontend, etc.) are registered directly here.
func (a *App) registerRoutes(mux *http.ServeMux) {
	// Debug profiling endpoint — goroutine dump, heap, etc.
	// Registered without method prefix so they match both GET and POST,
	// and the subtree pattern /debug/pprof/ catches profile sub-paths.
	mux.HandleFunc("/debug/pprof/", pprofIndex)
	mux.HandleFunc("/debug/pprof/cmdline", pprofCmdline)
	mux.HandleFunc("/debug/pprof/profile", pprofProfile)
	mux.HandleFunc("/debug/pprof/symbol", pprofSymbol)
	mux.HandleFunc("/debug/pprof/trace", pprofTrace)

	// API routes — monitor-backed (cached snapshots).
	mux.HandleFunc("GET /api/status", a.withCORS(a.handleStatus))
	mux.HandleFunc("GET /api/health", a.withCORS(a.handleHealth))
	mux.HandleFunc("GET /api/servers", a.withCORS(a.handleServers))
	mux.HandleFunc("GET /api/fleet", a.withCORS(a.handleFleet))

	// API routes — presence / coordination (from fleet monitor snapshot).
	mux.HandleFunc("GET /api/presence", a.withCORS(a.handlePresence))
	mux.HandleFunc("GET /api/claims", a.withCORS(a.handleClaims))
	mux.HandleFunc("GET /api/worktrees", a.withCORS(a.handleWorktrees))

	// API routes — agent list (from fleet monitor).
	mux.HandleFunc("GET /api/agents", a.withCORS(a.handleAgents))

	// API routes — direct bridge calls (parameterized queries).
	mux.HandleFunc("GET /api/sessions", a.withCORS(a.handleSessions))
	mux.HandleFunc("GET /api/sessions/{id}/entries", a.withCORS(a.handleSessionEntries))
	mux.HandleFunc("GET /api/sessions/{id}/trace", a.withCORS(a.handleSessionTrace))
	mux.HandleFunc("GET /api/tasks", a.withCORS(a.handleTasks))
	mux.HandleFunc("POST /api/tasks", a.withCORS(a.handleCreateTask))
	mux.HandleFunc("PATCH /api/tasks/{id}", a.withCORS(a.handleUpdateTask))
	mux.HandleFunc("GET /api/tunnels", a.withCORS(a.handleTunnels))
	mux.HandleFunc("GET /api/cache", a.withCORS(a.handleCacheStats))
	mux.HandleFunc("GET /api/templates", a.withCORS(a.handleTemplateList))
	mux.HandleFunc("GET /api/annotations", a.withCORS(a.handleAnnotationList))
	mux.HandleFunc("POST /api/annotations", a.withCORS(a.handleAnnotationCreate))
	mux.HandleFunc("GET /api/cost", a.withCORS(a.handleCost))
	mux.HandleFunc("GET /api/rbac", a.withCORS(a.handleRBAC))
	mux.HandleFunc("GET /api/otel", a.withCORS(a.handleOTel))
	mux.HandleFunc("GET /api/traces", a.withCORS(a.handleTraces))
	mux.HandleFunc("GET /api/events", a.withCORS(a.handleSSE))

	// API routes — catalog (enable/disable servers).
	mux.HandleFunc("GET /api/daemon-metrics", a.withCORS(a.handleDaemonMetrics))
	mux.HandleFunc("GET /api/catalog", a.withCORS(a.handleCatalogList))
	mux.HandleFunc("POST /api/catalog/{name}/enable", a.withCORS(a.handleCatalogEnable))
	mux.HandleFunc("POST /api/catalog/{name}/disable", a.withCORS(a.handleCatalogDisable))

	// API routes — engram tech-tree summary (counts by proof_status + tier).
	mux.HandleFunc("GET /api/engrams/summary", a.withCORS(a.handleEngramSummary))

	// API routes — topology graph.
	mux.HandleFunc("GET /api/topology", a.withCORS(a.handleTopology))

	// API routes — command center (KPIs, timeline).
	mux.HandleFunc("GET /api/kpis", a.withCORS(a.handleKPIs))
	mux.HandleFunc("GET /api/timeline", a.withCORS(a.handleTimeline))

	// Domain-managed routes — fleet, spawn, mobile, coordinator, sandbox.
	// Each domain module registers its own route group via the registry.
	if a.domainRegistry != nil {
		a.domainRegistry.RegisterAll(mux, a.withCORS)
	}

	// Agent context telemetry (branch addition — lives in hud package, not domain).
	mux.HandleFunc("GET /api/agent/context-telemetry", a.withCORS(a.handleAgentContextTelemetry))
	mux.HandleFunc("GET /api/agent/metrics", a.withCORS(a.handleAgentMetrics))

	// Lightweight health check — no bridge calls, no CORS overhead, sub-1ms response.
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})

	// Embed/subset config — read by the frontend at boot to decide which
	// top-level views to render in the nav. See Slice B5 of the HUD UX
	// overhaul (.loom/117) for the operator-subset rationale.
	mux.HandleFunc("GET /api/hud/config", a.withCORS(a.handleHUDConfig))

	// CORS preflight for all API routes.
	mux.HandleFunc("OPTIONS /api/", a.handlePreflight)

	// Static frontend files.
	a.serveFrontend(mux)
}

// handleHUDConfig returns boot-time config the frontend needs to decide
// which views are reachable. The subset field is "full" or "operator";
// allowed_views/allowed_sub_views describe the operator-subset allowlist
// the frontend uses to filter the nav and guard router.navigate.
//
// Always served — even when EmbedSubset is empty — so the frontend has
// a single fetch path and doesn't need to special-case missing config.
func (a *App) handleHUDConfig(w http.ResponseWriter, _ *http.Request) {
	subset := strings.ToLower(strings.TrimSpace(a.config.EmbedSubset))
	if subset == "" {
		subset = "full"
	}
	resp := map[string]any{
		"subset":            subset,
		"allowed_views":     allowedViewsForSubset(subset),
		"allowed_sub_views": allowedSubViewsForSubset(subset),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// operatorAllowedViews enumerates the top-level views shown when the HUD
// is embedded with --subset=operator. Mirrored on the frontend as the
// router guard. Stream is a sub-view under "activity", so we expose the
// parent view here.
var operatorAllowedViews = []string{"overview", "agents", "activity"}

// operatorAllowedSubViews enumerates the sub-views inside each allowed
// view. Empty list ⇒ all sub-views allowed.
var operatorAllowedSubViews = map[string][]string{
	"agents":   {"fleet"},
	"activity": {"stream"},
}

func allowedViewsForSubset(subset string) []string {
	if subset == "operator" {
		out := make([]string, len(operatorAllowedViews))
		copy(out, operatorAllowedViews)
		return out
	}
	return nil // nil ⇒ "no restriction"
}

func allowedSubViewsForSubset(subset string) map[string][]string {
	if subset == "operator" {
		out := make(map[string][]string, len(operatorAllowedSubViews))
		for k, v := range operatorAllowedSubViews {
			out[k] = append([]string(nil), v...)
		}
		return out
	}
	return nil
}

// serveFrontend serves the embedded Svelte dist directory, falling back to
// index.html for SPA client-side routing.
//
// Cache policy: index.html is served with no-cache so the browser always
// checks for a new version on reload. Hashed assets (JS/CSS in /assets/)
// are immutable and cached aggressively.
func (a *App) serveFrontend(mux *http.ServeMux) {
	distFS, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		a.logger.Warn("failed to open embedded frontend; frontend will not be served", "error", err)
		return
	}
	fileServer := http.FileServer(http.FS(distFS))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the exact file first.
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		trimmed := strings.TrimPrefix(path, "/")

		// Check if the file exists in the embedded FS.
		f, err := distFS.Open(trimmed)
		if err == nil {
			f.Close()
			// Content-hashed assets are immutable; cache forever.
			if strings.HasPrefix(trimmed, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				// HTML entry point: always revalidate so new builds are picked up.
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fall back to index.html for SPA routing (also no-cache).
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// withCORS wraps a handler to add CORS headers when in dev mode.
func (a *App) withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.config.Dev {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		}
		if a.mobileTokenOutsideMobileAPI(r) {
			a.writeError(w, http.StatusForbidden, "mobile_operator token is restricted to /api/mobile/v1 endpoints", nil)
			return
		}
		next(w, r)
	}
}

// handlePreflight responds to CORS preflight OPTIONS requests.
func (a *App) handlePreflight(w http.ResponseWriter, _ *http.Request) {
	if a.config.Dev {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	}
	w.WriteHeader(http.StatusNoContent)
}
