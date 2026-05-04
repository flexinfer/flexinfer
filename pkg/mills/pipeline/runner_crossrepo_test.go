package pipeline

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// fakeCrossRepoIntegrator records calls and returns canned states.
type fakeCrossRepoIntegrator struct {
	waitState  store.CrossRepoState
	waitErr    error
	mergeState store.CrossRepoState
	mergeErr   error

	waitCalls  atomic.Int32
	mergeCalls atomic.Int32
}

func (f *fakeCrossRepoIntegrator) WaitForGreen(_ context.Context, _ *store.CrossRepoRun) (store.CrossRepoState, error) {
	f.waitCalls.Add(1)
	return f.waitState, f.waitErr
}

func (f *fakeCrossRepoIntegrator) AtomicMerge(_ context.Context, _ *store.CrossRepoRun) (store.CrossRepoState, error) {
	f.mergeCalls.Add(1)
	return f.mergeState, f.mergeErr
}

// seedCrossRun writes an open cross_repo_run row for the given backlog id.
func seedCrossRun(t *testing.T, st *store.Store, backlogID string) *store.CrossRepoRun {
	t.Helper()
	row := &store.CrossRepoRun{
		ID:                "CRR-" + backlogID,
		BacklogItemID:     backlogID,
		State:             store.CrossRepoOpen,
		AtomicityStrategy: "all_or_revert",
		Repos: []store.CrossRepoRepoEntry{
			{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x", MRIID: int64Ptr(42)},
			{ProjectID: 51, RepoName: "loom", Branch: "feat/x", MRIID: int64Ptr(99)},
		},
	}
	if err := st.CrossRepo.PutRun(context.Background(), row); err != nil {
		t.Fatalf("seed cross run: %v", err)
	}
	return row
}

func int64Ptr(v int64) *int64 { return &v }

func TestRunner_NoCrossRepoRow_TakesSingleRepoPath(t *testing.T) {
	// Sanity: no cross_repo_run for this backlog → single-repo path
	// (existing behaviour unchanged: ends in PipelineDone).
	st, run, item := newRunnerEnv(t)
	disp := &fakeDispatcher{}
	r := New(st, newPassingGates(t), disp, nil)
	if err := r.Drive(context.Background(), run, item); err != nil {
		t.Fatalf("drive: %v", err)
	}
	got, err := st.Pipeline.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("getrun: %v", err)
	}
	if got.State != store.PipelineDone {
		t.Errorf("state = %s, want done (single-repo path)", got.State)
	}
}

func TestRunner_CrossRepoRowWithoutIntegrator_Escalates(t *testing.T) {
	st, run, item := newRunnerEnv(t)
	seedCrossRun(t, st, item.ID)

	disp := &fakeDispatcher{}
	r := New(st, newPassingGates(t), disp, nil)
	// Intentionally do NOT set r.CrossRepoIntegrator.

	if err := r.Drive(context.Background(), run, item); err != nil {
		t.Fatalf("drive: %v", err)
	}
	got, err := st.Pipeline.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("getrun: %v", err)
	}
	if got.State != store.PipelineEscalated {
		t.Errorf("state = %s, want escalated", got.State)
	}
}

func TestRunner_CrossRepoRowMergedSuccess_PersistsAndMarksDone(t *testing.T) {
	st, run, item := newRunnerEnv(t)
	cross := seedCrossRun(t, st, item.ID)

	integ := &fakeCrossRepoIntegrator{
		waitState:  store.CrossRepoGatesGreen,
		mergeState: store.CrossRepoMerged,
	}
	disp := &fakeDispatcher{}
	r := New(st, newPassingGates(t), disp, nil)
	r.CrossRepoIntegrator = integ

	if err := r.Drive(context.Background(), run, item); err != nil {
		t.Fatalf("drive: %v", err)
	}

	if integ.waitCalls.Load() != 1 {
		t.Errorf("WaitForGreen calls = %d, want 1", integ.waitCalls.Load())
	}
	if integ.mergeCalls.Load() != 1 {
		t.Errorf("AtomicMerge calls = %d, want 1", integ.mergeCalls.Load())
	}

	gotPipe, err := st.Pipeline.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("getrun: %v", err)
	}
	if gotPipe.State != store.PipelineDone {
		t.Errorf("pipeline state = %s, want done", gotPipe.State)
	}
	gotCross, err := st.CrossRepo.GetRun(context.Background(), cross.ID)
	if err != nil {
		t.Fatalf("get cross run: %v", err)
	}
	if gotCross.State != store.CrossRepoMerged {
		t.Errorf("cross state = %s, want merged", gotCross.State)
	}

	// Single-repo dispatcher must NOT have been called: the cross-repo
	// branch short-circuits the stage loop entirely.
	if calls := disp.callsList(); len(calls) != 0 {
		t.Errorf("dispatcher calls = %v, want none on cross-repo path", calls)
	}
}

func TestRunner_CrossRepoRunReverted_Escalates(t *testing.T) {
	st, run, item := newRunnerEnv(t)
	cross := seedCrossRun(t, st, item.ID)

	integ := &fakeCrossRepoIntegrator{
		waitState:  store.CrossRepoGatesGreen,
		mergeState: store.CrossRepoReverted,
		mergeErr:   errors.New("merge failed on loom"),
	}
	r := New(st, newPassingGates(t), &fakeDispatcher{}, nil)
	r.CrossRepoIntegrator = integ

	if err := r.Drive(context.Background(), run, item); err != nil {
		t.Fatalf("drive: %v", err)
	}
	gotPipe, err := st.Pipeline.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("getrun: %v", err)
	}
	if gotPipe.State != store.PipelineEscalated {
		t.Errorf("pipeline state = %s, want escalated", gotPipe.State)
	}
	gotCross, err := st.CrossRepo.GetRun(context.Background(), cross.ID)
	if err != nil {
		t.Fatalf("get cross run: %v", err)
	}
	if gotCross.State != store.CrossRepoReverted {
		t.Errorf("cross state = %s, want reverted (persisted before escalation)", gotCross.State)
	}
}

func TestRunner_CrossRepoWaitForGreenTimeout_Escalates(t *testing.T) {
	st, run, item := newRunnerEnv(t)
	cross := seedCrossRun(t, st, item.ID)

	integ := &fakeCrossRepoIntegrator{
		waitState: store.CrossRepoFailed,
		waitErr:   errors.New("timeout waiting on repo loom"),
	}
	r := New(st, newPassingGates(t), &fakeDispatcher{}, nil)
	r.CrossRepoIntegrator = integ

	if err := r.Drive(context.Background(), run, item); err != nil {
		t.Fatalf("drive: %v", err)
	}
	if integ.mergeCalls.Load() != 0 {
		t.Errorf("AtomicMerge should not be called after WaitForGreen failure")
	}
	gotPipe, _ := st.Pipeline.GetRun(context.Background(), run.ID)
	if gotPipe.State != store.PipelineEscalated {
		t.Errorf("pipeline state = %s, want escalated", gotPipe.State)
	}
	gotCross, _ := st.CrossRepo.GetRun(context.Background(), cross.ID)
	if gotCross.State != store.CrossRepoFailed {
		t.Errorf("cross state = %s, want failed", gotCross.State)
	}
}

func TestRunner_CrossRepoTerminalNonMerged_Escalates(t *testing.T) {
	// AtomicMerge returns no error but a non-merged terminal state — runner
	// must still escalate rather than mark the run done.
	st, run, item := newRunnerEnv(t)
	seedCrossRun(t, st, item.ID)

	integ := &fakeCrossRepoIntegrator{
		waitState:  store.CrossRepoGatesGreen,
		mergeState: store.CrossRepoFailed,
	}
	r := New(st, newPassingGates(t), &fakeDispatcher{}, nil)
	r.CrossRepoIntegrator = integ

	if err := r.Drive(context.Background(), run, item); err != nil {
		t.Fatalf("drive: %v", err)
	}
	gotPipe, _ := st.Pipeline.GetRun(context.Background(), run.ID)
	if gotPipe.State != store.PipelineEscalated {
		t.Errorf("pipeline state = %s, want escalated", gotPipe.State)
	}
}
