// cmd_mills_crossrepo.go implements `loom mills cross-repo` subcommands.
// Cross-repo runs coordinate atomic merges across multiple GitLab projects
// for a single backlog item (Mills v2 spec §"Cross-repo coordination flow").
// The Mac CLI is a thin viewport over the operator's REST surface; the
// canonical run state lives in the operator's SQLite store.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// canonicalCrossRepoStates lists the run states the operator emits per spec
// §"Cross-repo coordination flow" (table cross_repo_runs.state). Used to
// validate --state filters before round-tripping.
var canonicalCrossRepoStates = map[string]struct{}{
	"planning":    {},
	"open":        {},
	"gates_green": {},
	"merging":     {},
	"merged":      {},
	"reverted":    {},
	"failed":      {},
}

// crossRepoRunSummary mirrors slice 4.4's REST shape for both list rows and
// the show endpoint. Optional / pointer fields use omitempty so the same
// struct stays compatible if the operator surfaces additional metadata.
type crossRepoRunSummary struct {
	ID                string               `json:"id"`
	BacklogItemID     string               `json:"backlog_item_id"`
	State             string               `json:"state"`
	AtomicityStrategy string               `json:"atomicity_strategy"`
	Repos             []crossRepoRepoEntry `json:"repos"`
	CreatedAt         time.Time            `json:"created_at"`
	UpdatedAt         time.Time            `json:"updated_at"`
}

type crossRepoRepoEntry struct {
	ProjectID  int64  `json:"project_id"`
	RepoName   string `json:"repo_name,omitempty"`
	Branch     string `json:"branch"`
	MRIID      *int64 `json:"mr_iid,omitempty"`
	CIStatus   string `json:"ci_status,omitempty"`
	GateStatus string `json:"gate_status,omitempty"`
}

type crossRepoListResponse struct {
	Runs   []crossRepoRunSummary `json:"runs"`
	Total  int                   `json:"total"`
	Limit  int                   `json:"limit"`
	Filter string                `json:"filter,omitempty"`
}

type crossRepoAbortResponse struct {
	ID            string    `json:"id"`
	State         string    `json:"state"`
	PreviousState string    `json:"previous_state"`
	AbortedAt     time.Time `json:"aborted_at"`
}

// newMillsCrossRepoCmd is the parent for cross-repo run operations:
//   - list  (read)
//   - show  (read)
//   - abort (admin: requires a token)
func newMillsCrossRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cross-repo",
		Aliases: []string{"crossrepo", "xrepo"},
		Short:   "Cross-repo run control surface (list, show, abort)",
		Long: `Cross-repo runs coordinate atomic merges across multiple repos for one backlog item.
Read commands fall back to LOOM_MILLS_TOKEN if no admin token is set.
Abort requires LOOM_ADMIN_TOKEN or --admin-token (admin-gated, marks run failed).`,
	}
	// Persistent admin-token flag mirrors `squads` so users don't have to
	// re-pass it for the abort step in a chain.
	cmd.PersistentFlags().String("admin-token", "",
		"Admin token override (default: $LOOM_ADMIN_TOKEN, then $LOOM_MILLS_TOKEN)")

	cmd.AddCommand(
		newMillsCrossRepoListCmd(),
		newMillsCrossRepoShowCmd(),
		newMillsCrossRepoAbortCmd(),
	)
	return cmd
}

// newMillsCrossRepoListCmd renders the run inventory table. The operator
// supports filtering by state and backlog id; --limit caps response size.
func newMillsCrossRepoListCmd() *cobra.Command {
	var stateFilter, backlogFilter string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cross-repo runs (filter by state or backlog id)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateCrossRepoStates(stateFilter); err != nil {
				return err
			}
			client, err := resolveSquadsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			q := url.Values{}
			if stateFilter != "" {
				q.Set("state", stateFilter)
			}
			if backlogFilter != "" {
				q.Set("backlog_id", backlogFilter)
			}
			if limit > 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			path := "/api/mills/cross-repo/runs"
			if encoded := q.Encode(); encoded != "" {
				path += "?" + encoded
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

			var resp crossRepoListResponse
			if err := client.get(ctx, path, &resp); err != nil {
				return wrapMillsErr(client, err)
			}
			if len(resp.Runs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no cross-repo runs)")
				return nil
			}
			renderCrossRepoTable(cmd.OutOrStdout(), resp.Runs)
			return nil
		},
	}
	cmd.Flags().StringVar(&stateFilter, "state", "",
		"Filter by state (comma-separated): planning|open|gates_green|merging|merged|reverted|failed")
	cmd.Flags().StringVar(&backlogFilter, "backlog-id", "", "Filter by backlog item id")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max runs to return")
	return cmd
}

// validateCrossRepoStates checks every comma-separated entry in s against
// canonicalCrossRepoStates so the CLI fails fast on a typo instead of
// shipping garbage to the operator.
func validateCrossRepoStates(s string) error {
	if s == "" {
		return nil
	}
	for _, part := range strings.Split(s, ",") {
		st := strings.TrimSpace(part)
		if st == "" {
			continue
		}
		if _, ok := canonicalCrossRepoStates[st]; !ok {
			return fmt.Errorf("invalid state %q (allowed: planning|open|gates_green|merging|merged|reverted|failed)", st)
		}
	}
	return nil
}

// renderCrossRepoTable prints one row per run. Repos column collapses to
// "<comma-separated names> (N)" so wide deployments stay readable.
func renderCrossRepoTable(w io.Writer, runs []crossRepoRunSummary) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATE\tID\tBACKLOG\tREPOS\tCREATED")
	for _, r := range runs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			r.State,
			truncate(r.ID, 14),
			truncate(r.BacklogItemID, 22),
			summarizeCrossRepoRepos(r.Repos),
			r.CreatedAt.Format(time.RFC3339),
		)
	}
	_ = tw.Flush()
}

// summarizeCrossRepoRepos renders the repo list as "name1,name2 (N)".
// Falls back to project ids when repo names aren't populated.
func summarizeCrossRepoRepos(repos []crossRepoRepoEntry) string {
	if len(repos) == 0 {
		return "—"
	}
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		switch {
		case r.RepoName != "":
			names = append(names, r.RepoName)
		case r.ProjectID != 0:
			names = append(names, fmt.Sprintf("p%d", r.ProjectID))
		default:
			names = append(names, "?")
		}
	}
	joined := strings.Join(names, ",")
	if len(joined) > 32 {
		joined = joined[:31] + "…"
	}
	return fmt.Sprintf("%s (%d)", joined, len(repos))
}

// newMillsCrossRepoShowCmd renders a single run with its per-repo detail
// table. Backed by GET /api/mills/cross-repo/runs/{id}.
func newMillsCrossRepoShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show full detail for one cross-repo run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if id == "" {
				return errors.New("run id is required")
			}
			client, err := resolveSquadsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			path := "/api/mills/cross-repo/runs/" + url.PathEscape(id)
			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				var raw json.RawMessage
				if err := client.get(ctx, path, &raw); err != nil {
					return wrapCrossRepoHTTPErr(client, id, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}

			var run crossRepoRunSummary
			if err := client.get(ctx, path, &run); err != nil {
				return wrapCrossRepoHTTPErr(client, id, err)
			}
			renderCrossRepoDetail(cmd.OutOrStdout(), client.baseURL, run)
			return nil
		},
	}
}

func renderCrossRepoDetail(w io.Writer, base string, r crossRepoRunSummary) {
	fmt.Fprintf(w, "Cross-repo run %s @ %s\n", r.ID, base)
	fmt.Fprintf(w, "  state:        %s\n", r.State)
	fmt.Fprintf(w, "  backlog:      %s\n", valueOrDash(r.BacklogItemID))
	fmt.Fprintf(w, "  atomicity:    %s\n", valueOrDash(r.AtomicityStrategy))
	fmt.Fprintf(w, "  created:      %s\n", r.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "  updated:      %s\n", r.UpdatedAt.Format(time.RFC3339))

	if len(r.Repos) == 0 {
		fmt.Fprintln(w, "  repos:        (none)")
		return
	}
	fmt.Fprintln(w, "  repos:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "    REPO\tPROJECT\tBRANCH\tMR\tCI")
	for _, rr := range r.Repos {
		mr := "—"
		if rr.MRIID != nil {
			mr = fmt.Sprintf("!%d", *rr.MRIID)
		}
		ci := valueOrDash(rr.CIStatus)
		if rr.GateStatus != "" {
			ci = fmt.Sprintf("%s/%s", ci, rr.GateStatus)
		}
		fmt.Fprintf(tw, "    %s\t%d\t%s\t%s\t%s\n",
			valueOrDash(rr.RepoName),
			rr.ProjectID,
			valueOrDash(rr.Branch),
			mr,
			ci,
		)
	}
	_ = tw.Flush()
}

// newMillsCrossRepoAbortCmd hits POST /api/mills/cross-repo/runs/{id}/abort.
// Admin-token gated; per the spec abort marks the run failed but does NOT
// auto-close per-repo MRs (operator owners follow up manually).
func newMillsCrossRepoAbortCmd() *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:   "abort <id>",
		Short: "Abort a cross-repo run (admin-token required; marks run failed)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if id == "" {
				return errors.New("run id is required")
			}
			client, err := resolveSquadsClient(cmd)
			if err != nil {
				return err
			}
			if client.token == "" {
				return fmt.Errorf("abort requires an admin token; set $%s, $%s, or pass --admin-token",
					squadsAdminTokenEnv, squadsLegacyTokenEnv)
			}
			if !assumeYes {
				ok, err := confirmCrossRepoAbort(cmd.InOrStdin(), cmd.OutOrStdout(), id)
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(cmd.OutOrStdout(), "aborted: cancelled by user")
					return nil
				}
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			path := "/api/mills/cross-repo/runs/" + url.PathEscape(id) + "/abort"
			emitJSON, _ := cmd.Flags().GetBool("json")
			if emitJSON {
				var raw json.RawMessage
				if err := client.post(ctx, path, nil, &raw); err != nil {
					return wrapCrossRepoHTTPErr(client, id, err)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return err
			}

			var resp crossRepoAbortResponse
			if err := client.post(ctx, path, nil, &resp); err != nil {
				return wrapCrossRepoHTTPErr(client, id, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "aborted: %s (%s → %s)\n",
				resp.ID, valueOrDash(resp.PreviousState), valueOrDash(resp.State))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "Skip the interactive confirmation prompt")
	return cmd
}

// confirmCrossRepoAbort prints a destructive-action prompt and only returns
// true on an explicit "y"/"yes". Non-tty stdin without --yes returns false
// so scripted use must opt in via the flag.
func confirmCrossRepoAbort(in io.Reader, out io.Writer, id string) (bool, error) {
	fmt.Fprintf(out, "Abort cross-repo run %s? This marks it failed; per-repo MRs are NOT closed. [y/N]: ", id)
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// wrapCrossRepoHTTPErr decorates per-run errors with friendly hints. We
// special-case 401/403/404/409 because those are the failure modes a user
// can fix without digging into operator logs.
func wrapCrossRepoHTTPErr(c *millsClient, subject string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "operator returned 401"):
		return fmt.Errorf("%w\nhint: admin token rejected — check $%s / $%s, or pass --admin-token",
			err, squadsAdminTokenEnv, squadsLegacyTokenEnv)
	case strings.Contains(msg, "operator returned 403"):
		return fmt.Errorf("%w\nhint: token is missing the cross_repo.* policy bindings; ask the operator owner",
			err)
	case strings.Contains(msg, "operator returned 404"):
		return fmt.Errorf("%w\nhint: %q not found — try `loom mills cross-repo list` to see active runs",
			err, subject)
	case strings.Contains(msg, "operator returned 409"):
		return fmt.Errorf("%w\nhint: run %q is already in a terminal state (merged/reverted/failed) — abort is a no-op",
			err, subject)
	}
	return wrapMillsErr(c, err)
}
