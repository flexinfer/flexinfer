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

	loomcache "github.com/crb2nu/loom/internal/cache"
	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/coordinator"
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

	// TLS for gateway mode.
	TLSCert     string // Path to PEM certificate file.
	TLSKey      string // Path to PEM private key file.
	BindAddress string // Listen address (default: 127.0.0.1).

	// TUI mode: launch a bubbletea terminal UI instead of the web dashboard.
	TUI bool
}

// App is the HUD application. It holds the daemon client, agent bridge,
// background monitors, and an in-memory cache used to reduce repeated calls
// to the daemon.
type App struct {
	config       Config
	client       *bridge.DaemonClient
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

	// SSE streaming — daemon events → browser clients.
	sseHub *SSEHub

	// Coordinator — optional LLM-powered agent context intelligence.
	coordinator        *coordinator.Coordinator
	coordinatorMetrics *coordinator.Metrics

	// Timeline event log — ring buffer for unified activity timeline.
	eventLog *EventLog

	// Nudge queue — pending nudges per agent, delivered via heartbeat response.
	nudgeQueue *NudgeQueue

	// Mobile API hardening.
	mobileRateLimiter    *MobileRateLimiter
	mobileRevocationList *MobileTokenRevocationList
	deviceTokenStore     *DeviceTokenStore // Push notification device tokens (MBL-7).
}

// Run creates and starts the HUD application. This is the main entry point
// called from the CLI command.
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

	agent := bridge.NewAgentBridge(client)

	cacheCfg := loomcache.LoadConfigFromEnv()
	appCache := loomcache.New(cacheCfg, logger)

	app := &App{
		config:               cfg,
		client:               client,
		agent:                agent,
		cache:                appCache,
		cacheBackend:         cacheCfg.Backend,
		logger:               logger,
		nudgeQueue:           NewNudgeQueue(),
		mobileRevocationList: NewMobileTokenRevocationList(),
		deviceTokenStore:     NewDeviceTokenStore(),
		mobileRateLimiter: NewMobileRateLimiter(MobileRateLimitConfig{
			MutationPerMinute: cfg.MobileRateLimitMutation,
			ReadPerMinute:     cfg.MobileRateLimitRead,
		}),
	}

	defer appCache.Close()

	// Initialize and start background monitors.
	app.fleetMonitor = monitor.NewFleetMonitor(client, agent, logger)
	app.fleetMonitor.Start(15 * time.Second) // Slow cadence — granular agent.* SSE events carry real-time deltas.
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

	app.streamMonitor = monitor.NewStreamMonitor(agent, logger)
	app.streamMonitor.Start(5 * time.Second)
	defer app.streamMonitor.Stop()

	app.sandboxMonitor = monitor.NewSandboxMonitor(client, logger)
	app.sandboxMonitor.Start(10 * time.Second)
	defer app.sandboxMonitor.Stop()

	logger.Info("background monitors started",
		"fleet", "15s", "health", "5s", "memory", "10s", "workflow", "5s", "stream", "5s", "sandbox", "10s")

	// Bootstrap workflow definitions from .agents/workflows/*.yaml files.
	app.bootstrapWorkflowDefinitions()

	// Initialize SSE fan-out hub for browser clients.
	app.sseHub = NewSSEHub(logger)

	// Initialize timeline event log (ring buffer for activity timeline).
	app.eventLog = NewEventLog(1000)

	// Start session reaper — auto-ends orphaned sessions for offline agents.
	reaperCtx, reaperCancel := context.WithCancel(context.Background())
	defer reaperCancel()
	go app.sessionReaper(reaperCtx)

	// Wire monitor OnRefresh callbacks to broadcast fresh snapshots via SSE.
	// This enables "SSE-first" data flow: stores apply data directly from
	// these events rather than re-fetching via HTTP after receiving a signal.

	// Optional webhook pusher: forward presence+session snapshots to a remote
	// endpoint (e.g., flexdeck in the K8s cluster).
	var fleetWebhook *FleetWebhook
	if cfg.WebhookURL != "" {
		fleetWebhook = NewFleetWebhook(cfg.WebhookURL, cfg.WebhookToken, cfg.WebhookResolve, logger)
		logFields := []any{"url", cfg.WebhookURL}
		if cfg.WebhookResolve != "" {
			logFields = append(logFields, "resolve_override", cfg.WebhookResolve)
		}
		logger.Info("fleet webhook enabled", logFields...)
	}

	app.fleetMonitor.OnRefresh(func(snap monitor.FleetSnapshot) {
		// SSE broadcast to browser clients.
		data, err := json.Marshal(snap)
		if err == nil {
			app.sseHub.Broadcast(bridge.SSEEvent{
				ID:        fmt.Sprintf("hud-fleet-%d", time.Now().UnixMilli()),
				Type:      "hud.fleet",
				Timestamp: time.Now(),
				Data:      data,
			})
		}

		// Webhook push to remote endpoint (non-blocking).
		if fleetWebhook != nil {
			go fleetWebhook.Push(snap)
		}
	})
	app.healthMonitor.OnRefresh(func(servers []monitor.ServerHealthEntry) {
		data, err := json.Marshal(map[string]any{"servers": servers})
		if err != nil {
			return
		}
		app.sseHub.Broadcast(bridge.SSEEvent{
			ID:        fmt.Sprintf("hud-health-%d", time.Now().UnixMilli()),
			Type:      "hud.health",
			Timestamp: time.Now(),
			Data:      data,
		})
	})
	app.memoryMonitor.OnRefresh(func(stats *bridge.MemoryStatsResult) {
		// Transform to match the HTTP endpoint shape (items/tokens, not item_count/token_count).
		tierJSON := func(t bridge.MemoryTierStats) map[string]any {
			return map[string]any{"items": t.Items, "tokens": t.Tokens}
		}
		payload := map[string]any{
			"working_memory":    tierJSON(stats.WorkingMemory),
			"short_term_memory": tierJSON(stats.ShortTermMemory),
			"long_term_memory":  tierJSON(stats.LongTermMemory),
			"total_items":       stats.TotalItems,
			"total_tokens":      stats.TotalTokens,
		}
		if stats.CompressionRatio > 0 || stats.ItemsCompressedLast24h > 0 {
			payload["compression"] = map[string]any{
				"ratio":            stats.CompressionRatio,
				"compressed_items": stats.ItemsCompressedLast24h,
				"tokens_saved":     int(float64(stats.TotalTokens) * (1 - stats.CompressionRatio)),
				"added_24h":        stats.ItemsAddedLast24h,
				"compressed_24h":   stats.ItemsCompressedLast24h,
			}
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		app.sseHub.Broadcast(bridge.SSEEvent{
			ID:        fmt.Sprintf("hud-memory-%d", time.Now().UnixMilli()),
			Type:      "hud.memory",
			Timestamp: time.Now(),
			Data:      data,
		})
	})
	app.workflowMonitor.OnRefresh(func(workflows []bridge.WorkflowInfo) {
		data, err := json.Marshal(map[string]any{"workflows": workflows})
		if err != nil {
			return
		}
		app.sseHub.Broadcast(bridge.SSEEvent{
			ID:        fmt.Sprintf("hud-workflows-%d", time.Now().UnixMilli()),
			Type:      "hud.workflows",
			Timestamp: time.Now(),
			Data:      data,
		})
	})
	app.streamMonitor.OnRefresh(func(entries []monitor.StreamEntry) {
		data, err := json.Marshal(map[string]any{"entries": entries})
		if err != nil {
			return
		}
		app.sseHub.Broadcast(bridge.SSEEvent{
			ID:        fmt.Sprintf("hud-stream-%d", time.Now().UnixMilli()),
			Type:      "hud.stream",
			Timestamp: time.Now(),
			Data:      data,
		})
	})
	app.sandboxMonitor.OnRefresh(func(snap map[string]any) {
		snap["available"] = true
		data, err := json.Marshal(snap)
		if err != nil {
			return
		}
		app.sseHub.Broadcast(bridge.SSEEvent{
			ID:        fmt.Sprintf("hud-sandbox-%d", time.Now().UnixMilli()),
			Type:      "hud.sandbox",
			Timestamp: time.Now(),
			Data:      data,
		})
	})

	// Initialize coordinator if FlexInfer URL is configured.
	if cfg.FlexInferURL != "" {
		coordCfg := coordinator.ConfigFromEnv()
		// CLI flags override env vars.
		coordCfg.FlexInferURL = cfg.FlexInferURL
		if cfg.FlexInferKey != "" {
			coordCfg.FlexInferKey = cfg.FlexInferKey
		}
		if cfg.CoordinatorModel != "" {
			coordCfg.DefaultModel = cfg.CoordinatorModel
		}

		if err := coordCfg.Validate(); err != nil {
			logger.Error("coordinator config invalid", "error", err)
		} else {
			c := coordinator.NewCoordinator(coordCfg, agent, app.sseHub, logger)
			if c != nil {
				m := coordinator.NewMetrics()
				c.SetMetrics(m)
				if err := c.Start(); err != nil {
					logger.Warn("coordinator: failed to start, continuing without it", "error", err)
				} else {
					app.coordinator = c
					app.coordinatorMetrics = m
					defer c.Stop()
					logger.Info("coordinator started", "url", cfg.FlexInferURL, "model", coordCfg.DefaultModel)
				}
			}
		}
	}

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
		// Only broadcast to SSE hub when browser clients may be connected.
		// In TUI mode no browser connects, so skip the fan-out overhead.
		if !cfg.TUI {
			ec.OnAny(func(e bridge.SSEEvent) {
				app.sseHub.Broadcast(e)
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
	url := scheme + "://" + actualAddr

	// Write the bound port to a file so CLI commands can discover it.
	portFile := PortFilePath()
	actualPort := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	if err := os.WriteFile(portFile, []byte(actualPort), 0644); err != nil {
		logger.Warn("failed to write port file", "path", portFile, "error", err)
	} else {
		logger.Info("port file written", "path", portFile, "port", actualPort)
	}
	defer os.Remove(portFile)

	logger.Info("HUD server started", "url", url, "dev", cfg.Dev)
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
			Agent:  agent,
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
	mux.HandleFunc("GET /api/workflows", a.withCORS(a.handleWorkflowList))
	mux.HandleFunc("GET /api/workflows/{id}", a.withCORS(a.handleWorkflowDetail))
	mux.HandleFunc("POST /api/workflows/{id}/approve", a.withCORS(a.handleWorkflowApprove))
	mux.HandleFunc("POST /api/workflows/{id}/reject", a.withCORS(a.handleWorkflowReject))
	mux.HandleFunc("POST /api/workflows/{id}/cancel", a.withCORS(a.handleWorkflowCancel))
	mux.HandleFunc("GET /api/memory/stats", a.withCORS(a.handleMemoryStats))
	mux.HandleFunc("POST /api/memory/{id}/promote", a.withCORS(a.handleMemoryPromote))
	mux.HandleFunc("POST /api/memory/{id}/demote", a.withCORS(a.handleMemoryDemote))

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
	mux.HandleFunc("GET /api/memory/items", a.withCORS(a.handleMemoryItems))
	mux.HandleFunc("POST /api/memory", a.withCORS(a.handleMemoryAdd))
	mux.HandleFunc("DELETE /api/memory/{id}", a.withCORS(a.handleMemoryDelete))
	mux.HandleFunc("GET /api/memory/compaction", a.withCORS(a.handleMemoryCompaction))
	mux.HandleFunc("GET /api/graph/stats", a.withCORS(a.handleGraphStats))
	mux.HandleFunc("GET /api/graph/entities", a.withCORS(a.handleGraphEntities))
	mux.HandleFunc("GET /api/graph/entities/{id}", a.withCORS(a.handleGraphEntityDetail))
	mux.HandleFunc("POST /api/graph/entities", a.withCORS(a.handleGraphEntityCreate))
	mux.HandleFunc("DELETE /api/graph/entities/{id}", a.withCORS(a.handleGraphEntityDelete))
	mux.HandleFunc("POST /api/graph/relations", a.withCORS(a.handleGraphRelationCreate))
	mux.HandleFunc("DELETE /api/graph/relations/{id}", a.withCORS(a.handleGraphRelationDelete))
	mux.HandleFunc("GET /api/graph/path", a.withCORS(a.handleGraphFindPath))
	mux.HandleFunc("GET /api/stream", a.withCORS(a.handleContextStream))
	mux.HandleFunc("GET /api/tunnels", a.withCORS(a.handleTunnels))
	mux.HandleFunc("GET /api/cache", a.withCORS(a.handleCacheStats))
	mux.HandleFunc("GET /api/reasoning/chains", a.withCORS(a.handleReasoningChainList))
	mux.HandleFunc("GET /api/reasoning/chains/{id}", a.withCORS(a.handleReasoningChainDetail))
	mux.HandleFunc("POST /api/reasoning/chains", a.withCORS(a.handleReasoningChainCreate))
	mux.HandleFunc("GET /api/handoffs", a.withCORS(a.handleHandoffList))
	mux.HandleFunc("POST /api/handoffs", a.withCORS(a.handleHandoffCreate))
	mux.HandleFunc("POST /api/handoffs/{id}/accept", a.withCORS(a.handleHandoffAccept))
	mux.HandleFunc("GET /api/templates", a.withCORS(a.handleTemplateList))
	mux.HandleFunc("GET /api/annotations", a.withCORS(a.handleAnnotationList))
	mux.HandleFunc("POST /api/annotations", a.withCORS(a.handleAnnotationCreate))
	mux.HandleFunc("GET /api/sandbox", a.withCORS(a.handleSandbox))
	mux.HandleFunc("GET /api/sandbox/policy", a.withCORS(a.handleSandboxPolicy))
	mux.HandleFunc("POST /api/sandbox/start", a.withCORS(a.handleSandboxStart))
	mux.HandleFunc("POST /api/sandbox/stop", a.withCORS(a.handleSandboxStop))
	mux.HandleFunc("GET /api/events", a.withCORS(a.handleSSE))

	// API routes — mobile companion v1.
	mux.HandleFunc("GET /api/mobile/v1/ping", a.withCORS(a.handleMobilePing))
	mux.HandleFunc("GET /api/mobile/v1/dashboard", a.withCORS(a.handleMobileDashboard))
	mux.HandleFunc("GET /api/mobile/v1/sessions", a.withCORS(a.handleMobileSessions))
	mux.HandleFunc("GET /api/mobile/v1/sessions/{session_id}", a.withCORS(a.handleMobileSessionDetail))
	mux.HandleFunc("GET /api/mobile/v1/sessions/{session_id}/events", a.withCORS(a.handleMobileSessionEvents))
	mux.HandleFunc("GET /api/mobile/v1/tasks", a.withCORS(a.handleMobileTasks))
	mux.HandleFunc("GET /api/mobile/v1/workflows", a.withCORS(a.handleMobileWorkflows))
	mux.HandleFunc("GET /api/mobile/v1/workflows/{workflow_id}", a.withCORS(a.handleMobileWorkflowDetail))
	mux.HandleFunc("GET /api/mobile/v1/presence", a.withCORS(a.handleMobilePresence))
	mux.HandleFunc("GET /api/mobile/v1/memory/stats", a.withCORS(a.handleMobileMemoryStats))
	mux.HandleFunc("GET /api/mobile/v1/memory/items", a.withCORS(a.handleMobileMemoryItems))
	mux.HandleFunc("GET /api/mobile/v1/stream", a.withCORS(a.handleMobileStream))
	mux.HandleFunc("GET /api/mobile/v1/topology", a.withCORS(a.handleMobileTopology))
	mux.HandleFunc("GET /api/mobile/v1/graph/stats", a.withCORS(a.handleMobileGraphStats))
	mux.HandleFunc("GET /api/mobile/v1/graph/entities", a.withCORS(a.handleMobileGraphEntities))
	mux.HandleFunc("GET /api/mobile/v1/graph/path", a.withCORS(a.handleMobileGraphPath))
	mux.HandleFunc("GET /api/mobile/v1/reasoning/chains", a.withCORS(a.handleMobileReasoningChains))
	mux.HandleFunc("GET /api/mobile/v1/reasoning/chains/{chain_id}", a.withCORS(a.handleMobileReasoningChainDetail))
	mux.HandleFunc("GET /api/mobile/v1/events/stream", a.withCORS(a.handleMobileEventsStream))
	mux.HandleFunc("POST /api/mobile/v1/sessions", a.withCORS(a.handleMobileSessionCreate))
	mux.HandleFunc("POST /api/mobile/v1/sessions/{session_id}/end", a.withCORS(a.handleMobileSessionEnd))
	mux.HandleFunc("GET /api/mobile/v1/audit", a.withCORS(a.handleMobileAudit))
	mux.HandleFunc("GET /api/mobile/v1/alerts/policy", a.withCORS(a.handleMobileAlertsPolicy))
	mux.HandleFunc("POST /api/mobile/v1/push/register", a.withCORS(a.handleMobilePushRegister))
	mux.HandleFunc("POST /api/mobile/v1/push/unregister", a.withCORS(a.handleMobilePushUnregister))
	mux.HandleFunc("POST /api/mobile/v1/admin/revoke", a.withCORS(a.handleMobileAdminRevoke))

	// API routes — topology graph.
	mux.HandleFunc("GET /api/topology", a.withCORS(a.handleTopology))

	// API routes — command center (KPIs, timeline, dispatch, claims).
	mux.HandleFunc("GET /api/kpis", a.withCORS(a.handleKPIs))
	mux.HandleFunc("GET /api/timeline", a.withCORS(a.handleTimeline))
	mux.HandleFunc("POST /api/agent/dispatch", a.withCORS(a.handleAgentDispatch))
	mux.HandleFunc("DELETE /api/claims/{agent_id}/{file_path...}", a.withCORS(a.handleClaimRelease))

	// API routes — agent lifecycle (CLI hooks call these).
	mux.HandleFunc("POST /api/agent/session-start", a.withCORS(a.handleAgentSessionStart))
	mux.HandleFunc("POST /api/agent/session-end", a.withCORS(a.handleAgentSessionEnd))
	mux.HandleFunc("POST /api/agent/heartbeat", a.withCORS(a.handleAgentHeartbeat))
	mux.HandleFunc("POST /api/agent/task-update", a.withCORS(a.handleAgentTaskUpdate))
	mux.HandleFunc("GET /api/agent/session", a.withCORS(a.handleAgentSession))
	mux.HandleFunc("POST /api/agent/session-list", a.withCORS(a.handleAgentSessionList))
	mux.HandleFunc("POST /api/agent/session-prune", a.withCORS(a.handleAgentSessionPrune))
	mux.HandleFunc("POST /api/agent/context/add", a.withCORS(a.handleAgentContextAdd))
	mux.HandleFunc("GET /api/agent/context-inspect", a.withCORS(a.handleAgentContextInspect))
	mux.HandleFunc("POST /api/agent/nudge", a.withCORS(a.handleAgentNudge))
	mux.HandleFunc("GET /api/agent/nudge-queue", a.withCORS(a.handleAgentNudgeQueue))
	mux.HandleFunc("GET /api/agent/nudge-queue-policy", a.withCORS(a.handleAgentNudgeQueuePolicy))
	mux.HandleFunc("POST /api/agent/nudge-queue-policy", a.withCORS(a.handleAgentNudgeQueuePolicyUpdate))
	mux.HandleFunc("GET /api/knowledge", a.withCORS(a.handleKnowledge))
	mux.HandleFunc("POST /api/agent/workflow-define", a.withCORS(a.handleAgentWorkflowDefine))
	mux.HandleFunc("GET /api/agent/workflow-definitions", a.withCORS(a.handleAgentWorkflowDefinitions))

	// API routes — coordinator (LLM-powered agent context intelligence).
	mux.HandleFunc("GET /api/coordinator/status", a.withCORS(a.handleCoordinatorStatus))
	mux.HandleFunc("POST /api/coordinator/summarize/{session_id}", a.withCORS(a.handleCoordinatorSummarize))
	mux.HandleFunc("POST /api/coordinator/compress", a.withCORS(a.handleCoordinatorCompress))
	mux.HandleFunc("POST /api/coordinator/plan", a.withCORS(a.handleCoordinatorPlan))
	if a.coordinatorMetrics != nil {
		mux.Handle("GET /api/coordinator/metrics", a.coordinatorMetrics.Handler())
	}

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

// handleWorkflowList returns the workflow list from the workflow monitor.
// Transforms MCP field names (workflow_id, created_at) to frontend names (id, started_at).
func (a *App) handleWorkflowList(w http.ResponseWriter, _ *http.Request) {
	workflows := a.workflowMonitor.Workflows()
	result := make([]map[string]any, len(workflows))
	for i, wf := range workflows {
		result[i] = map[string]any{
			"id":           wf.ID,
			"name":         wf.Name,
			"status":       wf.Status,
			"current_step": wf.CurrentStep,
			"started_at":   wf.CreatedAt,
			"progress":     wf.Progress,
		}
		if wf.Error != "" {
			result[i]["error"] = wf.Error
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"workflows": result})
}

// handleWorkflowDetail returns detail for a single workflow (cached with 10s TTL).
// Transforms MCP field names to the shape the frontend expects.
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

	// Build frontend-compatible step list.
	steps := make([]map[string]any, len(detail.Steps))
	stepNames := make(map[string]string, len(detail.Steps))
	for i, s := range detail.Steps {
		stepNames[s.ID] = s.Name
		steps[i] = map[string]any{
			"id":     s.ID,
			"name":   s.Name,
			"type":   s.Type,
			"status": s.Status,
		}
	}
	events := make([]map[string]any, len(detail.Events))
	for i, e := range detail.Events {
		entry := map[string]any{
			"id":         e.ID,
			"event_type": e.EventType,
			"timestamp":  e.Timestamp,
		}
		if e.StepID != "" {
			entry["step_id"] = e.StepID
			if name := stepNames[e.StepID]; name != "" {
				entry["step_name"] = name
			}
		}
		if len(e.Details) > 0 {
			if msg, ok := e.Details["message"].(string); ok && msg != "" {
				entry["details"] = msg
			} else {
				raw, err := json.Marshal(e.Details)
				if err == nil {
					entry["details"] = string(raw)
				}
			}
		}
		events[i] = entry
	}

	result := map[string]any{
		"id":           detail.ID,
		"name":         detail.Name,
		"status":       detail.Status,
		"current_step": detail.CurrentStep,
		"progress":     detail.Progress,
		"started_at":   detail.CreatedAt,
		"steps":        steps,
		"events":       events,
	}
	if detail.StartedAt != "" {
		result["started_at"] = detail.StartedAt
	}
	if detail.CompletedAt != "" {
		result["completed_at"] = detail.CompletedAt
	}
	if detail.Error != "" {
		result["error"] = detail.Error
	}

	a.writeJSON(w, http.StatusOK, result)
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
	go a.workflowMonitor.Refresh()
	a.broadcastAgentEvent("hud.workflow.approve", map[string]any{
		"workflow_id": id,
		"step_id":     body.StepID,
	})
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
	go a.workflowMonitor.Refresh()
	a.broadcastAgentEvent("hud.workflow.reject", map[string]any{
		"workflow_id": id,
		"step_id":     body.StepID,
	})
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// handleWorkflowCancel cancels a running workflow and invalidates the cache.
func (a *App) handleWorkflowCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing workflow id", nil)
		return
	}
	if err := a.workflowMonitor.CancelWorkflow(id); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to cancel workflow", err)
		return
	}
	go a.workflowMonitor.Refresh()
	a.broadcastAgentEvent("hud.workflow.cancel", map[string]any{
		"workflow_id": id,
	})
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// handleMemoryStats returns memory hierarchy stats from the memory monitor.
// Transforms the bridge DTO (MCP field names) into the shape the frontend expects.
func (a *App) handleMemoryStats(w http.ResponseWriter, _ *http.Request) {
	stats := a.memoryMonitor.Stats()
	if stats == nil {
		directStats, err := a.agent.MemoryStats()
		if err != nil {
			a.writeError(w, http.StatusBadGateway, "failed to get memory stats", err)
			return
		}
		stats = directStats
	}

	// Frontend expects {items, tokens} per tier, not {item_count, token_count}.
	tierJSON := func(t bridge.MemoryTierStats) map[string]any {
		return map[string]any{"items": t.Items, "tokens": t.Tokens}
	}

	resp := map[string]any{
		"working_memory":    tierJSON(stats.WorkingMemory),
		"short_term_memory": tierJSON(stats.ShortTermMemory),
		"long_term_memory":  tierJSON(stats.LongTermMemory),
		"total_items":       stats.TotalItems,
		"total_tokens":      stats.TotalTokens,
	}
	if stats.CompressionRatio > 0 || stats.ItemsCompressedLast24h > 0 {
		resp["compression"] = map[string]any{
			"ratio":            stats.CompressionRatio,
			"compressed_items": stats.ItemsCompressedLast24h,
			"tokens_saved":     int(float64(stats.TotalTokens) * (1 - stats.CompressionRatio)),
			"added_24h":        stats.ItemsAddedLast24h,
			"compressed_24h":   stats.ItemsCompressedLast24h,
		}
	}
	a.writeJSON(w, http.StatusOK, resp)
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
	a.broadcastAgentEvent("hud.memory.promote", map[string]any{
		"id": id,
	})
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
	a.broadcastAgentEvent("hud.memory.demote", map[string]any{
		"id": id,
	})
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "demoted"})
}

// --- API handlers: Direct bridge calls (parameterized queries) ---

func (a *App) handleSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := a.agent.Sessions()
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list sessions", err)
		return
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
	// Transform from bridge DTO (MCP field names) to frontend field names.
	result := make([]map[string]any, len(items))
	for i, it := range items {
		result[i] = map[string]any{
			"id":            it.ID,
			"title":         it.Title,
			"content":       it.Content,
			"tier":          it.Tier,
			"importance":    it.Importance,
			"tokens":        it.Tokens,
			"status":        it.Status,
			"category":      it.Category,
			"accessed_at":   it.AccessedAt,
			"last_accessed": it.LastAccessed,
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"items": result})
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

	// Flatten ContextEntryInfo (score + nested entry) into the flat shape
	// the frontend expects: {id, entry_type, agent_id, agent, namespace, title, timestamp, score}.
	flat := make([]map[string]any, len(entries))
	for i, e := range entries {
		flat[i] = map[string]any{
			"id":         e.Entry.ID,
			"entry_type": e.Entry.EntryType,
			"agent_id":   e.Entry.AgentID,
			"agent":      e.Entry.AgentID,
			"namespace":  e.Entry.Namespace,
			"title":      e.Entry.Title,
			"content":    e.Entry.Content,
			"timestamp":  e.Entry.Timestamp,
			"score":      e.Score,
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"entries": flat})
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
	if err := a.agent.CreateTask(bridge.CreateTaskParams{
		SessionID:  body.SessionID,
		Title:      body.Title,
		Priority:   body.Priority,
		Tags:       body.Tags,
		Context:    body.Context,
		FilePath:   body.FilePath,
		LineNumber: body.LineNumber,
		BlockedBy:  body.BlockedBy,
	}); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to create task", err)
		return
	}
	a.broadcastAgentEvent("hud.task.create", map[string]any{
		"title":    body.Title,
		"priority": body.Priority,
	})
	a.writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
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

func (a *App) handleMemoryAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title      string `json:"title"`
		Content    string `json:"content"`
		Tier       string `json:"tier"`
		Importance string `json:"importance"`
		Category   string `json:"category"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Title == "" || body.Content == "" {
		a.writeError(w, http.StatusBadRequest, "title and content are required", nil)
		return
	}
	if err := a.agent.MemoryAdd(body.Title, body.Content, body.Tier, body.Importance, body.Category); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to add memory", err)
		return
	}
	go a.memoryMonitor.Refresh()
	a.broadcastAgentEvent("hud.memory.add", map[string]any{
		"title": body.Title,
		"tier":  body.Tier,
	})
	a.writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (a *App) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing memory item id", nil)
		return
	}
	if err := a.agent.MemoryDelete(id); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to delete memory", err)
		return
	}
	go a.memoryMonitor.Refresh()
	a.broadcastAgentEvent("hud.memory.delete", map[string]any{
		"id": id,
	})
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *App) handleMemoryCompaction(w http.ResponseWriter, _ *http.Request) {
	info, err := a.agent.CompactionStatus()
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to get compaction status", err)
		return
	}
	a.writeJSON(w, http.StatusOK, info)
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

func (a *App) handleGraphEntityDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing entity id", nil)
		return
	}
	detail, err := a.agent.EntityGet(id)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to get entity", err)
		return
	}
	a.writeJSON(w, http.StatusOK, detail)
}

func (a *App) handleGraphEntityCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string         `json:"name"`
		EntityType string         `json:"entity_type"`
		Namespace  string         `json:"namespace"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Name == "" || body.EntityType == "" {
		a.writeError(w, http.StatusBadRequest, "name and entity_type are required", nil)
		return
	}
	if err := a.agent.EntityAdd(body.Name, body.EntityType, body.Namespace, body.Properties); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to create entity", err)
		return
	}
	a.writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (a *App) handleGraphEntityDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing entity id", nil)
		return
	}
	if err := a.agent.EntityDelete(id); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to delete entity", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *App) handleGraphRelationCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SourceID     string `json:"source_id"`
		TargetID     string `json:"target_id"`
		RelationType string `json:"relation_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.SourceID == "" || body.TargetID == "" || body.RelationType == "" {
		a.writeError(w, http.StatusBadRequest, "source_id, target_id, and relation_type are required", nil)
		return
	}
	if err := a.agent.RelationAdd(body.SourceID, body.TargetID, body.RelationType); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to create relation", err)
		return
	}
	a.writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (a *App) handleGraphRelationDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing relation id", nil)
		return
	}
	if err := a.agent.RelationDelete(id); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to delete relation", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *App) handleGraphFindPath(w http.ResponseWriter, r *http.Request) {
	fromID := r.URL.Query().Get("from")
	toID := r.URL.Query().Get("to")
	if fromID == "" || toID == "" {
		a.writeError(w, http.StatusBadRequest, "from and to query params are required", nil)
		return
	}
	maxDepth := 5
	depthArg := r.URL.Query().Get("max_depth")
	if depthArg == "" {
		depthArg = r.URL.Query().Get("depth")
	}
	if d := depthArg; d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			maxDepth = parsed
		}
	}
	path, err := a.agent.GraphFindPath(fromID, toID, maxDepth)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to find path", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"path": path})
}

func (a *App) handleTunnels(w http.ResponseWriter, _ *http.Request) {
	result, err := a.client.Tunnels()
	if err != nil {
		// Fallback to empty if daemon doesn't support tunnels yet.
		a.logger.Debug("tunnels RPC failed, returning empty", "error", err)
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
	result, err := a.client.CacheStats()
	if err != nil {
		// Fallback to local HUD cache stats if daemon doesn't support cache RPC.
		a.logger.Debug("cache stats RPC failed, returning local cache", "error", err)
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

// handleSandbox returns devbox sandbox summary by calling the devbox_summary tool.
// The result is cached for 5s to avoid hammering the daemon on rapid refreshes.
// Returns {"available": false} if mcp-devbox is not running.
func (a *App) handleSandbox(w http.ResponseWriter, _ *http.Request) {
	if cached, ok := a.cache.Get("sandbox_summary"); ok {
		a.writeJSON(w, http.StatusOK, cached)
		return
	}

	result, err := a.client.CallTool("devbox_summary", nil)
	if err != nil {
		// Devbox not available — graceful fallback.
		a.logger.Debug("devbox_summary call failed, returning unavailable", "error", err)
		fallback := map[string]any{"available": false}
		a.cache.Set("sandbox_summary", fallback, 5*time.Second)
		a.writeJSON(w, http.StatusOK, fallback)
		return
	}

	// Parse the raw tool result and inject available=true.
	var summary map[string]any
	if err := json.Unmarshal(result, &summary); err != nil {
		a.logger.Debug("devbox_summary unmarshal failed", "error", err)
		fallback := map[string]any{"available": false}
		a.writeJSON(w, http.StatusOK, fallback)
		return
	}
	summary["available"] = true
	a.cache.Set("sandbox_summary", summary, 5*time.Second)
	a.writeJSON(w, http.StatusOK, summary)
}

// handleSandboxPolicy serves the sandbox policy from .sandbox-policy.json.
// Searches cwd and common profile directories for the policy file.
func (a *App) handleSandboxPolicy(w http.ResponseWriter, _ *http.Request) {
	if cached, ok := a.cache.Get("sandbox_policy"); ok {
		a.writeJSON(w, http.StatusOK, cached)
		return
	}

	// Search well-known locations for the policy file.
	cwd, _ := os.Getwd()
	candidates := []string{
		filepath.Join(cwd, ".sandbox-policy.json"),
		filepath.Join(cwd, ".claude", ".sandbox-policy.json"),
		filepath.Join(cwd, ".codex", ".sandbox-policy.json"),
		filepath.Join(cwd, ".gemini", ".sandbox-policy.json"),
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var policy map[string]any
		if err := json.Unmarshal(data, &policy); err != nil {
			continue
		}
		a.cache.Set("sandbox_policy", policy, 60*time.Second)
		a.writeJSON(w, http.StatusOK, policy)
		return
	}

	// No policy found — return empty.
	empty := map[string]any{"configured": false}
	a.cache.Set("sandbox_policy", empty, 30*time.Second)
	a.writeJSON(w, http.StatusOK, empty)
}

// handleSandboxStart triggers devbox_build for a project via the daemon.
// POST /api/sandbox/start
func (a *App) handleSandboxStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project string `json:"project"`
		AgentID string `json:"agent_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Project == "" {
		a.writeError(w, http.StatusBadRequest, "project is required", nil)
		return
	}

	args := map[string]any{"project": body.Project}
	if body.AgentID != "" {
		args["agent_id"] = body.AgentID
	}
	result, err := a.client.CallTool("devbox_build", args)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to start sandbox", err)
		return
	}

	// Invalidate summary cache so next poll picks up the new sandbox.
	a.cache.Invalidate("sandbox_summary")

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	parsed["ok"] = true
	a.writeJSON(w, http.StatusOK, parsed)
}

// handleSandboxStop stops a running sandbox container for a project.
// POST /api/sandbox/stop
func (a *App) handleSandboxStop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Project == "" {
		a.writeError(w, http.StatusBadRequest, "project is required", nil)
		return
	}

	_, err := a.client.CallTool("devbox_stop", map[string]any{"project": body.Project})
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to stop sandbox", err)
		return
	}

	// Invalidate summary cache.
	a.cache.Invalidate("sandbox_summary")

	a.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "project": body.Project})
}

func (a *App) handleReasoningChainList(w http.ResponseWriter, _ *http.Request) {
	chains, err := a.agent.ReasoningChainList()
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list reasoning chains", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"chains": chains})
}

func (a *App) handleReasoningChainDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing chain id", nil)
		return
	}
	detail, err := a.agent.ReasoningChainGet(id)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to get reasoning chain", err)
		return
	}
	a.writeJSON(w, http.StatusOK, detail)
}

func (a *App) handleReasoningChainCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Title == "" {
		a.writeError(w, http.StatusBadRequest, "title is required", nil)
		return
	}
	if err := a.agent.ReasoningChainAdd(body.Title, body.Description); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to create reasoning chain", err)
		return
	}
	a.writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (a *App) handleHandoffList(w http.ResponseWriter, _ *http.Request) {
	handoffs, err := a.agent.HandoffList()
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list handoffs", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"handoffs": handoffs})
}

func (a *App) handleHandoffCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ToAgent string `json:"to_agent"`
		Summary string `json:"summary"`
		Context string `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Summary == "" {
		a.writeError(w, http.StatusBadRequest, "summary is required", nil)
		return
	}
	if err := a.agent.HandoffCreate(body.ToAgent, body.Summary, body.Context); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to create handoff", err)
		return
	}
	a.broadcastAgentEvent("hud.handoff.created", map[string]any{
		"to_agent": body.ToAgent,
		"summary":  body.Summary,
	})
	a.writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (a *App) handleHandoffAccept(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing handoff id", nil)
		return
	}
	if err := a.agent.HandoffAccept(id); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to accept handoff", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
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

				_, endErr := a.agent.EndSession(bridge.SessionEndParams{
					SessionID: session.ID,
					AgentID:   agent.AgentID,
					Summarize: true,
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

// handleAgentDispatch dispatches a task to a specific agent from the HUD.
// POST /api/agent/dispatch
func (a *App) handleAgentDispatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetAgentID string   `json:"target_agent_id"`
		Title         string   `json:"title"`
		Context       string   `json:"context"`
		Priority      string   `json:"priority"`
		Tags          []string `json:"tags"`
		FilePath      string   `json:"file_path"`
		LineNumber    int      `json:"line_number"`
		BlockedBy     []string `json:"blocked_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	body.TargetAgentID = strings.TrimSpace(body.TargetAgentID)
	body.Title = strings.TrimSpace(body.Title)
	body.Context = strings.TrimSpace(body.Context)
	body.FilePath = strings.TrimSpace(body.FilePath)
	body.Tags = normalizeStringList(body.Tags)
	body.BlockedBy = normalizeStringList(body.BlockedBy)
	body.Priority = normalizeTaskPriority(body.Priority)
	if body.LineNumber < 0 {
		body.LineNumber = 0
	}

	if body.TargetAgentID == "" || body.Title == "" {
		a.writeError(w, http.StatusBadRequest, "target_agent_id and title are required", nil)
		return
	}

	result, err := a.agent.DispatchTask(bridge.DispatchTaskParams{
		TargetAgentID: body.TargetAgentID,
		Title:         body.Title,
		Context:       body.Context,
		Priority:      body.Priority,
		Tags:          body.Tags,
		FilePath:      body.FilePath,
		LineNumber:    body.LineNumber,
		BlockedBy:     body.BlockedBy,
	})
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to dispatch task", err)
		return
	}

	a.broadcastAgentEvent("agent.task.dispatched", map[string]any{
		"target_agent_id": body.TargetAgentID,
		"title":           body.Title,
		"priority":        body.Priority,
	})

	go a.fleetMonitor.Refresh()

	a.writeJSON(w, http.StatusOK, result)
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

// handleClaimRelease force-releases a file claim for an agent.
// DELETE /api/claims/{agent_id}/{file_path...}
func (a *App) handleClaimRelease(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	filePath := r.PathValue("file_path")
	if agentID == "" || filePath == "" {
		a.writeError(w, http.StatusBadRequest, "agent_id and file_path are required", nil)
		return
	}

	if err := a.agent.ReleaseFileClaim(agentID, filePath); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to release claim", err)
		return
	}

	a.broadcastAgentEvent("hud.claim.released", map[string]any{
		"agent_id":  agentID,
		"file_path": filePath,
	})

	go a.fleetMonitor.Refresh()

	a.writeJSON(w, http.StatusOK, map[string]string{"status": "released"})
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
