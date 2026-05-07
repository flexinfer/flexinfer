// cmd_rbac_status.go implements `loom rbac status`, a glance-level summary
// of the daemon's RBAC posture (UNIFY-4b, EPIC 2 #66).
//
// The subcommand calls the daemon's loom/rbac-config RPC, adapts the rich
// bridge.RBACConfigResult into the slim internal/visibility/contracts/rbac
// Snapshot type, and renders either a text summary or JSON via the shared
// cmd/loom/internal/render helpers.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/cmd/loom/internal/render"
	"github.com/crb2nu/loom/internal/hud/bridge"
	rbac "github.com/crb2nu/loom/internal/visibility/contracts/rbac"
)

// rbacFetcher is a seam for tests; production uses defaultRBACFetcher.
type rbacFetcher func(socketPath string) (*bridge.RBACConfigResult, error)

func defaultRBACFetcher(socketPath string) (*bridge.RBACConfigResult, error) {
	raw, err := call(socketPath, "loom/rbac-config", nil)
	if err != nil {
		return nil, err
	}
	var result bridge.RBACConfigResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse rbac-config: %w", err)
	}
	return &result, nil
}

// newRBACStatusCmd returns the `loom rbac status` subcommand. It is wired
// into the existing rbac parent command from cmd_rbac.go via init().
func newRBACStatusCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show a glance-level RBAC posture summary",
		Long: `Show a glance-level summary of the daemon's RBAC posture: audit state,
recent denial counts, and the most recent denied tool calls.

Exit codes:
  0  success — daemon reachable and snapshot rendered.
  1  daemon unreachable, RPC error, or RBAC degraded.`,
		Example: `  loom rbac status
  loom rbac status --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			socket := resolveSocketPath(cmd)
			return runRBACStatusCommand(cmd.OutOrStdout(), socket, jsonOutput, defaultRBACFetcher)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit JSON instead of a text summary")
	return cmd
}

// runRBACStatusCommand is the testable entry point.
//
// Returns a non-nil error when the daemon RPC fails, surfacing via cobra as
// a non-zero exit code per UNIFY-3 ergonomics (matches `loom status`).
func runRBACStatusCommand(out io.Writer, socketPath string, jsonOutput bool, fetch rbacFetcher) error {
	if fetch == nil {
		fetch = defaultRBACFetcher
	}
	cfg, err := fetch(socketPath)
	if err != nil {
		return fmt.Errorf("rbac status: daemon unreachable: %w", err)
	}
	snap := adaptRBACConfigToSnapshot(cfg)

	if jsonOutput {
		return render.JSON(out, snap)
	}
	return renderRBACSnapshot(out, snap)
}

// adaptRBACConfigToSnapshot lifts the daemon's RBACConfigResult shape into
// the slim Snapshot DTO that lives in internal/visibility/contracts/rbac.
//
// Fields not yet exposed by the daemon (PolicyVersion, SimulationMode) are
// left zero-valued; future EPIC 2 slices will fill them once the daemon
// surfaces those fields. See contracts/rbac/types.go for the canonical shape.
func adaptRBACConfigToSnapshot(cfg *bridge.RBACConfigResult) rbac.Snapshot {
	if cfg == nil {
		return rbac.Snapshot{}
	}
	out := rbac.Snapshot{
		AuditEnabled:   cfg.AuditEnabled,
		DeniedCount24h: cfg.DeniedCount,
	}
	for _, d := range cfg.RecentDenied {
		out.RecentDenials = append(out.RecentDenials, rbac.Denial{
			Time:     parseRBACTimestamp(d.Timestamp),
			Actor:    rbacActor(d),
			Resource: rbacResource(d),
			Reason:   d.Reason,
		})
	}
	return out
}

// parseRBACTimestamp parses an RFC3339 string and returns the zero Time on
// any failure so the JSON output stays stable.
func parseRBACTimestamp(ts string) time.Time {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// rbacActor renders the actor field from the denial entry's agent identity,
// preferring agent_id and falling back to role when no id is recorded.
func rbacActor(d bridge.RBACDeniedEntry) string {
	if strings.TrimSpace(d.AgentID) != "" {
		return d.AgentID
	}
	if strings.TrimSpace(d.Role) != "" {
		return "role:" + d.Role
	}
	return "unknown"
}

// rbacResource renders the resource as server__tool to match the daemon's
// internal naming convention used elsewhere in the CLI.
func rbacResource(d bridge.RBACDeniedEntry) string {
	srv := strings.TrimSpace(d.Server)
	tool := strings.TrimSpace(d.Tool)
	switch {
	case srv != "" && tool != "":
		return srv + "__" + tool
	case srv != "":
		return srv
	case tool != "":
		return tool
	default:
		return "unknown"
	}
}

// renderRBACSnapshot writes a human-friendly summary to out.
func renderRBACSnapshot(out io.Writer, snap rbac.Snapshot) error {
	if _, err := fmt.Fprintln(out, "RBAC posture"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "  audit_enabled:    %t\n", snap.AuditEnabled); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "  simulation_mode:  %t\n", snap.SimulationMode); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "  denied_count_24h: %d\n", snap.DeniedCount24h); err != nil {
		return err
	}
	if v := strings.TrimSpace(snap.PolicyVersion); v != "" {
		if _, err := fmt.Fprintf(out, "  policy_version:   %s\n", v); err != nil {
			return err
		}
	}

	if len(snap.RecentDenials) == 0 {
		_, err := fmt.Fprintln(out, "Recent denials: none")
		return err
	}

	if _, err := fmt.Fprintln(out, "Recent denials:"); err != nil {
		return err
	}
	tbl := render.Table{
		Headers: []string{"TIME", "ACTOR", "RESOURCE", "REASON"},
	}
	for _, d := range snap.RecentDenials {
		tbl.Rows = append(tbl.Rows, []string{
			rbacFormatTime(d.Time),
			d.Actor,
			d.Resource,
			d.Reason,
		})
	}
	return tbl.Render(out, render.Options{})
}

// rbacFormatTime renders Time as RFC3339 in UTC, or "-" when zero.
func rbacFormatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}
