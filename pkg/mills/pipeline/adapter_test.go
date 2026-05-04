package pipeline

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

func TestRunnerStarter_RoutesSingleSliceToRunner(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "h.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	item := &store.BacklogItem{
		ID:    "BL-SOLO",
		Title: "single slice item",
		State: store.BacklogQueued, Priority: store.P2,
	}
	if err := st.Backlog.Put(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	run := &store.PipelineRun{
		ID: "PIPE-SOLO", BacklogID: item.ID, Template: "x",
		State: store.PipelineQueued, Attempts: 1, StartedAt: time.Now(),
	}
	if err := st.Pipeline.PutRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	dispatched := int32(0)
	disp := workerFn(func(_ context.Context, _ JobContext) (StageOutput, error) {
		atomic.AddInt32(&dispatched, 1)
		return StageOutput{}, nil
	})
	r := New(st, newPassingGates(t), &fakeWorkerDispatch{w: disp}, nil)
	starter := NewRunnerStarter(r, nil)
	if err := starter.Start(context.Background(), run, item); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := st.Pipeline.GetRun(context.Background(), run.ID)
		if got != nil && (got.State == store.PipelineDone || got.State == store.PipelineEscalated) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := st.Pipeline.GetRun(context.Background(), run.ID)
	if got.State != store.PipelineDone {
		t.Errorf("state = %s, want done", got.State)
	}
	if atomic.LoadInt32(&dispatched) == 0 {
		t.Errorf("runner dispatcher should have been called")
	}
}

// fakeWorkerDispatch wraps a workerFn to satisfy WorkerDispatcher.
type fakeWorkerDispatch struct{ w Worker }

func (f *fakeWorkerDispatch) Dispatch(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem, stage Stage, prior map[string]StageOutput) (StageOutput, error) {
	return f.w.Run(ctx, JobContext{Run: run, Item: item, Stage: stage, Prior: prior})
}

func TestRunnerStarter_RoutesParallelSlicesThroughIntegrator(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "h.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	item := &store.BacklogItem{
		ID: "BL-PAR", Title: "fan out", State: store.BacklogQueued, Priority: store.P2,
		Slices: []store.Slice{
			{Name: "alpha", ParallelWith: []string{"beta"}},
			{Name: "beta", ParallelWith: []string{"alpha"}},
		},
	}
	if err := st.Backlog.Put(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	run := &store.PipelineRun{
		ID: "PIPE-PAR", BacklogID: item.ID, Template: "x",
		State: store.PipelineQueued, Attempts: 1, StartedAt: time.Now(),
	}
	if err := st.Pipeline.PutRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	disp := &fakeDispatcher{}
	r := New(st, newPassingGates(t), disp, nil)

	allocator := &fakeAllocator{}
	merger := &fakeMerger{sha: "deadbeef"}
	sub := &recordingSubRunner{store: st, settleMS: 5}
	itg := NewIntegrator(st, sub, allocator, merger)

	starter := NewRunnerStarter(r, itg)
	if err := starter.Start(context.Background(), run, item); err != nil {
		t.Fatalf("start: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := st.Pipeline.GetRun(context.Background(), run.ID)
		if got != nil && (got.State == store.PipelineDone || got.State == store.PipelineEscalated) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := st.Pipeline.GetRun(context.Background(), run.ID)
	if got.State != store.PipelineDone {
		t.Errorf("state = %s, want done after integrator+runner", got.State)
	}
	if len(merger.calls) != 1 {
		t.Errorf("merger should have been invoked once, got %d", len(merger.calls))
	}
	if len(allocator.allocated) != 2 {
		t.Errorf("expected 2 worktree allocations, got %d", len(allocator.allocated))
	}
}

func TestRunnerStarter_RejectsBadConfig(t *testing.T) {
	if err := (&RunnerStarter{}).Start(context.Background(), nil, nil); err == nil {
		t.Error("expected error for nil Runner")
	}
	r := &Runner{}
	s := NewRunnerStarter(r, nil)
	if err := s.Start(context.Background(), nil, &store.BacklogItem{ID: "x"}); err == nil {
		t.Error("expected error for nil run")
	}
	if err := s.Start(context.Background(), &store.PipelineRun{ID: "x"}, nil); err == nil {
		t.Error("expected error for nil item")
	}
}
