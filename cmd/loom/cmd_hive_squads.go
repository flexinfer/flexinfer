// cmd_hive_squads.go implements the `loom hive squads` subcommands. Squads
// are the per-area execution units (hud-frontend, gitops, ...) introduced in
// Hive v2 (see .loom/93-product-spec-hive-v2-hierarchical-swarm-2026-05-02.md
// §"REST + MCP surface"). The Mac CLI is a thin viewport over the operator's
// REST surface, so this file only shapes responses for humans + scripts; the
// canonical squad config + outcomes live in the operator's SQLite store.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// squadsAdminTokenEnv is the env var the slice spec mandates for squads
// admin operations (route-test). We also fall back to LOOM_HIVE_TOKEN for
// backward compatibility with the rest of `loom hive`.
const (
	squadsAdminTokenEnv  = "LOOM_ADMIN_TOKEN"
	squadsLegacyTokenEnv = "LOOM_HIVE_TOKEN"
)

// newHiveSquadsCmd is the parent for the four squads operations:
//   - list       (read)
//   - show       (read)
//   - memory     (read, paginated)
//   - route-test (admin: requires a token)
func newHiveSquadsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "squads",
		Short: "Squads control surface (list, show, memory, route-test)",
		Long: `Squads are the per-area execution units that pick up backlog items.
Read commands fall back to LOOM_HIVE_TOKEN if no admin token is set.
Mutating commands (route-test) require LOOM_ADMIN_TOKEN or --admin-token.`,
	}
	// Persistent admin-token flag is shared by all squads subcommands so
	// users don't have to re-pass it for the route-test step in a chain.
	cmd.PersistentFlags().String("admin-token", "",
		"Admin token override (default: $LOOM_ADMIN_TOKEN, then $LOOM_HIVE_TOKEN)")

	cmd.AddCommand(
		newHiveSquadsListCmd(),
		newHiveSquadsShowCmd(),
		newHiveSquadsMemoryCmd(),
		newHiveSquadsRouteTestCmd(),
	)
	return cmd
}

// resolveSquadsClient is a thin wrapper around resolveHiveClient that lets
// `--admin-token` and $LOOM_ADMIN_TOKEN take precedence over the legacy
// $LOOM_HIVE_TOKEN. Read commands also accept the legacy token (handy for
// users who already export it for council work).
func resolveSquadsClient(cmd *cobra.Command) (*hiveClient, error) {
	client, err := resolveHiveClient(cmd)
	if err != nil {
		return nil, err
	}
	tokenFlag, _ := cmd.Flags().GetString("admin-token")
	tok := strings.TrimSpace(tokenFlag)
	if tok == "" {
		tok = strings.TrimSpace(os.Getenv(squadsAdminTokenEnv))
	}
	if tok == "" {
		// Fall back to the same env the rest of `loom hive` uses; the
		// hiveClient already has it loaded, so respect that.
		tok = client.token
	}
	client.token = tok
	return client, nil
}

// squadSummary is the row shape `loom hive squads list` renders. The spec
// (§"REST + MCP surface") requires name + paths + success rate + in-flight
// + last loaded sha. Field tags mirror the operator's projection. Optional
// fields use omitempty so the same struct stays compatible if the operator
// surfaces additional metadata later.
type squadSummary struct {
	Name          string   `json:"name"`
	Paths         []string `json:"paths"`
	Enabled       bool     `json:"enabled"`
	BudgetShare   float64  `json:"budget_share,omitempty"`
	SuccessRate   float64  `json:"success_rate"`
	InFlight      int      `json:"in_flight"`
	LastLoadedSHA string   `json:"last_loaded_sha,omitempty"`
	UpdatedAt     string   `json:"updated_at,omitempty"`
}

// newHiveSquadsListCmd renders the inventory table. Empty list prints
// "(no squads)" so we never panic on an empty deployment.
func newHiveSquadsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all squads with config + 30-day outcomes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := resolveSquadsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				var raw json.RawMessage
				if err := client.get(ctx, "/api/hive/squads", &raw); err != nil {
					return wrapHiveErr(client, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}

			var squads []squadSummary
			if err := client.get(ctx, "/api/hive/squads", &squads); err != nil {
				return wrapHiveErr(client, err)
			}
			if len(squads) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no squads)")
				return nil
			}
			renderSquadsTable(cmd.OutOrStdout(), squads)
			return nil
		},
	}
}

// renderSquadsTable prints one row per squad using a tabwriter so columns
// align even when squad names or path lists vary widely in length.
func renderSquadsTable(w io.Writer, squads []squadSummary) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPATHS\tSUCCESS\tIN-FLIGHT\tLAST SHA")
	for _, s := range squads {
		paths := strings.Join(s.Paths, ",")
		if paths == "" {
			paths = "—"
		}
		paths = truncate(paths, 40)
		sha := truncate(valueOrDash(s.LastLoadedSHA), 10)
		fmt.Fprintf(tw, "%s\t%s\t%5.1f%%\t%d\t%s\n",
			s.Name, paths, s.SuccessRate*100, s.InFlight, sha)
	}
	_ = tw.Flush()
}

// squadDetail is the full per-squad payload `loom hive squads show <name>`
// renders. Memory + outcomes embed the smaller summary structs so users
// see a one-shot health view without an extra fetch. Use map[string]any
// for the policy/ensemble blobs so the CLI doesn't break when the operator
// adds new keys.
type squadDetail struct {
	squadSummary
	Tests          []string         `json:"tests,omitempty"`
	Gates          map[string]any   `json:"gates,omitempty"`
	Ensemble       map[string]any   `json:"ensemble,omitempty"`
	RecursionOn    bool             `json:"recursion_enabled,omitempty"`
	RecentMemory   []squadMemoryRow `json:"recent_memory,omitempty"`
	RecentOutcomes []squadOutcome   `json:"recent_outcomes,omitempty"`
}

type squadMemoryRow struct {
	ID         int64   `json:"id"`
	Kind       string  `json:"kind"`
	Title      string  `json:"title"`
	Body       string  `json:"body,omitempty"`
	Importance float64 `json:"importance"`
	CreatedAt  string  `json:"created_at"`
	LastSeenAt string  `json:"last_seen_at,omitempty"`
}

type squadOutcome struct {
	PathClass     string  `json:"path_class"`
	PipelineRunID string  `json:"pipeline_run_id"`
	Outcome       string  `json:"outcome"`
	CostUSD       float64 `json:"cost_usd"`
	CreatedAt     string  `json:"created_at"`
}

// newHiveSquadsShowCmd renders one squad's detail block. 404 surfaces as a
// user-readable message that names the squad — much friendlier than the
// raw "operator returned 404" hive client default.
func newHiveSquadsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show full detail for one squad (config, recent memory, outcomes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return errors.New("squad name is required")
			}
			client, err := resolveSquadsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			path := "/api/hive/squads/" + url.PathEscape(name)
			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				var raw json.RawMessage
				if err := client.get(ctx, path, &raw); err != nil {
					return wrapSquadHTTPErr(client, name, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}

			var detail squadDetail
			if err := client.get(ctx, path, &detail); err != nil {
				return wrapSquadHTTPErr(client, name, err)
			}
			renderSquadDetail(cmd.OutOrStdout(), client.baseURL, detail)
			return nil
		},
	}
}

// renderSquadDetail prints a structured human-readable squad summary.
// The blocks are ordered by importance to a human operator: identity,
// config knobs, recent memory (top by importance), recent outcomes.
func renderSquadDetail(w io.Writer, base string, d squadDetail) {
	fmt.Fprintf(w, "Squad %q @ %s\n", d.Name, base)
	state := "enabled"
	if !d.Enabled {
		state = "disabled"
	}
	fmt.Fprintf(w, "  state:           %s\n", state)
	fmt.Fprintf(w, "  paths:           %s\n", valueOrDash(strings.Join(d.Paths, ", ")))
	if len(d.Tests) > 0 {
		fmt.Fprintf(w, "  tests:           %s\n", strings.Join(d.Tests, ", "))
	}
	fmt.Fprintf(w, "  budget share:    %.2f\n", d.BudgetShare)
	fmt.Fprintf(w, "  success (30d):   %.1f%%\n", d.SuccessRate*100)
	fmt.Fprintf(w, "  in-flight:       %d\n", d.InFlight)
	fmt.Fprintf(w, "  recursion:       %t\n", d.RecursionOn)
	fmt.Fprintf(w, "  last loaded sha: %s\n", valueOrDash(d.LastLoadedSHA))
	if len(d.RecentMemory) > 0 {
		fmt.Fprintln(w, "  recent memory:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "    KIND\tIMPORTANCE\tTITLE")
		for _, m := range d.RecentMemory {
			fmt.Fprintf(tw, "    %s\t%.2f\t%s\n",
				m.Kind, m.Importance, truncate(m.Title, 60))
		}
		_ = tw.Flush()
	}
	if len(d.RecentOutcomes) > 0 {
		fmt.Fprintln(w, "  recent outcomes:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "    OUTCOME\tPATH_CLASS\tCOST\tWHEN")
		for _, o := range d.RecentOutcomes {
			fmt.Fprintf(tw, "    %s\t%s\t$%.2f\t%s\n",
				o.Outcome, truncate(o.PathClass, 40), o.CostUSD, o.CreatedAt)
		}
		_ = tw.Flush()
	}
}

// newHiveSquadsMemoryCmd lists working-memory entries for a squad. Sorted
// importance desc by the operator (per spec); the CLI just prints what
// comes back so changes to the ranking don't require a CLI release.
func newHiveSquadsMemoryCmd() *cobra.Command {
	var kind string
	var limit int
	cmd := &cobra.Command{
		Use:   "memory <name>",
		Short: "List recent squad working-memory entries (importance desc)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return errors.New("squad name is required")
			}
			client, err := resolveSquadsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			q := url.Values{}
			if kind != "" {
				q.Set("kind", kind)
			}
			if limit > 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			path := "/api/hive/squads/" + url.PathEscape(name) + "/memory"
			if encoded := q.Encode(); encoded != "" {
				path += "?" + encoded
			}

			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				var raw json.RawMessage
				if err := client.get(ctx, path, &raw); err != nil {
					return wrapSquadHTTPErr(client, name, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}

			var rows []squadMemoryRow
			if err := client.get(ctx, path, &rows); err != nil {
				return wrapSquadHTTPErr(client, name, err)
			}
			if len(rows) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "(no memory entries for %q)\n", name)
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "KIND\tIMPORTANCE\tCREATED\tTITLE")
			for _, m := range rows {
				fmt.Fprintf(tw, "%s\t%.2f\t%s\t%s\n",
					m.Kind, m.Importance, m.CreatedAt, truncate(m.Title, 80))
			}
			_ = tw.Flush()
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "",
		"Filter by entry kind (merge|tech_debt|convention|followup)")
	cmd.Flags().IntVar(&limit, "limit", 20, "Max entries to return")
	return cmd
}

// routeTestResponse intentionally uses a permissive shape — the slice spec
// flagged that the route-test endpoint contract may shift, so we surface
// whatever the operator returns rather than locking the CLI to an exact
// shape. Known fields are populated; unknown fields land in Extras for
// the JSON path or get printed verbatim.
type routeTestResponse struct {
	BacklogID  string  `json:"backlog_id,omitempty"`
	Squad      string  `json:"squad,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Reason     string  `json:"reason,omitempty"`
	Fallback   bool    `json:"fallback,omitempty"`
	// Candidates lets the CLI render the rejected squads + their scores
	// when the operator includes them; the spec calls these out as
	// "scoring trace" output.
	Candidates []struct {
		Name       string  `json:"name"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason,omitempty"`
	} `json:"candidates,omitempty"`
}

// newHiveSquadsRouteTestCmd simulates the routing decision for one
// backlog id without persisting anything. Admin-token gated. The route
// shape isn't yet finalised in main; we POST to the global path the slice
// prompt suggests (`/api/hive/squads/route-test`) and decode best-effort.
func newHiveSquadsRouteTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "route-test <backlog_id>",
		Short: "Simulate squad routing for a backlog id (admin-token required)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backlogID := strings.TrimSpace(args[0])
			if backlogID == "" {
				return errors.New("backlog id is required")
			}
			client, err := resolveSquadsClient(cmd)
			if err != nil {
				return err
			}
			if client.token == "" {
				return fmt.Errorf("route-test requires an admin token; set $%s, $%s, or pass --admin-token",
					squadsAdminTokenEnv, squadsLegacyTokenEnv)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			body := map[string]string{"backlog_id": backlogID}
			path := "/api/hive/squads/route-test"

			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				var raw json.RawMessage
				if err := client.post(ctx, path, body, &raw); err != nil {
					return wrapSquadHTTPErr(client, backlogID, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}

			var res routeTestResponse
			if err := client.post(ctx, path, body, &res); err != nil {
				return wrapSquadHTTPErr(client, backlogID, err)
			}
			renderRouteTest(cmd.OutOrStdout(), backlogID, res)
			return nil
		},
	}
}

// renderRouteTest prints the chosen squad + confidence + reason, then a
// scoring trace if the operator returned one. Empty squad shows up as
// "(unrouted)" rather than a blank line so the reader notices.
func renderRouteTest(w io.Writer, backlogID string, r routeTestResponse) {
	chosen := r.Squad
	if chosen == "" {
		chosen = "(unrouted)"
	}
	fmt.Fprintf(w, "Route test for %s\n", backlogID)
	fmt.Fprintf(w, "  squad:      %s\n", chosen)
	fmt.Fprintf(w, "  confidence: %.2f\n", r.Confidence)
	fmt.Fprintf(w, "  reason:     %s\n", valueOrDash(r.Reason))
	if r.Fallback {
		fmt.Fprintln(w, "  fallback:   yes (no squad cleared min_confidence)")
	}
	if len(r.Candidates) > 0 {
		fmt.Fprintln(w, "  candidates:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "    NAME\tCONFIDENCE\tREASON")
		for _, c := range r.Candidates {
			fmt.Fprintf(tw, "    %s\t%.2f\t%s\n",
				c.Name, c.Confidence, valueOrDash(c.Reason))
		}
		_ = tw.Flush()
	}
}

// wrapSquadHTTPErr decorates per-squad errors with friendly hints. We
// special-case 401/403 (auth surface) and 404 (mistyped name / missing
// squad) because those are the two failure modes a user can fix without
// digging into operator logs.
func wrapSquadHTTPErr(c *hiveClient, subject string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "operator returned 401"):
		return fmt.Errorf("%w\nhint: admin token rejected — check $%s / $%s, or pass --admin-token",
			err, squadsAdminTokenEnv, squadsLegacyTokenEnv)
	case strings.Contains(msg, "operator returned 403"):
		return fmt.Errorf("%w\nhint: token is missing the squads.* policy bindings; ask the operator owner",
			err)
	case strings.Contains(msg, "operator returned 404"):
		return fmt.Errorf("%w\nhint: %q not found — try `loom hive squads list` to see available squads",
			err, subject)
	}
	return wrapHiveErr(c, err)
}
