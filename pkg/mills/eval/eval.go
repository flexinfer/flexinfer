// Package eval is the three-loop evaluation framework for Loom Mills.
//
// Loop A (this slice — synchronous, per council run): the judge gates
// backlog mutation on the artifact's own quality. A council run that
// scores below the threshold (default 0.7) gets its run row marked
// "partial" and its backlog deltas dropped — the markdown still commits
// for audit, but the council's bad judgment doesn't propagate downstream.
//
// Loop B (Phase 4 slice 4.6 — per-merge attribution) and Loop C (Phase 6
// slice 6.4 — weekly cross-run) live in this same package next to Loop A
// so the eval_scores schema + helper code stays single-source.
package eval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/mills/council"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// Input is the bundle every Criterion + the Judge consume. Each field is
// optional — a Criterion ignores what it doesn't need — but realistic
// runs supply Sidecar + WriteResult + Store so every check has data.
type Input struct {
	// Sidecar is the structured deliverable from the council editor +
	// artifact writer. Required for every deterministic criterion.
	Sidecar *council.Sidecar

	// WriteResult is the writer's output, used for things like cross-
	// referencing the markdown filenames in error messages.
	WriteResult *council.WriteResult

	// EditorOutput is the editor's documents pre-write. Some criteria
	// (slice independence, plan completeness) inspect the body before
	// it's persisted so the judge can short-circuit at zero cost.
	EditorOutput *council.EditorOutput

	// Store gives Criteria access to canonical mills state — the
	// roadmap_intents table for alignment checks, recent council/
	// pipeline history for contradiction detection.
	Store *store.Store

	// Now is injectable for deterministic test windows.
	Now func() time.Time
}

func (in Input) now() time.Time {
	if in.Now != nil {
		return in.Now()
	}
	return time.Now()
}

// CriterionResult is the verdict of one Criterion. Score in [0,1]; the
// judge weights and aggregates. Reasons surface in eval_scores
// .breakdown_json so HUD / operators can see what tripped.
type CriterionResult struct {
	Name    string
	Score   float64
	Weight  float64
	Reasons []string
}

// Criterion is a single scoring rule.
type Criterion interface {
	Name() string
	Weight() float64
	Score(ctx context.Context, in Input) (CriterionResult, error)
}

// LLMJudge evaluates the one criterion that needs a language model
// (cross-run contradiction detection). The judge implementation always
// uses FlexInfer — the spec is explicit that frontier models are never
// used inside gates because the cost would scale poorly.
type LLMJudge interface {
	// JudgeContradiction returns a score in [0,1] (1 = no contradiction
	// detected) and a list of human-readable findings.
	JudgeContradiction(ctx context.Context, in Input) (float64, []string, error)
}

// FakeLLMJudge is the test double + dryrun fallback. Always returns the
// configured score so tests can exercise pass/partial paths.
type FakeLLMJudge struct {
	Score    float64
	Findings []string
	Err      error
}

// JudgeContradiction implements LLMJudge.
func (f *FakeLLMJudge) JudgeContradiction(_ context.Context, _ Input) (float64, []string, error) {
	if f.Err != nil {
		return 0, nil, f.Err
	}
	return f.Score, f.Findings, nil
}

// PartialThreshold is the aggregate score below which a council run is
// marked "partial" and its backlog mutations are dropped. Tunable per
// deployment via Judge.Threshold; this is the default.
const PartialThreshold = 0.7

// Judge runs every Criterion in the rubric against the input and returns
// an aggregate Verdict. The aggregate score is the weighted mean of the
// per-criterion scores (weights normalised so they sum to 1). Below
// Threshold the verdict's Partial flag is true; the operator uses that
// to skip backlog mutation for the run.
type Judge struct {
	// Criteria is the ordered rubric. DefaultRubric returns the v1 set;
	// production wiring may swap individual entries (e.g. a stricter
	// scope check) without re-creating the Judge.
	Criteria []Criterion

	// Threshold defaults to PartialThreshold when zero.
	Threshold float64
}

// Verdict is the Judge's output for one input. The reconciler persists
// it as one eval_scores row per council run.
type Verdict struct {
	Score    float64
	Partial  bool
	Results  []CriterionResult
	JudgedAt time.Time
	JudgedBy string // "rubric-v1"; LLM-driven criteria stamp their own attribution into Reasons
}

// Run executes every Criterion. Errors from a Criterion are fatal — the
// caller treats that as an infrastructure failure (retry the run);
// scoring failures (low score) return a Verdict with Partial=true.
func (j *Judge) Run(ctx context.Context, in Input) (*Verdict, error) {
	if j == nil || len(j.Criteria) == 0 {
		return nil, errors.New("eval: judge has no criteria")
	}
	if in.Sidecar == nil {
		return nil, errors.New("eval: input requires a Sidecar")
	}
	threshold := j.Threshold
	if threshold <= 0 {
		threshold = PartialThreshold
	}

	results := make([]CriterionResult, 0, len(j.Criteria))
	totalWeight := 0.0
	weighted := 0.0
	for _, c := range j.Criteria {
		r, err := c.Score(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("criterion %q: %w", c.Name(), err)
		}
		// Defensive: clamp score to [0,1] so a buggy criterion can't
		// drive the aggregate negative or above 1.
		if r.Score < 0 {
			r.Score = 0
		}
		if r.Score > 1 {
			r.Score = 1
		}
		results = append(results, r)
		totalWeight += r.Weight
		weighted += r.Weight * r.Score
	}
	score := 0.0
	if totalWeight > 0 {
		score = weighted / totalWeight
	}
	now := in.now()
	return &Verdict{
		Score:    score,
		Partial:  score < threshold,
		Results:  results,
		JudgedAt: now,
		JudgedBy: "rubric-v1",
	}, nil
}

// PersistTo records the verdict as an eval_scores row tagged to the
// given subject (typically the council run id). The breakdown carries
// every per-criterion result so HUD can render the full rubric.
func (v *Verdict) PersistTo(ctx context.Context, dao *store.EvalDAO, subjectID string) error {
	if v == nil {
		return errors.New("eval: nil verdict")
	}
	if dao == nil {
		return errors.New("eval: nil eval dao")
	}
	breakdown := map[string]any{
		"threshold": PartialThreshold,
		"partial":   v.Partial,
		"criteria":  v.Results,
	}
	score := &store.EvalScore{
		SubjectKind: store.EvalSubjectCouncilRun,
		SubjectID:   subjectID,
		Rubric:      v.JudgedBy,
		Score:       v.Score,
		Breakdown:   breakdown,
		JudgedBy:    v.JudgedBy,
		EvaluatedAt: v.JudgedAt,
		Notes:       formatVerdictNotes(v),
	}
	return dao.RecordScore(ctx, score)
}

// formatVerdictNotes renders a tiny human-readable summary of what
// pulled the score down so operators reading the HUD don't have to
// scroll into the breakdown JSON.
func formatVerdictNotes(v *Verdict) string {
	var weak []string
	for _, r := range v.Results {
		if r.Score < 1.0 {
			weak = append(weak, fmt.Sprintf("%s=%.2f", r.Name, r.Score))
		}
	}
	if len(weak) == 0 {
		return ""
	}
	sort.Strings(weak)
	return strings.Join(weak, ", ")
}
