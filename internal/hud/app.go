// Package hud implements the Agent HUD — an interactive dashboard for managing
// AI coding agents, MCP servers, workflows, memory, and the knowledge graph.
//
// The HUD runs as a local HTTP server that serves a Svelte frontend (embedded
// at build time) and exposes a JSON API backed by the loom daemon.
package hud

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
)

//go:embed frontend/dist
var frontendFS embed.FS

// Config holds the configuration for the HUD server.
type Config struct {
	SocketPath  string // Path to the loom daemon Unix socket.
	Dev         bool   // Development mode: enables CORS, skips embed.
	Port        int    // Port to listen on. 0 means pick a random available port.
	MetricsAddr string // Daemon metrics/events HTTP address (e.g., "localhost:9090").
}

// App is the HUD application. It holds the daemon client, agent bridge,
// background monitors, and an in-memory cache used to reduce repeated calls
// to the daemon.
type App struct {
	config Config
	client *bridge.DaemonClient
	agent  *bridge.AgentBridge
	cache  *bridge.Cache
	logger *slog.Logger

	// Background monitors — poll the bridge and maintain cached snapshots.
	fleetMonitor    *monitor.FleetMonitor
	healthMonitor   *monitor.HealthMonitor
	memoryMonitor   *monitor.MemoryMonitor
	workflowMonitor *monitor.WorkflowMonitor

	// SSE streaming — daemon events → browser clients.
	sseHub *SSEHub
}

// Run creates and starts the HUD application. This is the main entry point
// called from the CLI command.
func Run(cfg Config) error {
	logger := slog.Default().With("component", "hud")

	client := bridge.NewDaemonClient(cfg.SocketPath, logger)
	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer client.Close()

	agent := bridge.NewAgentBridge(client)

	app := &App{
		config: cfg,
		client: client,
		agent:  agent,
		cache:  bridge.NewCache(),
		logger: logger,
	}

	// Initialize and start background monitors.
	app.fleetMonitor = monitor.NewFleetMonitor(client, agent, logger)
	app.fleetMonitor.Start(5 * time.Second)
	defer app.fleetMonitor.Stop()

	app.healthMonitor = monitor.NewHealthMonitor(client, logger)
	app.healthMonitor.Start(5 * time.Second)
	defer app.healthMonitor.Stop()

	app.memoryMonitor = monitor.NewMemoryMonitor(agent, logger)
	app.memoryMonitor.Start(10 * time.Second)
	defer app.memoryMonitor.Stop()

	app.workflowMonitor = monitor.NewWorkflowMonitor(agent, logger)
	app.workflowMonitor.Start(5 * time.Second)
	defer app.workflowMonitor.Stop()

	logger.Info("background monitors started",
		"fleet", "5s", "health", "5s", "memory", "10s", "workflow", "5s")

	// Initialize SSE fan-out hub for browser clients.
	app.sseHub = NewSSEHub(logger)

	// Connect to daemon's SSE event stream if metrics address is configured.
	if cfg.MetricsAddr != "" {
		eventsURL := "http://" + cfg.MetricsAddr
		ec := bridge.NewEventConsumer(eventsURL, logger)

		// Wire daemon events to monitor refreshes. The OnAny handler below is
		// the single broadcast point for ALL events to browser clients.
		ec.On("server.health", func(e bridge.SSEEvent) {
			app.healthMonitor.Refresh()
		})
		ec.On("config.reload", func(e bridge.SSEEvent) {
			app.fleetMonitor.Refresh()
			app.healthMonitor.Refresh()
		})
		ec.On("process.start", func(e bridge.SSEEvent) {
			app.fleetMonitor.Refresh()
		})
		ec.On("process.stop", func(e bridge.SSEEvent) {
			app.fleetMonitor.Refresh()
		})
		ec.OnAny(func(e bridge.SSEEvent) {
			app.sseHub.Broadcast(e)
		})

		ec.Start(context.Background())
		defer ec.Stop()
		logger.Info("event consumer started", "url", eventsURL)
	}

	mux := http.NewServeMux()
	app.registerRoutes(mux)

	addr := "127.0.0.1:" + strconv.Itoa(cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	actualAddr := ln.Addr().String()
	url := "http://" + actualAddr
	logger.Info("HUD server started", "url", url, "dev", cfg.Dev)
	fmt.Printf("Agent HUD running at %s\n", url)

	openBrowser(url)

	// WriteTimeout must be 0 to support SSE (Server-Sent Events) connections
	// which are long-lived. A non-zero WriteTimeout would forcibly close SSE
	// streams after the timeout period.
	server := &http.Server{
		Handler:     mux,
		ReadTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ln)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down HUD server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}

// registerRoutes sets up all HTTP routes on the given ServeMux.
func (a *App) registerRoutes(mux *http.ServeMux) {
	// API routes — monitor-backed (cached snapshots).
	mux.HandleFunc("GET /api/status", a.withCORS(a.handleStatus))
	mux.HandleFunc("GET /api/health", a.withCORS(a.handleHealth))
	mux.HandleFunc("GET /api/servers", a.withCORS(a.handleServers))
	mux.HandleFunc("GET /api/fleet", a.withCORS(a.handleFleet))
	mux.HandleFunc("GET /api/workflows", a.withCORS(a.handleWorkflowList))
	mux.HandleFunc("GET /api/workflows/{id}", a.withCORS(a.handleWorkflowDetail))
	mux.HandleFunc("POST /api/workflows/{id}/approve", a.withCORS(a.handleWorkflowApprove))
	mux.HandleFunc("POST /api/workflows/{id}/reject", a.withCORS(a.handleWorkflowReject))
	mux.HandleFunc("GET /api/memory/stats", a.withCORS(a.handleMemoryStats))
	mux.HandleFunc("POST /api/memory/{id}/promote", a.withCORS(a.handleMemoryPromote))
	mux.HandleFunc("POST /api/memory/{id}/demote", a.withCORS(a.handleMemoryDemote))

	// API routes — direct bridge calls (parameterized queries).
	mux.HandleFunc("GET /api/sessions", a.withCORS(a.handleSessions))
	mux.HandleFunc("GET /api/tasks", a.withCORS(a.handleTasks))
	mux.HandleFunc("GET /api/memory/items", a.withCORS(a.handleMemoryItems))
	mux.HandleFunc("GET /api/graph/stats", a.withCORS(a.handleGraphStats))
	mux.HandleFunc("GET /api/graph/entities", a.withCORS(a.handleGraphEntities))
	mux.HandleFunc("GET /api/stream", a.withCORS(a.handleContextStream))
	mux.HandleFunc("GET /api/events", a.withCORS(a.handleSSE))

	// CORS preflight for all API routes.
	mux.HandleFunc("OPTIONS /api/", a.handlePreflight)

	// Static frontend files.
	a.serveFrontend(mux)
}

// serveFrontend serves the embedded Svelte dist directory, falling back to
// index.html for SPA client-side routing.
func (a *App) serveFrontend(mux *http.ServeMux) {
	distFS, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		a.logger.Warn("failed to open embedded frontend; frontend will not be served", "error", err)
		return
	}
	fileServer := http.FileServer(http.FS(distFS))

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the exact file first.
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		// Check if the file exists in the embedded FS.
		f, err := distFS.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fall back to index.html for SPA routing.
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// --- API handlers: Monitor-backed (cached snapshots) ---

// handleStatus returns daemon status from the fleet monitor snapshot.
// JSON contract: {"running": bool, "servers": int, "activeConns": int, "idleConns": int, "processes": []}
func (a *App) handleStatus(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, &bridge.StatusResult{
		Running:     snap.DaemonRunning,
		Servers:     snap.ServerCount,
		ActiveConns: snap.ActiveConns,
		Processes:   snap.Processes,
	})
}

// handleHealth returns server health from the health monitor.
// JSON contract: {"servers": {"name": {"local": {...}, "hub": {...}, "target": "..."}}}
func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) {
	servers := a.healthMonitor.Servers()

	// Reshape into the existing HealthResult JSON contract.
	result := make(map[string]bridge.ServerHealth, len(servers))
	for _, s := range servers {
		sh := bridge.ServerHealth{
			Target: s.Target,
		}
		// Map the consolidated entry back to the local/hub shape.
		if s.Target == "hub" {
			sh.Hub = bridge.HealthEntry{
				Healthy:      s.Healthy,
				ConsecFails:  s.ConsecFails,
				AvgLatencyMs: s.AvgLatencyMs,
				ErrorMessage: s.ErrorMessage,
			}
		} else {
			sh.Local = bridge.HealthEntry{
				Healthy:      s.Healthy,
				ConsecFails:  s.ConsecFails,
				AvgLatencyMs: s.AvgLatencyMs,
				ErrorMessage: s.ErrorMessage,
			}
		}
		result[s.Name] = sh
	}
	a.writeJSON(w, http.StatusOK, &bridge.HealthResult{Servers: result})
}

// handleServers returns MCP server info from the health monitor.
// JSON contract: {"servers": [{"name": "...", "categories": [], "running": bool}]}
func (a *App) handleServers(w http.ResponseWriter, _ *http.Request) {
	servers := a.healthMonitor.Servers()

	// Reshape into the existing ServersResult JSON contract.
	infos := make([]bridge.ServerInfo, len(servers))
	for i, s := range servers {
		infos[i] = bridge.ServerInfo{
			Name:        s.Name,
			Categories:  s.Categories,
			Description: s.Description,
			Running:     s.Running,
		}
	}
	a.writeJSON(w, http.StatusOK, &bridge.ServersResult{Servers: infos})
}

// handleFleet returns the full fleet snapshot — a single aggregated view
// of daemon status, sessions, tasks, memory, graph, and workflows.
func (a *App) handleFleet(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, snap)
}

// handleWorkflowList returns the workflow list from the workflow monitor.
func (a *App) handleWorkflowList(w http.ResponseWriter, _ *http.Request) {
	workflows := a.workflowMonitor.Workflows()
	a.writeJSON(w, http.StatusOK, map[string]any{"workflows": workflows})
}

// handleWorkflowDetail returns detail for a single workflow (cached with 10s TTL).
func (a *App) handleWorkflowDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing workflow id", nil)
		return
	}
	detail, err := a.workflowMonitor.Detail(id)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to get workflow", err)
		return
	}
	a.writeJSON(w, http.StatusOK, detail)
}

// handleWorkflowApprove approves a workflow step and invalidates the cache.
func (a *App) handleWorkflowApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing workflow id", nil)
		return
	}
	var body struct {
		StepID string `json:"step_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.StepID == "" {
		a.writeError(w, http.StatusBadRequest, "missing step_id", nil)
		return
	}
	if err := a.workflowMonitor.ApproveStep(id, body.StepID); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to approve step", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// handleWorkflowReject rejects a workflow step and invalidates the cache.
func (a *App) handleWorkflowReject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing workflow id", nil)
		return
	}
	var body struct {
		StepID string `json:"step_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.StepID == "" {
		a.writeError(w, http.StatusBadRequest, "missing step_id", nil)
		return
	}
	if err := a.workflowMonitor.RejectStep(id, body.StepID); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to reject step", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// handleMemoryStats returns memory hierarchy stats from the memory monitor.
func (a *App) handleMemoryStats(w http.ResponseWriter, _ *http.Request) {
	stats := a.memoryMonitor.Stats()
	if stats == nil {
		// Stats not yet available — fall back to direct call.
		directStats, err := a.agent.MemoryStats()
		if err != nil {
			a.writeError(w, http.StatusBadGateway, "failed to get memory stats", err)
			return
		}
		a.writeJSON(w, http.StatusOK, directStats)
		return
	}
	a.writeJSON(w, http.StatusOK, stats)
}

// handleMemoryPromote promotes a memory item via the monitor (auto-refreshes stats).
func (a *App) handleMemoryPromote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing memory item id", nil)
		return
	}
	if err := a.memoryMonitor.Promote(id); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to promote memory", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "promoted"})
}

// handleMemoryDemote demotes a memory item via the monitor (auto-refreshes stats).
func (a *App) handleMemoryDemote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing memory item id", nil)
		return
	}
	if err := a.memoryMonitor.Demote(id); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to demote memory", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "demoted"})
}

// --- API handlers: Direct bridge calls (parameterized queries) ---

func (a *App) handleSessions(w http.ResponseWriter, _ *http.Request) {
	sessions, err := a.agent.Sessions()
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list sessions", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (a *App) handleTasks(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	var (
		tasks []bridge.TaskInfo
		err   error
	)
	if sessionID != "" {
		tasks, err = a.agent.Tasks(sessionID)
	} else {
		tasks, err = a.agent.AllTasks()
	}
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list tasks", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (a *App) handleMemoryItems(w http.ResponseWriter, r *http.Request) {
	tier := r.URL.Query().Get("tier")
	query := r.URL.Query().Get("query")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	items, err := a.agent.MemoryRecall(tier, query, limit)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to recall memory", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleGraphStats(w http.ResponseWriter, _ *http.Request) {
	if cached, ok := a.cache.Get("graph_stats"); ok {
		a.writeJSON(w, http.StatusOK, cached)
		return
	}
	stats, err := a.agent.GraphStats()
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to get graph stats", err)
		return
	}
	a.cache.Set("graph_stats", stats, 10*time.Second)
	a.writeJSON(w, http.StatusOK, stats)
}

func (a *App) handleGraphEntities(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	entityType := r.URL.Query().Get("type")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	entities, err := a.agent.EntityFind(query, entityType, limit)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to find entities", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"entities": entities})
}

func (a *App) handleContextStream(w http.ResponseWriter, r *http.Request) {
	var since time.Time
	if s := r.URL.Query().Get("since"); s != "" {
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			since = parsed
		}
	}
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	entries, err := a.agent.ContextStream(since, limit)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to get context stream", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// handleSSE delegates to the SSE hub to stream real-time daemon events to
// browser clients. Falls back to heartbeat-only if the hub is not initialized.
func (a *App) handleSSE(w http.ResponseWriter, r *http.Request) {
	if a.sseHub != nil {
		a.sseHub.ServeHTTP(w, r)
		return
	}

	// Fallback: heartbeat-only SSE stream.
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.writeError(w, http.StatusInternalServerError, "streaming not supported", nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if a.config.Dev {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}

	fmt.Fprintf(w, "event: connected\ndata: {\"time\":%q}\n\n", time.Now().Format(time.RFC3339))
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case t := <-ticker.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {\"time\":%q}\n\n", t.Format(time.RFC3339))
			flusher.Flush()
		}
	}
}

// --- Middleware and helpers ---

// withCORS wraps a handler to add CORS headers when in dev mode.
func (a *App) withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.config.Dev {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		next(w, r)
	}
}

// handlePreflight responds to CORS preflight OPTIONS requests.
func (a *App) handlePreflight(w http.ResponseWriter, _ *http.Request) {
	if a.config.Dev {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeJSON marshals v as JSON and writes it to the response.
func (a *App) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		a.logger.Error("failed to write JSON response", "error", err)
	}
}

// writeError writes a JSON error response.
func (a *App) writeError(w http.ResponseWriter, status int, message string, err error) {
	if err != nil {
		a.logger.Error(message, "error", err)
	}
	a.writeJSON(w, status, map[string]string{"error": message})
}

// openBrowser attempts to open a URL in the default browser.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	_ = cmd.Start()
}
