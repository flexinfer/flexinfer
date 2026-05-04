package eval

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "h.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seedMergedRun(t *testing.T, st *store.Store, runID, backlogID, councilID string, costUSD float64, durationMin int, gateRows int, gatePass int, retryStage string, retryAttempts int) (*store.PipelineRun, *store.BacklogItem) {
	t.Helper()
	ctx := context.Background()
	if councilID != "" {
		if err := st.Council.Put(ctx, &store.CouncilRun{
			ID:        councilID,
			Trigger:   store.CouncilTriggerManual,
			StartedAt: time.Date(2026, 4, 26, 11, 0, 0, 0, time.UTC),
			Outcome:   store.CouncilOutcomeSuccess,
		}); err != nil {
			t.Fatalf("seed council: %v", err)
		}
	}
	item := &store.BacklogItem{
		ID:       backlogID,
		Title:    "x",
		State:    store.BacklogMerged,
		Priority: store.P2,
		Budget: store.Budget{
			MaxCostUSD:         1.0,
			MaxPipelineMinutes: 60,
		},
	}
	if councilID != "" {
		cr := councilID
		item.CouncilRunID = &cr
	}
	if err := st.Backlog.Put(ctx, item); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	end := now.Add(time.Duration(durationMin) * time.Minute)
	mr := int64(101)
	run := &store.PipelineRun{
		ID:        runID,
		BacklogID: backlogID,
		Template:  "mills-default-pipeline",
		State:     store.PipelineDone,
		Attempts:  1,
		StartedAt: now,
		EndedAt:   &end,
		CostUSD:   costUSD,
		MRIID:     &mr,
	}
	if err := st.Pipeline.PutRun(ctx, run); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	// Seed gates: gateRows total, gatePass of which passed.
	for i := 0; i < gateRows; i++ {
		out := store.GateOutcomeFail
		if i < gatePass {
			out = store.GateOutcomePass
		}
		if err := st.Pipeline.PutGate(ctx, &store.GateOutcome{
			PipelineRunID: run.ID,
			AfterStage:    "post_implement_gate",
			GateName:      string(rune('a' + i)),
			Outcome:       out,
			EvaluatedAt:   now,
		}); err != nil {
			t.Fatalf("seed gate: %v", err)
		}
	}
	// Seed stage_results: one happy stage + N attempts of retryStage if requested.
	ok := store.StageOutcomeSuccess
	if err := st.Pipeline.PutStage(ctx, &store.StageResult{
		PipelineRunID: run.ID, Stage: "implement", Attempt: 1,
		StartedAt: now, EndedAt: &end, Outcome: &ok,
	}); err != nil {
		t.Fatalf("seed stage: %v", err)
	}
	for i := 1; i <= retryAttempts; i++ {
		if err := st.Pipeline.PutStage(ctx, &store.StageResult{
			PipelineRunID: run.ID, Stage: retryStage, Attempt: i,
			StartedAt: now, EndedAt: &end, Outcome: &ok,
		}); err != nil {
			t.Fatalf("seed retry stage: %v", err)
		}
	}
	// Seed merge stage with merged_sha for the breakdown.
	if err := st.Pipeline.PutStage(ctx, &store.StageResult{
		PipelineRunID: run.ID, Stage: "merge", Attempt: 1,
		StartedAt: now, EndedAt: &end, Outcome: &ok,
		Artifacts: map[string]any{"merged_sha": "abc1234"},
	}); err != nil {
		t.Fatalf("seed merge stage: %v", err)
	}
	return run, item
}

func TestOutcomeAttributor_OnMerged_HappyPath(t *testing.T) {
	st := openTestStore(t)
	run, item := seedMergedRun(t, st, "PIPE-A-1", "BL-A", "COUNCIL-1", 0.5, 30, 5, 5, "tests", 1)
	a := NewOutcomeAttributor(st)
	if err := a.OnMerged(context.Background(), run, item); err != nil {
		t.Fatalf("attribute: %v", err)
	}
	scores, err := st.Eval.LatestPerSubject(context.Background(), store.EvalSubjectPipelineRun, run.ID)
	if err != nil {
		t.Fatalf("read scores: %v", err)
	}
	if len(scores) != 1 {
		t.Fatalf("expected 1 score, got %d", len(scores))
	}
	sc := scores[0]
	if sc.Rubric != PipelineOutcomeRubric {
		t.Errorf("rubric = %s", sc.Rubric)
	}
	if sc.Score < 0.8 || sc.Score > 1 {
		t.Errorf("score = %f, expected high (gates pass, under budget)", sc.Score)
	}
	if v, ok := sc.Breakdown["merged_sha"].(string); !ok || v != "abc1234" {
		t.Errorf("merged_sha breakdown = %v", sc.Breakdown["merged_sha"])
	}
}

func TestOutcomeAttributor_GateFailLowersScore(t *testing.T) {
	st := openTestStore(t)
	// 1 of 5 gates passed → gate_pass_rate = 0.2.
	run, item := seedMergedRun(t, st, "PIPE-B-1", "BL-B", "COUNCIL-1", 0.5, 30, 5, 1, "implement", 0)
	a := NewOutcomeAttributor(st)
	if err := a.OnMerged(context.Background(), run, item); err != nil {
		t.Fatalf("attribute: %v", err)
	}
	scores, _ := st.Eval.LatestPerSubject(context.Background(), store.EvalSubjectPipelineRun, run.ID)
	// 4 of 5 components at 1.0 + 0.2 gate-pass = 0.84. Assert it's
	// strictly less than the all-passing case (1.0).
	if scores[0].Score >= 0.9 {
		t.Errorf("score = %f, expected lower with 80%% gate fails", scores[0].Score)
	}
	if v, ok := scores[0].Breakdown["gate_pass_rate"].(float64); !ok || v > 0.21 || v < 0.19 {
		t.Errorf("gate_pass_rate = %v, want ~0.2", scores[0].Breakdown["gate_pass_rate"])
	}
}

func TestOutcomeAttributor_RetryEfficiencyDegrades(t *testing.T) {
	st := openTestStore(t)
	// 1 implement attempt + 3 extra retry attempts on tests = 3 extra → 1/(1+3)=0.25.
	run, item := seedMergedRun(t, st, "PIPE-C-1", "BL-C", "COUNCIL-1", 0.5, 30, 1, 1, "tests", 4)
	a := NewOutcomeAttributor(st)
	if err := a.OnMerged(context.Background(), run, item); err != nil {
		t.Fatalf("attribute: %v", err)
	}
	scores, _ := st.Eval.LatestPerSubject(context.Background(), store.EvalSubjectPipelineRun, run.ID)
	v, ok := scores[0].Breakdown["retry_efficiency"].(float64)
	if !ok || v > 0.26 || v < 0.24 {
		t.Errorf("retry_efficiency = %v, want ~0.25 (3 extra attempts)", scores[0].Breakdown["retry_efficiency"])
	}
}

func TestOutcomeAttributor_OverBudgetCostDegrades(t *testing.T) {
	st := openTestStore(t)
	// budget=1, observed=2 → 1/(2-1)=1 over the budget span (budget*2-budget=budget=1) → score=0.
	run, item := seedMergedRun(t, st, "PIPE-D-1", "BL-D", "", 2.0, 30, 1, 1, "implement", 0)
	a := NewOutcomeAttributor(st)
	if err := a.OnMerged(context.Background(), run, item); err != nil {
		t.Fatalf("attribute: %v", err)
	}
	scores, _ := st.Eval.LatestPerSubject(context.Background(), store.EvalSubjectPipelineRun, run.ID)
	v, _ := scores[0].Breakdown["cost_efficiency"].(float64)
	if v != 0 {
		t.Errorf("cost_efficiency = %v, want 0 at 2× budget", v)
	}
}

func TestOutcomeAttributor_Idempotent(t *testing.T) {
	st := openTestStore(t)
	run, item := seedMergedRun(t, st, "PIPE-E-1", "BL-E", "", 0.5, 30, 1, 1, "implement", 0)
	a := NewOutcomeAttributor(st)
	if err := a.OnMerged(context.Background(), run, item); err != nil {
		t.Fatalf("first attribute: %v", err)
	}
	if err := a.OnMerged(context.Background(), run, item); err != nil {
		t.Fatalf("second attribute: %v", err)
	}
	scores, _ := st.Eval.LatestPerSubject(context.Background(), store.EvalSubjectPipelineRun, run.ID)
	if len(scores) != 1 {
		t.Errorf("idempotent skip should leave a single row, got %d", len(scores))
	}
}

func TestOutcomeAttributor_RejectsNonDoneRun(t *testing.T) {
	st := openTestStore(t)
	run := &store.PipelineRun{ID: "PIPE-X", BacklogID: "BL-X", State: store.PipelineImplementing, StartedAt: time.Now()}
	if err := st.Backlog.Put(context.Background(), &store.BacklogItem{ID: "BL-X", State: store.BacklogQueued, Priority: store.P2}); err != nil {
		t.Fatal(err)
	}
	if err := st.Pipeline.PutRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	a := NewOutcomeAttributor(st)
	if err := a.OnMerged(context.Background(), run, &store.BacklogItem{ID: "BL-X"}); err == nil {
		t.Error("expected error for non-Done state")
	}
}

func TestCouncilROI_AggregatesAcrossMultipleMerges(t *testing.T) {
	st := openTestStore(t)
	a := NewOutcomeAttributor(st)
	// Two backlog items both attributed to COUNCIL-99.
	run1, item1 := seedMergedRun(t, st, "PIPE-R1", "BL-R1", "COUNCIL-99", 0.4, 20, 1, 1, "implement", 0)
	run2, item2 := seedMergedRun(t, st, "PIPE-R2", "BL-R2", "COUNCIL-99", 0.5, 25, 1, 1, "implement", 0)
	if err := a.OnMerged(context.Background(), run1, item1); err != nil {
		t.Fatalf("attribute 1: %v", err)
	}
	if err := a.OnMerged(context.Background(), run2, item2); err != nil {
		t.Fatalf("attribute 2: %v", err)
	}
	roi := NewCouncilROI(st)
	written, err := roi.AggregateSince(context.Background(), time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if written != 1 {
		t.Errorf("expected 1 council_roi row, got %d", written)
	}
	scores, err := st.Eval.LatestPerSubject(context.Background(), store.EvalSubjectCouncilRun, "COUNCIL-99")
	if err != nil {
		t.Fatalf("read council eval: %v", err)
	}
	if len(scores) != 1 {
		t.Fatalf("expected 1 council eval, got %d", len(scores))
	}
	sc := scores[0]
	if sc.Rubric != CouncilROIRubric {
		t.Errorf("rubric = %s", sc.Rubric)
	}
	if v, ok := sc.Breakdown["scored_runs"].(float64); !ok || int(v) != 2 {
		t.Errorf("scored_runs = %v, want 2", sc.Breakdown["scored_runs"])
	}
	if v, ok := sc.Breakdown["items_total"].(float64); !ok || int(v) != 2 {
		t.Errorf("items_total = %v, want 2", sc.Breakdown["items_total"])
	}
}

func TestCouncilROI_SkipsCouncilWithNoMerges(t *testing.T) {
	st := openTestStore(t)
	// Council with a queued backlog item but no merged pipeline run.
	if err := st.Council.Put(context.Background(), &store.CouncilRun{
		ID: "COUNCIL-EMPTY", Trigger: store.CouncilTriggerManual,
		StartedAt: time.Now(), Outcome: store.CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatal(err)
	}
	cr := "COUNCIL-EMPTY"
	if err := st.Backlog.Put(context.Background(), &store.BacklogItem{
		ID: "BL-EMPTY", Priority: store.P2, State: store.BacklogQueued, CouncilRunID: &cr,
	}); err != nil {
		t.Fatal(err)
	}
	roi := NewCouncilROI(st)
	written, err := roi.AggregateSince(context.Background(), time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if written != 0 {
		t.Errorf("expected 0 rows written for council with no merged items, got %d", written)
	}
}

func TestCouncilROI_FiltersOnSinceWindow(t *testing.T) {
	st := openTestStore(t)
	// Old council run before window.
	if err := st.Council.Put(context.Background(), &store.CouncilRun{
		ID: "COUNCIL-OLD", Trigger: store.CouncilTriggerManual,
		StartedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Outcome:   store.CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatal(err)
	}
	cr := "COUNCIL-OLD"
	if err := st.Backlog.Put(context.Background(), &store.BacklogItem{
		ID: "BL-OLD", Priority: store.P2, State: store.BacklogMerged, CouncilRunID: &cr,
	}); err != nil {
		t.Fatal(err)
	}
	roi := NewCouncilROI(st)
	written, err := roi.AggregateSince(context.Background(), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if written != 0 {
		t.Errorf("council before since window should be skipped; wrote %d", written)
	}
}
