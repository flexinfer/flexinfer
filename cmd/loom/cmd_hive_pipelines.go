package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newHivePipelinesCmd is the operator-facing read surface for the pipeline
// reconciler. The autonomous loop runs entirely server-side; without this
// command the only way to see what the reconciler is doing is hitting the
// REST API by hand. Lists + detail only — pause/resume/escalate happen
// via the existing POST endpoints which we'll expose later if needed.
func newHivePipelinesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pipelines",
		Short:   "Pipeline runs (list active + per-run detail)",
		Aliases: []string{"pipeline"},
	}
	cmd.AddCommand(
		newHivePipelinesListCmd(),
		newHivePipelinesGetCmd(),
	)
	return cmd
}

// pipelineRunSummary is the subset of store.PipelineRun the CLI table
// renders. Field tags match the json the operator's handler emits via
// json.Marshal of *store.PipelineRun (capitalised).
type pipelineRunSummary struct {
	ID           string  `json:"ID"`
	BacklogID    string  `json:"BacklogID"`
	State        string  `json:"State"`
	CurrentStage string  `json:"CurrentStage"`
	Attempts     int     `json:"Attempts"`
	MRIID        *int64  `json:"MRIID"`
	CostUSD      float64 `json:"CostUSD"`
	StartedAt    string  `json:"StartedAt"`
	EndedAt      *string `json:"EndedAt"`
}

// newHivePipelinesListCmd lists pipeline runs in any non-terminal state.
// The handler-side filter is fixed (queued/planning/.../merging) so the
// CLI doesn't need a --state flag — terminal runs would clutter an
// "active" list.
func newHivePipelinesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List active pipeline runs (any non-terminal state)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := resolveHiveClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				var raw json.RawMessage
				if err := client.get(ctx, "/api/hive/pipeline/runs", &raw); err != nil {
					return wrapHiveErr(client, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}

			var runs []pipelineRunSummary
			if err := client.get(ctx, "/api/hive/pipeline/runs", &runs); err != nil {
				return wrapHiveErr(client, err)
			}
			if len(runs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no active pipeline runs)")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-32s %-14s %-15s %-18s %-7s %-10s %s\n",
				"RUN ID", "BACKLOG", "STATE", "STAGE", "ATT", "COST", "STARTED")
			for _, r := range runs {
				mr := "-"
				if r.MRIID != nil {
					mr = fmt.Sprintf("!%d", *r.MRIID)
				}
				stage := r.CurrentStage
				if stage == "" {
					stage = "-"
				}
				_ = mr
				fmt.Fprintf(cmd.OutOrStdout(), "%-32s %-14s %-15s %-18s %-7d $%-9.2f %s\n",
					truncate(r.ID, 32), truncate(r.BacklogID, 14),
					r.State, truncate(stage, 18), r.Attempts, r.CostUSD, r.StartedAt)
			}
			return nil
		},
	}
}

// pipelineRunDetail mirrors the operator's GetRun handler shape — a
// PipelineRun with stage_results + gate_outcomes inlined so the CLI can
// render a one-call detail view.
type pipelineRunDetail struct {
	pipelineRunSummary
	Stages []pipelineStageRow `json:"Stages"`
	Gates  []pipelineGateRow  `json:"Gates"`
}

type pipelineStageRow struct {
	Stage     string  `json:"Stage"`
	Attempt   int     `json:"Attempt"`
	Outcome   *string `json:"Outcome"`
	CostUSD   float64 `json:"CostUSD"`
	StartedAt string  `json:"StartedAt"`
	EndedAt   *string `json:"EndedAt"`
	LogTail   string  `json:"LogTail,omitempty"`
}

type pipelineGateRow struct {
	GateName    string   `json:"GateName"`
	AfterStage  string   `json:"AfterStage"`
	Outcome     string   `json:"Outcome"`
	JudgedBy    string   `json:"JudgedBy"`
	Reasons     []string `json:"Reasons,omitempty"`
	EvaluatedAt string   `json:"EvaluatedAt"`
}

// newHivePipelinesGetCmd renders one run's full detail: state + stage
// history + gate verdicts. Mirrors the council `runs get` command's
// structure so users build muscle memory.
func newHivePipelinesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <run-id>",
		Short: "Show one pipeline run with stage history + gate verdicts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveHiveClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				var raw json.RawMessage
				if err := client.get(ctx, "/api/hive/pipeline/runs/"+args[0], &raw); err != nil {
					return wrapHiveErr(client, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}

			var d pipelineRunDetail
			if err := client.get(ctx, "/api/hive/pipeline/runs/"+args[0], &d); err != nil {
				return wrapHiveErr(client, err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Pipeline run: %s\n", d.ID)
			fmt.Fprintf(out, "  backlog item:   %s\n", d.BacklogID)
			fmt.Fprintf(out, "  state:          %s\n", d.State)
			if d.CurrentStage != "" {
				fmt.Fprintf(out, "  current stage:  %s\n", d.CurrentStage)
			}
			fmt.Fprintf(out, "  attempts:       %d\n", d.Attempts)
			if d.MRIID != nil {
				fmt.Fprintf(out, "  merge request:  !%d\n", *d.MRIID)
			}
			fmt.Fprintf(out, "  total cost:     $%.2f\n", d.CostUSD)
			fmt.Fprintf(out, "  started:        %s\n", d.StartedAt)
			if d.EndedAt != nil {
				fmt.Fprintf(out, "  ended:          %s\n", *d.EndedAt)
			}

			if len(d.Stages) > 0 {
				fmt.Fprintln(out, "\nStage history:")
				fmt.Fprintf(out, "  %-22s %-3s %-9s %-10s %s\n",
					"STAGE", "ATT", "OUTCOME", "COST", "STARTED")
				for _, s := range d.Stages {
					outcome := "-"
					if s.Outcome != nil {
						outcome = *s.Outcome
					}
					fmt.Fprintf(out, "  %-22s %-3d %-9s $%-9.2f %s\n",
						truncate(s.Stage, 22), s.Attempt, outcome, s.CostUSD, s.StartedAt)
				}
			}

			if len(d.Gates) > 0 {
				fmt.Fprintln(out, "\nGate verdicts:")
				for _, g := range d.Gates {
					fmt.Fprintf(out, "  %s after %s: %s (%s)",
						g.GateName, g.AfterStage, g.Outcome, g.JudgedBy)
					if len(g.Reasons) > 0 {
						fmt.Fprintf(out, " — %s", strings.Join(g.Reasons, "; "))
					}
					fmt.Fprintln(out)
				}
			}
			return nil
		},
	}
}

// truncate clips s to at most n chars; longer strings are suffixed with
// an ellipsis to keep table columns aligned.
func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
