package crossrepo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// fakeWorktreeManager records allocate + release calls and can be
// programmed to fail on the Nth allocate. Path naming is deterministic so
// tests can assert exact values.
type fakeWorktreeManager struct {
	mu           sync.Mutex
	allocCalls   []WorktreeAllocateRequest
	releaseCalls []string
	failOnN      int // 1-based; 0 disables
	allocErr     error
	releaseErr   error
}

func (f *fakeWorktreeManager) Allocate(_ context.Context, req WorktreeAllocateRequest) (WorktreeAllocateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allocCalls = append(f.allocCalls, req)
	if f.failOnN > 0 && len(f.allocCalls) == f.failOnN {
		return WorktreeAllocateResult{}, fmt.Errorf("forced alloc fail #%d", f.failOnN)
	}
	if f.allocErr != nil {
		return WorktreeAllocateResult{}, f.allocErr
	}
	return WorktreeAllocateResult{
		Path:   fmt.Sprintf("/tmp/wt/%s-%s", req.RepoName, req.BranchName),
		Branch: req.BranchName,
	}, nil
}

func (f *fakeWorktreeManager) Release(_ context.Context, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseCalls = append(f.releaseCalls, path)
	return f.releaseErr
}

func (f *fakeWorktreeManager) snapshot() (allocs []WorktreeAllocateRequest, releases []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	allocs = append(allocs, f.allocCalls...)
	releases = append(releases, f.releaseCalls...)
	return
}

// newPlannerEnv builds an isolated Loader (skip watch) backed by a
// two-repo registry plus a fake WorktreeManager.
func newPlannerEnv(t *testing.T) (*Loader, *fakeWorktreeManager, *store.BacklogItem) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.yaml")
	if err := os.WriteFile(path, []byte(validTwoRepos), 0o600); err != nil {
		t.Fatalf("seed registry: %v", err)
	}
	loader, err := NewLoader(context.Background(), path, nil, LoaderOptions{SkipWatch: true})
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	mgr := &fakeWorktreeManager{}
	item := &store.BacklogItem{ID: "BL-CROSS-1", Title: "two-repo change"}
	return loader, mgr, item
}

func TestPlanner_HappyPath(t *testing.T) {
	loader, mgr, item := newPlannerEnv(t)
	planner := NewPlanner(loader, mgr, nil)

	repos := []store.CrossRepoRepoEntry{
		{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x-loom-core"},
		{ProjectID: 51, RepoName: "loom", Branch: "feat/x-loom-vscode"},
	}
	plan, err := planner.Plan(context.Background(), item, repos)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.BacklogItemID != item.ID {
		t.Errorf("BacklogItemID = %q, want %q", plan.BacklogItemID, item.ID)
	}
	if plan.AtomicityStrategy != "all_or_revert" {
		t.Errorf("AtomicityStrategy = %q, want all_or_revert", plan.AtomicityStrategy)
	}
	if len(plan.Repos) != 2 {
		t.Fatalf("Repos len = %d, want 2", len(plan.Repos))
	}
	want := []struct {
		name      string
		branch    string
		projectID int64
		path      string
	}{
		{"loom-core", "feat/x-loom-core", 47, "/tmp/wt/loom-core-feat/x-loom-core"},
		{"loom", "feat/x-loom-vscode", 51, "/tmp/wt/loom-feat/x-loom-vscode"},
	}
	for i, w := range want {
		got := plan.Repos[i]
		if got.Entry.Name != w.name {
			t.Errorf("Repos[%d].Entry.Name = %q, want %q", i, got.Entry.Name, w.name)
		}
		if got.Branch != w.branch {
			t.Errorf("Repos[%d].Branch = %q, want %q", i, got.Branch, w.branch)
		}
		if got.WorktreePath != w.path {
			t.Errorf("Repos[%d].WorktreePath = %q, want %q", i, got.WorktreePath, w.path)
		}
		hint := got.PipelineHint
		if hint.RepoName != w.name || hint.Branch != w.branch || hint.ProjectID != w.projectID {
			t.Errorf("Repos[%d].PipelineHint = %+v, want repo=%s branch=%s project=%d",
				i, hint, w.name, w.branch, w.projectID)
		}
	}

	// Round-trippability: the hint must serialise into a CrossRepoRepoEntry
	// the integrator can later read without recomputing fields.
	for i, pr := range plan.Repos {
		hint := pr.PipelineHint
		round := store.CrossRepoRepoEntry{
			ProjectID: hint.ProjectID,
			RepoName:  hint.RepoName,
			Branch:    hint.Branch,
		}
		if round != hint {
			t.Errorf("Repos[%d] not round-trippable: got %+v, want %+v", i, round, hint)
		}
	}

	allocs, releases := mgr.snapshot()
	if len(allocs) != 2 {
		t.Errorf("alloc calls = %d, want 2", len(allocs))
	}
	if len(releases) != 0 {
		t.Errorf("release calls = %d, want 0 (happy path)", len(releases))
	}
}

func TestPlanner_UnknownRepoErrors(t *testing.T) {
	loader, mgr, item := newPlannerEnv(t)
	planner := NewPlanner(loader, mgr, nil)

	repos := []store.CrossRepoRepoEntry{
		{RepoName: "loom-core", Branch: "feat/x"},
		{RepoName: "phantom-repo", Branch: "feat/x"},
	}
	_, err := planner.Plan(context.Background(), item, repos)
	if err == nil {
		t.Fatalf("expected error for unknown repo, got nil")
	}
	if !strings.Contains(err.Error(), "phantom-repo") {
		t.Errorf("error = %v, want it to mention phantom-repo", err)
	}

	// First repo's worktree was allocated then released; phantom never reached allocator.
	allocs, releases := mgr.snapshot()
	if len(allocs) != 1 {
		t.Errorf("alloc calls = %d, want 1 (only loom-core before unknown)", len(allocs))
	}
	if len(releases) != 1 {
		t.Errorf("release calls = %d, want 1 (rollback)", len(releases))
	}
	if len(releases) == 1 && releases[0] != "/tmp/wt/loom-core-feat/x" {
		t.Errorf("release[0] = %q, want /tmp/wt/loom-core-feat/x", releases[0])
	}
}

func TestPlanner_AllocatorFailsMidStreamReleasesPriors(t *testing.T) {
	loader, mgr, item := newPlannerEnv(t)
	mgr.failOnN = 2 // second alloc fails
	planner := NewPlanner(loader, mgr, nil)

	repos := []store.CrossRepoRepoEntry{
		{RepoName: "loom-core", Branch: "feat/x"},
		{RepoName: "loom", Branch: "feat/x"},
	}
	_, err := planner.Plan(context.Background(), item, repos)
	if err == nil {
		t.Fatalf("expected allocator failure, got nil")
	}
	if !strings.Contains(err.Error(), "loom") || !strings.Contains(err.Error(), "alloc") {
		t.Errorf("error = %v, want mention of repo + alloc", err)
	}

	allocs, releases := mgr.snapshot()
	if len(allocs) != 2 {
		t.Errorf("alloc calls = %d, want 2 (1 ok + 1 fail)", len(allocs))
	}
	if len(releases) != 1 {
		t.Errorf("release calls = %d, want 1 (only the first succeeded)", len(releases))
	}
	if len(releases) == 1 && releases[0] != "/tmp/wt/loom-core-feat/x" {
		t.Errorf("release[0] = %q, want loom-core path", releases[0])
	}
}

func TestPlanner_EmptyReposErrors(t *testing.T) {
	loader, mgr, item := newPlannerEnv(t)
	planner := NewPlanner(loader, mgr, nil)
	if _, err := planner.Plan(context.Background(), item, nil); err == nil {
		t.Fatalf("expected error for empty repos, got nil")
	}
	if _, err := planner.Plan(context.Background(), item, []store.CrossRepoRepoEntry{}); err == nil {
		t.Fatalf("expected error for empty repos slice, got nil")
	}
	allocs, releases := mgr.snapshot()
	if len(allocs) != 0 || len(releases) != 0 {
		t.Errorf("expected no allocator calls on empty input; got allocs=%d releases=%d",
			len(allocs), len(releases))
	}
}

func TestPlanner_GuardsAgainstNilArgs(t *testing.T) {
	loader, mgr, _ := newPlannerEnv(t)
	planner := NewPlanner(loader, mgr, nil)
	if _, err := planner.Plan(context.Background(), nil, []store.CrossRepoRepoEntry{{RepoName: "loom-core", Branch: "x"}}); err == nil {
		t.Errorf("expected error for nil item")
	}
	if _, err := planner.Plan(context.Background(), &store.BacklogItem{}, []store.CrossRepoRepoEntry{{RepoName: "loom-core", Branch: "x"}}); err == nil {
		t.Errorf("expected error for empty item.ID")
	}
}

func TestPlanner_HintRequiresBranch(t *testing.T) {
	loader, mgr, item := newPlannerEnv(t)
	planner := NewPlanner(loader, mgr, nil)
	_, err := planner.Plan(context.Background(), item, []store.CrossRepoRepoEntry{
		{RepoName: "loom-core"}, // missing branch
	})
	if err == nil {
		t.Fatalf("expected error for missing branch")
	}
	if !strings.Contains(err.Error(), "branch") {
		t.Errorf("error = %v, want mention of missing branch", err)
	}
}
