package pipeline

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// fakeAllocator returns deterministic worktree paths and counts allocate
// + release calls so tests can assert lifecycle balance.
type fakeAllocator struct {
	mu        sync.Mutex
	allocated []WorktreeRequest
	released  []WorktreeHandle
	failOn    string
}

func (f *fakeAllocator) Allocate(_ context.Context, req WorktreeRequest) (WorktreeHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allocated = append(f.allocated, req)
	if f.failOn == req.SliceName {
		return WorktreeHandle{}, fmt.Errorf("alloc fail %s", req.SliceName)
	}
	return WorktreeHandle{
		Path:   "/tmp/wt-" + req.BacklogID + "-" + req.SliceName,
		Branch: "feat/" + req.BacklogID + "/" + req.SliceName,
	}, nil
}

func (f *fakeAllocator) Release(_ context.Context, h WorktreeHandle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, h)
	return nil
}

// fakeMerger reports a canned outcome.
type fakeMerger struct {
	calls    []MergeBranchesRequest
	conflict bool
	files    []string
	sha      string
	err      error
}

func (m *fakeMerger) Merge(_ context.Context, req MergeBranchesRequest) (MergeBranchesResponse, error) {
	m.calls = append(m.calls, req)
	if m.err != nil {
		return MergeBranchesResponse{}, m.err
	}
	return MergeBranchesResponse{
		Conflict:        m.conflict,
		ConflictedFiles: m.files,
		IntegratedSHA:   m.sha,
	}, nil
}

// recordingSubRunner drives a sub-run by marking it Done in the store.
// It records the IDs and parallelism actually achieved.
type recordingSubRunner struct {
	mu       sync.Mutex
	store    *store.Store
	runs     []string
	maxLive  int32
	live     int32
	failFor  map[string]bool
	settleMS int
}

func (r *recordingSubRunner) Drive(ctx context.Context, run *store.PipelineRun, _ *store.BacklogItem) error {
	atomic.AddInt32(&r.live, 1)
	defer atomic.AddInt32(&r.live, -1)
	for {
		live := atomic.LoadInt32(&r.live)
		max := atomic.LoadInt32(&r.maxLive)
		if live > max && atomic.CompareAndSwapInt32(&r.maxLive, max, live) {
			break
		}
		if live <= max {
			break
		}
	}
	if r.settleMS > 0 {
		time.Sleep(time.Duration(r.settleMS) * time.Millisecond)
	}
	r.mu.Lock()
	r.runs = append(r.runs, run.ID)
	failed := r.failFor != nil && r.failFor[run.ID]
	r.mu.Unlock()

	now := time.Now()
	if failed {
		run.State = store.PipelineEscalated
		run.EndedAt = &now
	} else {
		run.State = store.PipelineDone
		run.EndedAt = &now
	}
	return r.store.Pipeline.PutRun(ctx, run)
}

func newIntegratorEnv(t *testing.T) (*store.Store, *store.PipelineRun, *store.BacklogItem) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "h.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	item := &store.BacklogItem{
		ID: "BL-PAR-1", Title: "fan-out test", State: store.BacklogQueued, Priority: store.P2,
		Slices: []store.Slice{
			{Name: "alpha", Files: []string{"a.go"}, ParallelWith: []string{"beta"}},
			{Name: "beta", Files: []string{"b.go"}, ParallelWith: []string{"alpha"}},
		},
	}
	if err := st.Backlog.Put(context.Background(), item); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}
	run := &store.PipelineRun{
		ID:        "PIPE-BL-PAR-1",
		BacklogID: item.ID,
		Template:  "mills-default-pipeline",
		State:     store.PipelineQueued,
		Attempts:  1,
		StartedAt: time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
	}
	if err := st.Pipeline.PutRun(context.Background(), run); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return st, run, item
}

func TestShouldFanOut(t *testing.T) {
	cases := []struct {
		name string
		item *store.BacklogItem
		want bool
	}{
		{"nil", nil, false},
		{"single slice", &store.BacklogItem{Slices: []store.Slice{{Name: "x"}}}, false},
		{"two slices but no parallel marker", &store.BacklogItem{Slices: []store.Slice{{Name: "a"}, {Name: "b"}}}, false},
		{"two slices with parallel", &store.BacklogItem{Slices: []store.Slice{{Name: "a", ParallelWith: []string{"b"}}, {Name: "b"}}}, true},
	}
	for _, c := range cases {
		if got := ShouldFanOut(c.item); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestIntegrator_HappyPath(t *testing.T) {
	st, run, item := newIntegratorEnv(t)
	alloc := &fakeAllocator{}
	merger := &fakeMerger{sha: "deadbeef"}
	sub := &recordingSubRunner{store: st, settleMS: 5}
	itg := NewIntegrator(st, sub, alloc, merger)

	if err := itg.Run(context.Background(), run, item); err != nil {
		t.Fatalf("integrator run: %v", err)
	}

	got, err := st.Pipeline.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("getrun: %v", err)
	}
	if got.State != store.PipelineMR {
		t.Errorf("state = %s, want mr", got.State)
	}
	if got.CurrentStage != "mr" {
		t.Errorf("current_stage = %q, want mr", got.CurrentStage)
	}
	if len(alloc.allocated) != 2 {
		t.Errorf("allocated = %d, want 2", len(alloc.allocated))
	}
	if len(alloc.released) != 2 {
		t.Errorf("released = %d, want 2 (every allocate must be paired with release)", len(alloc.released))
	}
	if len(merger.calls) != 1 {
		t.Fatalf("merger calls = %d, want 1", len(merger.calls))
	}
	if len(merger.calls[0].SliceBranches) != 2 {
		t.Errorf("slice branches = %v", merger.calls[0].SliceBranches)
	}
	if merger.calls[0].SliceBranches[0] >= merger.calls[0].SliceBranches[1] {
		t.Errorf("slice branches not sorted: %v", merger.calls[0].SliceBranches)
	}
}

func TestIntegrator_MergeConflictEscalates(t *testing.T) {
	st, run, item := newIntegratorEnv(t)
	itg := NewIntegrator(st, &recordingSubRunner{store: st}, &fakeAllocator{}, &fakeMerger{conflict: true, files: []string{"a.go"}})
	if err := itg.Run(context.Background(), run, item); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := st.Pipeline.GetRun(context.Background(), run.ID)
	if got.State != store.PipelineEscalated {
		t.Errorf("state = %s, want escalated", got.State)
	}
}

func TestIntegrator_SubRunFailureEscalates(t *testing.T) {
	st, run, item := newIntegratorEnv(t)
	failID := run.ID + "-alpha"
	sub := &recordingSubRunner{
		store:   st,
		failFor: map[string]bool{failID: true},
	}
	itg := NewIntegrator(st, sub, &fakeAllocator{}, &fakeMerger{})
	if err := itg.Run(context.Background(), run, item); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := st.Pipeline.GetRun(context.Background(), run.ID)
	if got.State != store.PipelineEscalated {
		t.Errorf("state = %s, want escalated", got.State)
	}
}

func TestIntegrator_AllocateFailureEscalates(t *testing.T) {
	st, run, item := newIntegratorEnv(t)
	itg := NewIntegrator(st, &recordingSubRunner{store: st}, &fakeAllocator{failOn: "beta"}, &fakeMerger{})
	if err := itg.Run(context.Background(), run, item); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := st.Pipeline.GetRun(context.Background(), run.ID)
	if got.State != store.PipelineEscalated {
		t.Errorf("state = %s, want escalated", got.State)
	}
}

func TestIntegrator_MaxParallelHonored(t *testing.T) {
	st, run, item := newIntegratorEnv(t)
	// 4 slices, MaxParallel=2 should see at most 2 concurrent sub-runs.
	item.Slices = []store.Slice{
		{Name: "s1", ParallelWith: []string{"s2", "s3", "s4"}},
		{Name: "s2", ParallelWith: []string{"s1"}},
		{Name: "s3", ParallelWith: []string{"s1"}},
		{Name: "s4", ParallelWith: []string{"s1"}},
	}
	if err := st.Backlog.Put(context.Background(), item); err != nil {
		t.Fatalf("update backlog: %v", err)
	}
	sub := &recordingSubRunner{store: st, settleMS: 30}
	itg := NewIntegrator(st, sub, &fakeAllocator{}, &fakeMerger{sha: "x"})
	itg.MaxParallel = 2
	if err := itg.Run(context.Background(), run, item); err != nil {
		t.Fatalf("run: %v", err)
	}
	if max := atomic.LoadInt32(&sub.maxLive); max > 2 {
		t.Errorf("max live = %d, want <= 2 (MaxParallel)", max)
	}
}

func TestIntegrator_NotConfiguredErrors(t *testing.T) {
	itg := &Integrator{}
	if err := itg.Run(context.Background(), &store.PipelineRun{}, &store.BacklogItem{}); !errors.Is(err, errors.New("integrator: not configured")) && err == nil {
		t.Error("expected not-configured error")
	}
}

func TestIntegrator_RejectsSingleSliceItem(t *testing.T) {
	st, run, item := newIntegratorEnv(t)
	item.Slices = []store.Slice{{Name: "only"}}
	if err := st.Backlog.Put(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	itg := NewIntegrator(st, &recordingSubRunner{store: st}, &fakeAllocator{}, &fakeMerger{})
	if err := itg.Run(context.Background(), run, item); err == nil {
		t.Error("expected error for single-slice item")
	}
}
