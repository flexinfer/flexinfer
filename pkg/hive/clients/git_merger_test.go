package clients

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/crb2nu/loom/pkg/hive/pipeline"
)

// fakeGitRunner records every git command and returns canned stdout/
// stderr/exit code keyed on the args. Tests register expectations
// before calling Merge.
type fakeGitRunner struct {
	mu    sync.Mutex
	calls [][]string
	// scripted maps "subcommand" or full args joined by space → response
	scripted map[string]gitResponse
	// fallback applies when no key matches.
	fallback gitResponse
}

type gitResponse struct {
	stdout string
	stderr string
	code   int
	err    error
}

func (r *fakeGitRunner) Run(_ context.Context, _ string, name string, args ...string) (string, string, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{name}, args...))
	for prefix, resp := range r.scripted {
		if strings.HasPrefix(strings.Join(args, " "), prefix) {
			return resp.stdout, resp.stderr, resp.code, resp.err
		}
	}
	return r.fallback.stdout, r.fallback.stderr, r.fallback.code, r.fallback.err
}

func (r *fakeGitRunner) gitCalls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = append([]string(nil), c...)
	}
	return out
}

// callsContain returns true when one of the recorded calls starts with
// the given args[1:] (i.e. matches `git <args...>`).
func (r *fakeGitRunner) callsContain(prefix ...string) bool {
	for _, call := range r.gitCalls() {
		if len(call) < 1+len(prefix) || call[0] != "git" {
			continue
		}
		ok := true
		for i, a := range prefix {
			if call[1+i] != a {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func newGitMerger(runner CommandRunner) *GitBranchMerger {
	return &GitBranchMerger{
		RepoRoot:          "/workspace/loom-core",
		Runner:            runner,
		IntegrationPrefix: "integrate/",
	}
}

// ----- Config validation -----

func TestGitBranchMerger_RejectsMissingConfig(t *testing.T) {
	m := &GitBranchMerger{}
	if _, err := m.Merge(context.Background(), pipeline.MergeBranchesRequest{}); err == nil {
		t.Error("expected error when Runner nil")
	}
	m2 := &GitBranchMerger{Runner: &fakeGitRunner{}}
	if _, err := m2.Merge(context.Background(), pipeline.MergeBranchesRequest{}); err == nil {
		t.Error("expected error when RepoRoot empty")
	}
	m3 := newGitMerger(&fakeGitRunner{})
	if _, err := m3.Merge(context.Background(), pipeline.MergeBranchesRequest{BacklogID: "BL"}); err == nil {
		t.Error("expected error when SliceBranches empty")
	}
}

// ----- Happy path -----

func TestGitBranchMerger_HappyPathProducesIntegratedSHA(t *testing.T) {
	runner := &fakeGitRunner{
		scripted: map[string]gitResponse{
			"rev-parse HEAD": {stdout: "deadbeef1234\n"},
		},
	}
	m := newGitMerger(runner)
	resp, err := m.Merge(context.Background(), pipeline.MergeBranchesRequest{
		BacklogID:     "BL-X",
		BaseBranch:    "main",
		SliceBranches: []string{"feat/BL-X/alpha", "feat/BL-X/beta"},
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if resp.Conflict {
		t.Errorf("expected clean merge")
	}
	if resp.IntegratedSHA != "deadbeef1234" {
		t.Errorf("IntegratedSHA = %q", resp.IntegratedSHA)
	}
	// Confirm the expected sequence ran.
	if !runner.callsContain("fetch", "--prune", "origin") {
		t.Error("expected fetch")
	}
	if !runner.callsContain("checkout", "-b", "integrate/BL-X", "origin/main") {
		t.Error("expected checkout of integration branch off origin/main")
	}
	if !runner.callsContain("merge", "--no-ff", "--no-edit", "feat/BL-X/alpha") {
		t.Error("expected merge of slice alpha")
	}
	if !runner.callsContain("merge", "--no-ff", "--no-edit", "feat/BL-X/beta") {
		t.Error("expected merge of slice beta")
	}
	if !strings.Contains(resp.LogTail, "git fetch") {
		t.Error("LogTail should record commands")
	}
}

func TestGitBranchMerger_DefaultsBaseBranchToMain(t *testing.T) {
	runner := &fakeGitRunner{
		scripted: map[string]gitResponse{
			"rev-parse HEAD": {stdout: "abc\n"},
		},
	}
	m := newGitMerger(runner)
	if _, err := m.Merge(context.Background(), pipeline.MergeBranchesRequest{
		BacklogID:     "BL-Y",
		SliceBranches: []string{"feat/BL-Y/x"},
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !runner.callsContain("checkout", "-b", "integrate/BL-Y", "origin/main") {
		t.Error("expected default base branch=main")
	}
}

func TestGitBranchMerger_HonoursExplicitIntegrationBranch(t *testing.T) {
	runner := &fakeGitRunner{
		scripted: map[string]gitResponse{
			"rev-parse HEAD": {stdout: "x\n"},
		},
	}
	m := newGitMerger(runner)
	if _, err := m.Merge(context.Background(), pipeline.MergeBranchesRequest{
		BacklogID:         "BL-Z",
		IntegrationBranch: "merge/special-branch",
		SliceBranches:     []string{"feat/BL-Z/a"},
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !runner.callsContain("checkout", "-b", "merge/special-branch", "origin/main") {
		t.Error("explicit IntegrationBranch override not honoured")
	}
}

// ----- Conflict path -----

func TestGitBranchMerger_DetectsConflictAndAborts(t *testing.T) {
	runner := &fakeGitRunner{
		scripted: map[string]gitResponse{
			// First slice merges fine; second conflicts.
			"merge --no-ff --no-edit feat/BL/alpha": {stdout: "Merge made\n"},
			"merge --no-ff --no-edit feat/BL/beta":  {stdout: "CONFLICT in foo.go\n", code: 1},
			"status --porcelain":                    {stdout: "UU foo.go\nUU bar.go\nM  baz.go\n"},
		},
	}
	m := newGitMerger(runner)
	resp, err := m.Merge(context.Background(), pipeline.MergeBranchesRequest{
		BacklogID:     "BL",
		SliceBranches: []string{"feat/BL/alpha", "feat/BL/beta"},
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !resp.Conflict {
		t.Errorf("expected Conflict=true")
	}
	if len(resp.ConflictedFiles) != 2 {
		t.Errorf("conflicted files = %v, want [foo.go, bar.go]", resp.ConflictedFiles)
	}
	if resp.ConflictedFiles[0] != "foo.go" || resp.ConflictedFiles[1] != "bar.go" {
		t.Errorf("conflicted files wrong: %v", resp.ConflictedFiles)
	}
	if !runner.callsContain("merge", "--abort") {
		t.Error("expected `git merge --abort` after conflict")
	}
}

func TestGitBranchMerger_PreExistingBranchDeletedFirst(t *testing.T) {
	// Simulate `branch -D integrate/BL-X` returning exit 1 (branch
	// not found) — should be ignored, integration must proceed.
	runner := &fakeGitRunner{
		scripted: map[string]gitResponse{
			"branch -D integrate/BL-X": {stderr: "error: branch not found", code: 1},
			"rev-parse HEAD":           {stdout: "ok\n"},
		},
	}
	m := newGitMerger(runner)
	resp, err := m.Merge(context.Background(), pipeline.MergeBranchesRequest{
		BacklogID:     "BL-X",
		SliceBranches: []string{"feat/BL-X/x"},
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if resp.Conflict {
		t.Errorf("a missing pre-existing branch is fine, expected clean merge")
	}
	if !runner.callsContain("branch", "-D", "integrate/BL-X") {
		t.Error("expected branch deletion attempt")
	}
}

func TestGitBranchMerger_FetchFailureSurfacesError(t *testing.T) {
	runner := &fakeGitRunner{
		scripted: map[string]gitResponse{
			"fetch --prune origin": {stderr: "fatal: could not read", err: errFakeNetwork},
		},
	}
	m := newGitMerger(runner)
	_, err := m.Merge(context.Background(), pipeline.MergeBranchesRequest{
		BacklogID:     "BL",
		SliceBranches: []string{"x"},
	})
	if err == nil {
		t.Error("expected error from fetch failure")
	}
}

func TestGitBranchMerger_CheckoutFailureSurfacesError(t *testing.T) {
	runner := &fakeGitRunner{
		scripted: map[string]gitResponse{
			"checkout -b integrate/BL origin/main": {stderr: "boom", code: 128},
		},
	}
	m := newGitMerger(runner)
	_, err := m.Merge(context.Background(), pipeline.MergeBranchesRequest{
		BacklogID:     "BL",
		SliceBranches: []string{"x"},
	})
	if err == nil {
		t.Error("expected error when checkout fails")
	}
}

// ----- Conflict-code parser -----

func TestIsConflictCode(t *testing.T) {
	for _, c := range []string{"UU", "AA", "DD", "AU", "UA", "UD", "DU"} {
		if !isConflictCode(c) {
			t.Errorf("expected %q to be a conflict code", c)
		}
	}
	for _, c := range []string{"M ", " M", "??", "A ", "MM"} {
		if isConflictCode(c) {
			t.Errorf("expected %q NOT to be a conflict code", c)
		}
	}
}

// errFakeNetwork is a sentinel test error used to drive transient
// failures through the runner.
var errFakeNetwork = &fakeNetworkErr{}

type fakeNetworkErr struct{}

func (e *fakeNetworkErr) Error() string { return "fake network error" }
