package eval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// PipelineOutcomeRubric is the persisted rubric name for Loop B
// per-merge attribution scores. The version suffix lets the formula
// roll forward without breaking eval-row comparability.
const PipelineOutcomeRubric = "pipeline_outcome_v1"

// CouncilROIRubric names the daily-aggregated downstream-success rubric
// keyed on council_run_id. Different rubric so council scores can be
// queried separately from per-merge attribution.
const CouncilROIRubric = "council_roi_v1"

// OutcomeAttributor turns a merged pipeline_run into one EvalScore row
// scoring the run on (success, time-to-merge, retry count, gate pass
// rate, cost overage). Slice 4.6 ships the deterministic side; the LLM
// rubric for "did this code change actually solve the problem?" is left
// to a future cross-run loop.
//
// Idempotency: the attributor checks LatestPerSubject before writing so
// re-running on the same merge produces a no-op rather than duplicate
// rows. The reconciler is welcome to call OnMerged on every merged
// transition without coordination.
type OutcomeAttributor struct {
	Store  *store.Store
	Clock  func() time.Time
	Logger *slog.Logger
}

// NewOutcomeAttributor constructs an attributor with sensible defaults.
func NewOutcomeAttributor(s *store.Store) *OutcomeAttributor {
	return &OutcomeAttributor{
		Store:  s,
		Clock:  time.Now,
		Logger: slog.Default(),
	}
}

// OnMerged is the hook the reconciler calls when a pipeline_runs row
// transitions to PipelineDone (i.e. the merge stage completed cleanly
// and cleanup ran). It computes the score and records exactly one
// pipeline_run eval row.
func (a *OutcomeAttributor) OnMerged(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error {
	if a == nil || a.Store == nil {
		return errors.New("attributor: not configured")
	}
	if run == nil || run.ID == "" {
		return errors.New("attributor: run.ID required")
	}
	if run.State != store.PipelineDone {
		return fmt.Errorf("attributor: refusing to score run in state %s", run.State)
	}
	existing, err := a.Store.Eval.LatestPerSubject(ctx, store.EvalSubjectPipelineRun, run.ID)
	if err != nil {
		return fmt.Errorf("attributor: read existing: %w", err)
	}
	for _, sc := range existing {
		if sc.Rubric == PipelineOutcomeRubric {
			a.logger().Info("attributor: idempotent skip", "run", run.ID)
			return nil
		}
	}
	score, breakdown, err := a.computeScore(ctx, run, item)
	if err != nil {
		return err
	}
	return a.Store.Eval.RecordScore(ctx, &store.EvalScore{
		SubjectKind: store.EvalSubjectPipelineRun,
		SubjectID:   run.ID,
		Rubric:      PipelineOutcomeRubric,
		Score:       score,
		Breakdown:   breakdown,
		JudgedBy:    "outcome-attributor",
		EvaluatedAt: a.now(),
		Notes:       fmt.Sprintf("backlog=%s mr=%v", item.ID, run.MRIID),
	})
}

// computeScore is the deterministic formula. We avoid an LLM here so
// per-merge attribution is reproducible and cheap.
//
// Components (each in [0,1]; final score is the average):
//
//	success         = 1 (we only score Done runs)
//	time_to_merge   = 1 if duration <= budget; degrades to 0 at 3× budget
//	retry_efficiency = 1 / (1 + total_extra_attempts)
//	gate_pass_rate  = (#pass / #total)
//	cost_efficiency = 1 if cost <= budget; degrades at 2× budget
func (a *OutcomeAttributor) computeScore(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) (float64, map[string]any, error) {
	stages, err := a.Store.Pipeline.ListStages(ctx, run.ID)
	if err != nil {
		return 0, nil, fmt.Errorf("list stages: %w", err)
	}
	gates, err := a.Store.Pipeline.ListGates(ctx, run.ID)
	if err != nil {
		return 0, nil, fmt.Errorf("list gates: %w", err)
	}

	// time_to_merge
	var duration time.Duration
	if run.EndedAt != nil {
		duration = run.EndedAt.Sub(run.StartedAt)
	}
	timeScore := 1.0
	if budget := time.Duration(item.Budget.MaxPipelineMinutes) * time.Minute; budget > 0 {
		timeScore = degradeRatio(duration, budget, 3)
	}

	// retry_efficiency: extra attempts beyond 1-per-stage.
	stagesByName := make(map[string]int)
	for _, s := range stages {
		if s.Attempt > stagesByName[s.Stage] {
			stagesByName[s.Stage] = s.Attempt
		}
	}
	extra := 0
	for _, attempts := range stagesByName {
		if attempts > 1 {
			extra += attempts - 1
		}
	}
	retryScore := 1.0 / (1.0 + float64(extra))

	// gate_pass_rate
	passes, total := 0, 0
	for _, g := range gates {
		total++
		if g.Outcome == store.GateOutcomePass {
			passes++
		}
	}
	gateScore := 1.0
	if total > 0 {
		gateScore = float64(passes) / float64(total)
	}

	// cost_efficiency
	costScore := 1.0
	if budget := item.Budget.MaxCostUSD; budget > 0 {
		costScore = degradeFloat(run.CostUSD, budget, 2)
	}

	// Equal-weight average of five components.
	final := (1.0 + timeScore + retryScore + gateScore + costScore) / 5.0

	breakdown := map[string]any{
		"success":          1.0,
		"time_to_merge":    timeScore,
		"retry_efficiency": retryScore,
		"gate_pass_rate":   gateScore,
		"cost_efficiency":  costScore,
		"duration_seconds": duration.Seconds(),
		"extra_attempts":   extra,
		"gates_total":      total,
		"gates_passed":     passes,
		"cost_usd":         run.CostUSD,
		"merged_sha":       lookupMergedSHA(stages),
	}
	if run.MRIID != nil {
		breakdown["mr_iid"] = *run.MRIID
	}
	return final, breakdown, nil
}

// degradeRatio returns 1 if observed <= budget; linearly degrades to 0
// as observed approaches multiplier × budget; clamps to 0 beyond.
func degradeRatio(observed, budget time.Duration, multiplier int) float64 {
	if budget <= 0 || observed <= budget {
		return 1.0
	}
	maxOver := time.Duration(multiplier) * budget
	if observed >= maxOver {
		return 0.0
	}
	over := observed - budget
	span := maxOver - budget
	return 1.0 - float64(over)/float64(span)
}

// degradeFloat is the float twin of degradeRatio.
func degradeFloat(observed, budget float64, multiplier int) float64 {
	if budget <= 0 || observed <= budget {
		return 1.0
	}
	maxOver := budget * float64(multiplier)
	if observed >= maxOver {
		return 0.0
	}
	over := observed - budget
	span := maxOver - budget
	return 1.0 - over/span
}

// lookupMergedSHA pulls merged_sha out of the merge stage's artifacts.
func lookupMergedSHA(stages []*store.StageResult) string {
	for _, s := range stages {
		if s.Stage != "merge" || s.Artifacts == nil {
			continue
		}
		if v, ok := s.Artifacts["merged_sha"].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func (a *OutcomeAttributor) now() time.Time {
	if a.Clock != nil {
		return a.Clock()
	}
	return time.Now()
}

func (a *OutcomeAttributor) logger() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.Default()
}
