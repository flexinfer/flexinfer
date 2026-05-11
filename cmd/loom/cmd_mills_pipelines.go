package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
	"github.com/spf13/cobra"
)

// newMillsPipelinesCmd is the operator-facing read surface for the pipeline
// reconciler. The autonomous loop runs entirely server-side; without this
// command the only way to see what the reconciler is doing is hitting the
// REST API by hand. Lists + detail only — pause/resume/escalate happen
// via the existing POST endpoints which we'll expose later if needed.
func newMillsPipelinesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pipelines",
		Short:   "Pipeline runs (list active + per-run detail)",
		Aliases: []string{"pipeline"},
	}
	cmd.AddCommand(
		newMillsPipelinesListCmd(),
		newMillsPipelinesGetCmd(),
		newMillsPipelinesCanaryCmd(),
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

type pipelineStartSummary struct {
	RunID     string   `json:"run_id"`
	BacklogID string   `json:"backlog_id"`
	Decision  string   `json:"decision"`
	State     string   `json:"state"`
	Reason    string   `json:"reason,omitempty"`
	Blockers  []string `json:"blockers,omitempty"`
}

// newMillsPipelinesListCmd lists pipeline runs in any non-terminal state.
// The handler-side filter is fixed (queued/planning/.../merging) so the
// CLI doesn't need a --state flag — terminal runs would clutter an
// "active" list.
func newMillsPipelinesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List active pipeline runs (any non-terminal state)",
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
				if err := client.get(ctx, "/api/mills/pipeline/runs", &raw); err != nil {
					return wrapMillsErr(client, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}

			var runs []pipelineRunSummary
			if err := client.get(ctx, "/api/mills/pipeline/runs", &runs); err != nil {
				return wrapMillsErr(client, err)
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

// newMillsPipelinesGetCmd renders one run's full detail: state + stage
// history + gate verdicts. Mirrors the council `runs get` command's
// structure so users build muscle memory.
func newMillsPipelinesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <run-id>",
		Short: "Show one pipeline run with stage history + gate verdicts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveMillsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				var raw json.RawMessage
				if err := client.get(ctx, "/api/mills/pipeline/runs/"+args[0], &raw); err != nil {
					return wrapMillsErr(client, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}

			var d pipelineRunDetail
			if err := client.get(ctx, "/api/mills/pipeline/runs/"+args[0], &d); err != nil {
				return wrapMillsErr(client, err)
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

func newMillsPipelinesCanaryCmd() *cobra.Command {
	var id string
	var priority string
	var path string
	var title string
	cmd := &cobra.Command{
		Use:   "canary",
		Short: "Queue and start the deterministic Mills heartbeat canary",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := resolveMillsClient(cmd)
			if err != nil {
				return err
			}
			if id == "" {
				id = "MILLS-CANARY-" + time.Now().UTC().Format("20060102-150405")
			}
			if title == "" {
				title = "Mills canary: update heartbeat fixture"
			}
			item := millsCanaryBacklogItem(id, title, priority, path)
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
			defer cancel()

			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				var created json.RawMessage
				if err := client.post(ctx, "/api/mills/backlog", item, &created); err != nil {
					return wrapMillsErr(client, err)
				}
				var started json.RawMessage
				startPath := "/api/mills/pipeline/runs/" + url.PathEscape(id) + "/start"
				if err := client.post(ctx, startPath, nil, &started); err != nil {
					return wrapMillsErr(client, err)
				}
				_, err := fmt.Fprintf(cmd.OutOrStdout(), `{"backlog":%s,"start":%s}`+"\n", string(created), string(started))
				return err
			}

			var created backlogItemSummary
			if err := client.post(ctx, "/api/mills/backlog", item, &created); err != nil {
				return wrapMillsErr(client, err)
			}
			var started pipelineStartSummary
			startPath := "/api/mills/pipeline/runs/" + url.PathEscape(id) + "/start"
			if err := client.post(ctx, startPath, nil, &started); err != nil {
				return wrapMillsErr(client, err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Mills canary queued @ %s\n", client.baseURL)
			fmt.Fprintf(out, "  backlog item: %s\n", created.ID)
			fmt.Fprintf(out, "  fixture path: %s\n", path)
			fmt.Fprintf(out, "  start:        %s\n", started.Decision)
			if started.RunID != "" {
				fmt.Fprintf(out, "  pipeline run: %s (%s)\n", started.RunID, started.State)
			}
			if started.Reason != "" {
				fmt.Fprintf(out, "  reason:       %s\n", started.Reason)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Backlog id to use (default: MILLS-CANARY-<utc timestamp>)")
	cmd.Flags().StringVar(&priority, "priority", "P3", "Backlog priority")
	cmd.Flags().StringVar(&path, "path", "testdata/mills-canary/heartbeat.md", "Repo-relative fixture path the canary may touch")
	cmd.Flags().StringVar(&title, "title", "", "Backlog title override")
	return cmd
}

func millsCanaryBacklogItem(id, title, priority, path string) store.BacklogItem {
	if priority == "" {
		priority = "P3"
	}
	if path == "" {
		path = "testdata/mills-canary/heartbeat.md"
	}
	return store.BacklogItem{
		ID:       id,
		Title:    title,
		Labels:   []string{"mills-canary", "safe-fixture"},
		State:    store.BacklogQueued,
		Priority: store.Priority(priority),
		SpecDoc: fmt.Sprintf(
			"Deterministic Mills canary. Update only `%s`: set the visible Run ID to `%s`, keep the file valid Markdown, and do not touch code or generated assets.",
			path, id,
		),
		Success: store.SuccessCriteria{
			Tests: []string{
				"go test ./cmd/loom -run Mills",
			},
			ManualCheck: "The merge request touches only the canary fixture path and reaches green CI before merge.",
		},
		Budget: store.Budget{
			MaxCostUSD:         1,
			MaxTurns:           6,
			MaxPipelineMinutes: 20,
		},
		Slices: []store.Slice{{
			Name:  "heartbeat-fixture",
			Files: []string{path},
			Tests: []string{"go test ./cmd/loom -run Mills"},
		}},
		CreatedBy: "loom mills pipelines canary",
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
