package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// WorktreeAllocator hands out a fresh per-slice worktree path. The
// production allocator wraps `agent_worktree_allocate` from
// mcp-agent-context; tests inject a fake that returns deterministic paths.
type WorktreeAllocator interface {
	Allocate(ctx context.Context, req WorktreeRequest) (WorktreeHandle, error)
	Release(ctx context.Context, handle WorktreeHandle) error
}

// WorktreeRequest carries the bits the allocator needs.
type WorktreeRequest struct {
	BacklogID  string
	SliceName  string
	BranchName string
	BaseBranch string
	Purpose    string
}

// WorktreeHandle identifies an allocated worktree by branch + path.
type WorktreeHandle struct {
	Path   string
	Branch string
}

// BranchMerger combines per-slice branches into a single integration
// branch. Returns Conflict=true when a clean merge isn't possible — the
// integrator escalates on conflict rather than trying clever resolution.
type BranchMerger interface {
	Merge(ctx context.Context, req MergeBranchesRequest) (MergeBranchesResponse, error)
}

// MergeBranchesRequest names the branches to combine and the integration
// branch to land them on.
type MergeBranchesRequest struct {
	BacklogID         string
	IntegrationBranch string
	SliceBranches     []string
	BaseBranch        string
}

// MergeBranchesResponse reports the integration outcome.
type MergeBranchesResponse struct {
	Conflict        bool
	ConflictedFiles []string
	IntegratedSHA   string
	LogTail         string
}

// SubRunner is the per-slice equivalent of Runner.Drive. The integrator
// invokes one per parallel slice. The default Runner satisfies this; a
// thinner pipeline (e.g. only plan→implement→tests) is also valid as
// long as it leaves a usable diff on the slice's branch.
type SubRunner interface {
	Drive(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error
}

// Integrator runs a backlog item through the parallel-slice path.
//
// Fan-out: if the item has 2+ slices and any slice declares ParallelWith,
// the integrator allocates a worktree per slice, copies the BacklogItem
// scoped to that slice, drives a sub-pipeline via SubRunner, then merges
// the resulting branches into a single integration branch.
//
// Single-slice items skip the fan-out entirely and the parent runner
// drives the standard DAG.
type Integrator struct {
	Store     *store.Store
	Sub       SubRunner
	Allocator WorktreeAllocator
	Merger    BranchMerger
	Clock     func() time.Time
	Logger    *slog.Logger
	// MaxParallel caps the number of concurrent sub-runs. 0 means
	// "no cap"; recommend setting from policy.budgets.pipeline
	// .max_concurrent_runs at startup.
	MaxParallel int
	// Escalator publishes the failure record + issue + handoff when a
	// fan-out parent transitions to escalated. Nil falls back to the
	// bare state transition.
	Escalator EscalationHandler
}

// NewIntegrator constructs an Integrator with sensible defaults.
func NewIntegrator(s *store.Store, sub SubRunner, alloc WorktreeAllocator, merger BranchMerger) *Integrator {
	return &Integrator{
		Store:     s,
		Sub:       sub,
		Allocator: alloc,
		Merger:    merger,
		Clock:     time.Now,
		Logger:    slog.Default(),
	}
}

// ShouldFanOut reports whether the item triggers parallel sub-runs.
// Exposed so the operator's wiring can decide which path to drive.
func ShouldFanOut(item *store.BacklogItem) bool {
	if item == nil || len(item.Slices) < 2 {
		return false
	}
	for _, s := range item.Slices {
		if len(s.ParallelWith) > 0 {
			return true
		}
	}
	return false
}

// Run drives the parallel path. Pre-condition: ShouldFanOut(item) is true.
//
// The parent run is updated as sub-runs progress; on success the parent
// transitions to PipelineMR (the merger has produced an integration
// branch ready for an MR), on conflict the parent transitions to
// PipelineEscalated with the conflicted files recorded.
func (i *Integrator) Run(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error {
	if i == nil || i.Store == nil || i.Sub == nil || i.Allocator == nil || i.Merger == nil {
		return errors.New("integrator: not configured")
	}
	if !ShouldFanOut(item) {
		return errors.New("integrator: item does not require fan-out")
	}

	parentRun := *run
	parentRun.State = store.PipelineSlicing
	parentRun.CurrentStage = "fan_out"
	if err := i.Store.Pipeline.PutRun(ctx, &parentRun); err != nil {
		return fmt.Errorf("integrator: persist parent: %w", err)
	}

	results, err := i.runSubRuns(ctx, &parentRun, item)
	if err != nil {
		return i.escalateWithItem(ctx, &parentRun, item, fmt.Sprintf("sub-run failure: %v", err))
	}

	// Stable order: sort by slice name so merge order is deterministic.
	sort.SliceStable(results, func(a, b int) bool { return results[a].SliceName < results[b].SliceName })
	branches := make([]string, 0, len(results))
	for _, r := range results {
		if r.Branch != "" {
			branches = append(branches, r.Branch)
		}
	}

	integrationBranch := BranchContractFor(&parentRun, item, Stage{ID: "mr"}, "").IntegrationBranch
	if integrationBranch == "" {
		return i.escalateWithItem(ctx, &parentRun, item, "integration branch unavailable")
	}
	mergeResp, err := i.Merger.Merge(ctx, MergeBranchesRequest{
		BacklogID:         item.ID,
		IntegrationBranch: integrationBranch,
		SliceBranches:     branches,
		BaseBranch:        "main",
	})
	if err != nil {
		return i.escalateWithItem(ctx, &parentRun, item, fmt.Sprintf("merge failed: %v", err))
	}
	if mergeResp.Conflict {
		i.event(ctx, "pipeline.integrate.conflict", "fail", map[string]any{
			"run":   parentRun.ID,
			"item":  item.ID,
			"files": mergeResp.ConflictedFiles,
		})
		return i.escalateWithItem(ctx, &parentRun, item, fmt.Sprintf("merge conflict in %d files", len(mergeResp.ConflictedFiles)))
	}

	// Land the integration branch on the parent run; the runner picks up
	// from PipelineMR and drives the rest of the DAG (mr → ci → merge →
	// cleanup) against this combined branch.
	parentRun.State = store.PipelineMR
	parentRun.CurrentStage = "mr"
	parentRun.WorktreePath = "" // parent doesn't own a worktree directly
	if err := i.Store.Pipeline.PutRun(ctx, &parentRun); err != nil {
		return fmt.Errorf("integrator: persist parent post-merge: %w", err)
	}
	*run = parentRun
	i.event(ctx, "pipeline.integrate.merged", "ok", map[string]any{
		"run":             parentRun.ID,
		"item":            item.ID,
		"branches":        branches,
		"integration_sha": mergeResp.IntegratedSHA,
		"branch":          integrationBranch,
	})
	return nil
}

// SubRunResult summarises one sub-run's outcome. The integrator collects
// these before merging branches.
type SubRunResult struct {
	SliceName string
	Run       *store.PipelineRun
	Branch    string
	Err       error
}

func (i *Integrator) runSubRuns(ctx context.Context, parent *store.PipelineRun, item *store.BacklogItem) ([]SubRunResult, error) {
	slices := i.parallelSlices(item)
	if len(slices) == 0 {
		return nil, errors.New("integrator: no parallel slices to run")
	}

	results := make([]SubRunResult, len(slices))
	var wg sync.WaitGroup
	sem := i.semaphore()

	// Sub-runs share backlog_id with the parent but the schema enforces
	// UNIQUE(backlog_id, attempts), so we offset each sub-run's attempts
	// off parent.Attempts*subRunAttemptOffset. Per-slice retries within
	// a sub-run still use 1..maxAttempts inside that namespace because
	// they're persisted on stage_results, not pipeline_runs.attempts.
	baseAttempts := parent.Attempts * subRunAttemptOffset

	for idx, sl := range slices {
		idx, sl := idx, sl
		attempts := baseAttempts + idx + 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			if sem != nil {
				sem <- struct{}{}
				defer func() { <-sem }()
			}
			results[idx] = i.runOneSlice(ctx, parent, item, sl, attempts)
		}()
	}
	wg.Wait()

	// First-error wins; the integrator does not partial-merge.
	for _, r := range results {
		if r.Err != nil {
			return results, r.Err
		}
	}
	return results, nil
}

// subRunAttemptOffset spaces sub-run attempt numbers off the parent's
// attempt so the UNIQUE(backlog_id, attempts) schema constraint is
// satisfied across parent + sub-runs of one backlog item.
const subRunAttemptOffset = 1000

func (i *Integrator) runOneSlice(ctx context.Context, parent *store.PipelineRun, item *store.BacklogItem, sl store.Slice, attempts int) SubRunResult {
	res := SubRunResult{SliceName: sl.Name}
	branch := BranchContractFor(parent, item, Stage{ID: "implement"}, sl.Name).SliceBranch
	if branch == "" {
		res.Err = fmt.Errorf("branch contract unavailable for slice %s", sl.Name)
		return res
	}
	wt, err := i.Allocator.Allocate(ctx, WorktreeRequest{
		BacklogID:  item.ID,
		SliceName:  sl.Name,
		BranchName: branch,
		BaseBranch: "main",
		Purpose:    fmt.Sprintf("mills slice %s of %s", sl.Name, item.ID),
	})
	if err != nil {
		res.Err = fmt.Errorf("allocate worktree for slice %s: %w", sl.Name, err)
		return res
	}
	defer func() {
		if rerr := i.Allocator.Release(ctx, wt); rerr != nil && i.Logger != nil {
			i.Logger.Warn("integrator: release worktree", "slice", sl.Name, "error", rerr)
		}
	}()

	// Scope a copy of the item to just this slice so the implement
	// worker doesn't accidentally touch siblings' files.
	subItem := *item
	subItem.Slices = []store.Slice{sl}

	subRun := &store.PipelineRun{
		ID:              fmt.Sprintf("%s-%s", parent.ID, sl.Name),
		BacklogID:       item.ID,
		Template:        parent.Template,
		State:           store.PipelineQueued,
		Attempts:        attempts,
		WorktreePath:    wt.Path,
		StartedAt:       i.now(),
		ParentSessionID: parent.ID,
	}
	if err := i.Store.Pipeline.PutRun(ctx, subRun); err != nil {
		res.Err = fmt.Errorf("persist sub-run %s: %w", sl.Name, err)
		return res
	}
	if err := i.Sub.Drive(ctx, subRun, &subItem); err != nil {
		res.Err = fmt.Errorf("drive sub-run %s: %w", sl.Name, err)
		return res
	}
	persisted, err := i.Store.Pipeline.GetRun(ctx, subRun.ID)
	if err != nil {
		res.Err = fmt.Errorf("read sub-run %s: %w", sl.Name, err)
		return res
	}
	res.Run = persisted
	res.Branch = wt.Branch
	if persisted.State == store.PipelineEscalated {
		res.Err = fmt.Errorf("sub-run %s escalated", sl.Name)
	}
	return res
}

// parallelSlices returns the slices that are part of a parallel group.
// Slices without ParallelWith are still included if any sibling marks
// them; the simplest interpretation is "every slice in an item that
// fan-outs is part of the fan-out."
func (i *Integrator) parallelSlices(item *store.BacklogItem) []store.Slice {
	out := make([]store.Slice, 0, len(item.Slices))
	out = append(out, item.Slices...)
	return out
}

func (i *Integrator) semaphore() chan struct{} {
	if i.MaxParallel <= 0 {
		return nil
	}
	return make(chan struct{}, i.MaxParallel)
}

func (i *Integrator) escalateWithItem(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem, reason string) error {
	t := i.now()
	run.State = store.PipelineEscalated
	run.EndedAt = &t
	if err := i.Store.Pipeline.PutRun(ctx, run); err != nil {
		return fmt.Errorf("integrator: persist escalated: %w", err)
	}
	i.event(ctx, "pipeline.integrate.escalated", "error", map[string]any{
		"run": run.ID, "reason": reason,
	})
	if i.Escalator != nil && item != nil {
		if err := i.Escalator.Handle(ctx, run, item, reason); err != nil && i.Logger != nil {
			i.Logger.Warn("integrator escalator failed", "run", run.ID, "error", err)
		}
	}
	return nil
}

func (i *Integrator) now() time.Time {
	if i.Clock != nil {
		return i.Clock()
	}
	return time.Now()
}

func (i *Integrator) event(ctx context.Context, kind, outcome string, payload map[string]any) {
	if i.Store == nil || i.Store.Events == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["outcome"] = outcome
	if err := i.Store.Events.Append(ctx, &store.Event{
		Actor:   "integrator",
		Kind:    kind,
		Payload: payload,
	}); err != nil && i.Logger != nil {
		i.Logger.Warn("integrator: append event", "error", err, "kind", kind)
	}
}
