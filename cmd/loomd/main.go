// loomd is the Loom daemon - a local MCP server multiplexer with connection pooling.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/crb2nu/loom/internal/daemon"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	var cfg daemon.Config

	rootCmd := &cobra.Command{
		Use:     "loomd",
		Short:   "Loom daemon - unified MCP hub management",
		Version: version,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cfg)
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

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cfg daemon.Config) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d, err := daemon.New(cfg)
	if err != nil {
		return fmt.Errorf("create daemon: %w", err)
	}

	if err := d.Start(ctx); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh
	cancel()

	return d.Stop()
}
