// cmd_health.go implements `loom health`, the unified visibility CLI for
// per-MCP-server health snapshots (UNIFY-4c, EPIC 2 #66).
//
// The command pulls a snapshot of the daemon's loom/health RPC, normalizes
// it into the canonical internal/visibility/contracts/health types, and
// renders either a column-aligned text table or a JSON document via the
// shared cmd/loom/internal/render helpers.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	render "github.com/crb2nu/loom/cmd/loom/internal/render"
	health "github.com/crb2nu/loom/internal/visibility/contracts/health"
)

// healthFetcher is a seam for tests; production wiring uses the daemon
// socket JSON-RPC path, and tests inject a stub returning canned data.
type healthFetcher func(socketPath string) (*health.HealthResult, error)

// defaultHealthFetcher invokes the loom/health RPC over the unix socket and
// unmarshals into the canonical contracts type.
func defaultHealthFetcher(socketPath string) (*health.HealthResult, error) {
	raw, err := call(socketPath, "loom/health", nil)
	if err != nil {
		return nil, err
	}
	var result health.HealthResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse health: %w", err)
	}
	return &result, nil
}

func newHealthCmd(socketPath string) *cobra.Command {
	var (
		jsonOutput bool
		watch      time.Duration
	)

	cmd := &cobra.Command{
		Use:   "health",
		Short: "Show per-MCP-server health snapshots",
		Long: `Show per-MCP-server health snapshots from the daemon's loom/health RPC.

The output lists each server with its local healthy state, hub-side healthy
state, transport, and average latency. Divergences between the health monitor
and router (when present) are surfaced as a footer summary.

Use --json for machine-readable output. Use --watch <duration> to refresh in
place at the given cadence (minimum 1s; values below are clamped).

Exit codes:
  0  success — daemon reachable and snapshot rendered.
  1  daemon unreachable, RPC error, or any server reports degraded state.`,
		Example: `  loom health
  loom health --json
  loom health --watch 5s`,
		RunE: func(cmd *cobra.Command, args []string) error {
			socket := resolveSocketPath(cmd)
			if socket == "" {
				socket = socketPath
			}
			return runHealthCommand(cmd.Context(), cmd.OutOrStdout(), socket, jsonOutput, watch, defaultHealthFetcher)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit JSON instead of a text table")
	cmd.Flags().DurationVar(&watch, "watch", 0, "Refresh in place at this cadence (e.g. 5s); 0 disables watch")
	return cmd
}

// runHealthCommand is the testable entry point.
//
// When watch is non-zero, the command runs render.Watch with a SIGINT-
// cancellable context so Ctrl-C exits cleanly. The minimum honored interval
// is 1s; smaller values are clamped up.
func runHealthCommand(ctx context.Context, out io.Writer, socketPath string, jsonOutput bool, watch time.Duration, fetch healthFetcher) error {
	if fetch == nil {
		fetch = defaultHealthFetcher
	}

	render1 := func() error {
		snap, err := fetch(socketPath)
		if err != nil {
			return fmt.Errorf("health: daemon unreachable: %w", err)
		}
		if jsonOutput {
			return render.JSON(out, snap)
		}
		return renderHealthTable(out, snap)
	}

	if watch <= 0 {
		return render1()
	}

	// Clamp to >= 1s so a typo like `--watch 100ms` doesn't pin a CPU.
	if watch < time.Second {
		watch = time.Second
	}

	if ctx == nil {
		ctx = context.Background()
	}
	wctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return render.Watch(wctx, out, watch, render1)
}

// renderHealthTable writes a text table suitable for terminal viewing.
//
// One row per server with local + hub healthy state, transport, and average
// latency. A footer summarizes divergence entries when present.
func renderHealthTable(out io.Writer, snap *health.HealthResult) error {
	if snap == nil {
		return fmt.Errorf("health: nil snapshot")
	}

	if len(snap.Servers) == 0 {
		_, err := fmt.Fprintln(out, "No servers reported by health monitor.")
		return err
	}

	tbl := render.Table{
		Headers: []string{"SERVER", "LOCAL", "HUB", "TRANSPORT", "TARGET", "LATENCY_MS"},
	}

	// Stable, sortable ordering by server name so output is deterministic.
	names := make([]string, 0, len(snap.Servers))
	for name := range snap.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		s := snap.Servers[name]
		tbl.Rows = append(tbl.Rows, []string{
			name,
			healthBool(s.Local.Healthy),
			healthBool(s.Hub.Healthy),
			emptyDash(s.Transport),
			emptyDash(s.Target),
			healthLatency(s),
		})
	}
	if err := tbl.Render(out, render.Options{}); err != nil {
		return err
	}

	if len(snap.Divergence) > 0 {
		if _, err := fmt.Fprintln(out, "\nDivergences:"); err != nil {
			return err
		}
		for _, d := range snap.Divergence {
			if _, err := fmt.Fprintf(out, "  %s: %s\n", d.Server, d.Reason); err != nil {
				return err
			}
		}
	}

	return nil
}

// healthBool renders a healthy bool as a short, scannable string.
func healthBool(b bool) string {
	if b {
		return "ok"
	}
	return "down"
}

// healthLatency picks the more representative of local/hub avg latency for
// the latency column. When both are present, prefer local (where the daemon
// physically lives); when only hub is reported, fall back to it.
func healthLatency(s health.ServerHealth) string {
	if s.Local.AvgLatencyMs > 0 {
		return strconv.FormatFloat(s.Local.AvgLatencyMs, 'f', 1, 64)
	}
	if s.Hub.AvgLatencyMs > 0 {
		return strconv.FormatFloat(s.Hub.AvgLatencyMs, 'f', 1, 64)
	}
	return "-"
}

// emptyDash returns "-" for empty strings so empty cells render visibly.
func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
