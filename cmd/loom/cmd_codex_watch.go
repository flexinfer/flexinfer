package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/internal/hud/codexwatch"
	"github.com/crb2nu/loom/pkg/eventpub"
)

// newCodexWatchCmd creates the `loom codex-watch` long-running command.
// It tails ~/.codex/sessions JSONL files and republishes Codex Desktop
// activity as canonical loom events (session.start / tool.call.start /
// tool.call.end / agent.status.change / session.end).
//
// See .loom/23-product-spec-codex-session-tail-2026-05-16.md.
func newCodexWatchCmd() *cobra.Command {
	var (
		sessionsDir       string
		daemonURL         string
		fromAll           bool
		pollInterval      time.Duration
		discoveryInterval time.Duration
		idleTimeout       time.Duration
		maxLifetime       time.Duration
		verbose           bool
	)

	cmd := &cobra.Command{
		Use:   "codex-watch",
		Short: "Tail Codex Desktop sessions and republish activity as loom events",
		Long: `Watch ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl and emit canonical loom
events (session.start, tool.call.start, tool.call.end, agent.status.change,
session.end) so Codex Desktop sessions appear in the HUD fleet view alongside
Claude Code and other agents.

By default only new appends are tailed; pass --from-all to backfill from
historical files. The watcher exits cleanly on SIGINT/SIGTERM and emits a
synthetic session.end for every active tailer on shutdown.

Configure the target daemon via --daemon-url or $LOOM_DAEMON_HTTP_URL; defaults
to http://127.0.0.1:9876 (the loomd events port).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logLevel := slog.LevelInfo
			if verbose {
				logLevel = slog.LevelDebug
			}
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

			pub := eventpub.NewHTTPPublisher(
				resolveDaemonHTTPURL(daemonURL),
				resolveAdminToken(),
				logger.With("component", "eventpub"),
			)

			w, err := codexwatch.NewWatcher(pub, codexwatch.Options{
				SessionsDir:       sessionsDir,
				FromAll:           fromAll,
				PollInterval:      pollInterval,
				DiscoveryInterval: discoveryInterval,
				IdleTimeout:       idleTimeout,
				MaxLifetime:       maxLifetime,
				Logger:            logger.With("component", "codexwatch"),
			})
			if err != nil {
				return fmt.Errorf("codex-watch: %w", err)
			}

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			logger.Info("codexwatch: starting",
				"sessions_dir", sessionsDir,
				"daemon_url", resolveDaemonHTTPURL(daemonURL),
				"from_all", fromAll,
			)
			w.Run(ctx)
			logger.Info("codexwatch: stopped")
			return nil
		},
	}

	cmd.Flags().StringVar(&sessionsDir, "sessions-dir", "", "Codex sessions root (default: $HOME/.codex/sessions)")
	cmd.Flags().StringVar(&daemonURL, "daemon-url", "", "Daemon HTTP URL (default: $LOOM_DAEMON_HTTP_URL or http://127.0.0.1:9876)")
	cmd.Flags().BoolVar(&fromAll, "from-all", false, "Backfill all historical files instead of just new appends (debug)")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", 500*time.Millisecond, "How often each tailer re-stats its file")
	cmd.Flags().DurationVar(&discoveryInterval, "discovery-interval", 2*time.Second, "How often the watcher rescans for new session files")
	cmd.Flags().DurationVar(&idleTimeout, "idle-timeout", 30*time.Minute, "How long without growth before emitting session.end")
	cmd.Flags().DurationVar(&maxLifetime, "max-lifetime", 4*time.Hour, "Hard ceiling on a single tailer goroutine")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Enable debug logging")

	return cmd
}
