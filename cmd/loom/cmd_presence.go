// cmd_presence.go implements `loom presence list`, the unified visibility
// CLI for agent presence snapshots (UNIFY-4c, EPIC 2 #66).
//
// The command pulls a snapshot of the daemon-fronted HUD presence list,
// normalizes it into the canonical
// internal/visibility/contracts/presence.PresenceInfo type, and renders
// either a column-aligned text table or a JSON document via the shared
// cmd/loom/internal/render helpers.
//
// The fetcher signature mirrors `loom health` / `loom cost` /
// `loom rbac status`: a single function that returns the canonical contracts
// type plus an error. Tests inject a stub returning canned data; production
// uses the HUD's GET /api/presence endpoint.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	render "github.com/crb2nu/loom/cmd/loom/internal/render"
	presencectr "github.com/crb2nu/loom/internal/visibility/contracts/presence"
)

// presenceListResult is the shape returned by GET /api/presence.
//
// The HUD wraps the agent slice in an envelope so the JSON serialization can
// grow status counts in future. This CLI consumes only Agents.
type presenceListResult struct {
	Agents []presencectr.PresenceInfo `json:"agents"`
}

// presenceFetcher is a seam for tests; production wiring uses the HUD's
// /api/presence endpoint via hudGet.
type presenceFetcher func(port string) (*presenceListResult, error)

// defaultPresenceFetcher invokes GET /api/presence over the HUD HTTP API.
func defaultPresenceFetcher(port string) (*presenceListResult, error) {
	raw, err := hudGet(port, "/api/presence")
	if err != nil {
		return nil, err
	}
	var result presenceListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse presence: %w", err)
	}
	return &result, nil
}

func newPresenceCmd(_ string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "presence",
		Short: "Inspect agent presence (heartbeats, status, branches)",
		Long: `Inspect agent presence — heartbeats, status, branch, and current task.

Subcommands snapshot the daemon-fronted presence list returned by the HUD
and render either a JSON document or a text table.`,
	}
	cmd.AddCommand(newPresenceListCmd())
	return cmd
}

func newPresenceListCmd() *cobra.Command {
	var (
		jsonOutput bool
		filterSpec string
		watch      time.Duration
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List agents in the presence registry",
		Long: `List agents in the presence registry with their heartbeat status,
current branch, and active task.

Use --filter status=… to limit rows to a single status (case-insensitive
exact match). Use --json for machine-readable output. Use --watch <duration>
to refresh in place at the given cadence (minimum 1s; values below are
clamped).

Exit codes:
  0  success — HUD reachable and snapshot rendered.
  1  HUD unreachable, RPC error, or invalid filter.`,
		Example: `  loom presence list
  loom presence list --json
  loom presence list --filter status=active
  loom presence list --watch 5s`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filter, err := render.ParseFilter(filterSpec)
			if err != nil {
				return err
			}
			port := resolvePort(cmd)
			return runPresenceListCommand(cmd.Context(), cmd.OutOrStdout(), port, jsonOutput, filter, watch, defaultPresenceFetcher)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit JSON instead of a text table")
	cmd.Flags().StringVar(&filterSpec, "filter", "", "Filter rows by key=value (e.g. status=active)")
	cmd.Flags().DurationVar(&watch, "watch", 0, "Refresh in place at this cadence (e.g. 5s); 0 disables watch")
	return cmd
}

// runPresenceListCommand is the testable entry point.
//
// When watch is non-zero, the command runs render.Watch with a SIGINT-
// cancellable context so Ctrl-C exits cleanly. The minimum honored interval
// is 1s; smaller values are clamped up.
func runPresenceListCommand(ctx context.Context, out io.Writer, port string, jsonOutput bool, filter render.Filter, watch time.Duration, fetch presenceFetcher) error {
	if fetch == nil {
		fetch = defaultPresenceFetcher
	}

	render1 := func() error {
		snap, err := fetch(port)
		if err != nil {
			return fmt.Errorf("presence: HUD unreachable: %w", err)
		}
		filtered := filterPresenceRows(snap.Agents, filter)
		if jsonOutput {
			return render.JSON(out, presenceListResult{Agents: filtered})
		}
		return renderPresenceTable(out, filtered)
	}

	if watch <= 0 {
		return render1()
	}

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

// filterPresenceRows applies a render.Filter to the agent slice.
//
// Supported filter keys (case-insensitive on key + value): status, agent_id,
// agent_type, branch. An empty filter passes every row.
func filterPresenceRows(rows []presencectr.PresenceInfo, filter render.Filter) []presencectr.PresenceInfo {
	if len(filter) == 0 {
		return rows
	}
	out := make([]presencectr.PresenceInfo, 0, len(rows))
	for _, p := range rows {
		row := map[string]string{
			"status":     p.Status,
			"agent_id":   p.AgentID,
			"agent_type": p.AgentType,
			"branch":     p.Branch,
		}
		if filter.Match(row) {
			out = append(out, p)
		}
	}
	return out
}

// renderPresenceTable writes a text table suitable for terminal viewing.
//
// One row per agent with id, status, last-heartbeat, branch, and current
// task. Empty fields render as "-" so columns stay visibly aligned.
func renderPresenceTable(out io.Writer, rows []presencectr.PresenceInfo) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(out, "No agents in presence registry.")
		return err
	}

	tbl := render.Table{
		Headers: []string{"AGENT_ID", "STATUS", "LAST_SEEN", "BRANCH", "TASK"},
	}

	// Stable, sortable ordering by agent id so output is deterministic.
	sorted := append([]presencectr.PresenceInfo(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].AgentID < sorted[j].AgentID })

	for _, p := range sorted {
		tbl.Rows = append(tbl.Rows, []string{
			emptyDash(p.AgentID),
			emptyDash(p.Status),
			emptyDash(p.LastHeartbeat),
			emptyDash(p.Branch),
			emptyDash(p.CurrentTask),
		})
	}
	return tbl.Render(out, render.Options{})
}
