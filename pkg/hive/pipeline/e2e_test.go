package pipeline_test

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive"
	"github.com/crb2nu/loom/pkg/hive/eval"
	"github.com/crb2nu/loom/pkg/hive/gates"
	"github.com/crb2nu/loom/pkg/hive/pipeline"
	"github.com/crb2nu/loom/pkg/hive/store"
)

const policyFixture = `version: 1
enabled: true
budgets:
  council:  { max_usd_per_run: 10, max_usd_per_day: 100 }
  pipeline: { max_usd_per_run: 5,  max_usd_per_day: 50, max_concurrent_runs: 4, max_runs_per_day: 20 }
council:
  reviewers:
    - { name: skeptic,  model: claude }
    - { name: shipper,  model: codex }
    - { name: scout,    model: weaver }
  editor: { name: synthesist, model: claude }
pipeline:
  retry: { max_attempts: 3, cooldown_seconds: 0 }
  default_template: hive-default-pipeline
gates:
  diff_size:   { max_lines: 800 }
  scope:       { allow_paths_outside_slice: false }
  path_policy: { protected: [] }
  secret_scan: { enabled: true }
  commit_format:
    require_conventional: true
human_review:
  paths_requiring_review: []
  labels_requiring_review: []
`

// TestPipeline_E2E_QueuedItemMergesWithEvalRow exercises the full chain:
// reconciler → pipeline.Runner (via hive.PipelineStarter contract) →
// stage DAG with NoOpDispatcher → markDone → OnMerged →
// OutcomeAttributor → eval_scores row.
//
// Acceptance for slice 4.7: a backlog item with state=queued ends as
// pipeline_runs.state=done with mr_iid populated and a populated
// pipeline_outcome eval row.
func TestPipeline_E2E_QueuedItemMergesWithEvalRow(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "h.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	policyPath := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(policyPath, []byte(policyFixture), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	pm, err := hive.NewPolicyManager(context.Background(), policyPath, hive.PolicyManagerOptions{SkipWatch: true})
	if err != nil {
		t.Fatalf("policy mgr: %v", err)
	}
	t.Cleanup(func() { _ = pm.Close() })

	budget := hive.NewBudget(pm, hive.NewStoreBudgetReader(st))

	// Build the runner with NoOp dispatcher + default deterministic gates.
	gateRegistry := gates.Default()
	runner := pipeline.New(st, gateRegistry, &pipeline.NoOpDispatcher{MRIID: 4242}, pm)

	// Hook the OutcomeAttributor so the run produces an eval row on merge.
	attributor := eval.NewOutcomeAttributor(st)
	runner.OnMerged = attributor.OnMerged

	rec := hive.NewReconciler(st, pm, budget, runner)

	// Seed a queued backlog item.
	item := &store.BacklogItem{
		ID:       "BL-E2E-1",
		Title:    "end-to-end pipeline smoke",
		State:    store.BacklogQueued,
		Priority: store.P2,
		Budget: store.Budget{
			MaxCostUSD:         1.0,
			MaxPipelineMinutes: 60,
		},
	}
	if err := st.Backlog.Put(context.Background(), item); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}

	res, err := rec.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.Started != 1 {
		t.Fatalf("expected 1 started, got %+v", res)
	}

	// Wait for the runner goroutine to drive the pipeline to terminal state.
	deadline := time.Now().Add(5 * time.Second)
	var donePR *store.PipelineRun
	for time.Now().Before(deadline) {
		runs, err := st.Pipeline.ListByBacklog(context.Background(), item.ID)
		if err != nil {
			t.Fatalf("list runs: %v", err)
		}
		if len(runs) > 0 && (runs[0].State == store.PipelineDone || runs[0].State == store.PipelineEscalated) {
			donePR = runs[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if donePR == nil {
		t.Fatalf("pipeline run did not reach terminal state in time")
	}
	if donePR.State != store.PipelineDone {
		t.Fatalf("pipeline state = %s, want done", donePR.State)
	}
	if donePR.MRIID == nil || *donePR.MRIID != 4242 {
		t.Errorf("mr_iid = %v, want 4242 (from NoOpDispatcher)", donePR.MRIID)
	}

	// Eval row is written by the OnMerged hook synchronously inside
	// markDone — but markDone runs in a goroutine spawned by
	// Runner.Start, so there's a small window between the
	// pipeline_runs.state→done write and the eval_scores insert. Poll
	// briefly for the row before asserting.
	var scores []*store.EvalScore
	evalDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(evalDeadline) {
		scores, err = st.Eval.LatestPerSubject(context.Background(), store.EvalSubjectPipelineRun, donePR.ID)
		if err != nil {
			t.Fatalf("read eval: %v", err)
		}
		if len(scores) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(scores) != 1 {
		t.Fatalf("expected 1 eval row, got %d", len(scores))
	}
	sc := scores[0]
	if sc.Rubric != eval.PipelineOutcomeRubric {
		t.Errorf("rubric = %s, want %s", sc.Rubric, eval.PipelineOutcomeRubric)
	}
	if sc.Score <= 0 || sc.Score > 1 {
		t.Errorf("score = %f, want in (0,1]", sc.Score)
	}
	if v, ok := sc.Breakdown["mr_iid"].(float64); !ok || int64(v) != 4242 {
		t.Errorf("breakdown.mr_iid = %v, want 4242", sc.Breakdown["mr_iid"])
	}
}

// TestPipeline_E2E_BudgetExhaustionDefersStart shows the reconciler
// integration honors hive.Budget so a budget-exhausted item does not
// reach the runner — slice 4.7 acceptance for max_runs_per_day caps.
func TestPipeline_E2E_BudgetExhaustionDefersStart(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "h.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Tighten the policy so a $5 item fails per-run cap.
	tighter := policyFixture
	tighter = replaceFirst(tighter, "max_usd_per_run: 5,",
		"max_usd_per_run: 0.01,")
	policyPath := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(policyPath, []byte(tighter), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	pm, err := hive.NewPolicyManager(context.Background(), policyPath, hive.PolicyManagerOptions{SkipWatch: true})
	if err != nil {
		t.Fatalf("policy mgr: %v", err)
	}
	t.Cleanup(func() { _ = pm.Close() })
	budget := hive.NewBudget(pm, hive.NewStoreBudgetReader(st))

	startCount := int32(0)
	runner := starterFn(func(_ context.Context, _ *store.PipelineRun, _ *store.BacklogItem) error {
		atomic.AddInt32(&startCount, 1)
		return nil
	})
	rec := hive.NewReconciler(st, pm, budget, runner)

	item := &store.BacklogItem{
		ID: "BL-BUDGET-1", Title: "expensive", State: store.BacklogQueued, Priority: store.P2,
		Budget: store.Budget{MaxCostUSD: 5.0, MaxPipelineMinutes: 60},
	}
	if err := st.Backlog.Put(context.Background(), item); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}

	res, err := rec.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.Deferred != 1 {
		t.Errorf("expected 1 deferred, got %+v", res)
	}
	if atomic.LoadInt32(&startCount) != 0 {
		t.Errorf("starter should not have been called when budget rejects the item")
	}
}

// starterFn adapts a function into hive.PipelineStarter.
type starterFn func(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error

func (f starterFn) Start(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error {
	return f(ctx, run, item)
}

// replaceFirst is a tiny local helper so the test doesn't pull in regexp.
func replaceFirst(s, old, new string) string {
	idx := indexOf(s, old)
	if idx < 0 {
		return s
	}
	return s[:idx] + new + s[idx+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
