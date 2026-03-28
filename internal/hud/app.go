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
	"github.com/crb2nu/loom/internal/hud/alerting"
	"github.com/crb2nu/loom/internal/hud/autofix"
	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/coordinator"
	"github.com/crb2nu/loom/internal/hud/domain"
	"github.com/crb2nu/loom/internal/hud/monitor"
	"github.com/crb2nu/loom/internal/hud/orchestration"
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
	fleetMonitor         *monitor.FleetMonitor
	healthMonitor        *monitor.HealthMonitor
	memoryMonitor        *monitor.MemoryMonitor
	workflowMonitor      *monitor.WorkflowMonitor
	streamMonitor        *monitor.StreamMonitor
	sandboxMonitor       *monitor.SandboxMonitor
	costMonitor          *monitor.CostMonitor
	pipelineMonitor      *monitor.PipelineMonitor
	contextHealthMonitor *monitor.ContextHealthMonitor
	codebaseMonitor      *monitor.CodebaseMonitor
	orchMonitor          *orchestration.OrchestrationMonitor

	// Orchestration engine — auto-dispatch, load balancing, conflict prevention.
	orchEngine *orchestration.Engine

	// Alert engine — pipeline failure alerting and notification dispatch.
	alertEngine   *alerting.AlertEngine
	autofixEngine *autofix.AutoFixEngine

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

	actualPort := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	portFile, err := WritePortFile(ln.Addr().(*net.TCPAddr).Port)
	if err != nil {
		logger.Warn("failed to write port file", "path", portFile, "error", err)
	} else {
		logger.Info("port file written", "path", portFile, "port", actualPort)
	}
	defer func() {
		if err := RemovePortFile(); err != nil {
			logger.Warn("failed to remove port file", "path", portFile, "error", err)
		}
	}()

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
