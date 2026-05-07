// cmd_tasks.go implements `loom tasks list`, the unified visibility CLI for
// agent task snapshots (UNIFY-4c, EPIC 2 #66).
//
// The command pulls a snapshot of the HUD's GET /api/tasks endpoint and
// renders either a JSON document or a column-aligned text table via the
// shared cmd/loom/internal/render helpers.
//
// TODO(EPIC 2 follow-up): migrate to internal/visibility/contracts/tasks
// once the scaffold is filled. The canonical TaskInfo currently lives in
// internal/hud/bridge because its UnmarshalJSON depends on bridge-local
// CanonicalProject and *PipelineRef helpers (see contracts/tasks/types.go
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

// tasksListResult is the shape returned by GET /api/tasks.
type tasksListResult struct {
	Tasks []bridge.TaskInfo `json:"tasks"`
}

// tasksFetcher is a seam for tests; production wiring uses the HUD's
// /api/tasks endpoint via hudGet.
type tasksFetcher func(port string) (*tasksListResult, error)

// defaultTasksFetcher invokes GET /api/tasks over the HUD HTTP API.
func defaultTasksFetcher(port string) (*tasksListResult, error) {
	raw, err := hudGet(port, "/api/tasks")
	if err != nil {
		return nil, err
	}
	var result tasksListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse tasks: %w", err)
	}
	return &result, nil
}

func newTasksCmd(_ string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tasks",
		Short: "Inspect agent tasks (id, title, status, agent)",
		Long: `Inspect agent tasks tracked by the daemon-fronted task registry.

Subcommands snapshot the HUD's task list and render either a JSON
document or a text table.`,
	}
	cmd.AddCommand(newTasksListCmd())
	return cmd
}

func newTasksListCmd() *cobra.Command {
	var (
		jsonOutput bool
		filterSpec string
		watch      time.Duration
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List agent tasks",
		Long: `List agent tasks with id, title, status, agent_id, and last update time.

Use --filter status=… to limit rows to a single status (case-insensitive
exact match). Use --json for machine-readable output. Use --watch <duration>
to refresh in place at the given cadence (minimum 1s; values below are
clamped).

Exit codes:
  0  success — HUD reachable and snapshot rendered.
  1  HUD unreachable, RPC error, or invalid filter.`,
		Example: `  loom tasks list
  loom tasks list --json
  loom tasks list --filter status=pending
  loom tasks list --watch 5s`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filter, err := render.ParseFilter(filterSpec)
			if err != nil {
				return err
			}
			port := resolvePort(cmd)
			return runTasksListCommand(cmd.Context(), cmd.OutOrStdout(), port, jsonOutput, filter, watch, defaultTasksFetcher)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit JSON instead of a text table")
	cmd.Flags().StringVar(&filterSpec, "filter", "", "Filter rows by key=value (e.g. status=pending)")
	cmd.Flags().DurationVar(&watch, "watch", 0, "Refresh in place at this cadence (e.g. 5s); 0 disables watch")
	return cmd
}

// runTasksListCommand is the testable entry point.
//
// When watch is non-zero, the command runs render.Watch with a SIGINT-
// cancellable context so Ctrl-C exits cleanly. The minimum honored interval
// is 1s; smaller values are clamped up.
func runTasksListCommand(ctx context.Context, out io.Writer, port string, jsonOutput bool, filter render.Filter, watch time.Duration, fetch tasksFetcher) error {
	if fetch == nil {
		fetch = defaultTasksFetcher
	}

	render1 := func() error {
		snap, err := fetch(port)
		if err != nil {
			return fmt.Errorf("tasks: HUD unreachable: %w", err)
		}
		filtered := filterTasksRows(snap.Tasks, filter)
		if jsonOutput {
			return render.JSON(out, tasksListResult{Tasks: filtered})
		}
		return renderTasksTable(out, filtered)
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

// filterTasksRows applies a render.Filter to the task slice.
//
// Supported filter keys (case-insensitive on key + value): status, agent,
// agent_id, namespace, priority, project. The "agent" alias maps to
// agent_id for ergonomic flag use.
func filterTasksRows(rows []bridge.TaskInfo, filter render.Filter) []bridge.TaskInfo {
	if len(filter) == 0 {
		return rows
	}
	out := make([]bridge.TaskInfo, 0, len(rows))
	for _, t := range rows {
		row := map[string]string{
			"status":    t.Status,
			"agent":     t.AgentID,
			"agent_id":  t.AgentID,
			"namespace": t.Namespace,
			"priority":  t.Priority,
			"project":   t.Project,
		}
		if filter.Match(row) {
			out = append(out, t)
		}
	}
	return out
}

// renderTasksTable writes a text table suitable for terminal viewing.
func renderTasksTable(out io.Writer, rows []bridge.TaskInfo) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(out, "No tasks reported.")
		return err
	}

	tbl := render.Table{
		Headers: []string{"ID", "TITLE", "STATUS", "AGENT_ID", "UPDATED"},
	}

	// Stable, sortable ordering by id so output is deterministic.
	sorted := append([]bridge.TaskInfo(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	for _, t := range sorted {
		tbl.Rows = append(tbl.Rows, []string{
			emptyDash(t.ID),
			emptyDash(t.Title),
			emptyDash(t.Status),
			emptyDash(t.AgentID),
			emptyDash(t.UpdatedAt),
		})
	}
	return tbl.Render(out, render.Options{})
}
