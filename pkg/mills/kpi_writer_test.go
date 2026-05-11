package mills

import (
	"context"
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
