// Package runner is the operator-side wiring that stitches every
// council component (brief → reviewers → editor → artifacts → judge →
// mutator) into one end-to-end Run() call. Lives in its own package
// so pkg/hive/council and pkg/hive/eval can stay free of each other's
// imports (eval already depends on council; the runner depends on both
// + store + the policy manager, which would create a cycle if it
// lived in either).
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/hive"
	"github.com/crb2nu/loom/pkg/hive/council"
	"github.com/crb2nu/loom/pkg/hive/eval"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// Runner stitches the slice 3.1–3.6 components into a single end-to-end
// council pass: roadmap extract → brief → reviewers → editor →
// artifacts → judge → mutator. It is the surface the operator's REST
// handlers + scheduler trigger; the FakeReviewer + FakeEditor make the
// dryrun path exercise every step without a live agent.
//
// Wiring is dependency-injected so production swaps in the spawn-backed
// reviewer + editor without touching this file.
type Runner struct {
	Store     *store.Store
	Policy    *hive.PolicyManager
	Budget    *hive.Budget
	Reviewers *council.Dispatcher
	Editor    council.Editor
	// Moderator is the optional Phase 5 dependency wired only when
	// `policy.debate.enabled.<trigger>` is true. When nil (the v1
	// default) the runner skips the debate path even if the policy
	// flag is set, falling back to single-pass and logging the
	// degradation. Production wires a frontier-model moderator.
	Moderator council.Moderator
	Writer    *council.ArtifactWriter
	Mutator   *council.BacklogMutator
	Judge     *eval.Judge
	RepoRoot  string
	Logger    *slog.Logger

	// Now is injectable for deterministic IDs in tests + dryrun. Defaults
	// to time.Now.
	Now func() time.Time

	// OnArtifactsCommitted fires after a non-dryrun council run has
	// successfully persisted its artifacts + verdict + (optionally)
	// backlog deltas. The audit Triggers wire here to enqueue an
	// adversarial review against the freshly-committed artifact set.
	// Errors are logged but do NOT roll back the run.
	OnArtifactsCommitted func(ctx context.Context, run *store.CouncilRun, refs []store.ArtifactRef)
}

// RunInput tunes one Run() invocation.
type RunInput struct {
	// Trigger identifies what fired the run; surfaced into council_runs.
	Trigger store.CouncilTrigger
	// Dryrun makes the run write artifacts to a scratch dir under
	// RepoRoot/.loom/dryrun/<runID>/ instead of .loom/, skip backlog
	// mutation, and return the populated RunResult so the caller can
	// inspect what *would* have happened.
	Dryrun bool
	// Reason is a free-form note (e.g. "manual via CLI") logged into
	// council_runs.notes.
	Reason string
}

// RunResult is the audit footprint of one Run call.
type RunResult struct {
	RunID         string
	Brief         *council.Brief
	Reviews       []council.ReviewerOutput
	Editor        *council.EditorOutput
	Write         *council.WriteResult
	Verdict       *eval.Verdict
	Mutation      *council.MutationResult
	Dryrun        bool
	StartedAt     time.Time
	EndedAt       time.Time
	CostUSDApprox float64
}

// Run executes a council pass end to end. Errors fall into two buckets:
//   - infrastructure: brief/reviewers/editor/writer/judge couldn't run
//     at all → return non-nil error, the operator retries.
//   - quality: judge marked the run partial → run completes, mutations
//     are skipped, RunResult.Verdict.Partial is true, error is nil.
func (r *Runner) Run(ctx context.Context, in RunInput) (*RunResult, error) {
	if r == nil || r.Store == nil || r.Policy == nil || r.Editor == nil || r.Writer == nil {
		return nil, errors.New("council runner not configured")
	}
	if r.Reviewers == nil {
		return nil, errors.New("council runner: reviewer dispatcher required")
	}
	if r.Judge == nil {
		return nil, errors.New("council runner: judge required")
	}
	if r.Mutator == nil {
		return nil, errors.New("council runner: backlog mutator required")
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	policy := r.Policy.Current()
	if !policy.IsEnabled() {
		return nil, errors.New("council runner: policy disabled")
	}

	res := &RunResult{
		RunID:     newCouncilRunID(now),
		Dryrun:    in.Dryrun,
		StartedAt: now,
	}
	r.logf("council run starting", "run_id", res.RunID, "trigger", in.Trigger, "dryrun", in.Dryrun)

	// ----- Brief -----
	brief, err := council.Compile(ctx, council.BriefSources{Store: r.Store, RepoRoot: r.RepoRoot, Now: r.Now})
	if err != nil {
		return res, fmt.Errorf("brief: %w", err)
	}
	res.Brief = brief

	// ----- Reviewers + Editor -----
	// Hive v2 Phase 5: when policy.Debate enables this trigger, the
	// reviewers + editor run as a multi-round debate via
	// council.Debate. Otherwise we follow the v1 single-pass path
	// (parallel reviewers → editor.Edit). Both paths converge on the
	// same EditorOutput shape so the artifact / judge / mutator
	// stages downstream are unchanged.
	lenses := council.LensesFromPolicy(policy)
	reviews := []council.ReviewerOutput(nil)
	var out *council.EditorOutput
	debateEnabledByPolicy := len(lenses) > 0 && policy.Debate.Enabled.AllowedFor(string(in.Trigger))
	debateEngaged := debateEnabledByPolicy && r.Moderator != nil
	if debateEnabledByPolicy && !debateEngaged {
		r.logf("debate skipped: no moderator wired",
			"run_id", res.RunID, "trigger", in.Trigger)
	}
	if debateEngaged {
		debate := &council.Debate{
			Editor:    r.Editor,
			Reviewers: r.Reviewers,
			Lenses:    lenses,
			Moderator: r.Moderator,
			Now:       r.Now,
		}
		dres, derr := debate.Run(ctx, council.DebateInput{
			Brief:              brief,
			MaxUSD:             policy.Debate.MaxUSD,
			MaxRounds:          policy.Debate.MaxRounds,
			EarlyExitThreshold: policy.Debate.EarlyExitThreshold,
			PerReviewerTimeout: 30 * time.Second,
			MinQuorum:          (len(lenses) + 1) / 2,
		})
		if derr != nil {
			return res, fmt.Errorf("debate: %w", derr)
		}
		out = dres.Editor
		reviews = dres.Reviews
		res.CostUSDApprox = dres.TotalCostUSD
		// Stamp the per-round transcript onto the sidecar so the
		// artifact writer + audit pool consume the same shape.
		out.Sidecar.Debate = &council.SidecarDebate{
			Enabled:         true,
			Rounds:          dres.Rounds,
			EarlyExitReason: dres.EarlyExitReason,
			TotalCostUSD:    dres.TotalCostUSD,
		}
		// Roll the full debate spend (editor.propose + every
		// reviewer.critique + every moderator.assess + every
		// editor.revise) into the run's CostUSD so the artifact
		// writer's downstream stamp into council_runs.cost_*_usd
		// reflects the *full* debate cost — not just the editor's
		// final-revise cost (which is what out.CostUSD alone holds).
		// Without this, CouncilDAO.SumCostSince undercounts debate
		// runs and the daily council cap can overshoot.
		//
		// All debate spend is attributed to the Frontier bucket in
		// slice 5.2; per-round backend tracking lands in slice 5.3
		// alongside the HUD panel and refines the split.
		out.Sidecar.CostUSD = council.SidecarCost{
			Frontier: dres.TotalCostUSD,
			Local:    0,
		}
		r.logf("debate complete",
			"run_id", res.RunID,
			"trigger", in.Trigger,
			"rounds", len(dres.Rounds),
			"early_exit_reason", dres.EarlyExitReason,
			"cost_usd", dres.TotalCostUSD,
		)
	} else {
		if len(lenses) > 0 {
			reviews, err = r.Reviewers.Dispatch(ctx, brief, lenses, council.DispatchOptions{
				PerReviewerTimeout: 30 * time.Second,
				MinQuorum:          (len(lenses) + 1) / 2, // simple majority by default
			})
			if err != nil {
				r.logf("reviewer quorum failure", "run_id", res.RunID, "error", err)
				// Continue — the editor can still produce
				// something, but the judge will likely score
				// this run partial.
			}
		}
		out, err = r.Editor.Edit(ctx, brief, reviews)
		if err != nil {
			return res, fmt.Errorf("editor: %w", err)
		}
		res.CostUSDApprox = out.CostUSD
		for _, rv := range reviews {
			res.CostUSDApprox += rv.CostUSD
		}
	}
	res.Reviews = reviews
	res.Editor = out

	// ----- Artifacts -----
	writer := r.Writer
	if in.Dryrun && writer != nil {
		// Re-target writer at a per-run scratch dir so dryrun doesn't
		// pollute the canonical .loom/ directory. The mkdir is best-
		// effort; failure surfaces as a write error below.
		dryDir := filepath.Join(r.RepoRoot, ".loom", "dryrun", res.RunID, ".loom")
		if err := mkdirAll(dryDir, 0o755); err != nil {
			return res, fmt.Errorf("dryrun mkdir: %w", err)
		}
		writer = &council.ArtifactWriter{
			RepoRoot: filepath.Join(r.RepoRoot, ".loom", "dryrun", res.RunID),
			Now:      r.Now,
		}
	}
	wr, err := writer.Write(ctx, res.RunID, out)
	if err != nil {
		return res, fmt.Errorf("artifacts: %w", err)
	}
	res.Write = wr

	// ----- Judge (Loop A) -----
	verdict, err := r.Judge.Run(ctx, eval.Input{
		Sidecar:      &out.Sidecar,
		WriteResult:  wr,
		EditorOutput: out,
		Store:        r.Store,
		Now:          r.Now,
	})
	if err != nil {
		return res, fmt.Errorf("eval: %w", err)
	}
	res.Verdict = verdict

	// ----- Persist council run -----
	wr.Run.Trigger = in.Trigger
	wr.Run.Notes = strings.TrimSpace(in.Reason)
	if verdict.Partial {
		wr.Run.Outcome = store.CouncilOutcomePartial
	}
	if !in.Dryrun {
		if err := r.Store.Council.Put(ctx, wr.Run); err != nil {
			return res, fmt.Errorf("persist council run: %w", err)
		}
		if err := verdict.PersistTo(ctx, r.Store.Eval, res.RunID); err != nil {
			r.logf("persist eval verdict failed", "run_id", res.RunID, "error", err)
		}
		// Persist debate transcript rows after the council run row
		// exists (FK on council_run_id). Slice 5.2: skip in dryrun
		// to keep .loom/dryrun runs zero-side-effect; best-effort
		// logging on per-row error so a single failure doesn't
		// unwind the whole run (the run already succeeded — its
		// artifacts are already on disk).
		if out.Sidecar.Debate != nil && len(out.Sidecar.Debate.Rounds) > 0 {
			persistDebateTranscript(ctx, r.Store.Debate, res.RunID, out.Sidecar.Debate.Rounds, r.now, r.logf)
		}
	}

	// ----- Mutator -----
	// Dryrun synthesises a no-op mutation result so the audit log
	// records the intent without writing to the canonical store. We
	// can't go through Apply() with SkipBecausePartial because the
	// council run row also wasn't persisted, and BacklogItem's FK on
	// council_run_id would fail.
	var mutation *council.MutationResult
	if in.Dryrun {
		mutation = &council.MutationResult{
			TotalProposed: len(out.BacklogProposals),
			Skipped:       true,
			SkipReason:    "dryrun",
		}
	} else {
		mutation, err = r.Mutator.Apply(ctx, res.RunID, out, council.MutationOptions{
			// 0 → mutator default (10) per .loom/89- §10.x.
			SkipBecausePartial: verdict.Partial,
			RepoRoot:           r.RepoRoot,
		})
		if err != nil {
			return res, fmt.Errorf("backlog mutator: %w", err)
		}
	}
	res.Mutation = mutation

	// Refresh BacklogDeltas on the persisted run row.
	if !in.Dryrun && len(mutation.CreatedItems) > 0 {
		wr.Run.BacklogDeltas.Created = mutation.CreatedIDs()
		end := r.now()
		wr.Run.EndedAt = &end
		if err := r.Store.Council.Put(ctx, wr.Run); err != nil {
			r.logf("re-persist council run with deltas failed", "run_id", res.RunID, "error", err)
		}
	}

	res.EndedAt = r.now()
	if !in.Dryrun {
		trigger := string(in.Trigger)
		outcome := string(wr.Run.Outcome)
		hive.CouncilRunsTotal.WithLabelValues(trigger, outcome).Inc()
		hive.CouncilCostUSDTotal.WithLabelValues(trigger).Add(res.CostUSDApprox)
		hive.CouncilDurationSeconds.WithLabelValues(trigger).Observe(res.EndedAt.Sub(res.StartedAt).Seconds())
		// Fire the post-commit hook so the audit Triggers can enqueue
		// an adversarial review of the freshly-committed artifact set.
		// Best-effort: hook errors are not surfaced (the run succeeded
		// regardless), so we wrap in a safe call that logs internally.
		if r.OnArtifactsCommitted != nil {
			r.OnArtifactsCommitted(ctx, wr.Run, wr.ArtifactRefs)
		}
	}
	r.logf("council run complete",
		"run_id", res.RunID,
		"score", verdict.Score,
		"partial", verdict.Partial,
		"created", len(mutation.CreatedItems),
		"truncated", mutation.Truncated,
		"cost_usd_approx", res.CostUSDApprox,
	)
	return res, nil
}

// newCouncilRunID composes COUNCIL-YYYY-MM-DD-HHMMSS so two runs the
// same day don't collide and humans can sort them visually.
func newCouncilRunID(t time.Time) string {
	return "COUNCIL-" + t.Format("2006-01-02-150405")
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Runner) logf(msg string, kv ...any) {
	if r.Logger != nil {
		r.Logger.Info(msg, kv...)
	}
}

// mkdirAll is a tiny wrapper so the Runner can be patched in tests if
// FS injection ever becomes useful. Today it's a straight pass-through
// to os.MkdirAll.
func mkdirAll(path string, perm uint32) error {
	return os.MkdirAll(path, os.FileMode(perm))
}

// persistDebateTranscript inserts every SidecarDebateRound into
// council_debate_rounds. Lives outside Runner.Run so the loop body
// stays scannable; per-row failures are logged but do not bubble — by
// the time we get here the council artifacts are already on disk and
// the council_runs row is committed, so re-raising would leave the
// caller no recovery path. Returns the number of rows successfully
// persisted (mainly for tests + future telemetry).
func persistDebateTranscript(
	ctx context.Context,
	dao *store.DebateDAO,
	runID string,
	rounds []council.SidecarDebateRound,
	now func() time.Time,
	logf func(msg string, kv ...any),
) int {
	if dao == nil || len(rounds) == 0 {
		return 0
	}
	written := 0
	for i := range rounds {
		round := &rounds[i]
		dbRound := &store.CouncilDebateRound{
			CouncilRunID:   runID,
			RoundIndex:     round.Round,
			Role:           store.DebateRole(round.Role),
			CostUSD:        round.CostUSD,
			Summary:        round.Summary,
			ArtifactDeltas: artifactDeltasToStore(round.ArtifactDeltas),
			CreatedAt:      now(),
		}
		if err := dao.AppendRound(ctx, dbRound); err != nil {
			if logf != nil {
				logf("persist debate round failed",
					"run_id", runID,
					"round_index", round.Round,
					"role", round.Role,
					"error", err,
				)
			}
			continue
		}
		written++
	}
	return written
}

// artifactDeltasToStore adapts the sidecar's typed delta slice to the
// loose []map[string]any shape the store DAO accepts. Mirrors the
// schema's `artifact_deltas_json` column (free-form JSON).
func artifactDeltasToStore(deltas []council.SidecarDebateDelta) []map[string]any {
	if len(deltas) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(deltas))
	for _, d := range deltas {
		entry := map[string]any{"path": d.Path}
		if d.LineRange != "" {
			entry["line_range"] = d.LineRange
		}
		if d.Action != "" {
			entry["action"] = d.Action
		}
		out = append(out, entry)
	}
	return out
}
