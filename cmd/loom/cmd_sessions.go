// cmd_sessions.go implements `loom sessions list`, the unified visibility
// CLI for agent session snapshots (UNIFY-4c, EPIC 2 #66).
//
// The command pulls a snapshot of the HUD's GET /api/sessions endpoint and
// renders either a JSON document or a column-aligned text table via the
// shared cmd/loom/internal/render helpers.
//
// TODO(EPIC 2 follow-up): migrate to internal/visibility/contracts/sessions
// once the scaffold is filled. The canonical SessionInfo currently lives in
// internal/hud/bridge because its UnmarshalJSON depends on bridge-local
// CanonicalProject and *PipelineRef helpers (see contracts/sessions/types.go
// for the migration note).
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
	"github.com/crb2nu/loom/internal/hud/bridge"
)

// sessionsListResult is the shape returned by GET /api/sessions.
type sessionsListResult struct {
	Sessions []bridge.SessionInfo `json:"sessions"`
}

// sessionsFetcher is a seam for tests; production wiring uses the HUD's
// /api/sessions endpoint via hudGet.
type sessionsFetcher func(port string) (*sessionsListResult, error)

// defaultSessionsFetcher invokes GET /api/sessions over the HUD HTTP API.
func defaultSessionsFetcher(port string) (*sessionsListResult, error) {
	raw, err := hudGet(port, "/api/sessions")
	if err != nil {
		return nil, err
	}
	var result sessionsListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse sessions: %w", err)
	}
	return &result, nil
}

func newSessionsCmd(_ string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Inspect agent sessions (id, agent, namespace, status)",
		Long: `Inspect agent sessions tracked by the daemon-fronted session registry.

Subcommands snapshot the HUD's session list and render either a JSON
document or a text table.`,
	}
	cmd.AddCommand(newSessionsListCmd())
	return cmd
}

func newSessionsListCmd() *cobra.Command {
	var (
		jsonOutput bool
		filterSpec string
		watch      time.Duration
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List active agent sessions",
		Long: `List agent sessions with id, agent_id, namespace, status, and start time.

Use --filter agent=… to limit rows to a single agent (case-insensitive
exact match). Use --json for machine-readable output. Use --watch <duration>
to refresh in place at the given cadence (minimum 1s; values below are
clamped).

Exit codes:
  0  success — HUD reachable and snapshot rendered.
  1  HUD unreachable, RPC error, or invalid filter.`,
		Example: `  loom sessions list
  loom sessions list --json
  loom sessions list --filter agent=claude-code
  loom sessions list --watch 5s`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filter, err := render.ParseFilter(filterSpec)
			if err != nil {
				return err
			}
			port := resolvePort(cmd)
			return runSessionsListCommand(cmd.Context(), cmd.OutOrStdout(), port, jsonOutput, filter, watch, defaultSessionsFetcher)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit JSON instead of a text table")
	cmd.Flags().StringVar(&filterSpec, "filter", "", "Filter rows by key=value (e.g. agent=claude-code)")
	cmd.Flags().DurationVar(&watch, "watch", 0, "Refresh in place at this cadence (e.g. 5s); 0 disables watch")
	return cmd
}

// runSessionsListCommand is the testable entry point.
//
// When watch is non-zero, the command runs render.Watch with a SIGINT-
// cancellable context so Ctrl-C exits cleanly. The minimum honored interval
// is 1s; smaller values are clamped up.
func runSessionsListCommand(ctx context.Context, out io.Writer, port string, jsonOutput bool, filter render.Filter, watch time.Duration, fetch sessionsFetcher) error {
	if fetch == nil {
		fetch = defaultSessionsFetcher
	}

	render1 := func() error {
		snap, err := fetch(port)
		if err != nil {
			return fmt.Errorf("sessions: HUD unreachable: %w", err)
		}
		filtered := filterSessionsRows(snap.Sessions, filter)
		if jsonOutput {
			return render.JSON(out, sessionsListResult{Sessions: filtered})
		}
		return renderSessionsTable(out, filtered)
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

// filterSessionsRows applies a render.Filter to the session slice.
//
// Supported filter keys (case-insensitive on key + value): agent, agent_id,
// namespace, status, project. The "agent" alias maps to agent_id so the
// flag's documented form (`--filter agent=...`) works alongside the raw
// JSON field name.
func filterSessionsRows(rows []bridge.SessionInfo, filter render.Filter) []bridge.SessionInfo {
	if len(filter) == 0 {
		return rows
	}
	out := make([]bridge.SessionInfo, 0, len(rows))
	for _, s := range rows {
		row := map[string]string{
			"agent":     s.AgentID,
			"agent_id":  s.AgentID,
			"namespace": s.Namespace,
			"status":    s.Status,
			"project":   s.Project,
		}
		if filter.Match(row) {
			out = append(out, s)
		}
	}
	return out
}

// renderSessionsTable writes a text table suitable for terminal viewing.
func renderSessionsTable(out io.Writer, rows []bridge.SessionInfo) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(out, "No sessions reported.")
		return err
	}

	tbl := render.Table{
		Headers: []string{"ID", "AGENT_ID", "NAMESPACE", "STATUS", "STARTED"},
	}

	// Stable, sortable ordering by id so output is deterministic.
	sorted := append([]bridge.SessionInfo(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	for _, s := range sorted {
		tbl.Rows = append(tbl.Rows, []string{
			emptyDash(s.ID),
			emptyDash(s.AgentID),
			emptyDash(s.Namespace),
			emptyDash(s.Status),
			emptyDash(s.StartedAt),
		})
	}
	return tbl.Render(out, render.Options{})
}
