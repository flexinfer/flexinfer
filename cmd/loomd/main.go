// loomd is the Loom daemon - a local MCP server multiplexer with connection pooling.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/internal/daemon"
)

var version = "dev"

func main() {
	var cfg daemon.Config
	var metricsAddr string

	rootCmd := &cobra.Command{
		Use:     "loomd",
		Short:   "Loom daemon - unified MCP hub management",
		Version: version,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cfg, metricsAddr)
		},
	}

	flags := rootCmd.Flags()
	defaultCfg := daemon.DefaultConfig()

	flags.StringVar(&cfg.SocketPath, "socket", defaultCfg.SocketPath, "Unix socket path")
	flags.StringVar(&cfg.RegistryPath, "registry", defaultCfg.RegistryPath, "MCP registry YAML path")
	flags.StringVar(&cfg.Target, "target", defaultCfg.Target, "Target profile (codex, kilocode, vscode)")
	flags.StringVar(&cfg.HubURL, "hub-url", defaultCfg.HubURL, "MCP hub WebSocket URL")
	flags.BoolVar(&cfg.HubFallback, "hub-fallback", defaultCfg.HubFallback, "Fallback to hub when local fails")
	flags.BoolVar(&cfg.HubPrefer, "hub-prefer", defaultCfg.HubPrefer, "Prefer hub over local servers (hub-capable servers only)")
	flags.StringSliceVar(&cfg.WarmOnStart, "warm", nil, "Servers to warm up on start")
	flags.BoolVar(&cfg.Debug, "debug", false, "Enable debug logging")
	flags.StringVar(&metricsAddr, "metrics-addr", "", "Address for metrics endpoint (e.g., :9090)")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cfg daemon.Config, metricsAddr string) error {
	// Best-effort raise the file descriptor limit early. A low RLIMIT_NOFILE
	// prevents loomd from spawning many MCP servers (EMFILE / "too many open files").
	tuneNoFileLimit(slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Export socket path so child MCP servers (e.g., mcp-agent-context) can
	// dial back to the daemon for tool execution (workflow loopback).
	os.Setenv("LOOM_SOCKET", cfg.SocketPath)

	d, err := daemon.New(cfg)
	if err != nil {
		return fmt.Errorf("create daemon: %w", err)
	}

	if err := d.Start(ctx); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	// Start metrics server if address is provided
	if metricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", d.MetricsHandler())
		mux.HandleFunc("/health", d.HealthHandler())
		mux.HandleFunc("/events", d.EventBus().ServeSSE)

		server := &http.Server{
			Addr:    metricsAddr,
			Handler: mux,
		}

		go func() {
			slog.Info("metrics server started", "addr", metricsAddr)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("metrics server error", "error", err)
			}
		}()

		// Shutdown metrics server on context cancel
		go func() {
			<-ctx.Done()
			server.Shutdown(context.Background())
		}()
	}

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh
	cancel()

	return d.Stop()
}
