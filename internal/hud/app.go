// Package hud implements the Agent HUD — an interactive dashboard for managing
// AI coding agents, MCP servers, workflows, memory, and the knowledge graph.
//
// The HUD runs as a local HTTP server that serves a Svelte frontend (embedded
// at build time) and exposes a JSON API backed by the loom daemon.
package hud

import (
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/trace"

	loomcache "github.com/crb2nu/loom/internal/cache"
	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/coordinator"
	"github.com/crb2nu/loom/internal/hud/domain"
	"github.com/crb2nu/loom/internal/hud/monitor"
	"github.com/crb2nu/loom/internal/hud/window"
	"github.com/crb2nu/loom/internal/tui"
)

//go:embed frontend/dist
var frontendFS embed.FS

// Config holds the configuration for the HUD server.
type Config struct {
	SocketPath  string // Path to the loom daemon Unix socket.
	Dev         bool   // Development mode: enables CORS, skips embed.
	Port        int    // Port to listen on. 0 means pick a random available port.
	MetricsAddr string // Daemon metrics/events HTTP address (e.g., "localhost:9090").
	Overlay     bool   // Enable macOS native overlay (NSPanel + Cmd+Shift+L hotkey).

	// Overlay appearance (only used when Overlay is true).
	OverlayEdge         string  // Screen edge: "right" or "left" (default "right").
	OverlayWidth        int     // Panel width in points (default 380).
	OverlayOpacity      float64 // Background opacity 0.0–1.0 (default 0.92).
	OverlayCornerRadius float64 // Corner radius in points (default 12).

	// Coordinator (FlexInfer LLM integration). Empty URL = disabled.
	FlexInferURL     string // FlexInfer proxy URL (e.g., "http://flexinfer-proxy:8080").
	FlexInferKey     string // Optional API key for FlexInfer.
	CoordinatorModel string // Default model for coordinator tasks (e.g., "qwen3-8b").

	// Webhook push: forward presence+session snapshots to a remote endpoint.
	WebhookURL     string // Push URL (e.g., "https://deck.flexinfer.ai/api/agents/hud/push").
	WebhookToken   string // Bearer token for push auth.
	WebhookResolve string // Override DNS resolution for webhook hostname (e.g., LAN IP to bypass Cloudflare).
	AdminToken     string // Token required for admin-only HUD mutations.
	// Mobile operator API auth policy.
	MobileOperatorToken  string // Bearer token for /api/mobile/v1 routes.
	MobileOperatorScopes string // Comma-separated scopes granted to mobile token.

	// Mobile rate limiting.
	MobileRateLimitMutation int // Max mutation requests per actor per minute (0 = disabled).
	MobileRateLimitRead     int // Max read requests per actor per minute (0 = disabled).

	// Mobile push notifications (behind feature flag).
	MobilePushEnabled bool // Enable push notification endpoints (default: false).

	// APNs configuration for push delivery.
	APNsKeyPath string // Path to .p8 signing key.
	APNsKeyID   string // APNs key ID from Apple Developer portal.
	APNsTeamID  string // Apple Developer Team ID.
	APNsTopic   string // APNs topic (bundle ID).
	APNsSandbox bool   // Use sandbox APNs endpoint (development).

	// TLS for gateway mode.
	TLSCert     string // Path to PEM certificate file.
	TLSKey      string // Path to PEM private key file.
	BindAddress string // Listen address (default: 127.0.0.1).

	// TUI mode: launch a bubbletea terminal UI instead of the web dashboard.
	TUI bool

	// Spawn orchestrator (headless agent spawning via devbox K8s backend).
	SpawnEnabled    bool   // Enable spawn endpoints (default: false).
	SpawnKubeconfig string // Kubeconfig for spawn K8s backend.
	SpawnNamespace  string // K8s namespace for spawn pods (default: "devbox").
	SpawnRegistry   string // Image registry (default: "registry.harbor.lan").
	SpawnSyncMode   string // Workspace sync: "git-clone" or "nfs".
	SpawnGitBaseURL string // Git base URL for git-clone mode.
	SpawnGitSecret  string // K8s secret with git token.
	SpawnProjects   string // Comma-separated project names for spawn picker.

	// Pipeline monitoring (GitLab CI).
	PipelineProjects string // Comma-separated GitLab project paths to monitor.
}

// App is the HUD application. It holds the daemon client, agent bridge,
// background monitors, and an in-memory cache used to reduce repeated calls
// to the daemon.
type App struct {
	config       Config
	client       bridge.Caller
	agent        *bridge.AgentBridge
	cache        loomcache.Store
	cacheBackend string // "memory" or "redis" — exposed in /api/health.
	logger       *slog.Logger

	// Background monitors — poll the bridge and maintain cached snapshots.
	fleetMonitor    *monitor.FleetMonitor
	healthMonitor   *monitor.HealthMonitor
	memoryMonitor   *monitor.MemoryMonitor
	workflowMonitor *monitor.WorkflowMonitor
	streamMonitor   *monitor.StreamMonitor
	sandboxMonitor  *monitor.SandboxMonitor
	costMonitor     *monitor.CostMonitor
	pipelineMonitor *monitor.PipelineMonitor

	// SSE streaming — daemon events → browser clients.
	sseHub *SSEHub

	// Coordinator — optional LLM-powered agent context intelligence.
	coordinator         *coordinator.Coordinator
	coordinatorMetrics  *coordinator.Metrics
	agentContextMetrics *AgentContextMetrics
	agentContextLatest  *AgentContextLatestStore

	// Timeline event log — ring buffer for unified activity timeline.
	eventLog *EventLog

	// Nudge queue — pending nudges per agent, delivered via heartbeat response.
	nudgeQueue *NudgeQueue

	// Mobile API hardening.
	mobileRateLimiter    *MobileRateLimiter
	mobileRevocationList *MobileTokenRevocationList
	deviceTokenStore     *DeviceTokenStore // Push notification device tokens (MBL-7).

	// OTel instrumentation.
	tracer       trace.Tracer
	metrics      *HUDMetrics
	otelShutdown func(context.Context) error

	// Push notification bridge (SSE events → APNs).
	pushBridge *PushEventBridge

	// Headless agent spawn orchestrator.
	spawner *SpawnOrchestrator

	// Domain registry — self-contained feature modules that own their routes.
	domainRegistry *domain.Registry
}

const (
	// pushTokenCleanupInterval controls how often stale push tokens are pruned.
	pushTokenCleanupInterval = 1 * time.Hour
	// pushTokenMaxIdle controls token staleness before automatic removal.
	pushTokenMaxIdle = 30 * 24 * time.Hour
)

// Run creates and starts the HUD application. This is the main entry point
// called from the CLI command. It delegates to NewApp + StartMonitors for
// construction and monitor lifecycle, then adds standalone-only concerns
// (daemon client, event consumer, TLS, signal handling).
func Run(cfg Config) error {
	var logger *slog.Logger
	if cfg.TUI {
		// In TUI mode, route HUD logs to the TUI log file so they don't
		// corrupt the bubbletea alt-screen.
		logger = newHUDTUILogger().With("component", "hud")
	} else {
		logger = slog.Default().With("component", "hud")
	}

	client := bridge.NewDaemonClient(cfg.SocketPath, logger)
	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer client.Close()

	app, err := NewApp(cfg, client, logger)
	if err != nil {
		return fmt.Errorf("create app: %w", err)
	}

	ctx := context.Background()
	if err := app.StartMonitors(ctx); err != nil {
		return fmt.Errorf("start monitors: %w", err)
	}
	defer app.StopMonitors()

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
		ec.On("decomp.hint", func(e bridge.SSEEvent) {
			// Log to activity timeline.
			app.eventLog.Append(TimelineEntry{
				Timestamp: e.Timestamp,
				EventType: "decomp.hint",
				Data:      e.Data,
			})

			// Parse event data for nudge content.
			var hint struct {
				Server     string `json:"server"`
				Tool       string `json:"tool"`
				Suggestion string `json:"suggestion"`
				Workflow   string `json:"workflow"`
			}
			if err := json.Unmarshal(e.Data, &hint); err != nil {
				logger.Warn("decomp.hint: failed to parse event data", "err", err)
				return
			}

			content := fmt.Sprintf("Tool %q returned a large response. %s", hint.Tool, hint.Suggestion)

			// Enqueue advisory nudge for all active agents.
			snap := app.fleetMonitor.Snapshot()
			for _, a := range snap.Agents {
				if a.Status != "active" {
					continue
				}
				app.nudgeQueue.Add(a.AgentID, NudgeEntry{
					ID:        NewNudgeID(a.AgentID),
					Type:      "context_inject",
					Lane:      "advice",
					Content:   content,
					FromAgent: "hud",
				})
			}
		})
		// Only broadcast to SSE hub when browser clients may be connected.
		// In TUI mode no browser connects, so skip the fan-out overhead.
		if !cfg.TUI {
			ec.OnAny(func(e bridge.SSEEvent) {
				app.sseHub.Broadcast(e)
			})
		}

		// Wire push bridge to daemon events for push-worthy notifications.
		if app.pushBridge != nil {
			ec.OnAny(func(e bridge.SSEEvent) {
				go app.pushBridge.HandleEvent(e)
			})
		}

		ec.Start(context.Background())
		defer ec.Stop()
		logger.Info("event consumer started", "url", eventsURL)
	}

	mux := http.NewServeMux()
	app.registerRoutes(mux)

	bindAddr := cfg.BindAddress
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	addr := bindAddr + ":" + strconv.Itoa(cfg.Port)
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	// Wrap with TLS if cert and key are configured.
	scheme := "http"
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		cert, tlsErr := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if tlsErr != nil {
			ln.Close()
			return fmt.Errorf("load TLS cert/key: %w", tlsErr)
		}
		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		ln = tls.NewListener(ln, tlsCfg)
		scheme = "https"
		logger.Info("TLS enabled", "cert", cfg.TLSCert)
	} else if cfg.MobileOperatorToken != "" && bindAddr != "127.0.0.1" && bindAddr != "localhost" {
		logger.Warn("mobile operator token configured without TLS on non-localhost address",
			"bind", bindAddr)
	}

	actualAddr := ln.Addr().String()
	url := browserURL(scheme, bindAddr, ln.Addr())

	// Write the bound port to a file so CLI commands can discover it.
	portFile := PortFilePath()
	actualPort := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	if err := os.WriteFile(portFile, []byte(actualPort), 0644); err != nil {
		logger.Warn("failed to write port file", "path", portFile, "error", err)
	} else {
		logger.Info("port file written", "path", portFile, "port", actualPort)
	}
	defer os.Remove(portFile)

	logger.Info("HUD server started", "url", url, "listen_addr", actualAddr, "dev", cfg.Dev)
	fmt.Printf("Agent HUD running at %s\n", url)

	if !cfg.TUI {
		openBrowser(url)
	}

	// WriteTimeout must be 0 to support SSE (Server-Sent Events) connections
	// which are long-lived. A non-zero WriteTimeout would forcibly close SSE
	// streams after the timeout period.
	server := &http.Server{
		Handler:     mux,
		ReadTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM/SIGHUP.
	// SIGHUP is sent when the controlling terminal closes (e.g., Ghostty quick terminal).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ln)
	}()

	if cfg.TUI {
		// TUI mode: run bubbletea on the main thread while the HTTP server
		// runs in the background goroutine above. Same pattern as overlay mode.
		go func() {
			select {
			case err := <-errCh:
				if err != nil {
					logger.Error("HTTP server error", "error", err)
				}
			case <-ctx.Done():
			}
		}()

		tuiErr := tuiRun(tui.Deps{
			Agent:  app.agent,
			Fleet:  app.fleetMonitor,
			Health: app.healthMonitor,
			Memory: app.memoryMonitor,
			Stream: app.streamMonitor,
		}, ctx)

		// TUI exited — shut down HTTP server.
		stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
		return tuiErr
	}

	if cfg.Overlay {
		if !window.Available() {
			return fmt.Errorf("native overlay requires a CGO-enabled darwin build")
		}

		// Native macOS overlay mode: the HTTP server runs in a background
		// goroutine above, and we run the Cocoa event loop on the main thread.
		// Carbon hotkeys and AppKit panels need an active run loop on thread 0.
		// NOTE: runtime.LockOSThread() is called in cmd/loom/main.go init()
		// to guarantee goroutine 1 stays on thread 0 from process start.

		// Initialize NSApplication before any AppKit calls.
		window.InitApp()

		// Build overlay URL with query parameter so the frontend renders
		// the compact OverlayShell instead of the full dashboard.
		overlayURL := url + "?overlay=1"
		window.CreateOverlayPanel(window.OverlayConfig{
			Edge:          cfg.OverlayEdge,
			Width:         cfg.OverlayWidth,
			Opacity:       cfg.OverlayOpacity,
			CornerRadius:  cfg.OverlayCornerRadius,
			URL:           overlayURL,
			RememberState: true,
		})
		if err := window.RegisterHotkey(window.AnimatedToggle); err != nil {
			logger.Warn("failed to register Cmd+Shift+L hotkey", "error", err)
		} else {
			logger.Info("native overlay enabled — press Cmd+Shift+L to toggle")
			fmt.Println("Native overlay: press Cmd+Shift+L to toggle")
		}

		// Watch for signal/error in background and stop the event loop.
		go func() {
			select {
			case err := <-errCh:
				if err != nil {
					logger.Error("HTTP server error", "error", err)
				}
			case <-ctx.Done():
			}
			logger.Info("shutting down HUD server")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server.Shutdown(shutdownCtx)
			window.UnregisterHotkey()
			window.Destroy()
			window.StopApp()
		}()

		// Block on the Cocoa event loop (runs until StopApp is called).
		window.RunApp()
		return nil
	}

	// Non-overlay mode: block on signal/error directly.
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
	mux.HandleFunc("GET /api/events", a.withCORS(a.handleSSE))

	// API routes — catalog (enable/disable servers).
	mux.HandleFunc("GET /api/daemon-metrics", a.withCORS(a.handleDaemonMetrics))
	mux.HandleFunc("GET /api/catalog", a.withCORS(a.handleCatalogList))
	mux.HandleFunc("POST /api/catalog/{name}/enable", a.withCORS(a.handleCatalogEnable))
	mux.HandleFunc("POST /api/catalog/{name}/disable", a.withCORS(a.handleCatalogDisable))

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

	// CORS preflight for all API routes.
	mux.HandleFunc("OPTIONS /api/", a.handlePreflight)

	// Static frontend files.
	a.serveFrontend(mux)
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
// JSON contract: {"servers": {"name": {"local": {...}, "hub": {...}, "target": "...", "divergence": ...}}, "divergence": [...]}
func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) {
	servers := a.healthMonitor.Servers()

	// Reshape into the existing HealthResult JSON contract.
	healthServers := make(map[string]bridge.ServerHealth, len(servers))
	var divergences []bridge.HealthDivergenceEntry
	for _, s := range servers {
		sh := bridge.ServerHealth{
			Target:     s.Target,
			Divergence: s.Divergence,
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
		if s.Divergence != nil {
			divergences = append(divergences, bridge.HealthDivergenceEntry{
				Server: s.Name,
				Reason: s.Divergence.Reason,
			})
		}
		healthServers[s.Name] = sh
	}
	a.writeJSON(w, http.StatusOK, struct {
		Servers      map[string]bridge.ServerHealth `json:"servers"`
		Divergence   []bridge.HealthDivergenceEntry `json:"divergence,omitempty"`
		CacheBackend string                         `json:"cache_backend"`
	}{
		Servers:      healthServers,
		Divergence:   divergences,
		CacheBackend: a.cacheBackend,
	})
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
			ToolCount:   s.ToolCount,
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

// handlePresence returns agent presence data from the fleet monitor snapshot.
func (a *App) handlePresence(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, map[string]any{
		"agents":         snap.Agents,
		"active_agents":  snap.ActiveAgents,
		"idle_agents":    snap.IdleAgents,
		"offline_agents": snap.OfflineAgents,
		"total":          snap.ActiveAgents + snap.IdleAgents + snap.OfflineAgents,
	})
}

// handleClaims returns file claims from the fleet monitor snapshot.
func (a *App) handleClaims(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, map[string]any{
		"claims": snap.FileClaims,
		"count":  len(snap.FileClaims),
	})
}

// handleWorktrees returns worktree assignments from the fleet monitor snapshot.
func (a *App) handleWorktrees(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, map[string]any{
		"worktrees":        snap.Worktrees,
		"active_worktrees": snap.ActiveWorktrees,
	})
}

// --- API handlers: Direct bridge calls (parameterized queries) ---

func (a *App) handleSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := a.agent.Sessions()
	if err != nil {
		a.logger.Warn("sessions upstream error, falling back to fleet snapshot", "error", err)
		sessions = a.fleetMonitor.Snapshot().Sessions
	}

	// Optional time filter: ?since=<RFC3339> — return only sessions started
	// after the given time or still active (ended_at is empty).
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if since, parseErr := time.Parse(time.RFC3339, sinceStr); parseErr == nil {
			filtered := make([]bridge.SessionInfo, 0, len(sessions))
			for _, s := range sessions {
				// Keep active sessions (no end time).
				if s.EndedAt == "" {
					filtered = append(filtered, s)
					continue
				}
				// Keep sessions started after the since time.
				if started, err := time.Parse(time.RFC3339, s.StartedAt); err == nil && !started.Before(since) {
					filtered = append(filtered, s)
				}
			}
			sessions = filtered
		}
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

// --- API handlers: CRUD operations (v2) ---

func (a *App) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID  string   `json:"session_id"`
		Title      string   `json:"title"`
		Priority   string   `json:"priority"`
		Tags       []string `json:"tags"`
		Context    string   `json:"context"`
		FilePath   string   `json:"file_path"`
		LineNumber int      `json:"line_number"`
		BlockedBy  []string `json:"blocked_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	body.Title = strings.TrimSpace(body.Title)
	if body.Title == "" {
		a.writeError(w, http.StatusBadRequest, "title is required", nil)
		return
	}
	body.Priority = normalizeTaskPriority(body.Priority)
	body.Tags = normalizeStringList(body.Tags)
	body.BlockedBy = normalizeStringList(body.BlockedBy)
	body.Context = strings.TrimSpace(body.Context)
	body.FilePath = strings.TrimSpace(body.FilePath)
	if body.LineNumber < 0 {
		body.LineNumber = 0
	}
	taskResult, err := a.agent.CreateTask(bridge.CreateTaskParams{
		SessionID:  body.SessionID,
		Title:      body.Title,
		Priority:   body.Priority,
		Tags:       body.Tags,
		Context:    body.Context,
		FilePath:   body.FilePath,
		LineNumber: body.LineNumber,
		BlockedBy:  body.BlockedBy,
	})
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to create task", err)
		return
	}
	a.broadcastAgentEvent("hud.task.create", map[string]any{
		"title":    body.Title,
		"priority": body.Priority,
	})
	taskID := ""
	if taskResult != nil && len(taskResult.TaskIDs) > 0 {
		taskID = taskResult.TaskIDs[0]
	}
	a.writeJSON(w, http.StatusCreated, map[string]any{"status": "created", "task_id": taskID})
}

func (a *App) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing task id", nil)
		return
	}
	var body struct {
		Status     string `json:"status"`
		Priority   string `json:"priority"`
		Resolution string `json:"resolution"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if err := a.agent.UpdateTask(bridge.UpdateTaskParams{
		ID:         id,
		Status:     body.Status,
		Priority:   body.Priority,
		Resolution: body.Resolution,
	}); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to update task", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (a *App) handleAgents(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, map[string]any{"agents": snap.Agents})
}

func (a *App) handleSessionEntries(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing session id", nil)
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	entries, err := a.agent.SessionEntries(id, limit)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to get session entries", err)
		return
	}
	flat := make([]map[string]any, len(entries))
	for i, e := range entries {
		flat[i] = map[string]any{
			"id":          e.Entry.ID,
			"entry_type":  e.Entry.EntryType,
			"agent_id":    e.Entry.AgentID,
			"namespace":   e.Entry.Namespace,
			"title":       e.Entry.Title,
			"content":     e.Entry.Content,
			"timestamp":   e.Entry.Timestamp,
			"score":       e.Score,
			"file_path":   e.Entry.FilePath,
			"line_start":  e.Entry.LineStart,
			"line_end":    e.Entry.LineEnd,
			"token_count": e.Entry.TokenCount,
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"entries": flat})
}

func (a *App) handleTunnels(w http.ResponseWriter, _ *http.Request) {
	raw, err := a.client.Call("loom/tunnels", nil)
	if err != nil {
		// Fallback to empty if daemon doesn't support tunnels yet.
		a.logger.Debug("tunnels RPC failed, returning empty", "error", err)
		a.writeJSON(w, http.StatusOK, map[string]any{"tunnels": []any{}, "count": 0})
		return
	}
	var result bridge.TunnelsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		a.logger.Debug("tunnels unmarshal failed", "error", err)
		a.writeJSON(w, http.StatusOK, map[string]any{"tunnels": []any{}, "count": 0})
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"tunnels":   result.Tunnels,
		"count":     result.Total,
		"connected": result.Connected,
	})
}

func (a *App) handleCacheStats(w http.ResponseWriter, _ *http.Request) {
	raw, err := a.client.Call("loom/cache/stats", nil)
	if err != nil {
		// Fallback to local HUD cache stats if daemon doesn't support cache RPC.
		a.logger.Debug("cache stats RPC failed, returning local cache", "error", err)
		a.writeJSON(w, http.StatusOK, map[string]any{
			"entries":  a.cache.Len(),
			"hit_rate": 0.0,
		})
		return
	}
	var result bridge.CacheStatsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		a.logger.Debug("cache stats unmarshal failed", "error", err)
		a.writeJSON(w, http.StatusOK, map[string]any{
			"entries":  a.cache.Len(),
			"hit_rate": 0.0,
		})
		return
	}
	hitRate := 0.0
	if result.TotalHits > 0 && result.Entries > 0 {
		hitRate = float64(result.TotalHits) / float64(result.TotalHits+int64(result.Entries))
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"entries":    result.Entries,
		"size_bytes": result.SizeBytes,
		"max_bytes":  result.MaxBytes,
		"hit_rate":   hitRate,
		"enabled":    result.Enabled,
	})
}

func (a *App) handleCost(w http.ResponseWriter, _ *http.Request) {
	snap := a.costMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, snap)
}

// fetchRBACConfig fetches RBAC configuration from the daemon with graceful
// degradation. Returns a zero-value result (enabled=false) on any error.
func (a *App) fetchRBACConfig() bridge.RBACConfigResult {
	raw, err := a.client.Call("loom/rbac-config", nil)
	if err != nil {
		a.logger.Debug("rbac-config call failed", "error", err)
		return bridge.RBACConfigResult{}
	}
	var result bridge.RBACConfigResult
	if err := json.Unmarshal(raw, &result); err != nil {
		a.logger.Debug("rbac-config unmarshal failed", "error", err)
		return bridge.RBACConfigResult{}
	}
	return result
}

// fetchOTelStatus fetches OTel observability status from the daemon with
// graceful degradation. Returns a zero-value result on any error.
func (a *App) fetchOTelStatus() bridge.OTelStatusResult {
	raw, err := a.client.Call("loom/otel-status", nil)
	if err != nil {
		a.logger.Debug("otel-status call failed", "error", err)
		return bridge.OTelStatusResult{}
	}
	var result bridge.OTelStatusResult
	if err := json.Unmarshal(raw, &result); err != nil {
		a.logger.Debug("otel-status unmarshal failed", "error", err)
		return bridge.OTelStatusResult{}
	}
	return result
}

func (a *App) handleRBAC(w http.ResponseWriter, _ *http.Request) {
	result := a.fetchRBACConfig()
	a.writeJSON(w, http.StatusOK, result)
}

func (a *App) handleOTel(w http.ResponseWriter, _ *http.Request) {
	result := a.fetchOTelStatus()
	a.writeJSON(w, http.StatusOK, result)
}

// doSandboxStart calls devbox_build through the daemon and refreshes the
// sandbox monitor. Returns the parsed result map or an error.
func (a *App) doSandboxStart(project, agentID string) (map[string]any, error) {
	args := map[string]any{"project": project}
	if agentID != "" {
		args["agent_id"] = agentID
	}
	result, err := a.client.CallTool("devbox_build", args)
	if err != nil {
		return nil, err
	}
	go a.sandboxMonitor.Refresh()

	parsed, err := bridge.ParseToolResultMap(result)
	if err != nil {
		return nil, nil // non-fatal: build succeeded but response is opaque
	}
	return parsed, nil
}

// doSandboxStop stops a running sandbox and refreshes the sandbox monitor.
func (a *App) doSandboxStop(project string) error {
	_, err := a.client.CallTool("devbox_stop", map[string]any{"project": project})
	if err != nil {
		return err
	}
	go a.sandboxMonitor.Refresh()
	return nil
}

func (a *App) handleTemplateList(w http.ResponseWriter, _ *http.Request) {
	templates, err := a.agent.TemplateList()
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list templates", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

func (a *App) handleAnnotationList(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("file")
	annotations, err := a.agent.AnnotationGet(filePath)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list annotations", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"annotations": annotations})
}

func (a *App) handleAnnotationCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
		Category string `json:"category"`
		Line     int    `json:"line"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.FilePath == "" || body.Content == "" {
		a.writeError(w, http.StatusBadRequest, "file_path and content are required", nil)
		return
	}
	if err := a.agent.AnnotationAdd(body.FilePath, body.Content, body.Category, body.Line); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to create annotation", err)
		return
	}
	a.writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

// --- Session reaper ---

// sessionReaper periodically checks for offline agents with active sessions
// and auto-ends them. This ensures heartbeat-only agents (like Codex) get
// reliable session cleanup without native session-end hooks.
func isMobileManagedPresence(agent bridge.PresenceInfo) bool {
	if strings.EqualFold(strings.TrimSpace(agent.AgentType), "mobile") {
		return true
	}
	desc := strings.ToLower(strings.TrimSpace(agent.Description))
	return strings.HasPrefix(desc, "mobile session")
}

func (a *App) sessionReaper(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	const offlineThreshold = 5 * time.Minute

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap := a.fleetMonitor.Snapshot()
			now := time.Now()

			for _, agent := range snap.Agents {
				if agent.Status != "offline" {
					continue
				}
				if isMobileManagedPresence(agent) {
					continue
				}
				// Check if agent has been offline long enough.
				hb, err := time.Parse(time.RFC3339, agent.LastHeartbeat)
				if err != nil {
					continue
				}
				if now.Sub(hb) < offlineThreshold {
					continue
				}

				// Find active session for this agent.
				session, err := a.agent.GetActiveSession(agent.AgentID)
				if err != nil || session == nil {
					continue
				}

				a.logger.Info("session reaper: ending orphaned session",
					"agent_id", agent.AgentID,
					"session_id", session.ID,
					"offline_since", agent.LastHeartbeat)

				summarize := true
				_, endErr := a.agent.EndSession(bridge.SessionEndParams{
					SessionID: session.ID,
					AgentID:   agent.AgentID,
					Summarize: &summarize,
				})
				if endErr != nil {
					a.logger.Warn("session reaper: failed to end session",
						"agent_id", agent.AgentID, "error", endErr)
					continue
				}

				a.broadcastAgentEvent("agent.session.reaped", map[string]any{
					"agent_id":   agent.AgentID,
					"session_id": session.ID,
					"reason":     "offline_timeout",
				})

				go a.fleetMonitor.Refresh()
			}
		}
	}
}

// pushTokenReaper periodically removes stale push registration tokens.
func (a *App) pushTokenReaper(ctx context.Context) {
	if a.deviceTokenStore == nil {
		return
	}
	ticker := time.NewTicker(pushTokenCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if removed := a.cleanupStalePushTokensNow(time.Now(), pushTokenMaxIdle); removed > 0 {
				a.logger.Info("push token reaper removed stale device tokens", "removed", removed)
			}
		}
	}
}

// cleanupStalePushTokensNow removes tokens that have been idle longer than maxIdle.
// It is extracted for deterministic tests and one-shot invocations.
func (a *App) cleanupStalePushTokensNow(now time.Time, maxIdle time.Duration) int {
	if a.deviceTokenStore == nil || maxIdle <= 0 {
		return 0
	}
	cutoff := now.Add(-maxIdle)
	return a.deviceTokenStore.CleanupStale(cutoff)
}

// --- Command center API handlers ---

// handleKPIs returns daily aggregate KPI metrics for the overview panel.
// GET /api/kpis
func (a *App) handleKPIs(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()

	kpis := a.fleetMonitor.KPIs()

	a.writeJSON(w, http.StatusOK, map[string]any{
		"sessions_today":        kpis.SessionsToday,
		"tokens_today":          kpis.TokensToday,
		"tasks_completed_today": kpis.TasksCompletedToday,
		"active_agents":         snap.ActiveAgents,
		"pending_approvals":     snap.PendingApprovals,
		"file_conflicts":        kpis.FileConflicts,
		"conflict_details":      kpis.ConflictDetails,
	})
}

// handleTimeline returns chronological agent lifecycle events from the ring buffer.
// GET /api/timeline?since=<RFC3339>&limit=<int>
func (a *App) handleTimeline(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	eventType := strings.TrimSpace(r.URL.Query().Get("event_type"))

	var entries []TimelineEntry
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			entries = a.eventLog.Since(t, 0)
		} else {
			entries = a.eventLog.All(0)
		}
	} else {
		entries = a.eventLog.All(0)
	}

	entries = filterTimelineEntries(entries, agentID, eventType)
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	if entries == nil {
		entries = []TimelineEntry{}
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"count":   len(entries),
	})
}

func filterTimelineEntries(entries []TimelineEntry, agentID, eventType string) []TimelineEntry {
	agentID = strings.TrimSpace(agentID)
	eventType = strings.TrimSpace(eventType)
	if agentID == "" && eventType == "" {
		return entries
	}

	filtered := make([]TimelineEntry, 0, len(entries))
	for _, entry := range entries {
		if agentID != "" && entry.AgentID != agentID {
			continue
		}
		if eventType != "" && entry.EventType != eventType {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func normalizeTaskPriority(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "low", "medium", "high", "critical":
		return strings.ToLower(strings.TrimSpace(priority))
	default:
		return "medium"
	}
}

func normalizeStringList(values []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		normalized = append(normalized, v)
	}
	return normalized
}

// --- Middleware and helpers ---

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

// pprof handler wrappers for custom ServeMux registration.
func pprofIndex(w http.ResponseWriter, r *http.Request)   { pprof.Index(w, r) }
func pprofCmdline(w http.ResponseWriter, r *http.Request) { pprof.Cmdline(w, r) }
func pprofProfile(w http.ResponseWriter, r *http.Request) { pprof.Profile(w, r) }
func pprofSymbol(w http.ResponseWriter, r *http.Request)  { pprof.Symbol(w, r) }
func pprofTrace(w http.ResponseWriter, r *http.Request)   { pprof.Trace(w, r) }

// newHUDTUILogger creates a logger that writes to ~/.config/loom/logs/tui.log.
// Used in TUI mode so HUD log output doesn't corrupt the alt-screen.
func newHUDTUILogger() *slog.Logger {
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".config", "loom", "logs")
	_ = os.MkdirAll(logDir, 0755)

	f, err := os.OpenFile(filepath.Join(logDir, "tui.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	}
	return slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{}))
}

// openBrowser attempts to open a URL in the default browser.
func openBrowser(url string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cancel()
		return
	}
	go func() {
		defer cancel()
		_ = cmd.Run()
	}()
}

func browserURL(scheme, bindAddr string, addr net.Addr) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return scheme + "://" + addr.String()
	}
	return scheme + "://" + net.JoinHostPort(browserHost(bindAddr, host), port)
}

func browserHost(bindAddr, listenHost string) string {
	host := strings.Trim(strings.TrimSpace(bindAddr), "[]")
	if host == "" {
		host = strings.Trim(strings.TrimSpace(listenHost), "[]")
	}

	switch host {
	case "", "0.0.0.0", "::", "*":
		return "127.0.0.1"
	case "localhost", "127.0.0.1", "::1":
		return host
	default:
		return host
	}
}

// spawnAdapter adapts SpawnOrchestrator to the monitor.SpawnLister interface.
type spawnAdapter struct {
	orch *SpawnOrchestrator
}

func (a spawnAdapter) ListSpawnInfos() []monitor.SpawnInfo {
	states := a.orch.ListSpawns()
	infos := make([]monitor.SpawnInfo, 0, len(states))
	for _, s := range states {
		info := monitor.SpawnInfo{
			SpawnID:   s.SpawnID,
			AgentID:   s.AgentID,
			PodName:   s.PodName,
			Status:    string(s.Status),
			Project:   s.Request.Project,
			Branch:    s.Request.Branch,
			Task:      s.Request.TaskDescription,
			AgentType: s.Request.AgentType,
			StartedAt: s.StartedAt.Format(time.RFC3339),
		}
		if s.EndedAt != nil {
			info.EndedAt = s.EndedAt.Format(time.RFC3339)
		}
		info.Error = s.Error
		infos = append(infos, info)
	}
	return infos
}
