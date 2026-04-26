package eval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// CouncilROI aggregates per-merge attribution rows up to the council
// run that originally produced the backlog items. Loop B's downstream
// signal: did the plans this council emitted actually result in
// shipped, healthy merges?
//
// The aggregator is window-based: AggregateSince(since) walks every
// council_run that ended in [since, now), pulls its backlog items via
// CouncilRunID, and rolls the matching pipeline_outcome_v1 scores into
// one council_run row keyed on rubric=council_roi_v1.
//
// One eval row per council run per call. Idempotent on the (council_run
// id, rubric) pair: re-aggregating the same window updates rather than
// duplicates.
type CouncilROI struct {
	Store  *store.Store
	Clock  func() time.Time
	Logger *slog.Logger
}

// NewCouncilROI returns an aggregator with sensible defaults.
func NewCouncilROI(s *store.Store) *CouncilROI {
	return &CouncilROI{
		Store:  s,
		Clock:  time.Now,
		Logger: slog.Default(),
	}
}

// AggregateSince walks all council runs (the operator caps the lookback
// via since) and emits one council_roi_v1 score per run that has at
// least one merged backlog item with a pipeline_outcome score.
//
// Returns the number of eval rows written/updated.
func (c *CouncilROI) AggregateSince(ctx context.Context, since time.Time) (int, error) {
	if c == nil || c.Store == nil {
		return 0, errors.New("council_roi: not configured")
	}
	runs, err := c.Store.Council.List(ctx, 0)
	if err != nil {
		return 0, fmt.Errorf("list council runs: %w", err)
	}

	written := 0
	for _, run := range runs {
		if run.StartedAt.Before(since) {
			continue
		}
		score, breakdown, ok, err := c.aggregateOne(ctx, run)
		if err != nil {
			return written, err
		}
		if !ok {
			continue
		}
		if err := c.Store.Eval.RecordScore(ctx, &store.EvalScore{
			SubjectKind: store.EvalSubjectCouncilRun,
			SubjectID:   run.ID,
			Rubric:      CouncilROIRubric,
			Score:       score,
			Breakdown:   breakdown,
			JudgedBy:    "council-roi",
			EvaluatedAt: c.now(),
		}); err != nil {
			return written, fmt.Errorf("record council_roi for %s: %w", run.ID, err)
		}
		written++
	}
	return written, nil
}

// aggregateOne computes the ROI score for one council run. Returns
// ok=false when the council run has no merged downstream items yet —
// the operator skips writing rather than emitting a score of 0 that
// would penalise plans whose pipelines simply haven't run.
func (c *CouncilROI) aggregateOne(ctx context.Context, run *store.CouncilRun) (float64, map[string]any, bool, error) {
	items, err := c.Store.Backlog.List(ctx)
	if err != nil {
		return 0, nil, false, fmt.Errorf("list backlog: %w", err)
	}
	var matchedIDs []string
	for _, item := range items {
		if item.CouncilRunID == nil || *item.CouncilRunID != run.ID {
			continue
		}
		matchedIDs = append(matchedIDs, item.ID)
	}
	if len(matchedIDs) == 0 {
		return 0, nil, false, nil
	}

	mergedItems := 0
	totalScore := 0.0
	scoredRuns := 0
	pipelineRuns := 0
	for _, id := range matchedIDs {
		runs, err := c.Store.Pipeline.ListByBacklog(ctx, id)
		if err != nil {
			return 0, nil, false, fmt.Errorf("list pipeline runs for %s: %w", id, err)
		}
		for _, pr := range runs {
			pipelineRuns++
			if pr.State != store.PipelineDone {
				continue
			}
			scores, err := c.Store.Eval.LatestPerSubject(ctx, store.EvalSubjectPipelineRun, pr.ID)
			if err != nil {
				return 0, nil, false, fmt.Errorf("read scores for %s: %w", pr.ID, err)
			}
			for _, sc := range scores {
				if sc.Rubric != PipelineOutcomeRubric {
					continue
				}
				totalScore += sc.Score
				scoredRuns++
				mergedItems++
			}
		}
	}
	if scoredRuns == 0 {
		return 0, nil, false, nil
	}
	avg := totalScore / float64(scoredRuns)
	breakdown := map[string]any{
		"items_total":   len(matchedIDs),
		"items_merged":  mergedItems,
		"pipeline_runs": pipelineRuns,
		"scored_runs":   scoredRuns,
		"avg_outcome":   avg,
	}
	return avg, breakdown, true, nil
}

func (c *CouncilROI) now() time.Time {
	if c.Clock != nil {
		return c.Clock()
	}
	return time.Now()
}
