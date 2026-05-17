package mills

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

func TestKPIWriter_RecordWritesRollingSnapshot(t *testing.T) {
	env := newRecEnv(t, nil)
	ctx := context.Background()
	now := env.now

	if err := env.store.Backlog.Put(ctx, &store.BacklogItem{
		ID: "BACK-QUEUED", Title: "queued", State: store.BacklogQueued,
		Priority: store.P2, CreatedBy: "test",
	}); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}
	if err := env.store.Backlog.Put(ctx, &store.BacklogItem{
		ID: "BACK-DONE", Title: "done", State: store.BacklogMerged,
		Priority: store.P2, CreatedBy: "test",
	}); err != nil {
		t.Fatalf("seed done backlog: %v", err)
	}
	if err := env.store.Council.Put(ctx, &store.CouncilRun{
		ID: "COUNCIL-1", Trigger: store.CouncilTriggerManual,
		StartedAt: now.Add(-time.Hour), Outcome: store.CouncilOutcomeSuccess,
		CostFrontierUSD: 0.7, CostLocalUSD: 0.3,
	}); err != nil {
		t.Fatalf("seed council: %v", err)
	}
	if err := env.store.Pipeline.PutRun(ctx, &store.PipelineRun{
		ID: "PIPE-1", BacklogID: "BACK-DONE", Template: "mills-default",
		State: store.PipelineDone, StartedAt: now.Add(-30 * time.Minute),
		CostUSD: 2.5,
	}); err != nil {
		t.Fatalf("seed pipeline: %v", err)
	}
	if err := env.store.Pipeline.PutGate(ctx, &store.GateOutcome{
		PipelineRunID: "PIPE-1", GateName: "diff_size",
		Outcome: store.GateOutcomePass, EvaluatedAt: now.Add(-20 * time.Minute),
	}); err != nil {
		t.Fatalf("seed gate: %v", err)
	}
	if err := env.store.Eval.RecordScore(ctx, &store.EvalScore{
		SubjectKind: store.EvalSubjectPipelineRun, SubjectID: "PIPE-1",
		Rubric: "pipeline_outcome_v1", Score: 0.8,
		EvaluatedAt: now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("seed eval: %v", err)
	}

	writer := NewKPIWriter(env.store, env.policy)
	writer.Clock = func() time.Time { return now }
	writer.Windows = []time.Duration{kpiWindow1d}

	if err := writer.Record(ctx); err != nil {
		t.Fatalf("record: %v", err)
	}

	snap, err := env.store.KPI.Latest(ctx, int(kpiWindow1d.Seconds()))
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if snap.SnapshotAt != now {
		t.Fatalf("snapshot_at = %s, want %s", snap.SnapshotAt, now)
	}
	assertMetric(t, snap.Metrics, "queue_depth", float64(1))
	assertMetric(t, snap.Metrics, "active_pipeline_runs", float64(0))
	assertMetric(t, snap.Metrics, "council_runs", float64(1))
	assertMetric(t, snap.Metrics, "council_cost_usd", float64(1))
	assertMetric(t, snap.Metrics, "pipeline_runs", float64(1))
	assertMetric(t, snap.Metrics, "pipeline_merged_runs", float64(1))
	assertMetric(t, snap.Metrics, "pipeline_cost_usd", 2.5)
	assertMetric(t, snap.Metrics, "gate_pass_rate", float64(1))
	assertMetric(t, snap.Metrics, "eval_average_score", 0.8)
	if got, ok := snap.Metrics["policy_enabled"].(bool); !ok || !got {
		t.Fatalf("policy_enabled = %#v, want true", snap.Metrics["policy_enabled"])
	}
}

func assertMetric(t *testing.T, metrics map[string]any, key string, want float64) {
	t.Helper()
	got, ok := metrics[key].(float64)
	if !ok {
		t.Fatalf("%s = %#v, want float64", key, metrics[key])
	}
	if got != want {
		t.Fatalf("%s = %v, want %v", key, got, want)
	}
}

// TestKPIWriter_FrontendKeys_PopulatedWhenDataAvailable pins the
// five keys MillsKPIRow.svelte renders. The cards stay dark when
// the snapshot is missing any of them, so the writer must produce
// these whenever the underlying counts make them defined. Each
// metric has a comment in kpi_writer.go documenting its proxy
// definition; this test pins that contract end-to-end.
func TestKPIWriter_FrontendKeys_PopulatedWhenDataAvailable(t *testing.T) {
	env := newRecEnv(t, nil)
	ctx := context.Background()
	now := env.now

	// Two merged runs (one fast, one slow) and one escalated run so
	// every ratio is non-degenerate: mergedRuns=2, escalatedRuns=1,
	// total terminal=3, durations=[60s, 180s] for merged,
	// total pipelineCost = $2 + $3 + $0.5 = $5.50 (window-wide,
	// not merged-only), councilCost=$1.
	mustPutRun := func(id string, st store.PipelineState, started time.Time, dur time.Duration, cost float64) {
		t.Helper()
		backlogID := "BACK-" + id
		if err := env.store.Backlog.Put(ctx, &store.BacklogItem{
			ID: backlogID, Title: id, State: store.BacklogMerged,
			Priority: store.P2, CreatedBy: "test",
		}); err != nil {
			t.Fatalf("seed backlog %s: %v", backlogID, err)
		}
		end := started.Add(dur)
		var endPtr *time.Time
		if dur > 0 {
			endPtr = &end
		}
		if err := env.store.Pipeline.PutRun(ctx, &store.PipelineRun{
			ID: id, BacklogID: backlogID, Template: "mills-default",
			State: st, StartedAt: started, EndedAt: endPtr, CostUSD: cost,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	mustPutRun("PIPE-FAST", store.PipelineDone, now.Add(-2*time.Hour), 60*time.Second, 2.0)
	mustPutRun("PIPE-SLOW", store.PipelineDone, now.Add(-90*time.Minute), 180*time.Second, 3.0)
	mustPutRun("PIPE-ESC", store.PipelineEscalated, now.Add(-time.Hour), 30*time.Second, 0.5)

	if err := env.store.Council.Put(ctx, &store.CouncilRun{
		ID: "COUNCIL-1", Trigger: store.CouncilTriggerManual,
		StartedAt:       now.Add(-time.Hour),
		Outcome:         store.CouncilOutcomeSuccess,
		CostFrontierUSD: 0.7, CostLocalUSD: 0.3,
	}); err != nil {
		t.Fatalf("seed council: %v", err)
	}

	writer := NewKPIWriter(env.store, env.policy)
	writer.Clock = func() time.Time { return now }
	writer.Windows = []time.Duration{kpiWindow1d}
	if err := writer.Record(ctx); err != nil {
		t.Fatalf("record: %v", err)
	}
	snap, err := env.store.KPI.Latest(ctx, int(kpiWindow1d.Seconds()))
	if err != nil {
		t.Fatalf("latest: %v", err)
	}

	// cost_per_merged_pipeline_usd = $5.50 total window cost / 2 merged = $2.75.
	// Numerator is window-wide pipeline cost (includes escalated runs),
	// reflecting "$ spent per successful merge" including the cost of
	// failed work.
	assertMetric(t, snap.Metrics, "cost_per_merged_pipeline_usd", 2.75)
	// cost_per_merged_change_usd: today aliases per-pipeline
	assertMetric(t, snap.Metrics, "cost_per_merged_change_usd", 2.75)
	// auto_merge_rate = 2 done / (2 done + 1 escalated) = 0.6666...
	assertMetricCloseTo(t, snap.Metrics, "auto_merge_rate", 2.0/3.0, 1e-9)
	// regression_rate = 1 escalated / 3 terminal = 0.3333...
	assertMetricCloseTo(t, snap.Metrics, "regression_rate", 1.0/3.0, 1e-9)
	// council_roi = 2 merged / $1 council cost = 2.0
	assertMetric(t, snap.Metrics, "council_roi", 2.0)
	// slice_to_merge_p50_seconds: median of [60, 180] = 120
	assertMetric(t, snap.Metrics, "slice_to_merge_p50_seconds", 120.0)
}

// TestKPIWriter_FrontendKeys_OmittedWhenInsufficientData pins the
// negative half of the contract: when there's no data to compute
// a ratio, the key is omitted entirely so MillsKPIRow shows "—"
// rather than "$0" or "0%" (which would imply efficient operation
// when reality is "no activity yet").
func TestKPIWriter_FrontendKeys_OmittedWhenInsufficientData(t *testing.T) {
	env := newRecEnv(t, nil)
	ctx := context.Background()
	now := env.now

	writer := NewKPIWriter(env.store, env.policy)
	writer.Clock = func() time.Time { return now }
	writer.Windows = []time.Duration{kpiWindow1d}
	if err := writer.Record(ctx); err != nil {
		t.Fatalf("record: %v", err)
	}
	snap, err := env.store.KPI.Latest(ctx, int(kpiWindow1d.Seconds()))
	if err != nil {
		t.Fatalf("latest: %v", err)
	}

	for _, key := range []string{
		"cost_per_merged_change_usd",
		"cost_per_merged_pipeline_usd",
		"auto_merge_rate",
		"regression_rate",
		"council_roi",
		"slice_to_merge_p50_seconds",
	} {
		if _, present := snap.Metrics[key]; present {
			t.Errorf("%s present with no data; want omitted to render '—'", key)
		}
	}
}

// TestKPIWriter_P50_OddCountReturnsExactMiddle pins the odd-n
// median branch since the FrontendKeys test exercises only the
// even-n interpolated case.
func TestKPIWriter_P50_OddCountReturnsExactMiddle(t *testing.T) {
	env := newRecEnv(t, nil)
	ctx := context.Background()
	now := env.now

	for i, dur := range []time.Duration{30 * time.Second, 90 * time.Second, 240 * time.Second} {
		backlogID := fmt.Sprintf("BACK-%d", i)
		if err := env.store.Backlog.Put(ctx, &store.BacklogItem{
			ID: backlogID, Title: backlogID, State: store.BacklogMerged,
			Priority: store.P2, CreatedBy: "test",
		}); err != nil {
			t.Fatalf("seed backlog %d: %v", i, err)
		}
		end := now.Add(-time.Hour).Add(dur)
		if err := env.store.Pipeline.PutRun(ctx, &store.PipelineRun{
			ID: fmt.Sprintf("PIPE-%d", i), BacklogID: backlogID,
			Template: "mills-default", State: store.PipelineDone,
			StartedAt: now.Add(-time.Hour), EndedAt: &end, CostUSD: 1,
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	writer := NewKPIWriter(env.store, env.policy)
	writer.Clock = func() time.Time { return now }
	writer.Windows = []time.Duration{kpiWindow1d}
	if err := writer.Record(ctx); err != nil {
		t.Fatalf("record: %v", err)
	}
	snap, err := env.store.KPI.Latest(ctx, int(kpiWindow1d.Seconds()))
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	// Median of [30, 90, 240] = 90 (exact middle, no interpolation)
	assertMetric(t, snap.Metrics, "slice_to_merge_p50_seconds", 90.0)
}

func assertMetricCloseTo(t *testing.T, metrics map[string]any, key string, want, tol float64) {
	t.Helper()
	got, ok := metrics[key].(float64)
	if !ok {
		t.Fatalf("%s = %#v, want float64", key, metrics[key])
	}
	if diff := got - want; diff < -tol || diff > tol {
		t.Fatalf("%s = %v, want within %v of %v", key, got, tol, want)
	}
}
