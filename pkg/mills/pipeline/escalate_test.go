package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/gates"
	"github.com/crb2nu/loom/pkg/mills/store"
)

type fakeIssue struct {
	mu    sync.Mutex
	calls []IssueRequest
	resp  IssueResponse
	err   error
}

func (f *fakeIssue) CreateIssue(_ context.Context, req IssueRequest) (IssueResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if f.err != nil {
		return IssueResponse{}, f.err
	}
	return f.resp, nil
}

type fakeHandoff struct {
	mu    sync.Mutex
	calls []HandoffRequest
	resp  HandoffResponse
	err   error
}

func (f *fakeHandoff) CreateHandoff(_ context.Context, req HandoffRequest) (HandoffResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if f.err != nil {
		return HandoffResponse{}, f.err
	}
	return f.resp, nil
}

func newEscalateEnv(t *testing.T) (*store.Store, *store.PipelineRun, *store.BacklogItem) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "h.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	item := &store.BacklogItem{
		ID: "BL-ESC-1", Title: "x", State: store.BacklogQueued, Priority: store.P1,
	}
	if err := st.Backlog.Put(ctx, item); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	run := &store.PipelineRun{
		ID: "PIPE-ESC-1", BacklogID: item.ID, Template: "x",
		State: store.PipelineImplementing, Attempts: 1, StartedAt: now,
		CostUSD: 0.42,
	}
	if err := st.Pipeline.PutRun(ctx, run); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	// Seed two stage_results so the failure record has rows.
	out := store.StageOutcomeError
	end := now.Add(time.Second)
	for _, s := range []string{"plan_slice", "implement"} {
		oc := out
		if s == "plan_slice" {
			ok := store.StageOutcomeSuccess
			oc = ok
		}
		if err := st.Pipeline.PutStage(ctx, &store.StageResult{
			PipelineRunID: run.ID,
			Stage:         s,
			Attempt:       1,
			StartedAt:     now,
			EndedAt:       &end,
			Outcome:       &oc,
			CostUSD:       0.05,
			LogTail:       "line1\nline2\nline3\nfinal failure: boom",
		}); err != nil {
			t.Fatalf("seed stage %s: %v", s, err)
		}
	}
	if err := st.Pipeline.PutGate(ctx, &store.GateOutcome{
		PipelineRunID: run.ID, AfterStage: "post_implement_gate",
		GateName: "diff_size", Outcome: store.GateOutcomeFail,
		Reasons:     []string{"diff > 800 lines"},
		EvaluatedAt: now,
	}); err != nil {
		t.Fatalf("seed gate: %v", err)
	}
	return st, run, item
}

func TestEscalator_BuildRecord(t *testing.T) {
	st, run, item := newEscalateEnv(t)
	e := NewEscalator(st, nil, nil)
	rec, err := e.BuildRecord(context.Background(), run, item, "test reason")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if rec.BacklogID != item.ID || rec.PipelineRunID != run.ID {
		t.Errorf("ids wrong: %+v", rec)
	}
	if rec.Reason != "test reason" {
		t.Errorf("reason = %q", rec.Reason)
	}
	if len(rec.StageStack) != 2 {
		t.Errorf("stage stack len = %d, want 2", len(rec.StageStack))
	}
	if len(rec.GateVerdicts) != 1 || rec.GateVerdicts[0].Outcome != string(store.GateOutcomeFail) {
		t.Errorf("gate verdicts wrong: %+v", rec.GateVerdicts)
	}
	if !strings.Contains(rec.LastLogTail, "boom") {
		t.Errorf("log tail missing recent line: %q", rec.LastLogTail)
	}
}

func TestEscalator_HandlePostsIssueAndHandoff(t *testing.T) {
	st, run, item := newEscalateEnv(t)
	issue := &fakeIssue{resp: IssueResponse{IID: 99, URL: "https://gl/issues/99"}}
	handoff := &fakeHandoff{resp: HandoffResponse{HandoffID: "h-1"}}
	e := NewEscalator(st, issue, handoff)
	e.HandTo = "human-on-call"
	if err := e.Handle(context.Background(), run, item, "stage X exceeded retries"); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(issue.calls) != 1 {
		t.Fatalf("issue calls = %d", len(issue.calls))
	}
	got := issue.calls[0]
	if !strings.Contains(got.Title, run.ID) {
		t.Errorf("issue title missing run id: %q", got.Title)
	}
	if !containsAll(got.Labels, "mills-escalation", "kind/incident", "priority/P1") {
		t.Errorf("labels missing: %v", got.Labels)
	}
	if !strings.Contains(got.Description, "Stage history") {
		t.Errorf("issue body missing stage history")
	}
	if !strings.Contains(got.Description, "diff_size") {
		t.Errorf("issue body missing gate verdicts")
	}
	if len(handoff.calls) != 1 {
		t.Fatalf("handoff calls = %d", len(handoff.calls))
	}
	hr := handoff.calls[0]
	if hr.To != "human-on-call" {
		t.Errorf("handoff to = %q", hr.To)
	}
	if hr.IssueURL != "https://gl/issues/99" {
		t.Errorf("issue url not propagated to handoff")
	}
}

func TestEscalator_IssueFailureDoesNotBlockHandoff(t *testing.T) {
	st, run, item := newEscalateEnv(t)
	issue := &fakeIssue{err: errIssueDown}
	handoff := &fakeHandoff{}
	e := NewEscalator(st, issue, handoff)
	if err := e.Handle(context.Background(), run, item, "x"); err != nil {
		t.Fatalf("handle should swallow issue errors: %v", err)
	}
	if len(handoff.calls) != 1 {
		t.Errorf("handoff should still fire when issue fails")
	}
}

func TestRunner_EscalatorIsInvokedOnEscalation(t *testing.T) {
	st, run, item := newRunnerEnv(t)
	disp := &fakeDispatcher{}
	// Build a registry where one gate fails; the rest pass. This drives
	// the post_implement_gate to fail and exhaust retries.
	gr := newGateRegistryWithOneFailure(t, "scope")
	issue := &fakeIssue{resp: IssueResponse{IID: 1, URL: "https://gl/i/1"}}
	handoff := &fakeHandoff{}
	e := NewEscalator(st, issue, handoff)
	r := New(st, gr, disp, nil)
	r.Escalator = e
	if err := r.Drive(context.Background(), run, item); err != nil {
		t.Fatalf("drive: %v", err)
	}
	if len(issue.calls) == 0 {
		t.Errorf("expected escalator to post issue on retry-cap exceed")
	}
	if len(handoff.calls) == 0 {
		t.Errorf("expected escalator to create handoff")
	}
}

func TestIntegrator_EscalatorIsInvokedOnConflict(t *testing.T) {
	st, run, item := newIntegratorEnv(t)
	issue := &fakeIssue{}
	handoff := &fakeHandoff{}
	e := NewEscalator(st, issue, handoff)
	itg := NewIntegrator(st, &recordingSubRunner{store: st}, &fakeAllocator{}, &fakeMerger{conflict: true, files: []string{"a.go"}})
	itg.Escalator = e
	if err := itg.Run(context.Background(), run, item); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(issue.calls) != 1 || len(handoff.calls) != 1 {
		t.Errorf("expected one issue + one handoff on integrator escalation; got issue=%d handoff=%d", len(issue.calls), len(handoff.calls))
	}
}

func newGateRegistryWithOneFailure(t *testing.T, failName string) *gates.Registry {
	t.Helper()
	r := gates.NewRegistry()
	for _, name := range []string{"diff_size", "scope", "path_policy", "secret_scan", "commit_format"} {
		if name == failName {
			r.Register(&alwaysFailGate{name: name})
			continue
		}
		r.Register(&alwaysPassGate{name: name})
	}
	return r
}

func containsAll(haystack []string, needles ...string) bool {
	set := make(map[string]bool, len(haystack))
	for _, h := range haystack {
		set[h] = true
	}
	for _, n := range needles {
		if !set[n] {
			return false
		}
	}
	return true
}

var errIssueDown = newSentinelError("issue service down")

type sentinelError struct{ msg string }

func (e *sentinelError) Error() string  { return e.msg }
func newSentinelError(msg string) error { return &sentinelError{msg: msg} }
