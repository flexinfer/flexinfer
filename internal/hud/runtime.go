package hud

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/window"
	"github.com/crb2nu/loom/internal/tui"
)

// Run creates and starts the HUD application. This is the main entry point
// called from the CLI command. It delegates to NewApp + StartMonitors for
// construction and monitor lifecycle, then adds standalone-only concerns
// (daemon client, event consumer, TLS, signal handling).
func Run(cfg Config) error {
	logger := runtimeLogger(cfg)

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

	stopConsumer := app.startStandaloneEventConsumer(cfg, logger)
	defer stopConsumer()

	mux := http.NewServeMux()
	app.registerRoutes(mux)

	server, ln, url, err := app.newStandaloneServer(mux)
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ln)
	}()

	return app.runStandaloneLoop(server, url, errCh, cfg, logger)
}

func runtimeLogger(cfg Config) *slog.Logger {
	if cfg.TUI {
		// In TUI mode, route HUD logs to the TUI log file so they don't
		// corrupt the bubbletea alt-screen.
		return newHUDTUILogger().With("component", "hud")
	}
	return slog.Default().With("component", "hud")
}

func (a *App) startStandaloneEventConsumer(cfg Config, logger *slog.Logger) func() {
	if cfg.MetricsAddr == "" {
		return func() {}
	}

	eventsURL := "http://" + cfg.MetricsAddr
	ec := bridge.NewEventConsumer(eventsURL, logger)

	// Wire daemon events to monitor refreshes. The OnAny handler below is
	// the single broadcast point for ALL events to browser clients.
	ec.On("server.health", func(e bridge.SSEEvent) {
		a.healthMonitor.Refresh()
	})
	ec.On("config.reload", func(e bridge.SSEEvent) {
		a.fleetMonitor.Refresh()
		a.healthMonitor.Refresh()
	})
	ec.On("process.start", func(e bridge.SSEEvent) {
		a.fleetMonitor.Refresh()
	})
	ec.On("process.stop", func(e bridge.SSEEvent) {
		a.fleetMonitor.Refresh()
	})
	ec.On("decomp.hint", func(e bridge.SSEEvent) {
		a.handleStandaloneDecompHint(logger, e)
	})
	// Only broadcast to SSE hub when browser clients may be connected.
	// In TUI mode no browser connects, so skip the fan-out overhead.
	if !cfg.TUI {
		ec.OnAny(func(e bridge.SSEEvent) {
			a.sseHub.Broadcast(e)
		})
	}

	// Wire push bridge to daemon events for push-worthy notifications.
	if a.pushBridge != nil {
		ec.OnAny(func(e bridge.SSEEvent) {
			go a.pushBridge.HandleEvent(e)
		})
	}

	ec.Start(context.Background())
	logger.Info("event consumer started", "url", eventsURL)
	return ec.Stop
}

func (a *App) handleStandaloneDecompHint(logger *slog.Logger, e bridge.SSEEvent) {
	// Log to activity timeline.
	a.eventLog.Append(TimelineEntry{
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
	snap := a.fleetMonitor.Snapshot()
	for _, agent := range snap.Agents {
		if agent.Status != "active" {
			continue
		}
		a.nudgeQueue.Add(agent.AgentID, NudgeEntry{
			ID:        NewNudgeID(agent.AgentID),
			Type:      "context_inject",
			Lane:      "advice",
			Content:   content,
			FromAgent: "hud",
		})
	}
}

func (a *App) newStandaloneServer(mux *http.ServeMux) (*http.Server, net.Listener, string, error) {
	bindAddr := a.config.BindAddress
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	addr := bindAddr + ":" + strconv.Itoa(a.config.Port)
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, nil, "", fmt.Errorf("listen on %s: %w", addr, err)
	}

	ln, url, cleanup, err := a.finalizeStandaloneListener(bindAddr, ln)
	if err != nil {
		ln.Close()
		return nil, nil, "", err
	}

	server := &http.Server{
		Handler:     mux,
		ReadTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second,
	}

	return server, &listenerWithCleanup{Listener: ln, cleanup: cleanup}, url, nil
}

func (a *App) finalizeStandaloneListener(bindAddr string, ln net.Listener) (net.Listener, string, func(), error) {
	logger := a.logger
	scheme := "http"
	if a.config.TLSCert != "" && a.config.TLSKey != "" {
		cert, tlsErr := tls.LoadX509KeyPair(a.config.TLSCert, a.config.TLSKey)
		if tlsErr != nil {
			return nil, "", nil, fmt.Errorf("load TLS cert/key: %w", tlsErr)
		}
		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		ln = tls.NewListener(ln, tlsCfg)
		scheme = "https"
		logger.Info("TLS enabled", "cert", a.config.TLSCert)
	} else if a.config.MobileOperatorToken != "" && bindAddr != "127.0.0.1" && bindAddr != "localhost" {
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

	logger.Info("HUD server started", "url", url, "listen_addr", actualAddr, "dev", a.config.Dev)
	fmt.Printf("Agent HUD running at %s\n", url)

	if !a.config.TUI {
		openBrowser(url)
	}

	return ln, url, func() {
		if err := RemovePortFile(); err != nil {
			logger.Warn("failed to remove port file", "path", portFile, "error", err)
		}
	}, nil
}

func (a *App) runStandaloneLoop(server *http.Server, url string, errCh <-chan error, cfg Config, logger *slog.Logger) error {
	// Graceful shutdown on SIGINT/SIGTERM/SIGHUP.
	// SIGHUP is sent when the controlling terminal closes (e.g., Ghostty quick terminal).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	if cfg.TUI {
		return a.runStandaloneTUI(ctx, stop, server, errCh, logger)
	}
	if cfg.Overlay {
		return a.runStandaloneOverlay(ctx, server, errCh, logger, url)
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

func (a *App) runStandaloneTUI(ctx context.Context, stop context.CancelFunc, server *http.Server, errCh <-chan error, logger *slog.Logger) error {
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
		Agent:  a.agent,
		Fleet:  a.fleetMonitor,
		Health: a.healthMonitor,
		Memory: a.memoryMonitor,
		Stream: a.streamMonitor,
	}, ctx)

	// TUI exited — shut down HTTP server.
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(shutdownCtx)
	return tuiErr
}

func (a *App) runStandaloneOverlay(ctx context.Context, server *http.Server, errCh <-chan error, logger *slog.Logger, url string) error {
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
		Edge:          a.config.OverlayEdge,
		Width:         a.config.OverlayWidth,
		Opacity:       a.config.OverlayOpacity,
		CornerRadius:  a.config.OverlayCornerRadius,
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

type listenerWithCleanup struct {
	net.Listener
	cleanup func()
}

func (l *listenerWithCleanup) Close() error {
	if l.cleanup != nil {
		l.cleanup()
		l.cleanup = nil
	}
	return l.Listener.Close()
}
