package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// fakeReviewerForTriggers is a tiny Reviewer the trigger tests can drive
// without spinning up the full mocking surface from dispatcher_test.go.
type fakeReviewerForTriggers struct {
	mu       sync.Mutex
	calls    int
	response string
	cost     float64
}

func (f *fakeReviewerForTriggers) Backend() string { return "flexinfer" }

func (f *fakeReviewerForTriggers) Review(_ context.Context, _, _ string, _ float64) (string, float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.response == "" {
		f.response = `{"survival_score":0.92,"severity":"info","findings":[]}`
	}
	if f.cost == 0 {
		f.cost = 0.04
	}
	return f.response, f.cost, nil
}

func (f *fakeReviewerForTriggers) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// triggerEnv stitches together a real store, a real dispatcher backed by
// a fake reviewer, and a worker drained synchronously — so tests don't
// race on goroutine scheduling. The lifecycle helper closes the worker
// + store on cleanup.
type triggerEnv struct {
	t        *testing.T
	store    *store.Store
	worker   *QueueWorker
	triggers *Triggers
	reviewer *fakeReviewerForTriggers
}

func newTriggerEnv(t *testing.T) *triggerEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{
		Path: filepath.Join(dir, "hive.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	rev := &fakeReviewerForTriggers{}
	d := New(map[string]Reviewer{"flexinfer": rev}, MustLoadRubric())

	policy := PoolPolicy{
		Bulk: []PoolMember{{Backend: "flexinfer", Model: "llama-4-70b"}},
	}
	w := NewQueueWorker(d, st.Audit, policy, QueueOptions{
		Capacity:      8,
		PerJobTimeout: 5 * time.Second,
	})
	tr := &Triggers{Worker: w}
	return &triggerEnv{t: t, store: st, worker: w, triggers: tr, reviewer: rev}
}

// drain runs the worker until no jobs remain, then stops it. Used after
// every test that enqueues so the canonical store has the row written
// before assertions.
func (e *triggerEnv) drain(ctx context.Context) {
	e.t.Helper()
	done := make(chan struct{})
	go func() {
		e.worker.Run(ctx)
		close(done)
	}()
	// Wait long enough for the job to dispatch + record.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			e.t.Fatal("drain timed out")
		case <-time.After(50 * time.Millisecond):
			if len(e.worker.jobs) == 0 {
				e.worker.Stop()
				<-done
				return
			}
		}
	}
}

func TestQueueWorker_EnqueueAndDrain(t *testing.T) {
	env := newTriggerEnv(t)

	ok := env.worker.Enqueue(Request{
		SubjectKind: store.AuditSubjectCouncilArtifact,
		SubjectID:   "COUNCIL-1",
		Artifact:    "hello",
	})
	if !ok {
		t.Fatal("enqueue should succeed on empty queue")
	}

	env.drain(context.Background())

	rows, err := env.store.Audit.ListForSubject(context.Background(),
		store.AuditSubjectCouncilArtifact, "COUNCIL-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	got := rows[0]
	if got.RubricID != RubricID {
		t.Errorf("rubric id: got %q want %q", got.RubricID, RubricID)
	}
	if got.SurvivalScore != 0.92 {
		t.Errorf("survival: got %v want 0.92", got.SurvivalScore)
	}
}

func TestQueueWorker_PolicyDefaultsApply(t *testing.T) {
	env := newTriggerEnv(t)

	// Caller omits Pool — worker should fill from policy.
	env.worker.Enqueue(Request{
		SubjectKind: store.AuditSubjectPipelineMerge,
		SubjectID:   "PIPE-1",
		Artifact:    "diff --git a/x b/x",
	})
	env.drain(context.Background())
	if env.reviewer.Calls() == 0 {
		t.Error("reviewer should have been called via policy default pool")
	}
}

func TestQueueWorker_FullQueueDrops(t *testing.T) {
	env := newTriggerEnv(t)
	// Don't drain — leave the worker idle so the buffer fills.
	for i := 0; i < cap(env.worker.jobs); i++ {
		env.worker.Enqueue(Request{
			SubjectKind: store.AuditSubjectCouncilArtifact,
			SubjectID:   "FILL-" + itoa(i),
			Artifact:    "x",
		})
	}
	dropped := !env.worker.Enqueue(Request{
		SubjectKind: store.AuditSubjectCouncilArtifact,
		SubjectID:   "OVERFLOW",
		Artifact:    "x",
	})
	if !dropped {
		t.Error("buffer-full enqueue should return false")
	}
	env.worker.Stop()
}

func TestTriggers_OnArtifactsCommittedHappy(t *testing.T) {
	env := newTriggerEnv(t)
	env.triggers.LoadCouncilArtifact = func(_ context.Context, run *store.CouncilRun, _ []store.ArtifactRef) (string, string, error) {
		return "## Plan for " + run.ID, `{"slice":"A"}`, nil
	}

	env.triggers.OnArtifactsCommitted(context.Background(),
		&store.CouncilRun{ID: "COUNCIL-CMT"}, nil)
	env.drain(context.Background())

	rows, err := env.store.Audit.ListForSubject(context.Background(),
		store.AuditSubjectCouncilArtifact, "COUNCIL-CMT")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 council audit row, got %d", len(rows))
	}
}

func TestTriggers_OnArtifactsCommittedHandlesLoadErr(t *testing.T) {
	env := newTriggerEnv(t)
	env.triggers.LoadCouncilArtifact = func(_ context.Context, _ *store.CouncilRun, _ []store.ArtifactRef) (string, string, error) {
		return "", "", errors.New("disk gone")
	}

	// Must not panic — error is logged + the audit is silently skipped.
	env.triggers.OnArtifactsCommitted(context.Background(),
		&store.CouncilRun{ID: "COUNCIL-ERR"}, nil)
	// drain anyway to make sure no spurious row appeared
	env.drain(context.Background())
	rows, _ := env.store.Audit.ListForSubject(context.Background(),
		store.AuditSubjectCouncilArtifact, "COUNCIL-ERR")
	if len(rows) != 0 {
		t.Errorf("load-fail must not enqueue; got %d rows", len(rows))
	}
}

func TestTriggers_OnArtifactsCommittedSkipsEmptyArtifact(t *testing.T) {
	env := newTriggerEnv(t)
	env.triggers.LoadCouncilArtifact = func(_ context.Context, _ *store.CouncilRun, _ []store.ArtifactRef) (string, string, error) {
		return "", "", nil
	}
	env.triggers.OnArtifactsCommitted(context.Background(),
		&store.CouncilRun{ID: "COUNCIL-EMPTY"}, nil)
	env.drain(context.Background())
	rows, _ := env.store.Audit.ListForSubject(context.Background(),
		store.AuditSubjectCouncilArtifact, "COUNCIL-EMPTY")
	if len(rows) != 0 {
		t.Errorf("empty artifact must not enqueue; got %d rows", len(rows))
	}
}

func TestTriggers_OnPipelineMergedHappy(t *testing.T) {
	env := newTriggerEnv(t)
	env.triggers.LoadMergedDiff = func(_ context.Context, run *store.PipelineRun, _ *store.BacklogItem) (string, error) {
		return "diff --git a/foo.go b/foo.go\n+func Bar() {}", nil
	}

	if err := env.triggers.OnPipelineMerged(context.Background(),
		&store.PipelineRun{ID: "PIPE-MRG"}, &store.BacklogItem{ID: "HIVE-MRG"}); err != nil {
		t.Errorf("OnPipelineMerged should not surface enqueue errors; got %v", err)
	}
	env.drain(context.Background())

	rows, err := env.store.Audit.ListForSubject(context.Background(),
		store.AuditSubjectPipelineMerge, "PIPE-MRG")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 pipeline audit row, got %d", len(rows))
	}
}

func TestTriggers_OnPipelineMergedNeverErrors(t *testing.T) {
	env := newTriggerEnv(t)
	env.triggers.LoadMergedDiff = func(_ context.Context, _ *store.PipelineRun, _ *store.BacklogItem) (string, error) {
		return "", errors.New("gitlab unreachable")
	}
	// Loader error must not surface; chained OnMerged hooks must not see
	// audit failures as merge errors.
	if err := env.triggers.OnPipelineMerged(context.Background(),
		&store.PipelineRun{ID: "PIPE-ERR"}, nil); err != nil {
		t.Errorf("loader error should not propagate; got %v", err)
	}
}

func TestLoadCouncilArtifactFromFS_ReadsRefs(t *testing.T) {
	dir := t.TempDir()
	loomDir := filepath.Join(dir, ".loom")
	if err := os.MkdirAll(loomDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(loomDir, "01-research.md"), []byte("# Research body"), 0o600); err != nil {
		t.Fatalf("write md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(loomDir, "01-sidecar.json"), []byte(`{"slice":"A"}`), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	loader := LoadCouncilArtifactFromFS(dir)
	body, sidecar, err := loader(context.Background(),
		&store.CouncilRun{ID: "x"},
		[]store.ArtifactRef{
			{Kind: "research", Path: ".loom/01-research.md"},
			{Kind: "sidecar", Path: ".loom/01-sidecar.json"},
		},
	)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if body == "" || sidecar == "" {
		t.Fatalf("body=%q sidecar=%q both must be populated", body, sidecar)
	}
	if !contains(body, "Research body") {
		t.Errorf("body missing markdown content: %q", body)
	}
	if sidecar != `{"slice":"A"}` {
		t.Errorf("sidecar verbatim: got %q", sidecar)
	}
}

func TestLoadCouncilArtifactFromFS_PropagatesReadErr(t *testing.T) {
	loader := LoadCouncilArtifactFromFS(t.TempDir())
	_, _, err := loader(context.Background(),
		&store.CouncilRun{ID: "x"},
		[]store.ArtifactRef{{Kind: "research", Path: ".loom/missing.md"}},
	)
	if err == nil {
		t.Fatal("expected read error for missing file")
	}
}

func TestLoadCouncilArtifactFromFS_RejectsEmptyRoot(t *testing.T) {
	loader := LoadCouncilArtifactFromFS("")
	_, _, err := loader(context.Background(), &store.CouncilRun{ID: "x"}, nil)
	if err == nil {
		t.Fatal("expected error for empty repo root")
	}
}

// contains is a tiny strings.Contains shim so the test file doesn't
// need the strings import for one call.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && stringIndex(s, sub) >= 0
}

func stringIndex(s, sub string) int {
	if sub == "" {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
