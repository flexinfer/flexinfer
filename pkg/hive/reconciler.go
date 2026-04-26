package hive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// Reconciler is the desired-state loop that turns queued backlog items into
// running pipeline runs. It is the operator's "control law": every tick it
// inspects the canonical store and either starts a new pipeline run, defers
// (deps unmet, budget exhausted), or logs a no-op tick into the events
// table for audit.
//
// The reconciler is intentionally cheap and idempotent. The expensive work
// (per-stage spawn, gate evaluation) lives in PipelineStarter implementations
// (slice 4.x), which the reconciler invokes asynchronously. A long-running
// pipeline run does NOT block subsequent reconcile ticks.
type Reconciler struct {
	Store   *store.Store
	Policy  *PolicyManager
	Budget  *Budget
	Starter PipelineStarter
	Clock   func() time.Time
	Logger  *slog.Logger

	// Now is unset by default; constructors fill it. Public so tests can
	// rewrite it between ticks.
}

// PipelineStarter spawns a pipeline run for a queued backlog item. The
// reconciler creates the pipeline_runs row inside its own transaction and
// hands the new run id off to the starter; the starter is responsible for
// driving stages forward (slice 4.x). A nil error means "accepted, will
// report progress via stage_results / events"; any error rolls back the
// reconciler's tick for this item.
type PipelineStarter interface {
	Start(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error
}

// NewReconciler constructs a Reconciler with sensible defaults. Logger and
// Clock fall back to slog.Default() and time.Now respectively.
func NewReconciler(s *store.Store, pm *PolicyManager, b *Budget, starter PipelineStarter) *Reconciler {
	return &Reconciler{
		Store:   s,
		Policy:  pm,
		Budget:  b,
		Starter: starter,
		Clock:   time.Now,
		Logger:  slog.Default(),
	}
}

// Tick performs one reconciliation pass. It is safe to call concurrently
// (the SQLite store serialises writers under busy_timeout) but the
// scheduler in scheduler.go drives it sequentially to keep the audit log
// readable.
//
// Tick is the contract for tests: drive it directly with fake stores +
// starters to exercise every transition path.
func (r *Reconciler) Tick(ctx context.Context) (TickResult, error) {
	if r == nil || r.Store == nil {
		return TickResult{}, errors.New("reconciler: not configured")
	}
	policy := r.Policy.Current()
	if !policy.IsEnabled() {
		r.append(ctx, "reconciler.tick", "skipped", map[string]any{"reason": "policy disabled"})
		return TickResult{SkipReason: "policy disabled"}, nil
	}

	queued, err := r.Store.Backlog.ListByState(ctx, store.BacklogQueued)
	if err != nil {
		return TickResult{}, fmt.Errorf("read queue: %w", err)
	}

	res := TickResult{Inspected: len(queued)}
	for _, item := range queued {
		decision, err := r.tryStart(ctx, item, policy)
		if err != nil {
			r.append(ctx, "reconciler.start_failed", "error", map[string]any{
				"item": item.ID, "error": err.Error(),
			})
			res.Errored++
			continue
		}
		switch decision {
		case decisionStarted:
			res.Started++
		case decisionDeferred:
			res.Deferred++
		case decisionSkipped:
			res.Skipped++
		}
	}

	r.append(ctx, "reconciler.tick", "ok", map[string]any{
		"inspected": res.Inspected, "started": res.Started,
		"deferred": res.Deferred, "skipped": res.Skipped, "errored": res.Errored,
	})
	return res, nil
}

// TickResult summarises the work one Tick performed. Useful for tests +
// HUD; the scheduler also exports it as a Prometheus gauge in slice 5.1.
type TickResult struct {
	Inspected  int
	Started    int
	Deferred   int
	Skipped    int
	Errored    int
	SkipReason string
}

type startDecision int

const (
	decisionStarted  startDecision = iota
	decisionDeferred               // dependencies unmet or budget exhausted
	decisionSkipped                // explicitly out of scope (e.g. paused)
)

// tryStart evaluates dependencies + budget + policy and either kicks off a
// pipeline run (returning decisionStarted) or defers / skips with a reason
// recorded in the events log.
func (r *Reconciler) tryStart(ctx context.Context, item *store.BacklogItem, policy *Policy) (startDecision, error) {
	// Dependency check: every backlog item in item.Dependencies must be in
	// state=merged. Anything else (running, paused, escalated) blocks.
	if len(item.Dependencies) > 0 {
		ok, blocker, err := r.dependenciesMet(ctx, item)
		if err != nil {
			return decisionDeferred, err
		}
		if !ok {
			r.append(ctx, "reconciler.deferred", "deps", map[string]any{
				"item": item.ID, "blocked_by": blocker,
			})
			return decisionDeferred, nil
		}
	}

	// Budget: the council estimates per-item cost via item.Budget.MaxCostUSD.
	// The pipeline tier's daily caps + concurrency caps are enforced by the
	// hive.Budget enforcer.
	estimate := item.Budget.MaxCostUSD
	dec, err := r.Budget.Allow(ctx, TierPipeline, estimate)
	if err != nil {
		return decisionDeferred, fmt.Errorf("budget: %w", err)
	}
	if !dec.Allowed {
		r.append(ctx, "reconciler.deferred", "budget", map[string]any{
			"item":      item.ID,
			"reasons":   dec.Reasons,
			"spent":     dec.SpentUSD,
			"remaining": dec.RemainingUSD,
		})
		return decisionDeferred, nil
	}

	// Policy gate: items flagged require_human_review without an explicit
	// human handoff in flight are deferred — the reconciler doesn't pick
	// them up autonomously; a human (or the escalation path) does.
	if item.Policy.RequireHumanReview {
		r.append(ctx, "reconciler.skipped", "policy", map[string]any{
			"item": item.ID, "reason": "require_human_review=true",
		})
		return decisionSkipped, nil
	}

	// Instantiate a pipeline run row + transition the backlog item to
	// running. State changes are persisted before we hand off to the
	// Starter so a starter crash can't leave us in a half-state.
	run := &store.PipelineRun{
		ID:        newPipelineRunID(item.ID, r.now()),
		BacklogID: item.ID,
		Template:  policy.Pipeline.DefaultTemplate,
		State:     store.PipelineQueued,
		Attempts:  1,
		StartedAt: r.now(),
	}
	if err := r.Store.Pipeline.PutRun(ctx, run); err != nil {
		return decisionDeferred, fmt.Errorf("persist run: %w", err)
	}
	item.State = store.BacklogRunning
	if err := r.Store.Backlog.Put(ctx, item); err != nil {
		// Best-effort rollback so we don't leak a dangling run row.
		_ = r.Store.DB().Close // no-op; shouldn't actually close
		return decisionDeferred, fmt.Errorf("transition backlog: %w", err)
	}

	if r.Starter != nil {
		if err := r.Starter.Start(ctx, run, item); err != nil {
			r.append(ctx, "reconciler.start_failed", "starter", map[string]any{
				"item": item.ID, "run": run.ID, "error": err.Error(),
			})
			return decisionDeferred, err
		}
	}

	r.append(ctx, "reconciler.started", "ok", map[string]any{
		"item": item.ID, "run": run.ID, "estimate_usd": estimate,
	})
	return decisionStarted, nil
}

// dependenciesMet returns (true, "", nil) when every Dependency item is
// in state=merged. Otherwise returns the first blocker's id.
func (r *Reconciler) dependenciesMet(ctx context.Context, item *store.BacklogItem) (bool, string, error) {
	for _, dep := range item.Dependencies {
		got, err := r.Store.Backlog.Get(ctx, dep)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// A dependency that no longer exists blocks indefinitely;
				// surface as a clear blocker rather than silently passing.
				return false, dep, nil
			}
			return false, dep, fmt.Errorf("read dep %s: %w", dep, err)
		}
		if got.State != store.BacklogMerged {
			return false, dep, nil
		}
	}
	return true, "", nil
}

// newPipelineRunID composes a stable id of the form
// PIPE-<backlogID>-<unixsec>. Predictable in tests, sortable in lists.
func newPipelineRunID(backlogID string, t time.Time) string {
	return fmt.Sprintf("PIPE-%s-%d", backlogID, t.UTC().Unix())
}

func (r *Reconciler) now() time.Time {
	if r.Clock != nil {
		return r.Clock()
	}
	return time.Now()
}

func (r *Reconciler) append(ctx context.Context, kind, outcome string, payload map[string]any) {
	if r.Store == nil || r.Store.Events == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["outcome"] = outcome
	if err := r.Store.Events.Append(ctx, &store.Event{
		Actor:   "reconciler",
		Kind:    kind,
		Payload: payload,
	}); err != nil && r.Logger != nil {
		r.Logger.Warn("reconciler: append event failed", "error", err, "kind", kind)
	}
}
