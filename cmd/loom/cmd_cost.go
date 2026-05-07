// cmd_cost.go implements `loom cost`, the unified visibility CLI for
// cost/usage telemetry (UNIFY-4b, EPIC 2 #66).
//
// The command pulls a snapshot of the daemon's cost-stats RPC, normalizes
// it into the canonical internal/visibility/contracts/cost types, and
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
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	render "github.com/crb2nu/loom/cmd/loom/internal/render"
	cost "github.com/crb2nu/loom/internal/visibility/contracts/cost"
)

// costFetcher is a seam for tests: production wiring uses the daemon socket
// JSON-RPC path, and tests inject a stub returning canned data or errors.
type costFetcher func(socketPath string) (*cost.CostStatsResult, error)

// defaultCostFetcher invokes the loom/cost-stats RPC over the unix socket
// and unmarshals into the canonical contracts type.
func defaultCostFetcher(socketPath string) (*cost.CostStatsResult, error) {
	raw, err := call(socketPath, "loom/cost-stats", nil)
	if err != nil {
		return nil, err
	}
	var result cost.CostStatsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse cost-stats: %w", err)
	}
	return &result, nil
}

// costGroupBy enumerates the supported --by values.
type costGroupBy string

const (
	costGroupByAgent  costGroupBy = "agent"
	costGroupByServer costGroupBy = "server"
	costGroupByDay    costGroupBy = "day"
)

func parseCostGroupBy(s string) (costGroupBy, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "agent":
		return costGroupByAgent, nil
	case "server":
		return costGroupByServer, nil
	case "day":
		return costGroupByDay, nil
	default:
		return "", fmt.Errorf("invalid --by %q (expected agent|server|day)", s)
	}
}

func newCostCmd(socketPath string) *cobra.Command {
	var (
		by         string
		jsonOutput bool
		watch      time.Duration
	)

	cmd := &cobra.Command{
		Use:   "cost",
		Short: "Show MCP cost / usage telemetry",
		Long: `Show MCP cost / usage telemetry from the daemon's cost-stats RPC.

Group rows by agent, server, or day with --by. Use --json for machine-readable
output. Use --watch <duration> to refresh in place at the given cadence
(minimum 1s; values below are clamped).

Exit codes:
  0  success — daemon reachable and snapshot rendered.
  1  daemon unreachable, RPC error, or invalid flag value.`,
		Example: `  loom cost
  loom cost --by server
  loom cost --by day --json
  loom cost --watch 5s`,
		RunE: func(cmd *cobra.Command, args []string) error {
			groupBy, err := parseCostGroupBy(by)
			if err != nil {
				return err
			}
			socket := resolveSocketPath(cmd)
			if socket == "" {
				socket = socketPath
			}
			return runCostCommand(cmd.Context(), cmd.OutOrStdout(), socket, groupBy, jsonOutput, watch, defaultCostFetcher)
		},
	}

	cmd.Flags().StringVar(&by, "by", "agent", "Group rows by: agent|server|day")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit JSON instead of a text table")
	cmd.Flags().DurationVar(&watch, "watch", 0, "Refresh in place at this cadence (e.g. 5s); 0 disables watch")
	return cmd
}

// runCostCommand is the testable entry point.
//
// When watch is non-zero, the command runs render.Watch with a SIGINT-
// cancellable context so Ctrl-C exits cleanly. The minimum honored interval
// is 1s; smaller values are clamped up.
func runCostCommand(ctx context.Context, out io.Writer, socketPath string, groupBy costGroupBy, jsonOutput bool, watch time.Duration, fetch costFetcher) error {
	if fetch == nil {
		fetch = defaultCostFetcher
	}

	render1 := func() error {
		snap, err := fetch(socketPath)
		if err != nil {
			return fmt.Errorf("cost: daemon unreachable: %w", err)
		}
		if jsonOutput {
			return render.JSON(out, snap)
		}
		return renderCostTable(out, snap, groupBy)
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

// renderCostTable writes a text table suitable for terminal viewing.
//
// Layout depends on groupBy: agent rows expose call/error/denied/cached/avg-ms,
// server rows expose call/error/avg-ms, day rows roll all per-agent buckets up
// (the daemon contract does not yet emit per-day buckets, so the day view falls
// back to the snapshot timestamp + totals row to surface "today").
func renderCostTable(out io.Writer, snap *cost.CostStatsResult, groupBy costGroupBy) error {
	if snap == nil {
		return fmt.Errorf("cost: nil snapshot")
	}
	if !snap.Enabled {
		reason := snap.Reason
		if strings.TrimSpace(reason) == "" {
			reason = "cost tracking disabled"
		}
		_, err := fmt.Fprintf(out, "Cost tracking: disabled (%s)\n", reason)
		return err
	}

	header := fmt.Sprintf("Cost stats (group=%s)", groupBy)
	if ts := strings.TrimSpace(snap.Timestamp); ts != "" {
		header += " @ " + ts
	}
	if _, err := fmt.Fprintln(out, header); err != nil {
		return err
	}

	tbl := buildCostTable(snap, groupBy)
	if err := tbl.Render(out, render.Options{}); err != nil {
		return err
	}

	// Always print the totals footer so operators see denominator at a glance.
	t := snap.Totals
	_, err := fmt.Fprintf(out,
		"Totals: calls=%d errors=%d denied=%d cached=%d duration_ms=%d\n",
		t.CallCount, t.ErrorCount, t.DeniedCount, t.CachedCount, t.TotalDuration,
	)
	return err
}

// buildCostTable shapes the snapshot into a render.Table for the given grouping.
func buildCostTable(snap *cost.CostStatsResult, groupBy costGroupBy) render.Table {
	switch groupBy {
	case costGroupByServer:
		return costServerTable(snap.ByServer)
	case costGroupByDay:
		return costDayTable(snap)
	default:
		return costAgentTable(snap.ByAgent)
	}
}

func costAgentTable(rows []cost.CostAgentUsage) render.Table {
	out := render.Table{
		Headers: []string{"AGENT", "CALLS", "ERRORS", "DENIED", "CACHED", "AVG_MS"},
	}
	// Stable, sortable output.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].AgentID < rows[j].AgentID })
	for _, r := range rows {
		avg := costAvgMs(r.TotalDuration, r.CallCount)
		out.Rows = append(out.Rows, []string{
			r.AgentID,
			strconv.FormatInt(r.CallCount, 10),
			strconv.FormatInt(r.ErrorCount, 10),
			strconv.FormatInt(r.DeniedCount, 10),
			strconv.FormatInt(r.CachedCount, 10),
			avg,
		})
	}
	return out
}

func costServerTable(rows []cost.CostServerUsage) render.Table {
	out := render.Table{
		Headers: []string{"SERVER", "CALLS", "ERRORS", "AVG_MS"},
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Server < rows[j].Server })
	for _, r := range rows {
		avg := costAvgMs(r.TotalDuration, r.CallCount)
		out.Rows = append(out.Rows, []string{
			r.Server,
			strconv.FormatInt(r.CallCount, 10),
			strconv.FormatInt(r.ErrorCount, 10),
			avg,
		})
	}
	return out
}

// costDayTable summarizes the snapshot as a single "today" row keyed by the
// snapshot timestamp (when present). The daemon contract does not yet emit
// per-day buckets — this view is intentionally minimal until UNIFY-3b adds
// historical retention. See plan doc .loom/104-implementation-plan-unify-
// visibility-2026-05-06.md (Slice S9).
func costDayTable(snap *cost.CostStatsResult) render.Table {
	day := dayKey(snap.Timestamp)
	t := snap.Totals
	return render.Table{
		Headers: []string{"DAY", "CALLS", "ERRORS", "DENIED", "CACHED", "AVG_MS"},
		Rows: [][]string{{
			day,
			strconv.FormatInt(t.CallCount, 10),
			strconv.FormatInt(t.ErrorCount, 10),
			strconv.FormatInt(t.DeniedCount, 10),
			strconv.FormatInt(t.CachedCount, 10),
			costAvgMs(t.TotalDuration, t.CallCount),
		}},
	}
}

// dayKey returns the YYYY-MM-DD prefix of timestamp, or "today" when the
// daemon does not emit a parseable timestamp.
func dayKey(timestamp string) string {
	ts := strings.TrimSpace(timestamp)
	if ts == "" {
		return "today"
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.UTC().Format("2006-01-02")
	}
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

// costAvgMs returns total/calls as a string with one decimal, or "-" when
// calls is zero so empty rows don't display "NaN" or "Inf".
func costAvgMs(total, calls int64) string {
	if calls <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", float64(total)/float64(calls))
}
