package crossrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// WorktreeAllocator allocates one isolated worktree per repo so the
// per-repo pipeline stages can run in parallel without stepping on
// each other. Implementations wrap mcp-agent-context.agent_worktree_allocate
// (production) or return canned paths (tests).
type WorktreeAllocator interface {
	Allocate(ctx context.Context, req WorktreeAllocateRequest) (WorktreeAllocateResult, error)
}

// WorktreeReleaser releases a previously-allocated worktree. The planner
// invokes Release on every worktree it has already allocated when a later
// allocation in the same Plan call fails, so the caller never sees a
// half-materialised plan.
type WorktreeReleaser interface {
	Release(ctx context.Context, path string) error
}

// WorktreeManager bundles the two interfaces so production wiring is one
// bind point. Tests typically implement both on the same fake.
type WorktreeManager interface {
	WorktreeAllocator
	WorktreeReleaser
}

// WorktreeAllocateRequest is the per-repo allocation input the planner
// hands to the allocator. RepoName is the registry slug (not a path);
// the allocator is responsible for translating it into a clone target.
type WorktreeAllocateRequest struct {
	RepoName   string
	RepoURL    string
	BacklogID  string
	BranchName string
	BaseBranch string
}

// WorktreeAllocateResult is the per-repo allocation output.
type WorktreeAllocateResult struct {
	Path   string
	Branch string
}

// MultiRepoPlan is the materialised cross-repo plan returned by Plan().
// AtomicityStrategy mirrors store.CrossRepoRun.AtomicityStrategy and is
// always "all_or_revert" in v2.0.
type MultiRepoPlan struct {
	BacklogItemID     string
	Repos             []PlannedRepo
	AtomicityStrategy string
}

// PlannedRepo is one repo's slice of a MultiRepoPlan. PipelineHint is
// round-trippable into store.CrossRepoRepoEntry so the runner can persist
// the plan into cross_repo_runs.repos_json without recomputing fields.
type PlannedRepo struct {
	Entry        RepoEntry
	Branch       string
	WorktreePath string
	PipelineHint store.CrossRepoRepoEntry
}

// Planner turns a backlog item's per-repo entry list into a fully-resolved
// MultiRepoPlan: every RepoName must resolve in the registry, and every
// repo gets a worktree allocated. On any failure mid-stream the planner
// releases worktrees it already allocated so the caller never sees a
// partially-materialised plan.
type Planner struct {
	Loader  *Loader
	Manager WorktreeManager
	Logger  *slog.Logger
}

// NewPlanner constructs a Planner. A nil logger is replaced with a
// discarding logger so callers don't have to thread one in for tests.
func NewPlanner(loader *Loader, mgr WorktreeManager, log *slog.Logger) *Planner {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Planner{Loader: loader, Manager: mgr, Logger: log}
}

// Plan resolves every per-repo hint against the registry and allocates a
// worktree per repo. Repos are processed in declaration order so the
// resulting MultiRepoPlan is deterministic — the integrator later relies
// on this order for the merge sequence.
//
// On error: any already-allocated worktrees are released (best-effort)
// before returning. Release errors are logged but do not shadow the
// original error.
func (p *Planner) Plan(
	ctx context.Context,
	item *store.BacklogItem,
	repos []store.CrossRepoRepoEntry,
) (*MultiRepoPlan, error) {
	if p == nil {
		return nil, errors.New("crossrepo: planner: receiver is nil")
	}
	if p.Loader == nil {
		return nil, errors.New("crossrepo: planner: loader is required")
	}
	if p.Manager == nil {
		return nil, errors.New("crossrepo: planner: worktree manager is required")
	}
	if item == nil || item.ID == "" {
		return nil, errors.New("crossrepo: planner: backlog item required")
	}
	if len(repos) == 0 {
		return nil, errors.New("crossrepo: planner: repos[] is empty")
	}

	planned := make([]PlannedRepo, 0, len(repos))
	for i, hint := range repos {
		name := hint.RepoName
		if name == "" {
			p.releaseAll(ctx, planned)
			return nil, fmt.Errorf("crossrepo: planner: repos[%d] missing repo_name", i)
		}
		entry, ok := p.Loader.Find(name)
		if !ok {
			p.releaseAll(ctx, planned)
			return nil, fmt.Errorf("crossrepo: planner: repo %q not in registry", name)
		}
		branch := hint.Branch
		if branch == "" {
			p.releaseAll(ctx, planned)
			return nil, fmt.Errorf("crossrepo: planner: repos[%d] (%s) missing branch", i, name)
		}
		req := WorktreeAllocateRequest{
			RepoName:   entry.Name,
			RepoURL:    entry.URL,
			BacklogID:  item.ID,
			BranchName: branch,
			BaseBranch: entry.DefaultBranch,
		}
		res, err := p.Manager.Allocate(ctx, req)
		if err != nil {
			p.releaseAll(ctx, planned)
			return nil, fmt.Errorf("crossrepo: planner: allocate %s: %w", name, err)
		}
		hintCopy := store.CrossRepoRepoEntry{
			ProjectID: entry.ProjectID,
			RepoName:  entry.Name,
			Branch:    branch,
		}
		// Honour an explicit project_id override on the hint when the
		// registry row has none yet (zero is "not GitLab-tracked").
		if hint.ProjectID != 0 {
			hintCopy.ProjectID = hint.ProjectID
		}
		planned = append(planned, PlannedRepo{
			Entry:        entry,
			Branch:       branch,
			WorktreePath: res.Path,
			PipelineHint: hintCopy,
		})
		p.Logger.Info("crossrepo planner allocated worktree",
			"repo", entry.Name, "project_id", entry.ProjectID,
			"branch", branch, "path", res.Path)
	}

	return &MultiRepoPlan{
		BacklogItemID:     item.ID,
		Repos:             planned,
		AtomicityStrategy: "all_or_revert",
	}, nil
}

// releaseAll best-effort releases every already-allocated worktree. Used
// when Plan errors mid-stream so the caller never observes a half-built
// MultiRepoPlan. Release failures are logged but do not propagate.
func (p *Planner) releaseAll(ctx context.Context, planned []PlannedRepo) {
	for i := len(planned) - 1; i >= 0; i-- {
		path := planned[i].WorktreePath
		if path == "" {
			continue
		}
		if err := p.Manager.Release(ctx, path); err != nil {
			p.Logger.Warn("crossrepo planner release failed",
				"repo", planned[i].Entry.Name, "path", path, "err", err.Error())
		}
	}
}
