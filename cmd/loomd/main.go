// loomd is the Loom daemon - a local MCP server multiplexer with connection pooling.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/internal/daemon"
	"github.com/crb2nu/loom/pkg/mcpotel"
)

var version = "dev"

const defaultMetricsAddr = "127.0.0.1:9876"

func main() {
	var cfg daemon.Config
	var metricsAddr string
	metricsDefault := strings.TrimSpace(os.Getenv("LOOM_METRICS_ADDR"))
	if metricsDefault == "" {
		metricsDefault = defaultMetricsAddr
	}

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
	flags.StringVar(&metricsAddr, "metrics-addr", metricsDefault, "Address for metrics/health/events endpoint (e.g., 127.0.0.1:9876; empty disables)")
	flags.StringVar(&cfg.HTTPAddr, "http-addr", "", "Address for Streamable HTTP listener (e.g., :8088)")

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

	// Initialize OTel tracing for daemon lifecycle and request handling.
	_, shutdownTracer, err := mcpotel.InitTracer(ctx, "loomd", slog.Default())
	if err != nil {
		slog.Warn("OTel tracer init failed, continuing without tracing", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()

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

	metricsAddr = strings.TrimSpace(metricsAddr)

	// Start metrics server(s) if an address is provided.
	// Compatibility: if a non-default metrics addr is configured, also expose the
	// default local endpoint so health checks remain predictable across upgrades.
	if metricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", d.MetricsHandler())
		mux.HandleFunc("/health", d.HealthHandler())
		mux.HandleFunc("/events", d.EventBus().ServeSSE)

		addrs := []string{metricsAddr}
		if metricsAddr != defaultMetricsAddr {
			addrs = append(addrs, defaultMetricsAddr)
		}
		seen := make(map[string]struct{}, len(addrs))
		servers := make([]*http.Server, 0, len(addrs))

		for _, addr := range addrs {
			addr = strings.TrimSpace(addr)
			if addr == "" {
				continue
			}
			if _, ok := seen[addr]; ok {
				continue
			}
			seen[addr] = struct{}{}

			server := &http.Server{
				Addr:              addr,
				Handler:           mux,
				ReadHeaderTimeout: 5 * time.Second,
				IdleTimeout:       60 * time.Second,
			}
			servers = append(servers, server)

			go func(addr string, server *http.Server) {
				slog.Info("metrics server started", "addr", addr)
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					slog.Error("metrics server error", "addr", addr, "error", err)
				}
			}(addr, server)
		}

		// Shutdown metrics server(s) on context cancel
		go func() {
			<-ctx.Done()
			for _, server := range servers {
				_ = server.Shutdown(context.Background())
			}
		}()
	}

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh
	cancel()

	return d.Stop()
}
