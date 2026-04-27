package eval

import (
	"context"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// fixedNow returns a closure that yields a fixed time so cross-run
// windows are deterministic across CI environments.
func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// TestCrossRun_CleanWindow verifies all three checks score 1.0 when
// nothing is wrong — empty store, nothing stale, no flaky gates.
func TestCrossRun_CleanWindow(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	c := &CrossRunChecker{Store: st, Now: fixedNow(now)}

	res, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.StaleScore != 1.0 || res.RepeatedGateScore != 1.0 || res.ConflictingScore != 1.0 {
		t.Errorf("clean scores = (%.2f, %.2f, %.2f), want all 1.0",
			res.StaleScore, res.RepeatedGateScore, res.ConflictingScore)
	}
	if len(res.StaleItems) != 0 || len(res.RepeatedGates) != 0 || len(res.ConflictingOutcomes) != 0 {
		t.Errorf("clean window produced findings: %+v", res)
	}

	// Three eval rows persisted, all 1.0.
	scores, err := st.Eval.ListSince(context.Background(), now.Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(scores) != 3 {
		t.Errorf("got %d eval rows, want 3", len(scores))
	}
	for _, s := range scores {
		if s.SubjectKind != store.EvalSubjectCrossRun || s.JudgedBy != LoopCJudgedBy {
			t.Errorf("score %s: kind=%s judged_by=%s", s.Rubric, s.SubjectKind, s.JudgedBy)
		}
		if s.Score != 1.0 {
			t.Errorf("rubric %s: score=%.2f, want 1.0", s.Rubric, s.Score)
		}
	}
}

// TestCrossRun_StalePlansDetected seeds a queued backlog item with
// CreatedAt 6 days old and verifies the stale-plan score drops.
func TestCrossRun_StalePlansDetected(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// Two queued items: one fresh (today), one stale (6 days ago).
	if err := st.Backlog.Put(ctx, &store.BacklogItem{
		ID: "fresh", Title: "fresh", State: store.BacklogQueued, Priority: store.P3,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("put fresh: %v", err)
	}
	if err := st.Backlog.Put(ctx, &store.BacklogItem{
		ID: "stale", Title: "stale", State: store.BacklogQueued, Priority: store.P3,
		CreatedAt: now.Add(-6 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("put stale: %v", err)
	}

	c := &CrossRunChecker{Store: st, Now: fixedNow(now)}
	res, err := c.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := 0.5; res.StaleScore != want {
		t.Errorf("stale score = %.2f, want %.2f (1 of 2 stale)", res.StaleScore, want)
	}
	if len(res.StaleItems) != 1 || res.StaleItems[0] != "stale" {
		t.Errorf("stale items = %v, want [stale]", res.StaleItems)
	}
}

// TestCrossRun_RepeatedGateFailures seeds two pipeline runs each with a
// failure of the same gate, plus one isolated gate failure that should
// NOT be flagged (only 1 run).
func TestCrossRun_RepeatedGateFailures(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// Two runs in window with spec_conformance failures + one isolated.
	for _, runID := range []string{"r1", "r2"} {
		// Pipeline rows have a FK to backlog_items; seed both.
		if err := st.Backlog.Put(ctx, &store.BacklogItem{
			ID: runID + "-item", Title: "x", State: store.BacklogEscalated,
			Priority: store.P3, CreatedAt: now,
		}); err != nil {
			t.Fatalf("seed backlog %s: %v", runID, err)
		}
		if err := st.Pipeline.PutRun(ctx, &store.PipelineRun{
			ID:        runID,
			BacklogID: runID + "-item",
			Template:  "hive-default-pipeline",
			State:     store.PipelineEscalated,
			Attempts:  1,
			StartedAt: now.Add(-2 * 24 * time.Hour),
		}); err != nil {
			t.Fatalf("put run %s: %v", runID, err)
		}
		if err := st.Pipeline.PutGate(ctx, &store.GateOutcome{
			PipelineRunID: runID,
			AfterStage:    "implement",
			GateName:      "spec_conformance",
			Outcome:       store.GateOutcomeFail,
			Reasons:       []string{"divergent from spec"},
			JudgedBy:      "test",
			EvaluatedAt:   now.Add(-2 * 24 * time.Hour),
		}); err != nil {
			t.Fatalf("put gate %s: %v", runID, err)
		}
	}
	// Isolated 1-run failure on a different gate — should NOT be flagged.
	if err := st.Backlog.Put(ctx, &store.BacklogItem{
		ID: "r3-item", Title: "x", State: store.BacklogMerged,
		Priority: store.P3, CreatedAt: now,
	}); err != nil {
		t.Fatalf("seed r3 backlog: %v", err)
	}
	if err := st.Pipeline.PutRun(ctx, &store.PipelineRun{
		ID: "r3", BacklogID: "r3-item", Template: "x", State: store.PipelineDone,
		Attempts: 1, StartedAt: now.Add(-1 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("put r3: %v", err)
	}
	if err := st.Pipeline.PutGate(ctx, &store.GateOutcome{
		PipelineRunID: "r3", AfterStage: "tests",
		GateName: "isolated_gate", Outcome: store.GateOutcomeFail,
		JudgedBy: "test", EvaluatedAt: now.Add(-1 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("put gate r3: %v", err)
	}

	c := &CrossRunChecker{Store: st, Now: fixedNow(now)}
	res, err := c.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := res.RepeatedGates["spec_conformance"], 2; got != want {
		t.Errorf("spec_conformance count = %d, want %d", got, want)
	}
	if _, present := res.RepeatedGates["isolated_gate"]; present {
		t.Errorf("isolated_gate (1 run) should not be flagged: %v", res.RepeatedGates)
	}
	// 1 flaky gate → score 0.8.
	if want := 0.8; abs(res.RepeatedGateScore-want) > 1e-9 {
		t.Errorf("score = %.4f, want %.4f", res.RepeatedGateScore, want)
	}
}

// TestCrossRun_ConflictingOutcomes seeds two pipeline runs for the same
// backlog item ending in different terminal states (done + escalated).
func TestCrossRun_ConflictingOutcomes(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	if err := st.Backlog.Put(ctx, &store.BacklogItem{
		ID: "BL-1", Title: "x", State: store.BacklogEscalated,
		Priority: store.P3, CreatedAt: now,
	}); err != nil {
		t.Fatalf("seed BL-1: %v", err)
	}
	end := now.Add(-1 * 24 * time.Hour)
	if err := st.Pipeline.PutRun(ctx, &store.PipelineRun{
		ID: "r-done", BacklogID: "BL-1", Template: "x",
		State: store.PipelineDone, Attempts: 1,
		StartedAt: now.Add(-2 * 24 * time.Hour), EndedAt: &end,
	}); err != nil {
		t.Fatalf("put done: %v", err)
	}
	if err := st.Pipeline.PutRun(ctx, &store.PipelineRun{
		ID: "r-escalated", BacklogID: "BL-1", Template: "x",
		State: store.PipelineEscalated, Attempts: 2,
		StartedAt: now.Add(-3 * 24 * time.Hour), EndedAt: &end,
	}); err != nil {
		t.Fatalf("put escalated: %v", err)
	}
	// Non-terminal run for same item — should NOT count.
	if err := st.Pipeline.PutRun(ctx, &store.PipelineRun{
		ID: "r-running", BacklogID: "BL-1", Template: "x",
		State: store.PipelineImplementing, Attempts: 3,
		StartedAt: now,
	}); err != nil {
		t.Fatalf("put running: %v", err)
	}

	c := &CrossRunChecker{Store: st, Now: fixedNow(now)}
	res, err := c.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := res.ConflictingOutcomes["BL-1"]
	if len(got) != 2 {
		t.Errorf("conflict states for BL-1 = %v, want 2 distinct terminal states", got)
	}
	if want := 0.75; abs(res.ConflictingScore-want) > 1e-9 {
		t.Errorf("score = %.4f, want %.4f (1 conflict → 0.75)", res.ConflictingScore, want)
	}
}

// TestCrossRun_PersistsBreakdown confirms the eval row carries the
// machine-readable findings so the brief renderer can re-use them.
func TestCrossRun_PersistsBreakdown(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	if err := st.Backlog.Put(ctx, &store.BacklogItem{
		ID: "stale-1", Title: "x", State: store.BacklogQueued, Priority: store.P3,
		CreatedAt: now.Add(-10 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := &CrossRunChecker{Store: st, Now: fixedNow(now)}
	if _, err := c.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	scores, err := st.Eval.ListSince(ctx, now.Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var stale *store.EvalScore
	for _, s := range scores {
		if s.Rubric == LoopCRubricStalePlans {
			stale = s
			break
		}
	}
	if stale == nil {
		t.Fatalf("stale rubric row missing")
	}
	ids, ok := stale.Breakdown["stale_backlog_ids"].([]any)
	if !ok || len(ids) != 1 || ids[0] != "stale-1" {
		t.Errorf("breakdown.stale_backlog_ids = %v, want [stale-1]", stale.Breakdown["stale_backlog_ids"])
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
