// cmd_mills_council.go implements the `loom mills {council,backlog,eval}`
// subcommands. Mirrors the operator's REST surface so the Mac CLI is a
// thin viewport over the cluster's truth — no business logic here, just
// shaping responses for humans + scripts.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ----- council -----

func newMillsCouncilCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "council",
		Short: "Council planning tier (run, dryrun, list runs)",
	}
	cmd.AddCommand(
		newMillsCouncilRunCmd(false),
		newMillsCouncilRunCmd(true),
		newMillsCouncilRunsCmd(),
	)
	return cmd
}

// newMillsCouncilRunCmd returns either `run` or `dryrun` depending on the
// dryrun flag — the bodies are identical except for the route they hit
// and the human-readable summary they emit.
func newMillsCouncilRunCmd(dryrun bool) *cobra.Command {
	use := "run"
	short := "Trigger a council planning pass (admin token required)"
	path := "/api/mills/council/run"
	if dryrun {
		use = "dryrun"
		short = "Preview a council pass without persisting (admin token required)"
		path = "/api/mills/council/dryrun"
	}
	var reason string
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := resolveMillsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
			defer cancel()

			emitJSON, _ := cmd.Flags().GetBool("json")
			body := map[string]string{"trigger": "manual", "reason": reason}

			if emitJSON {
				var raw json.RawMessage
				if err := client.post(ctx, path, body, &raw); err != nil {
					return wrapMillsErr(client, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}

			var res councilRunResponse
			if err := client.post(ctx, path, body, &res); err != nil {
				return wrapMillsErr(client, err)
			}
			renderCouncilRun(cmd.OutOrStdout(), client.baseURL, dryrun, res)
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "manual via CLI",
		"Free-form reason logged into council_runs.notes")
	return cmd
}

// councilRunResponse mirrors the operator's projection. Tag names match
// the JSON keys handlers_council.go emits.
type councilRunResponse struct {
	RunID             string   `json:"run_id"`
	Dryrun            bool     `json:"dryrun"`
	StartedAt         string   `json:"started_at"`
	EndedAt           string   `json:"ended_at"`
	CostUSDApprox     float64  `json:"cost_usd_approx"`
	Score             float64  `json:"score"`
	Partial           bool     `json:"partial"`
	JudgedBy          string   `json:"judged_by"`
	BacklogProposed   int      `json:"backlog_proposed"`
	BacklogCreated    []string `json:"backlog_created"`
	BacklogTruncated  int      `json:"backlog_truncated"`
	BacklogSkipped    bool     `json:"backlog_skipped"`
	BacklogSkipReason string   `json:"backlog_skip_reason"`
	Artifacts         []struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
		ID   string `json:"id"`
	} `json:"artifacts"`
	SidecarPath string `json:"sidecar_path"`
}

func renderCouncilRun(w interface{ Write([]byte) (int, error) }, base string, dryrun bool, r councilRunResponse) {
	mode := "run"
	if dryrun {
		mode = "dryrun"
	}
	verdict := "pass"
	if r.Partial {
		verdict = "PARTIAL"
	}
	created := strings.Join(r.BacklogCreated, ", ")
	if created == "" {
		created = "—"
	}
	fmt.Fprintf(w, "Loom Mills council %s @ %s\n", mode, base)
	fmt.Fprintf(w, "  run id:           %s\n", r.RunID)
	fmt.Fprintf(w, "  judge:            %s (%.2f) %s\n", r.JudgedBy, r.Score, verdict)
	fmt.Fprintf(w, "  cost:             $%.2f\n", r.CostUSDApprox)
	fmt.Fprintf(w, "  artifacts:        %d (sidecar: %s)\n", len(r.Artifacts), valueOrDash(r.SidecarPath))
	fmt.Fprintf(w, "  backlog created:  %s (proposed=%d truncated=%d)\n",
		created, r.BacklogProposed, r.BacklogTruncated)
	if r.BacklogSkipped {
		fmt.Fprintf(w, "  backlog skipped:  %s\n", r.BacklogSkipReason)
	}
	for _, a := range r.Artifacts {
		fmt.Fprintf(w, "    - %s: %s\n", a.Kind, a.Path)
	}
}

func valueOrDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// newMillsCouncilRunsCmd lists past council runs (read-only).
func newMillsCouncilRunsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "runs",
		Short: "List recent council runs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := resolveMillsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				var raw json.RawMessage
				if err := client.get(ctx, "/api/mills/council/runs", &raw); err != nil {
					return wrapMillsErr(client, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}

			var runs []councilRunSummary
			if err := client.get(ctx, "/api/mills/council/runs", &runs); err != nil {
				return wrapMillsErr(client, err)
			}
			if len(runs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no council runs yet)")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-32s %-10s %-9s %-10s %s\n",
				"RUN ID", "TRIGGER", "OUTCOME", "COST_USD", "STARTED")
			for _, r := range runs {
				fmt.Fprintf(cmd.OutOrStdout(), "%-32s %-10s %-9s $%-9.2f %s\n",
					r.ID, r.Trigger, r.Outcome,
					r.CostFrontierUSD+r.CostLocalUSD, r.StartedAt)
			}
			return nil
		},
	}
}

// councilRunSummary is the subset of store.CouncilRun the CLI displays.
// Tag names match the JSON the DAO emits via the handler.
type councilRunSummary struct {
	ID              string  `json:"ID"`
	Trigger         string  `json:"Trigger"`
	Outcome         string  `json:"Outcome"`
	CostFrontierUSD float64 `json:"CostFrontierUSD"`
	CostLocalUSD    float64 `json:"CostLocalUSD"`
	StartedAt       string  `json:"StartedAt"`
}

// ----- backlog -----

func newMillsBacklogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backlog",
		Short: "Backlog snapshot (read-only list + per-item detail)",
	}
	cmd.AddCommand(
		newMillsBacklogListCmd(),
		newMillsBacklogGetCmd(),
	)
	return cmd
}

func newMillsBacklogListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every backlog item (newest first by updated_at)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := resolveMillsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				var raw json.RawMessage
				if err := client.get(ctx, "/api/mills/backlog", &raw); err != nil {
					return wrapMillsErr(client, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}
			var items []backlogItemSummary
			if err := client.get(ctx, "/api/mills/backlog", &items); err != nil {
				return wrapMillsErr(client, err)
			}
			if len(items) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(backlog empty)")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-26s %-10s %-3s %s\n",
				"ID", "STATE", "P", "TITLE")
			for _, it := range items {
				fmt.Fprintf(cmd.OutOrStdout(), "%-26s %-10s %-3s %s\n",
					it.ID, it.State, it.Priority, it.Title)
			}
			return nil
		},
	}
}

func newMillsBacklogGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Print one backlog item as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveMillsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			var raw json.RawMessage
			if err := client.get(ctx, "/api/mills/backlog/"+args[0], &raw); err != nil {
				return wrapMillsErr(client, err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(raw))
			return err
		},
	}
}

type backlogItemSummary struct {
	ID       string `json:"ID"`
	Title    string `json:"Title"`
	State    string `json:"State"`
	Priority string `json:"Priority"`
}

// ----- eval -----

func newMillsEvalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Eval scores (Loop A artifact judge, Loop B per-merge attribution)",
	}
	cmd.AddCommand(newMillsEvalListCmd())
	return cmd
}

func newMillsEvalListCmd() *cobra.Command {
	var since string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent eval scores (default: last 7 days)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := resolveMillsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			path := "/api/mills/eval/scores"
			if since != "" {
				path += "?since=" + since
			}
			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				var raw json.RawMessage
				if err := client.get(ctx, path, &raw); err != nil {
					return wrapMillsErr(client, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}
			var scores []evalScoreSummary
			if err := client.get(ctx, path, &scores); err != nil {
				return wrapMillsErr(client, err)
			}
			if len(scores) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no eval scores yet)")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-22s %-13s %-6s %-12s %s\n",
				"SUBJECT", "KIND", "SCORE", "RUBRIC", "EVALUATED")
			for _, s := range scores {
				fmt.Fprintf(cmd.OutOrStdout(), "%-22s %-13s %-6.2f %-12s %s\n",
					s.SubjectID, s.SubjectKind, s.Score, s.Rubric, s.EvaluatedAt)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "RFC3339 cutoff (default: last 7 days)")
	return cmd
}

type evalScoreSummary struct {
	SubjectID   string  `json:"SubjectID"`
	SubjectKind string  `json:"SubjectKind"`
	Rubric      string  `json:"Rubric"`
	Score       float64 `json:"Score"`
	EvaluatedAt string  `json:"EvaluatedAt"`
}
