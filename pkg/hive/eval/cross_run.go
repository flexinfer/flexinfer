package eval

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// Loop C — weekly cross-run consistency check. Runs once a week (default
// Sunday 06:00 UTC) and folds three signals into the canonical
// eval_scores table so the next council brief can surface them:
//
//  1. Stale plans — backlog items still in queued/running for > 5 days
//  2. Repeated gate failures — gate names that failed across multiple
//     pipeline runs in the window (signals a flaky or wrongly-tuned gate)
//  3. Conflicting outcomes — backlog items that have ≥2 pipeline runs
//     with divergent terminal states (e.g. one merged, one escalated),
//     suggesting churn or non-deterministic spawn behaviour
//
// We deliberately use deterministic heuristics rather than the LLM judge
// for v1: cheaper, observable, no auth boundary. The judge can layer in
// later as a fourth check if signal-to-noise warrants it.
//
// Each check produces one EvalScore row with SubjectKind=cross_run.
// Score is in [0, 1] where 1.0 = clean. The breakdown carries the raw
// findings so the council brief renderer can list specific items
// without re-running the queries.
const (
	// LoopCRubricStalePlans names the rubric written into eval_scores.rubric.
	LoopCRubricStalePlans = "loop_c_stale_plans"
	// LoopCRubricRepeatedGates names the rubric for the gate-failure check.
	LoopCRubricRepeatedGates = "loop_c_repeated_gate_failures"
	// LoopCRubricConflictingOutcomes names the rubric for the conflict check.
	LoopCRubricConflictingOutcomes = "loop_c_conflicting_outcomes"

	// LoopCJudgedBy is the JudgedBy attribution stamped on every Loop C row.
	LoopCJudgedBy = "loop_c_cross_run"

	// stalePlanThreshold is the age past which a non-terminal backlog item
	// counts as stale. 5 days because the operator's slowest realistic
	// pipeline (multi-slice features) should clear inside 4-5 days.
	stalePlanThreshold = 5 * 24 * time.Hour

	// crossRunWindow is how far back the gate / outcome checks look.
	crossRunWindow = 7 * 24 * time.Hour
)

// CrossRunChecker runs the three Loop C heuristics and persists their
// findings to eval_scores. Stateless — safe to construct per-tick.
type CrossRunChecker struct {
	Store  *store.Store
	Now    func() time.Time
	Logger *slog.Logger
}

// CrossRunResult is the aggregated outcome of one Run. Returned for
// tests + scheduler logging; the canonical record is the eval_scores
// rows persisted as a side effect.
type CrossRunResult struct {
	WindowStart         time.Time
	WindowEnd           time.Time
	StaleScore          float64
	RepeatedGateScore   float64
	ConflictingScore    float64
	StaleItems          []string            // backlog ids
	RepeatedGates       map[string]int      // gate_name → fail count
	ConflictingOutcomes map[string][]string // backlog id → terminal states observed
}

// Run executes all three checks and writes one EvalScore per check.
// Errors from individual checks don't abort the others — Loop C is
// best-effort by design (the council still runs without it).
func (c *CrossRunChecker) Run(ctx context.Context) (CrossRunResult, error) {
	if c == nil || c.Store == nil {
		return CrossRunResult{}, fmt.Errorf("cross_run: store required")
	}
	now := c.now().UTC()
	windowStart := now.Add(-crossRunWindow)
	subjectID := fmt.Sprintf("%s..%s", windowStart.Format("2006-01-02"), now.Format("2006-01-02"))

	res := CrossRunResult{
		WindowStart:         windowStart,
		WindowEnd:           now,
		RepeatedGates:       map[string]int{},
		ConflictingOutcomes: map[string][]string{},
	}

	if score, items, err := c.checkStalePlans(ctx, now); err != nil {
		c.warn("stale plans check failed", err)
	} else {
		res.StaleScore = score
		res.StaleItems = items
		c.persist(ctx, subjectID, LoopCRubricStalePlans, score, map[string]any{
			"stale_backlog_ids": items,
			"threshold_days":    int(stalePlanThreshold / (24 * time.Hour)),
		}, summarizeStale(items))
	}

	if score, gates, err := c.checkRepeatedGateFailures(ctx, windowStart); err != nil {
		c.warn("repeated gate check failed", err)
	} else {
		res.RepeatedGateScore = score
		res.RepeatedGates = gates
		c.persist(ctx, subjectID, LoopCRubricRepeatedGates, score, map[string]any{
			"gate_failure_counts": gates,
			"window_days":         int(crossRunWindow / (24 * time.Hour)),
		}, summarizeGateFailures(gates))
	}

	if score, conflicts, err := c.checkConflictingOutcomes(ctx, windowStart); err != nil {
		c.warn("conflicting outcomes check failed", err)
	} else {
		res.ConflictingScore = score
		res.ConflictingOutcomes = conflicts
		c.persist(ctx, subjectID, LoopCRubricConflictingOutcomes, score, map[string]any{
			"conflicts":   conflicts,
			"window_days": int(crossRunWindow / (24 * time.Hour)),
		}, summarizeConflicts(conflicts))
	}

	if c.Logger != nil {
		c.Logger.Info("cross-run check complete",
			"window", subjectID,
			"stale", len(res.StaleItems),
			"flaky_gates", len(res.RepeatedGates),
			"conflicts", len(res.ConflictingOutcomes),
		)
	}
	return res, nil
}

// checkStalePlans scores the fraction of non-terminal backlog items
// older than the threshold. 1.0 = none stale; 0.0 = every non-terminal
// item is over threshold.
func (c *CrossRunChecker) checkStalePlans(ctx context.Context, now time.Time) (float64, []string, error) {
	cutoff := now.Add(-stalePlanThreshold)
	var stale []string
	var total int
	for _, state := range []store.BacklogState{store.BacklogQueued, store.BacklogRunning} {
		items, err := c.Store.Backlog.ListByState(ctx, state)
		if err != nil {
			return 0, nil, err
		}
		for _, it := range items {
			total++
			if it.CreatedAt.Before(cutoff) {
				stale = append(stale, it.ID)
			}
		}
	}
	sort.Strings(stale)
	if total == 0 {
		return 1.0, nil, nil
	}
	return 1.0 - (float64(len(stale)) / float64(total)), stale, nil
}

// checkRepeatedGateFailures finds gate names that failed in ≥2 distinct
// pipeline runs in the window. 1.0 = none; 0.5 = one flaky gate; 0.0 =
// 5+ flaky gates (cap so the score doesn't asymptote slowly).
func (c *CrossRunChecker) checkRepeatedGateFailures(ctx context.Context, windowStart time.Time) (float64, map[string]int, error) {
	// Collect every pipeline run that ended in the window. We iterate
	// every state to catch escalated/paused pipelines whose gate signals
	// also matter (a gate that fails on the way to escalation is the
	// kind of thing Loop C is looking for).
	runIDs, err := c.runIDsSince(ctx, windowStart)
	if err != nil {
		return 0, nil, err
	}

	// Per-gate set of run ids that saw a fail. A gate counts as "flaky"
	// only when it failed in ≥2 distinct runs.
	failsByGate := map[string]map[string]struct{}{}
	for _, runID := range runIDs {
		gates, err := c.Store.Pipeline.ListGates(ctx, runID)
		if err != nil {
			return 0, nil, err
		}
		for _, g := range gates {
			if g.Outcome != store.GateOutcomeFail {
				continue
			}
			if failsByGate[g.GateName] == nil {
				failsByGate[g.GateName] = map[string]struct{}{}
			}
			failsByGate[g.GateName][runID] = struct{}{}
		}
	}

	flaky := map[string]int{}
	for name, runs := range failsByGate {
		if len(runs) >= 2 {
			flaky[name] = len(runs)
		}
	}

	score := 1.0
	if n := len(flaky); n > 0 {
		// 1 flaky → 0.8, 2 → 0.6, 3 → 0.4, 4 → 0.2, 5+ → 0.0.
		score = 1.0 - 0.2*float64(n)
		if score < 0 {
			score = 0
		}
	}
	return score, flaky, nil
}

// checkConflictingOutcomes finds backlog items whose pipeline runs in
// the window ended in ≥2 distinct terminal states (e.g. one merged + one
// escalated). 1.0 = none; capped slope similar to gate check.
func (c *CrossRunChecker) checkConflictingOutcomes(ctx context.Context, windowStart time.Time) (float64, map[string][]string, error) {
	runIDs, err := c.runIDsSince(ctx, windowStart)
	if err != nil {
		return 0, nil, err
	}
	statesByItem := map[string]map[string]struct{}{}
	for _, runID := range runIDs {
		run, err := c.Store.Pipeline.GetRun(ctx, runID)
		if err != nil {
			continue
		}
		if !isTerminalState(run.State) {
			continue
		}
		if statesByItem[run.BacklogID] == nil {
			statesByItem[run.BacklogID] = map[string]struct{}{}
		}
		statesByItem[run.BacklogID][string(run.State)] = struct{}{}
	}
	conflicts := map[string][]string{}
	for id, set := range statesByItem {
		if len(set) < 2 {
			continue
		}
		states := make([]string, 0, len(set))
		for s := range set {
			states = append(states, s)
		}
		sort.Strings(states)
		conflicts[id] = states
	}

	score := 1.0
	if n := len(conflicts); n > 0 {
		score = 1.0 - 0.25*float64(n)
		if score < 0 {
			score = 0
		}
	}
	return score, conflicts, nil
}

// runIDsSince returns pipeline run ids whose ended_at >= since.
// Walks every state because the DAO's only window-aware helpers are
// SumCostSince / CountSince which discard ids. Cheap enough — pipeline
// runs are O(100) per week.
func (c *CrossRunChecker) runIDsSince(ctx context.Context, since time.Time) ([]string, error) {
	out := map[string]struct{}{}
	for _, state := range allPipelineStates() {
		runs, err := c.Store.Pipeline.ListByState(ctx, state)
		if err != nil {
			return nil, err
		}
		for _, r := range runs {
			ref := r.StartedAt
			if r.EndedAt != nil && r.EndedAt.After(ref) {
				ref = *r.EndedAt
			}
			if ref.Before(since) {
				continue
			}
			out[r.ID] = struct{}{}
		}
	}
	ids := make([]string, 0, len(out))
	for id := range out {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (c *CrossRunChecker) persist(ctx context.Context, subjectID, rubric string, score float64, breakdown map[string]any, notes string) {
	rec := &store.EvalScore{
		SubjectKind: store.EvalSubjectCrossRun,
		SubjectID:   subjectID,
		Rubric:      rubric,
		Score:       score,
		Breakdown:   breakdown,
		JudgedBy:    LoopCJudgedBy,
		EvaluatedAt: c.now().UTC(),
		Notes:       notes,
	}
	if err := c.Store.Eval.RecordScore(ctx, rec); err != nil {
		c.warn("record score", err)
	}
}

func (c *CrossRunChecker) warn(msg string, err error) {
	if c.Logger != nil {
		c.Logger.Warn("loop C: "+msg, "error", err)
	}
}

func (c *CrossRunChecker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func allPipelineStates() []store.PipelineState {
	return []store.PipelineState{
		store.PipelineQueued, store.PipelinePlanning, store.PipelineSlicing,
		store.PipelineImplementing, store.PipelineTesting, store.PipelineReviewing,
		store.PipelineMR, store.PipelineCI, store.PipelineMerging,
		store.PipelineDone, store.PipelineEscalated, store.PipelinePaused,
	}
}

func isTerminalState(s store.PipelineState) bool {
	switch s {
	case store.PipelineDone, store.PipelineEscalated:
		return true
	}
	return false
}

// ----- summary renderers (used as Notes on the eval_scores rows) -----

func summarizeStale(ids []string) string {
	if len(ids) == 0 {
		return "no stale plans"
	}
	return fmt.Sprintf("%d backlog items stale (>%d days non-terminal): %s",
		len(ids), int(stalePlanThreshold/(24*time.Hour)), strings.Join(ids, ", "))
}

func summarizeGateFailures(gates map[string]int) string {
	if len(gates) == 0 {
		return "no repeated gate failures"
	}
	parts := make([]string, 0, len(gates))
	for name, n := range gates {
		parts = append(parts, fmt.Sprintf("%s×%d", name, n))
	}
	sort.Strings(parts)
	return "flaky gates: " + strings.Join(parts, ", ")
}

func summarizeConflicts(conflicts map[string][]string) string {
	if len(conflicts) == 0 {
		return "no conflicting outcomes"
	}
	parts := make([]string, 0, len(conflicts))
	for id, states := range conflicts {
		parts = append(parts, fmt.Sprintf("%s={%s}", id, strings.Join(states, "|")))
	}
	sort.Strings(parts)
	return "divergent terminal states: " + strings.Join(parts, "; ")
}
