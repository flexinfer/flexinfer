package pipeline

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/gates"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// fakeDispatcher records every Dispatch call and returns canned outputs
// keyed by stage id. A FailFor map causes the dispatcher to error N times
// for a given stage before falling through to a Success output.
type fakeDispatcher struct {
	mu      sync.Mutex
	canned  map[string]StageOutput
	calls   []string
	failFor map[string]int // stage id -> remaining failures
	err     error
}

func (f *fakeDispatcher) Dispatch(_ context.Context, _ *store.PipelineRun, _ *store.BacklogItem, stage Stage, _ map[string]StageOutput) (StageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, stage.ID)
	if f.err != nil {
		return StageOutput{}, f.err
	}
	if f.failFor != nil {
		if n := f.failFor[stage.ID]; n > 0 {
			f.failFor[stage.ID] = n - 1
			return StageOutput{}, fmt.Errorf("dispatch fail: %s", stage.ID)
		}
	}
	if out, ok := f.canned[stage.ID]; ok {
		return out, nil
	}
	return StageOutput{CostUSD: 0.01}, nil
}

func (f *fakeDispatcher) callsList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

type resumeAwareDispatcher struct {
	gotResume string
}

func (d *resumeAwareDispatcher) Dispatch(ctx context.Context, _ *store.PipelineRun, _ *store.BacklogItem, stage Stage, _ map[string]StageOutput) (StageOutput, error) {
	d.gotResume = resumeSpawnIDFromContext(ctx)
	if d.gotResume == "" {
		return StageOutput{}, fmt.Errorf("missing resume spawn id for %s", stage.ID)
	}
	return StageOutput{SpawnID: d.gotResume}, nil
}

type acceptedThenInterruptedDispatcher struct{}

func (d *acceptedThenInterruptedDispatcher) Dispatch(ctx context.Context, _ *store.PipelineRun, _ *store.BacklogItem, stage Stage, _ map[string]StageOutput) (StageOutput, error) {
	record := stageAcceptRecorderFromContext(ctx)
	if record == nil {
		return StageOutput{}, fmt.Errorf("missing accept recorder for %s", stage.ID)
	}
	if err := record("spawn-accepted"); err != nil {
		return StageOutput{}, err
	}
	return StageOutput{}, fmt.Errorf("poll interrupted")
}

type blockingDispatcher struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (d *blockingDispatcher) Dispatch(_ context.Context, _ *store.PipelineRun, _ *store.BacklogItem, stage Stage, _ map[string]StageOutput) (StageOutput, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	d.once.Do(func() { close(d.started) })
	<-d.release
	return StageOutput{SpawnID: "spawn-blocking"}, nil
}

func (d *blockingDispatcher) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

// alwaysFailGate is a gate that always returns Pass=false; used to drive
// the gate-fail retry path.
type alwaysFailGate struct{ name string }

func (g *alwaysFailGate) Name() string { return g.name }
func (g *alwaysFailGate) Evaluate(_ context.Context, _ gates.StageInput) (gates.Outcome, error) {
	return gates.Outcome{Pass: false, Reasons: []string{"forced fail"}, JudgedBy: "go"}, nil
}

// alwaysPassGate trivially passes.
type alwaysPassGate struct{ name string }

func (g *alwaysPassGate) Name() string { return g.name }
func (g *alwaysPassGate) Evaluate(_ context.Context, _ gates.StageInput) (gates.Outcome, error) {
	return gates.Outcome{Pass: true, JudgedBy: "go"}, nil
}

func newRunnerEnv(t *testing.T) (*store.Store, *store.PipelineRun, *store.BacklogItem) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "h.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	item := &store.BacklogItem{
		ID:       "BL-TEST-1",
		Title:    "test backlog item",
		State:    store.BacklogQueued,
		Priority: store.P2,
	}
	if err := st.Backlog.Put(ctx, item); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	run := &store.PipelineRun{
		ID:        "PIPE-BL-TEST-1-0",
		BacklogID: item.ID,
		Template:  "mills-default-pipeline",
		State:     store.PipelineQueued,
		Attempts:  1,
		StartedAt: now,
	}
	if err := st.Pipeline.PutRun(ctx, run); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return st, run, item
}

func newPassingGates(t *testing.T) *gates.Registry {
	t.Helper()
	r := gates.NewRegistry()
	for _, name := range []string{"diff_size", "scope", "path_policy", "secret_scan", "commit_format"} {
		r.Register(&alwaysPassGate{name: name})
	}
	return r
}

func TestRunner_DriveHappyPath(t *testing.T) {
	st, run, item := newRunnerEnv(t)
	disp := &fakeDispatcher{canned: map[string]StageOutput{
		"implement": {
			CostUSD:        0.10,
			FilesChanged:   []string{"foo.go"},
			LinesAdded:     5,
			LinesRemoved:   1,
			DiffPatch:      []byte("diff --git a/foo.go b/foo.go\n+x\n"),
			CommitMessages: []string{"feat: x"},
		},
		"mr":    {CostUSD: 0.05, MRIID: 42},
		"merge": {CostUSD: 0.03, MergedSHA: "abcdef"},
	}}
	r := New(st, newPassingGates(t), disp, nil)
	r.Clock = func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) }
	if err := r.Drive(context.Background(), run, item); err != nil {
		t.Fatalf("drive: %v", err)
	}
	got, err := st.Pipeline.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("getrun: %v", err)
	}
	if got.State != store.PipelineDone {
		t.Errorf("state = %s, want done", got.State)
	}
	gotItem, err := st.Backlog.Get(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("get backlog: %v", err)
	}
	if gotItem.State != store.BacklogMerged {
		t.Errorf("backlog state = %s, want merged", gotItem.State)
	}
	if got.MRIID == nil || *got.MRIID != 42 {
		t.Errorf("mr_iid = %v, want 42", got.MRIID)
	}
	// Expected non-gate stage calls in order (every non-gate stage exactly once).
	want := []string{"plan_slice", "research", "implement", "tests", "pr_self_review", "mr", "ci_watch", "merge", "cleanup"}
	gotCalls := disp.callsList()
	if len(gotCalls) != len(want) {
		t.Fatalf("calls = %v, want %v", gotCalls, want)
	}
	for i := range want {
		if gotCalls[i] != want[i] {
			t.Errorf("calls[%d] = %s, want %s", i, gotCalls[i], want[i])
		}
	}
	stages, err := st.Pipeline.ListStages(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list stages: %v", err)
	}
	if len(stages) != len(want) {
		t.Errorf("stage_results rows = %d, want %d", len(stages), len(want))
	}
	for _, sr := range stages {
		if sr.Outcome == nil || *sr.Outcome != store.StageOutcomeSuccess {
			t.Errorf("stage %s outcome = %v, want success", sr.Stage, sr.Outcome)
		}
	}
}

func TestRunner_GateFailRetriesUpstreamThenEscalates(t *testing.T) {
	st, run, item := newRunnerEnv(t)
	disp := &fakeDispatcher{}
	// Register one gate that always fails so post_implement_gate fails.
	gr := gates.NewRegistry()
	gr.Register(&alwaysFailGate{name: "diff_size"})

	r := New(st, gr, disp, nil)
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
	gotItem, err := st.Backlog.Get(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("get backlog: %v", err)
	}
	if gotItem.State != store.BacklogEscalated {
		t.Errorf("backlog state = %s, want escalated", gotItem.State)
	}
	// Implement should have been called maxAttempts (3) times before
	// escalation; plan_slice + research run once each.
	implementCalls := 0
	for _, c := range disp.callsList() {
		if c == "implement" {
			implementCalls++
		}
	}
	if implementCalls != 3 {
		t.Errorf("implement calls = %d, want 3 (maxAttempts)", implementCalls)
	}
	gateRows, err := st.Pipeline.ListGates(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list gates: %v", err)
	}
	if len(gateRows) == 0 {
		t.Errorf("expected gate_outcomes rows, got 0")
	}
	for _, g := range gateRows {
		if g.Outcome != store.GateOutcomeFail {
			t.Errorf("gate %s outcome = %s, want fail", g.GateName, g.Outcome)
		}
	}
}

func TestRunner_StageErrorRetriesThenSucceeds(t *testing.T) {
	st, run, item := newRunnerEnv(t)
	// First implement attempt fails; second succeeds.
	disp := &fakeDispatcher{
		failFor: map[string]int{"implement": 1},
	}
	r := New(st, newPassingGates(t), disp, nil)
	if err := r.Drive(context.Background(), run, item); err != nil {
		t.Fatalf("drive: %v", err)
	}
	got, err := st.Pipeline.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("getrun: %v", err)
	}
	if got.State != store.PipelineDone {
		t.Errorf("state = %s, want done", got.State)
	}
	implementCalls := 0
	for _, c := range disp.callsList() {
		if c == "implement" {
			implementCalls++
		}
	}
	if implementCalls != 2 {
		t.Errorf("implement calls = %d, want 2 (1 fail + 1 retry success)", implementCalls)
	}
	stages, err := st.Pipeline.ListStages(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list stages: %v", err)
	}
	implementRows := 0
	successRows := 0
	errorRows := 0
	for _, sr := range stages {
		if sr.Stage == "implement" {
			implementRows++
			if sr.Outcome != nil && *sr.Outcome == store.StageOutcomeSuccess {
				successRows++
			}
			if sr.Outcome != nil && *sr.Outcome == store.StageOutcomeError {
				errorRows++
				if !strings.Contains(sr.LogTail, "dispatch fail: implement") {
					t.Errorf("implement error log tail = %q, want dispatch error", sr.LogTail)
				}
			}
		}
	}
	if implementRows != 2 {
		t.Errorf("implement stage_results rows = %d, want 2", implementRows)
	}
	if successRows != 1 {
		t.Errorf("implement success rows = %d, want 1", successRows)
	}
	if errorRows != 1 {
		t.Errorf("implement error rows = %d, want 1", errorRows)
	}
}

func TestRunner_ResumesFromCurrentStage(t *testing.T) {
	st, run, item := newRunnerEnv(t)

	// Pre-populate a pretend prior implement success.
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	for _, s := range []string{"plan_slice", "research", "implement"} {
		out := store.StageOutcomeSuccess
		end := now
		artifacts := map[string]any{"stage_id": s}
		if s == "implement" {
			artifacts["files_changed"] = []any{"foo.go"}
		}
		if err := st.Pipeline.PutStage(context.Background(), &store.StageResult{
			PipelineRunID: run.ID,
			Stage:         s,
			Attempt:       1,
			StartedAt:     now,
			EndedAt:       &end,
			Outcome:       &out,
			Artifacts:     artifacts,
		}); err != nil {
			t.Fatalf("seed stage %s: %v", s, err)
		}
	}
	run.CurrentStage = "tests"
	run.State = store.PipelineTesting
	if err := st.Pipeline.PutRun(context.Background(), run); err != nil {
		t.Fatalf("seed run head: %v", err)
	}

	disp := &fakeDispatcher{}
	r := New(st, newPassingGates(t), disp, nil)
	if err := r.Drive(context.Background(), run, item); err != nil {
		t.Fatalf("drive: %v", err)
	}
	calls := disp.callsList()
	// Resume should skip plan_slice/research/implement.
	for _, c := range calls {
		if c == "plan_slice" || c == "research" || c == "implement" {
			t.Errorf("resume should not re-run %s", c)
		}
	}
	// And tests should be the first call this Drive made.
	if len(calls) == 0 || calls[0] != "tests" {
		t.Errorf("first call after resume = %v, want tests-first", calls)
	}
}

func TestRunner_ResumesPendingStageSpawnAttempt(t *testing.T) {
	st, run, item := newRunnerEnv(t)
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	run.CurrentStage = "plan_slice"
	run.State = store.PipelinePlanning
	if err := st.Pipeline.PutRun(context.Background(), run); err != nil {
		t.Fatalf("seed run head: %v", err)
	}
	if err := st.Pipeline.PutStage(context.Background(), &store.StageResult{
		PipelineRunID: run.ID,
		Stage:         "plan_slice",
		Attempt:       1,
		StartedAt:     now,
		SpawnID:       "spawn-existing",
		Artifacts:     map[string]any{"stage_id": "plan_slice"},
	}); err != nil {
		t.Fatalf("seed pending stage: %v", err)
	}

	disp := &resumeAwareDispatcher{}
	r := New(st, nil, disp, nil)
	r.Stages = []Stage{{ID: "plan_slice", Type: "llm", State: store.PipelinePlanning}}
	if err := r.Drive(context.Background(), run, item); err != nil {
		t.Fatalf("drive: %v", err)
	}
	if disp.gotResume != "spawn-existing" {
		t.Fatalf("resume spawn id = %q, want spawn-existing", disp.gotResume)
	}
	stages, err := st.Pipeline.ListStages(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list stages: %v", err)
	}
	if len(stages) != 1 {
		t.Fatalf("stage rows = %d, want existing attempt updated in-place", len(stages))
	}
	if stages[0].Attempt != 1 || stages[0].SpawnID != "spawn-existing" {
		t.Fatalf("stage row = %+v", stages[0])
	}
	if stages[0].Outcome == nil || *stages[0].Outcome != store.StageOutcomeSuccess {
		t.Fatalf("stage outcome = %v, want success", stages[0].Outcome)
	}
}

func TestRunner_KeepsAcceptedSpawnPendingOnInterruptedPoll(t *testing.T) {
	st, run, item := newRunnerEnv(t)
	r := New(st, nil, &acceptedThenInterruptedDispatcher{}, nil)
	r.Stages = []Stage{{ID: "plan_slice", Type: "llm", State: store.PipelinePlanning}}

	if err := r.Drive(context.Background(), run, item); err != nil {
		t.Fatalf("drive: %v", err)
	}
	stages, err := st.Pipeline.ListStages(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("list stages: %v", err)
	}
	if len(stages) != 1 {
		t.Fatalf("stage rows = %d, want pending accepted spawn", len(stages))
	}
	if stages[0].SpawnID != "spawn-accepted" {
		t.Fatalf("spawn id = %q, want accepted spawn preserved", stages[0].SpawnID)
	}
	if stages[0].Outcome != nil || stages[0].EndedAt != nil {
		t.Fatalf("stage should remain pending, got outcome=%v ended=%v", stages[0].Outcome, stages[0].EndedAt)
	}
	gotRun, err := st.Pipeline.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if gotRun.State != store.PipelinePlanning || gotRun.CurrentStage != "plan_slice" {
		t.Fatalf("run head = state %s current %q, want planning/plan_slice", gotRun.State, gotRun.CurrentStage)
	}
}

func TestRunner_StartGoroutineReachesTerminal(t *testing.T) {
	st, run, item := newRunnerEnv(t)
	disp := &fakeDispatcher{}
	r := New(st, newPassingGates(t), disp, nil)
	if err := r.Start(context.Background(), run, item); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := st.Pipeline.GetRun(context.Background(), run.ID)
		if err != nil {
			t.Fatalf("getrun: %v", err)
		}
		if got.State == store.PipelineDone || got.State == store.PipelineEscalated {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Start: run did not reach terminal state in time")
}

func TestRunner_StartSuppressesDuplicateActiveRun(t *testing.T) {
	st, run, item := newRunnerEnv(t)
	disp := &blockingDispatcher{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	r := New(st, newPassingGates(t), disp, nil)
	r.Stages = []Stage{{ID: "plan_slice", Type: "llm", State: store.PipelinePlanning}}
	if err := r.Start(context.Background(), run, item); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case <-disp.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first dispatch did not start")
	}
	if err := r.Start(context.Background(), run, item); err != nil {
		t.Fatalf("duplicate start: %v", err)
	}
	close(disp.release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, err := st.Pipeline.GetRun(context.Background(), run.ID); err == nil && got.State == store.PipelineDone {
			if calls := disp.callCount(); calls != 1 {
				t.Fatalf("dispatch calls = %d, want 1", calls)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run did not finish after releasing dispatcher")
}

func TestRunner_StartEscalatesFatalDriveError(t *testing.T) {
	st, run, item := newRunnerEnv(t)
	run.State = store.PipelinePlanning
	run.CurrentStage = "not-in-dag"
	if err := st.Pipeline.PutRun(context.Background(), run); err != nil {
		t.Fatalf("seed bad run head: %v", err)
	}

	r := New(st, newPassingGates(t), &fakeDispatcher{}, nil)
	if err := r.Start(context.Background(), run, item); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := st.Pipeline.GetRun(context.Background(), run.ID)
		if err != nil {
			t.Fatalf("getrun: %v", err)
		}
		if got.State == store.PipelineEscalated {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := st.Pipeline.GetRun(context.Background(), run.ID)
	t.Fatalf("state = %s, want escalated", got.State)
}

func TestRunner_StartRejectsBadConfig(t *testing.T) {
	r := &Runner{}
	if err := r.Start(context.Background(), &store.PipelineRun{ID: "x"}, &store.BacklogItem{ID: "y"}); err == nil {
		t.Errorf("expected error for unconfigured runner")
	}
	st, run, item := newRunnerEnv(t)
	r2 := New(st, newPassingGates(t), &fakeDispatcher{}, nil)
	if err := r2.Start(context.Background(), nil, item); !errors.Is(err, errors.New("pipeline: run.ID required")) && err == nil {
		t.Errorf("expected error for nil run, got nil")
	}
	if err := r2.Start(context.Background(), run, nil); err == nil {
		t.Errorf("expected error for nil item")
	}
}
