// hud_embed.go implements `loom hud --embed` (Slice S6 of EPIC 2 / UNIFY-2b).
//
// The embed mode runs the HUD in-process using the LocalCaller pattern from
// internal/hud/bridge/local_caller.go. The HUD lifetime is bound to the CLI
// process: SIGINT / SIGTERM tears it down.
//
// In contrast to the standalone `loom hud` (which connects to a running loom
// daemon over a Unix socket), `loom hud --embed` does not require an external
// daemon process. The Caller is wired to an in-process bridge.Dispatch
// function that returns method-not-found for every method, which keeps the
// HUD HTTP listener up and serving its own routes (e.g. /api/health, the
// embedded Svelte frontend) without a daemon. Monitor refreshes will fail
// silently — callers that need live data should keep using `loomd --hud-port`,
// which embeds a real daemon. This slice is a CLI-side seam that future work
// can fill in with a real in-process daemon dispatch.
//
// See docs/HUD_EMBEDDING.md for the embedded library API and lifecycle rules.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	"golang.org/x/sync/errgroup"

	"github.com/crb2nu/loom/internal/hud"
	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
	"github.com/crb2nu/loom/internal/tui"
)

// embedShutdownTimeout bounds how long server.Shutdown is allowed to block.
const embedShutdownTimeout = 5 * time.Second

// runEmbeddedHUD constructs an in-process HUD using a LocalCaller, mounts its
// routes on an HTTP server bound to 127.0.0.1:0 (OS-assigned port), prints the
// resulting URL, and blocks until ctx is cancelled (signal-driven) or the
// listener fails. When co-tui is true, the bubbletea TUI runs in the
// foreground while the HUD HTTP listener stays alive in a goroutine.
//
// dispatchOverride lets tests inject a custom bridge.Dispatch. When nil, a
// no-op dispatch returning a method-not-found error is used.
func runEmbeddedHUD(ctx context.Context, cfg hud.Config, coTUI bool, dispatchOverride bridge.Dispatch) error {
	logger := slog.Default().With("component", "hud-embed")

	// LocalCaller dispatches in-process. The default no-op dispatch makes
	// every monitor refresh fail with method-not-found; the HUD HTTP routes
	// still register and serve correctly, which is the contract for this slice.
	dispatch := dispatchOverride
	if dispatch == nil {
		dispatch = noopDispatch
	}
	caller := bridge.NewLocalCaller(dispatch)

	app, err := hud.NewApp(cfg, caller, logger)
	if err != nil {
		return fmt.Errorf("hud.NewApp: %w", err)
	}

	monitorCtx, cancelMonitors := context.WithCancel(ctx)
	defer cancelMonitors()
	if err := app.StartMonitors(monitorCtx); err != nil {
		return fmt.Errorf("hud.StartMonitors: %w", err)
	}
	defer app.StopMonitors()

	mux := http.NewServeMux()
	app.RegisterRoutes(mux)

	bindAddr := cfg.BindAddress
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	addr := net.JoinHostPort(bindAddr, "0")
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	tcpAddr, _ := ln.Addr().(*net.TCPAddr)
	url := fmt.Sprintf("http://%s/", net.JoinHostPort(bindAddr, fmt.Sprintf("%d", tcpAddr.Port)))
	fmt.Printf("loom hud (embed): %s\n", url)

	// Refresh in the background so monitor warm-up doesn't block listener.
	go app.RefreshMonitors()

	// Run the listener in an errgroup so the TUI co-host path (if enabled)
	// can wait on both the HTTP server and the TUI exit.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("hud serve: %w", err)
		}
		return nil
	})

	if coTUI {
		g.Go(func() error {
			deps, depsErr := buildEmbeddedTUIDeps(caller, logger)
			if depsErr != nil {
				return fmt.Errorf("build TUI deps: %w", depsErr)
			}
			defer deps.stop()
			return tui.RunWithDeps(deps.deps, gctx)
		})
	}

	// Watch for shutdown trigger (signal cascaded into ctx, or any goroutine error).
	g.Go(func() error {
		<-gctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), embedShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return nil
	})

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// runEmbeddedHUDFromCLI is the entry-point used by the loom hud command when
// --embed is set. It installs a SIGINT/SIGTERM handler local to the call so
// the cobra runtime stays unmodified.
func runEmbeddedHUDFromCLI(cfg hud.Config, coTUI bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runEmbeddedHUD(ctx, cfg, coTUI, nil)
}

// embeddedTUIDeps bundles the monitors created for the co-hosted TUI together
// with a stop function that tears them down. The HUD's own monitors are not
// shared because internal/hud does not export accessors for them; the polling
// overhead is in-process so the duplication cost is minimal.
type embeddedTUIDeps struct {
	deps tui.Deps
	stop func()
}

// buildEmbeddedTUIDeps constructs a fresh tui.Deps backed by the same Caller
// as the HUD. The TUI owns these monitors; their lifecycle is tied to the
// returned stop closure. Used only by the --embed --tui combination.
func buildEmbeddedTUIDeps(caller bridge.Caller, logger *slog.Logger) (*embeddedTUIDeps, error) {
	if caller == nil {
		return nil, errors.New("nil caller")
	}
	agent := bridge.NewAgentBridge(caller)
	fleet := monitor.NewFleetMonitor(caller, agent, logger)
	health := monitor.NewHealthMonitor(caller, logger)
	mem := monitor.NewMemoryMonitor(agent, logger)
	stream := monitor.NewStreamMonitor(agent, logger)

	fleet.Start(15 * time.Second)
	health.Start(5 * time.Second)
	mem.Start(10 * time.Second)
	stream.Start(5 * time.Second)

	return &embeddedTUIDeps{
		deps: tui.Deps{
			Agent:  agent,
			Fleet:  fleet,
			Health: health,
			Memory: mem,
			Stream: stream,
		},
		stop: func() {
			fleet.Stop()
			health.Stop()
			mem.Stop()
			stream.Stop()
		},
	}, nil
}

// noopDispatch is the default bridge.Dispatch used in --embed mode when no
// real daemon is wired in. Every JSON-RPC call returns a structured
// method-not-found error so monitors fail fast without hanging. The HUD HTTP
// routes that read from monitor caches still serve (with empty / default
// values), which is the documented contract for this slice.
func noopDispatch(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if msg == nil {
		return nil, errors.New("noop dispatch: nil message")
	}
	return &mcp.Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Error: &mcp.Error{
			Code:    mcp.MethodNotFound,
			Message: fmt.Sprintf("loom hud --embed: no daemon dispatch wired (method %q)", msg.Method),
		},
	}, nil
}
