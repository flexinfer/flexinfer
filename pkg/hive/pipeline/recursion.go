// Package pipeline — Phase 6 slice 6.1: bounded recursion guards.
//
// Hive v2 lets a pipeline worker fan out into sub-runs (e.g. a refactor
// slice splits into a "code" subrun + a "tests" subrun) up to a small
// depth cap, with a budget tree that prevents one runaway subrun from
// drinking the parent's whole quota and a cycle detector that prevents
// the same backlog item from recursing into itself.
//
// This file owns the *guards*; pkg/hive/store DAO owns the persistence
// (CreateSubrun); the operator's HTTP layer (handlers_pipeline.go)
// owns the MCP-flavored entry point. Slice 6.2 wires the worker side
// (tools/spawn-driver/src/control.ts).
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/hive"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// SubrunRequest is one validated bundle of inputs to SubrunCreate.
// Mirrors the operator endpoint body so the guard can be unit-tested
// without an HTTP round trip.
type SubrunRequest struct {
	// ParentRunID is the pipeline run that's spawning the subrun. The
	// guard fetches the parent row to compute depth + walk the
	// ancestor chain.
	ParentRunID string
	// BacklogID is the backlog item id the new subrun will work on.
	// Cycle detection rejects requests where BacklogID already
	// appears in the ancestor chain.
	BacklogID string
	// Template is the pipeline template to drive the subrun. Stored
	// verbatim on the new pipeline_runs row; not interpreted here.
	Template string
	// EstimateUSD is the caller's pre-spawn cost estimate for the
	// whole subrun (every stage). Compared against the parent
	// budget tree per policy.Recursion.SubrunMaxBudgetShare.
	EstimateUSD float64
	// SliceSpec is an opaque caller-supplied descriptor (typically a
	// short JSON or markdown payload) the worker will consume to
	// scope the subrun. Stored on the new run via the worktree path
	// or current_stage payload — slice 6.2 finalizes the wiring.
	// Held here so the guard can refuse pathological payloads at
	// the gate.
	SliceSpec string
}

// GuardCode names which guard rejected a subrun create. The string is
// load-bearing: it appears in the operator's HTTP response body and in
// the spec acceptance criteria ("depth=3 attempt rejected with
// `recursion_depth_exceeded`; over-budget subrun rejected with
// `budget_subrun_too_large`"). Keep these stable.
type GuardCode string

const (
	GuardRecursionDisabled    GuardCode = "recursion_disabled"
	GuardParentNotFound       GuardCode = "recursion_parent_not_found"
	GuardDepthExceeded        GuardCode = "recursion_depth_exceeded"
	GuardBudgetSubrunTooLarge GuardCode = "budget_subrun_too_large"
	GuardCycleDetected        GuardCode = "recursion_cycle_detected"
	GuardSliceSpecTooLarge    GuardCode = "recursion_slicespec_too_large"
	GuardMissingFields        GuardCode = "recursion_missing_fields"
)

// GuardError is the typed error every guard returns. The HTTP layer
// type-asserts to surface Code as the JSON body; everything else
// (validation, budget, cycle) flows through here.
type GuardError struct {
	Code    GuardCode
	Message string
}

func (e *GuardError) Error() string { return string(e.Code) + ": " + e.Message }

// guardErr is a tiny constructor that keeps call sites scannable.
func guardErr(code GuardCode, format string, args ...any) *GuardError {
	return &GuardError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// SubrunGuard is the slice-6.1 surface. Wiring is dependency-injected
// so unit tests can drive the guard against an in-memory store + a
// fixed *Policy without spinning up the full operator.
type SubrunGuard struct {
	// Store is the canonical pipeline + backlog DAO. Required.
	Store *store.Store
	// PolicyFunc returns the live *Policy. Production wires this to
	// the PolicyManager so hot-reloads of the recursion section
	// take effect without a restart.
	PolicyFunc func() *hive.Policy
	// Now is injectable for deterministic test ids. Defaults to
	// time.Now.
	Now func() time.Time
	// IDPrefix lets tests pin "PIPE-…" id strings. Empty falls back
	// to a default "PIPE-" prefix.
	IDPrefix string
}

// Defaults — kept outside the struct so they participate in the
// "policy zero values fall back to safe defaults" contract elsewhere
// in pkg/hive (see budget.go's tierLimits + RecursionPolicy doc).
const (
	defaultRecursionMaxDepth    = 2
	defaultRecursionBudgetShare = 0.5
	maxSliceSpecBytes           = 32 * 1024 // 32 KiB cap on opaque payload
	defaultSubrunIDPrefix       = "PIPE-"
)

// SubrunCreate runs every guard in order, then persists a new
// pipeline_runs row via store.PipelineDAO.CreateSubrun. Returns the
// new run id on success, a *GuardError on rejection (every guard sets
// .Code), or a wrapped DB error for transport-level failures.
//
// Order matters: cheap, pure checks first; DB-touching checks last so
// a misconfigured request fails fast.
func (g *SubrunGuard) SubrunCreate(ctx context.Context, req SubrunRequest) (string, error) {
	if g == nil || g.Store == nil || g.PolicyFunc == nil {
		return "", errors.New("recursion: SubrunGuard not configured")
	}

	// (1) Cheap field validation.
	if strings.TrimSpace(req.ParentRunID) == "" {
		return "", guardErr(GuardMissingFields, "parent_run_id required")
	}
	if strings.TrimSpace(req.BacklogID) == "" {
		return "", guardErr(GuardMissingFields, "backlog_id required")
	}
	if strings.TrimSpace(req.Template) == "" {
		return "", guardErr(GuardMissingFields, "template required")
	}
	if req.EstimateUSD < 0 {
		return "", guardErr(GuardMissingFields, "estimate_usd must be >= 0 (got %.4f)", req.EstimateUSD)
	}
	if len(req.SliceSpec) > maxSliceSpecBytes {
		return "", guardErr(GuardSliceSpecTooLarge,
			"slice_spec %d bytes exceeds %d-byte cap", len(req.SliceSpec), maxSliceSpecBytes)
	}

	// (2) Policy gate. RecursionPolicy.Enabled defaults false (V2-D6),
	// so the guard refuses by default — opt-in only.
	policy := g.PolicyFunc()
	if policy == nil {
		return "", errors.New("recursion: policy unavailable")
	}
	if !policy.Recursion.Enabled {
		return "", guardErr(GuardRecursionDisabled, "policy.recursion.enabled = false")
	}

	maxDepth := policy.Recursion.MaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultRecursionMaxDepth
	}
	share := policy.Recursion.SubrunMaxBudgetShare
	if share <= 0 {
		share = defaultRecursionBudgetShare
	}

	// (3) Parent lookup (DB hit). The parent must exist before we
	// can compute depth or walk the cycle chain.
	parent, err := g.Store.Pipeline.GetRun(ctx, req.ParentRunID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", guardErr(GuardParentNotFound,
				"parent run %q not found", req.ParentRunID)
		}
		return "", fmt.Errorf("recursion: get parent: %w", err)
	}

	// (4) Depth cap. Parent at depth N → child at N+1. Reject when
	// the new depth would exceed the policy ceiling.
	newDepth := parent.Depth + 1
	if newDepth > maxDepth {
		return "", guardErr(GuardDepthExceeded,
			"new depth %d > policy.recursion.max_depth %d", newDepth, maxDepth)
	}

	// (5) Budget tree. Each subrun may spend at most `share` of the
	// parent tier's per-run cap. The cap query reads from the
	// canonical Budgets.Pipeline limits so a hot-reload tightens
	// every in-flight branch without restart.
	parentMax := policy.Budgets.Pipeline.MaxUSDPerRun
	if parentMax > 0 {
		subrunCap := parentMax * share
		if req.EstimateUSD > subrunCap {
			return "", guardErr(GuardBudgetSubrunTooLarge,
				"estimate %.4f > parent_max_usd_per_run %.4f * share %.2f = %.4f",
				req.EstimateUSD, parentMax, share, subrunCap)
		}
	}

	// (6) Cycle detection. Walk the ancestor chain by ParentRunID,
	// collect every ancestor's BacklogID, and reject if the new
	// BacklogID matches. The walk is bounded by maxDepth iterations
	// — there can't be a longer chain by construction (every link
	// up was guarded by step (4) when it was created).
	if err := g.detectCycle(ctx, parent, req.BacklogID, maxDepth); err != nil {
		return "", err
	}

	// (7) Persist. CreateSubrun is the transactional helper that
	// stamps parent_run_id + depth = parent.depth + 1 + the
	// timestamp on a single INSERT.
	now := g.now()
	prefix := g.IDPrefix
	if prefix == "" {
		prefix = defaultSubrunIDPrefix
	}
	newID := prefix + now.Format("2006-01-02-150405.000000")
	newRun := &store.PipelineRun{
		ID:              newID,
		BacklogID:       req.BacklogID,
		Template:        req.Template,
		State:           store.PipelineQueued,
		Attempts:        0,
		StartedAt:       now,
		ParentSessionID: parent.ParentSessionID,
		ParentRunID:     &parent.ID,
		Depth:           newDepth,
	}
	if err := g.Store.Pipeline.CreateSubrun(ctx, newRun); err != nil {
		return "", fmt.Errorf("recursion: create subrun: %w", err)
	}
	// Claim the backlog item by transitioning it to Running so the
	// reconciler's main loop (which scans BacklogQueued) doesn't ALSO
	// try to start a duplicate pipeline run for the same item. The
	// subrun-pickup loop in pkg/hive/reconciler.go's Tick is the
	// authoritative kickoff path for recursion-spawned runs. Best-
	// effort: a missing backlog row is logged but doesn't unwind the
	// successful subrun create — the caller is expected to have
	// seeded the backlog before calling SubrunCreate.
	if err := g.claimBacklog(ctx, req.BacklogID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return newID, fmt.Errorf("recursion: claim backlog: %w", err)
	}
	hive.PipelineRecursionDepthHistogram.Observe(float64(newDepth))
	return newID, nil
}

// claimBacklog flips the target backlog item to Running so the
// parallel queued-backlog reconciler doesn't see it. Idempotent —
// already-Running items are left alone.
func (g *SubrunGuard) claimBacklog(ctx context.Context, backlogID string) error {
	item, err := g.Store.Backlog.Get(ctx, backlogID)
	if err != nil {
		return err
	}
	if item.State == store.BacklogRunning {
		return nil
	}
	item.State = store.BacklogRunning
	return g.Store.Backlog.Put(ctx, item)
}

// detectCycle walks the ancestor chain looking for a backlog id match
// against `wantBacklogID`. Returns nil when no cycle is found, a
// *GuardError when one is, or a DB error if a fetch fails mid-walk.
//
// Cap the walk at maxDepth+1 hops as a safety net against malformed
// data (a self-referential row would otherwise loop forever).
func (g *SubrunGuard) detectCycle(ctx context.Context, parent *store.PipelineRun, wantBacklogID string, maxDepth int) error {
	cur := parent
	for hops := 0; hops <= maxDepth; hops++ {
		if cur == nil {
			return nil
		}
		if cur.BacklogID == wantBacklogID {
			return guardErr(GuardCycleDetected,
				"backlog_id %q already appears at ancestor run %s (depth %d)",
				wantBacklogID, cur.ID, cur.Depth)
		}
		if cur.ParentRunID == nil || *cur.ParentRunID == "" {
			return nil
		}
		next, err := g.Store.Pipeline.GetRun(ctx, *cur.ParentRunID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// Broken chain — treat as no cycle (parent
				// missing). Logged at the call site.
				return nil
			}
			return fmt.Errorf("recursion: walk ancestor: %w", err)
		}
		cur = next
	}
	// Walked further than max_depth allows — chain is corrupted, but
	// we've already accepted no cycle so far. Treat as no cycle.
	return nil
}

func (g *SubrunGuard) now() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}
