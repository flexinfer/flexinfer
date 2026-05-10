package mills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// recordingStarter captures pipeline-start invocations for assertion.
type recordingStarter struct {
	mu    sync.Mutex
	runs  []*store.PipelineRun
	items []*store.BacklogItem
	fail  error
}

func (s *recordingStarter) Start(_ context.Context, run *store.PipelineRun, item *store.BacklogItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.runs = append(s.runs, run)
	s.items = append(s.items, item)
	return nil
}

func (s *recordingStarter) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runs)
}

// recTestEnv wires a real SQLite store + a SkipWatch policy manager + a
// fake starter for fast in-process reconciler tests.
type recTestEnv struct {
	store   *store.Store
	policy  *PolicyManager
	starter *recordingStarter
	rec     *Reconciler
	now     time.Time
}

func newRecEnv(t *testing.T, policyMutator func(*Policy)) *recTestEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "h.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Persist a baseline policy file so PolicyManager can load it. We use
	// SkipWatch so tests don't depend on fsnotify timing.
	p := Default()
	on := true
	p.Enabled = &on
	if policyMutator != nil {
		policyMutator(p)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("policy: %v", err)
	}
	policyPath := filepath.Join(dir, "policy.yaml")
	writePolicyYAMLForTest(t, policyPath, p)
	pm, err := NewPolicyManager(context.Background(), policyPath, PolicyManagerOptions{SkipWatch: true})
	if err != nil {
		t.Fatalf("policy mgr: %v", err)
	}
	t.Cleanup(func() { _ = pm.Close() })

	starter := &recordingStarter{}
	rec := NewReconciler(st, pm, NewBudget(pm, NewStoreBudgetReader(st)), starter)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	rec.Clock = func() time.Time { return now }

	return &recTestEnv{store: st, policy: pm, starter: starter, rec: rec, now: now}
}

// writePolicyYAMLForTest is a tiny YAML writer that gets us a parseable
// policy file without pulling yaml.v3 marshaling into the test surface.
// We rely on the fixtureV1 string from policy_test.go; this helper just
// drops a known-valid file at path.
func writePolicyYAMLForTest(t *testing.T, path string, p *Policy) {
	t.Helper()
	body := fixtureV1
	if p.Enabled != nil && !*p.Enabled {
		body = "version: 1\nenabled: false\n" +
			"budgets:\n  council:  { max_usd_per_run: 1, max_usd_per_day: 1 }\n  pipeline: { max_usd_per_run: 1, max_usd_per_day: 1 }\n" +
			"pipeline:\n  retry: { max_attempts: 1, cooldown_seconds: 0 }\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
}

// ----- Tests -----

func TestReconciler_PolicyDisabledShortCircuits(t *testing.T) {
	env := newRecEnv(t, func(p *Policy) {
		off := false
		p.Enabled = &off
	})
	res, err := env.rec.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.SkipReason != "policy disabled" {
		t.Errorf("expected policy-disabled skip, got %+v", res)
	}
	if env.starter.calls() != 0 {
		t.Errorf("starter should not be invoked when policy is off")
	}
}

func TestReconciler_AutonomyGateBlocksStarts(t *testing.T) {
	env := newRecEnv(t, nil)
	env.rec.AutonomyGate = func(context.Context) (bool, []string) {
		return false, []string{"repo_root: .loom directory is missing under repo root"}
	}
	ctx := context.Background()

	item := &store.BacklogItem{
		ID:        "MILLS-GATED",
		Title:     "blocked by capability",
		State:     store.BacklogQueued,
		Priority:  store.P2,
		CreatedBy: "test",
	}
	if err := env.store.Backlog.Put(ctx, item); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := env.rec.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.SkipReason != "autonomy blocked" {
		t.Fatalf("skip reason: got %q want autonomy blocked", res.SkipReason)
	}
	if env.starter.calls() != 0 {
		t.Fatalf("starter calls: got %d want 0", env.starter.calls())
	}
	got, err := env.store.Backlog.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("read backlog: %v", err)
	}
	if got.State != store.BacklogQueued {
		t.Fatalf("backlog state: got %q want %q", got.State, store.BacklogQueued)
	}
	runs, err := env.store.Pipeline.ListByBacklog(ctx, item.ID)
	if err != nil {
		t.Fatalf("read runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("pipeline runs: got %d want 0", len(runs))
	}
}

func TestReconciler_StartsQueuedItem(t *testing.T) {
	env := newRecEnv(t, nil)
	ctx := context.Background()

	item := &store.BacklogItem{
		ID:        "MILLS-1",
		Title:     "first",
		State:     store.BacklogQueued,
		Priority:  store.P2,
		Slices:    []store.Slice{{Name: "x", Files: []string{"a.go"}}},
		Budget:    store.Budget{MaxCostUSD: 1.0, MaxTurns: 30},
		CreatedBy: "test",
	}
	if err := env.store.Backlog.Put(ctx, item); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := env.rec.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.Started != 1 {
		t.Errorf("expected 1 started, got %+v", res)
	}
	if env.starter.calls() != 1 {
		t.Errorf("starter not invoked")
	}

	got, _ := env.store.Backlog.Get(ctx, item.ID)
	if got.State != store.BacklogRunning {
		t.Errorf("backlog item not transitioned: %v", got.State)
	}
	runs, _ := env.store.Pipeline.ListByBacklog(ctx, item.ID)
	if len(runs) != 1 {
		t.Errorf("pipeline run not persisted; got %d", len(runs))
	}
}

func TestReconciler_DefersOnUnmetDependency(t *testing.T) {
	env := newRecEnv(t, nil)
	ctx := context.Background()

	parent := &store.BacklogItem{
		ID: "MILLS-PARENT", Title: "parent", State: store.BacklogQueued,
		Priority: store.P2, CreatedBy: "test",
	}
	child := &store.BacklogItem{
		ID: "MILLS-CHILD", Title: "child", State: store.BacklogQueued,
		Priority: store.P2, CreatedBy: "test",
		Dependencies: []string{parent.ID},
	}
	if err := env.store.Backlog.Put(ctx, parent); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if err := env.store.Backlog.Put(ctx, child); err != nil {
		t.Fatalf("seed child: %v", err)
	}

	res, err := env.rec.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	// Parent should start; child should defer because parent isn't merged.
	if res.Started != 1 || res.Deferred != 1 {
		t.Errorf("expected started=1 deferred=1, got %+v", res)
	}

	// Mark parent merged + retry. Child must now start.
	parent.State = store.BacklogMerged
	if err := env.store.Backlog.Put(ctx, parent); err != nil {
		t.Fatalf("merge parent: %v", err)
	}
	res2, err := env.rec.Tick(ctx)
	if err != nil {
		t.Fatalf("tick2: %v", err)
	}
	if res2.Started != 1 {
		t.Errorf("expected child to start after parent merged, got %+v", res2)
	}
}

func TestReconciler_RespectsHumanReviewPolicy(t *testing.T) {
	env := newRecEnv(t, nil)
	ctx := context.Background()

	item := &store.BacklogItem{
		ID: "MILLS-HR", Title: "needs human", State: store.BacklogQueued,
		Priority: store.P2, CreatedBy: "test",
		Policy: store.ItemPolicy{RequireHumanReview: true},
	}
	if err := env.store.Backlog.Put(ctx, item); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := env.rec.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.Skipped != 1 || res.Started != 0 {
		t.Errorf("expected skipped=1 started=0, got %+v", res)
	}
	if env.starter.calls() != 0 {
		t.Errorf("starter must not run for human-review items")
	}
}

func TestReconciler_StarterFailureReverts(t *testing.T) {
	env := newRecEnv(t, nil)
	env.starter.fail = errors.New("boom")
	ctx := context.Background()

	item := &store.BacklogItem{
		ID: "MILLS-FAIL", Title: "fail", State: store.BacklogQueued,
		Priority: store.P2, CreatedBy: "test",
		Slices: []store.Slice{{Name: "x", Files: []string{"a.go"}}},
		Budget: store.Budget{MaxCostUSD: 1},
	}
	if err := env.store.Backlog.Put(ctx, item); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, _ := env.rec.Tick(ctx)
	// Starter failures surface as deferred + errored — the reconciler's
	// per-item error counts roll into Errored, not Started.
	if res.Errored != 1 {
		t.Errorf("expected errored=1, got %+v", res)
	}
}

func TestReconciler_TickIsAuditable(t *testing.T) {
	env := newRecEnv(t, nil)
	ctx := context.Background()

	if _, err := env.rec.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	events, err := env.store.Events.ListSince(ctx, env.now.Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var sawTick bool
	for _, e := range events {
		if e.Kind == "reconciler.tick" {
			sawTick = true
			break
		}
	}
	if !sawTick {
		t.Errorf("expected a reconciler.tick event in the audit log")
	}
}

// ----- Scheduler -----

func TestScheduler_RunStopsOnContextCancel(t *testing.T) {
	env := newRecEnv(t, nil)
	sch := NewScheduler(env.rec)
	sch.Interval = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sch.Run(ctx) }()

	time.Sleep(120 * time.Millisecond) // let at least 2 ticks fire
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("scheduler returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not exit within 2s of cancel")
	}
}

func TestScheduler_StopMethodEndsRun(t *testing.T) {
	env := newRecEnv(t, nil)
	sch := NewScheduler(env.rec)
	sch.Interval = 50 * time.Millisecond

	done := make(chan error, 1)
	go func() { done <- sch.Run(context.Background()) }()
	time.Sleep(80 * time.Millisecond)
	sch.Stop()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not exit within 2s of Stop()")
	}
}

// TestReconciler_PicksUpQueuedSubrun pins the slice-6.2 dispatcher
// pickup contract: a pipeline_run row in state=queued with a non-null
// parent_run_id and attempts=0 (i.e. created by recursion.SubrunGuard
// but never started) is picked up by the next reconcile tick and
// handed to PipelineStarter — exactly the way a fresh-from-backlog
// run is. The starter sees the same Run + Item shape, so the runner
// downstream needs no recursion-specific branch.
func TestReconciler_PicksUpQueuedSubrun(t *testing.T) {
	env := newRecEnv(t, nil)
	ctx := context.Background()

	// Seed a parent backlog item + parent pipeline run.
	parentItem := &store.BacklogItem{
		ID: "BACK-PARENT", Title: "parent", State: store.BacklogRunning,
		Priority: store.P2, CreatedBy: "test",
	}
	if err := env.store.Backlog.Put(ctx, parentItem); err != nil {
		t.Fatalf("seed parent backlog: %v", err)
	}
	parentRun := &store.PipelineRun{
		ID: "PIPE-P", BacklogID: "BACK-PARENT", Template: "mills-default",
		State: store.PipelineImplementing, StartedAt: env.now, Depth: 0,
	}
	if err := env.store.Pipeline.PutRun(ctx, parentRun); err != nil {
		t.Fatalf("seed parent run: %v", err)
	}

	// Seed a child backlog + a queued subrun row pointing at the
	// parent. This is what recursion.SubrunGuard.SubrunCreate would
	// have produced just before this tick — backlog state Running
	// because SubrunGuard claims the item to prevent the main
	// reconciler loop from double-picking.
	childItem := &store.BacklogItem{
		ID: "BACK-CHILD", Title: "child slice", State: store.BacklogRunning,
		Priority: store.P2, CreatedBy: "test",
	}
	if err := env.store.Backlog.Put(ctx, childItem); err != nil {
		t.Fatalf("seed child backlog: %v", err)
	}
	parentID := "PIPE-P"
	subrun := &store.PipelineRun{
		ID: "PIPE-C", BacklogID: "BACK-CHILD", Template: "mills-default",
		State: store.PipelineQueued, StartedAt: env.now,
		Depth: 1, ParentRunID: &parentID,
	}
	if err := env.store.Pipeline.CreateSubrun(ctx, subrun); err != nil {
		t.Fatalf("seed subrun: %v", err)
	}

	res, err := env.rec.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	// Tick should have invoked the starter once — for the subrun.
	// (No queued backlog items, so the main loop is a no-op.)
	if env.starter.calls() != 1 {
		t.Fatalf("starter calls: got %d want 1", env.starter.calls())
	}
	got := env.starter.runs[0]
	if got.ID != "PIPE-C" {
		t.Errorf("starter run id: got %q want PIPE-C", got.ID)
	}
	if got.Depth != 1 {
		t.Errorf("starter run depth: got %d want 1", got.Depth)
	}
	gotItem := env.starter.items[0]
	if gotItem.ID != "BACK-CHILD" {
		t.Errorf("starter item id: got %q want BACK-CHILD", gotItem.ID)
	}
	// res.Started should reflect the subrun pickup.
	if res.Started != 1 {
		t.Errorf("res.Started: got %d want 1", res.Started)
	}
	if res.Errored != 0 {
		t.Errorf("res.Errored: got %d want 0", res.Errored)
	}
}

// TestReconciler_SubrunPickupSurvivesStarterError pins that a single
// failing subrun start doesn't block the rest of the tick.
func TestReconciler_SubrunPickupSurvivesStarterError(t *testing.T) {
	env := newRecEnv(t, nil)
	ctx := context.Background()
	env.starter.fail = errors.New("simulated starter failure")

	if err := env.store.Backlog.Put(ctx, &store.BacklogItem{
		ID: "BACK-P", Title: "p", State: store.BacklogRunning,
		Priority: store.P2, CreatedBy: "test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.store.Pipeline.PutRun(ctx, &store.PipelineRun{
		ID: "PIPE-P", BacklogID: "BACK-P", Template: "mills-default",
		State: store.PipelineImplementing, StartedAt: env.now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.store.Backlog.Put(ctx, &store.BacklogItem{
		ID: "BACK-C", Title: "c", State: store.BacklogRunning,
		Priority: store.P2, CreatedBy: "test",
	}); err != nil {
		t.Fatal(err)
	}
	parentID := "PIPE-P"
	if err := env.store.Pipeline.CreateSubrun(ctx, &store.PipelineRun{
		ID: "PIPE-C", BacklogID: "BACK-C", Template: "mills-default",
		State: store.PipelineQueued, StartedAt: env.now,
		Depth: 1, ParentRunID: &parentID,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := env.rec.Tick(ctx)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.Errored != 1 {
		t.Errorf("expected one errored subrun start, got Errored=%d", res.Errored)
	}
}

func TestScheduler_DoubleRunErrors(t *testing.T) {
	env := newRecEnv(t, nil)
	sch := NewScheduler(env.rec)
	sch.Interval = time.Hour

	go func() { _ = sch.Run(context.Background()) }()
	time.Sleep(20 * time.Millisecond)

	if err := sch.Run(context.Background()); err == nil {
		t.Errorf("expected error on second Run()")
	}
	sch.Stop()
}
