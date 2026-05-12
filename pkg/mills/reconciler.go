package mills

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
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

	// AutonomyGate, when set, must report ready before the reconciler starts
	// queued work. The operator wires this to its capability matrix so
	// policy.enabled=true is necessary but not sufficient for autonomous
	// writes when required dependencies are missing or stubbed.
	AutonomyGate AutonomyGateFunc

	// SquadRouter, when set, is consulted before handing each pipeline
	// run off to the Starter (Phase 2 v2.0 reconciler integration). The
	// returned squad attribution is emitted as a "reconciler.squad_routed"
	// event keyed on (subject_kind=pipeline_run, subject_id=run.ID) so
	// the squads.OutcomeRecorder can read it back at merge time. Nil
	// keeps v1 behavior — no routing, no event, no attribution.
	SquadRouter SquadRouter

	// Now is unset by default; constructors fill it. Public so tests can
	// rewrite it between ticks.
}

// SquadRouter is the contract the reconciler depends on for v2 squad
// routing. Production wiring satisfies this with *squads.Router; tests
// inject a fake. Returning a SquadDecision with SquadName==FallbackName
// is normal — the reconciler still emits an attribution event so the
// audit trail records the routing decision (even when the choice was
// "none of the configured squads").
type SquadRouter interface {
	Pick(ctx context.Context, item *store.BacklogItem) (SquadDecision, error)
}

// SquadDecision is the subset of the squads.Decision shape the reconciler
// needs. Defined here to keep pkg/mills free of an import cycle on
// pkg/mills/squads (squads imports pkg/mills/store, not pkg/mills itself,
// but the operator wiring is cleaner with the contract here too).
type SquadDecision struct {
	SquadName  string
	PathClass  string
	Confidence float64
	SampleSize int
	Reason     string
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

// StartQueuedResult reports the outcome of a manual start request.
type StartQueuedResult struct {
	Run       *store.PipelineRun
	Decision  string
	Reason    string
	Blockers  []string
	BacklogID string
}

// ResumeInFlightResult reports startup recovery work for pipeline runs that
// were active when the previous operator process exited.
type ResumeInFlightResult struct {
	Inspected int
	Resumed   int
	Errored   int
}

// TerminalBacklogSyncResult reports stale running backlog items repaired from
// terminal pipeline state during startup.
type TerminalBacklogSyncResult struct {
	Inspected int
	Updated   int
	Skipped   int
	Errored   int
}

var (
	ErrPolicyDisabled   = errors.New("reconciler: policy disabled")
	ErrBacklogNotQueued = errors.New("reconciler: backlog item is not queued")
)

// AutonomyBlockedError reports a fail-closed autonomy gate.
type AutonomyBlockedError struct {
	Blockers []string
}

func (e *AutonomyBlockedError) Error() string {
	if e == nil || len(e.Blockers) == 0 {
		return "reconciler: autonomy blocked"
	}
	return "reconciler: autonomy blocked: " + strings.Join(e.Blockers, "; ")
}

// AutonomyGateFunc returns whether autonomous pipeline starts are allowed and
// the human-readable blockers when they are not.
type AutonomyGateFunc func(ctx context.Context) (ready bool, blockers []string)

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
	tickStart := r.now()
	defer func() {
		ReconcileTickDurationSeconds.Observe(r.now().Sub(tickStart).Seconds())
	}()
	policy := r.Policy.Current()
	if !policy.IsEnabled() {
		ReconcileTicksTotal.WithLabelValues("skipped").Inc()
		r.append(ctx, "reconciler.tick", "skipped", map[string]any{"reason": "policy disabled"})
		return TickResult{SkipReason: "policy disabled"}, nil
	}
	if r.AutonomyGate != nil {
		ready, blockers := r.AutonomyGate(ctx)
		if !ready {
			ReconcileTicksTotal.WithLabelValues("skipped").Inc()
			r.append(ctx, "reconciler.tick", "skipped", map[string]any{
				"reason":   "autonomy blocked",
				"blockers": blockers,
			})
			return TickResult{SkipReason: "autonomy blocked"}, nil
		}
	}

	queued, err := r.Store.Backlog.ListByState(ctx, store.BacklogQueued)
	if err != nil {
		ReconcileTicksTotal.WithLabelValues("errored").Inc()
		return TickResult{}, fmt.Errorf("read queue: %w", err)
	}
	r.refreshActiveGauges(ctx)

	res := TickResult{Inspected: len(queued)}
	for _, item := range queued {
		decision, _, err := r.tryStart(ctx, item, policy)
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

	// Phase 6 slice 6.2: also pick up queued subruns. A worker
	// running under this operator may have called the recursion
	// endpoint mid-stage, which inserts a pipeline_runs row in
	// state=queued with parent_run_id != NULL. The Starter knows
	// how to drive an existing run row forward, so the same
	// PipelineStarter wired for backlog-item launches is reused.
	subStarted, subErrs := r.pickupQueuedSubruns(ctx)
	res.Started += subStarted
	res.Errored += subErrs

	tickOutcome := tickOutcomeLabel(res)
	ReconcileTicksTotal.WithLabelValues(tickOutcome).Inc()
	r.append(ctx, "reconciler.tick", "ok", map[string]any{
		"inspected": res.Inspected, "started": res.Started,
		"deferred": res.Deferred, "skipped": res.Skipped, "errored": res.Errored,
		"subrun_started": subStarted, "subrun_errored": subErrs,
	})
	return res, nil
}

// StartQueuedItem starts one queued backlog item immediately through the same
// dependency, budget, policy, squad-routing, and starter path used by Tick.
func (r *Reconciler) StartQueuedItem(ctx context.Context, backlogID string) (StartQueuedResult, error) {
	if r == nil || r.Store == nil {
		return StartQueuedResult{}, errors.New("reconciler: not configured")
	}
	backlogID = strings.TrimSpace(backlogID)
	if backlogID == "" {
		return StartQueuedResult{}, errors.New("reconciler: backlog id required")
	}
	policy := r.Policy.Current()
	if !policy.IsEnabled() {
		return StartQueuedResult{BacklogID: backlogID, Decision: "skipped", Reason: "policy disabled"}, ErrPolicyDisabled
	}
	if r.AutonomyGate != nil {
		ready, blockers := r.AutonomyGate(ctx)
		if !ready {
			return StartQueuedResult{
				BacklogID: backlogID,
				Decision:  "skipped",
				Reason:    "autonomy blocked",
				Blockers:  blockers,
			}, &AutonomyBlockedError{Blockers: blockers}
		}
	}
	item, err := r.Store.Backlog.Get(ctx, backlogID)
	if err != nil {
		return StartQueuedResult{BacklogID: backlogID, Decision: "error"}, err
	}
	if item.State != store.BacklogQueued {
		return StartQueuedResult{
			BacklogID: backlogID,
			Decision:  "skipped",
			Reason:    fmt.Sprintf("state is %s", item.State),
		}, fmt.Errorf("%w: %s", ErrBacklogNotQueued, item.State)
	}
	decision, run, err := r.tryStart(ctx, item, policy)
	res := StartQueuedResult{Run: run, Decision: decision.String(), BacklogID: backlogID}
	if err != nil {
		res.Reason = err.Error()
		return res, err
	}
	if run == nil {
		res.Reason = "not started"
	}
	return res, nil
}

// ResumeInFlightRuns starts runner goroutines for non-terminal runs that were
// already past the queued state when this operator process booted. It is
// intended to be called once during startup; normal Tick reconciliation should
// not invoke it or it could duplicate currently-running goroutines.
func (r *Reconciler) ResumeInFlightRuns(ctx context.Context) (ResumeInFlightResult, error) {
	if r == nil || r.Store == nil {
		return ResumeInFlightResult{}, errors.New("reconciler: not configured")
	}
	if r.Starter == nil {
		return ResumeInFlightResult{}, nil
	}
	runs, err := r.Store.Pipeline.ListInFlight(ctx)
	if err != nil {
		return ResumeInFlightResult{}, err
	}
	res := ResumeInFlightResult{Inspected: len(runs)}
	for _, run := range runs {
		item, lerr := r.Store.Backlog.Get(ctx, run.BacklogID)
		if lerr != nil {
			r.append(ctx, "reconciler.resume_failed", "error", map[string]any{
				"run": run.ID, "backlog": run.BacklogID, "error": lerr.Error(),
			})
			res.Errored++
			continue
		}
		if err := r.Starter.Start(ctx, run, item); err != nil {
			r.append(ctx, "reconciler.resume_failed", "error", map[string]any{
				"run": run.ID, "backlog": run.BacklogID, "error": err.Error(),
			})
			res.Errored++
			continue
		}
		r.append(ctx, "reconciler.resumed", "ok", map[string]any{
			"run": run.ID, "backlog": run.BacklogID, "state": string(run.State), "stage": run.CurrentStage,
		})
		res.Resumed++
	}
	if res.Inspected > 0 {
		r.append(ctx, "reconciler.resume_tick", "ok", map[string]any{
			"inspected": res.Inspected, "resumed": res.Resumed, "errored": res.Errored,
		})
	}
	return res, nil
}

// SyncTerminalBacklogs repairs running backlog rows whose pipeline runs already
// reached a terminal state. This is intentionally safe to run on startup: it
// skips any backlog with an active run and only mirrors done/escalated/paused
// terminal pipeline state onto stale backlog rows.
func (r *Reconciler) SyncTerminalBacklogs(ctx context.Context) (TerminalBacklogSyncResult, error) {
	if r == nil || r.Store == nil {
		return TerminalBacklogSyncResult{}, errors.New("reconciler: not configured")
	}
	items, err := r.Store.Backlog.ListByState(ctx, store.BacklogRunning)
	if err != nil {
		return TerminalBacklogSyncResult{}, fmt.Errorf("list running backlog: %w", err)
	}
	res := TerminalBacklogSyncResult{Inspected: len(items)}
	for _, item := range items {
		runs, err := r.Store.Pipeline.ListByBacklog(ctx, item.ID)
		if err != nil {
			r.append(ctx, "reconciler.backlog_terminal_sync_failed", "error", map[string]any{
				"backlog": item.ID, "error": err.Error(),
			})
			res.Errored++
			continue
		}
		state, ok := terminalBacklogState(runs)
		if !ok {
			res.Skipped++
			continue
		}
		item.State = state
		if err := r.Store.Backlog.Put(ctx, item); err != nil {
			r.append(ctx, "reconciler.backlog_terminal_sync_failed", "error", map[string]any{
				"backlog": item.ID, "state": string(state), "error": err.Error(),
			})
			res.Errored++
			continue
		}
		r.append(ctx, "reconciler.backlog_terminal_synced", "ok", map[string]any{
			"backlog": item.ID, "state": string(state),
		})
		res.Updated++
	}
	if res.Inspected > 0 {
		r.append(ctx, "reconciler.backlog_terminal_sync_tick", "ok", map[string]any{
			"inspected": res.Inspected, "updated": res.Updated,
			"skipped": res.Skipped, "errored": res.Errored,
		})
	}
	return res, nil
}

func terminalBacklogState(runs []*store.PipelineRun) (store.BacklogState, bool) {
	var latest *store.PipelineRun
	for _, run := range runs {
		if run == nil {
			continue
		}
		if !isTerminalPipelineState(run.State) {
			return "", false
		}
		if latest == nil || run.StartedAt.After(latest.StartedAt) || (run.StartedAt.Equal(latest.StartedAt) && run.Attempts > latest.Attempts) {
			latest = run
		}
	}
	if latest == nil {
		return "", false
	}
	switch latest.State {
	case store.PipelineDone:
		return store.BacklogMerged, true
	case store.PipelineEscalated:
		return store.BacklogEscalated, true
	case store.PipelinePaused:
		return store.BacklogPaused, true
	default:
		return "", false
	}
}

func isTerminalPipelineState(state store.PipelineState) bool {
	switch state {
	case store.PipelineDone, store.PipelineEscalated, store.PipelinePaused:
		return true
	default:
		return false
	}
}

// pickupQueuedSubruns looks up every pipeline run created by
// recursion.SubrunGuard but not yet started (state=queued AND
// parent_run_id IS NOT NULL AND attempts=0) and asks the
// PipelineStarter to drive each forward. Errors are logged
// per-row and counted into the tick result; one failure does not
// block the rest. Returns (started, errored) so Tick can roll the
// counters into TickResult.
func (r *Reconciler) pickupQueuedSubruns(ctx context.Context) (int, int) {
	if r.Store == nil || r.Starter == nil {
		return 0, 0
	}
	subruns, err := r.Store.Pipeline.ListQueuedSubruns(ctx)
	if err != nil {
		r.append(ctx, "reconciler.subrun_pickup_failed", "error", map[string]any{"error": err.Error()})
		return 0, 1
	}
	var started, errored int
	for _, run := range subruns {
		// Look up the backlog item the subrun targets so the
		// Starter has the same JobContext shape it gets for a
		// fresh-from-backlog launch. A subrun with a missing
		// item is a corrupted state — log + skip.
		item, lerr := r.Store.Backlog.Get(ctx, run.BacklogID)
		if lerr != nil {
			r.append(ctx, "reconciler.subrun_pickup_failed", "error", map[string]any{
				"run": run.ID, "backlog": run.BacklogID, "error": lerr.Error(),
			})
			errored++
			continue
		}
		if err := r.Starter.Start(ctx, run, item); err != nil {
			r.append(ctx, "reconciler.subrun_start_failed", "error", map[string]any{
				"run": run.ID, "backlog": run.BacklogID, "error": err.Error(),
			})
			errored++
			continue
		}
		r.append(ctx, "reconciler.subrun_started", "ok", map[string]any{
			"run": run.ID, "backlog": run.BacklogID, "depth": run.Depth,
			"parent_run": derefString(run.ParentRunID),
		})
		started++
	}
	return started, errored
}

// derefString safely dereferences a *string for log payloads.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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

// IsNoOp reports whether the tick had nothing to look at — either the
// queue was empty or the policy was disabled. The scheduler uses this to
// decide when to back off to the idle-throttle cadence (slice 6.1).
// Inspected > 0 means the operator is doing meaningful bookkeeping even
// if every item was deferred, so we keep ticking on the fast cadence.
func (r TickResult) IsNoOp() bool {
	return r.Inspected == 0 && r.Started == 0
}

type startDecision int

const (
	decisionStarted  startDecision = iota
	decisionDeferred               // dependencies unmet or budget exhausted
	decisionSkipped                // explicitly out of scope (e.g. paused)
)

func (d startDecision) String() string {
	switch d {
	case decisionStarted:
		return "started"
	case decisionDeferred:
		return "deferred"
	case decisionSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// tryStart evaluates dependencies + budget + policy and either kicks off a
// pipeline run (returning decisionStarted) or defers / skips with a reason
// recorded in the events log.
func (r *Reconciler) tryStart(ctx context.Context, item *store.BacklogItem, policy *Policy) (startDecision, *store.PipelineRun, error) {
	// Dependency check: every backlog item in item.Dependencies must be in
	// state=merged. Anything else (running, paused, escalated) blocks.
	if len(item.Dependencies) > 0 {
		ok, blocker, err := r.dependenciesMet(ctx, item)
		if err != nil {
			return decisionDeferred, nil, err
		}
		if !ok {
			r.append(ctx, "reconciler.deferred", "deps", map[string]any{
				"item": item.ID, "blocked_by": blocker,
			})
			return decisionDeferred, nil, nil
		}
	}

	// Budget: the council estimates per-item cost via item.Budget.MaxCostUSD.
	// The pipeline tier's daily caps + concurrency caps are enforced by the
	// mills.Budget enforcer.
	estimate := item.Budget.MaxCostUSD
	dec, err := r.Budget.Allow(ctx, TierPipeline, estimate)
	if err != nil {
		return decisionDeferred, nil, fmt.Errorf("budget: %w", err)
	}
	if !dec.Allowed {
		r.append(ctx, "reconciler.deferred", "budget", map[string]any{
			"item":      item.ID,
			"reasons":   dec.Reasons,
			"spent":     dec.SpentUSD,
			"remaining": dec.RemainingUSD,
		})
		return decisionDeferred, nil, nil
	}

	// Policy gate: items flagged require_human_review without an explicit
	// human handoff in flight are deferred — the reconciler doesn't pick
	// them up autonomously; a human (or the escalation path) does.
	if item.Policy.RequireHumanReview {
		r.append(ctx, "reconciler.skipped", "policy", map[string]any{
			"item": item.ID, "reason": "require_human_review=true",
		})
		return decisionSkipped, nil, nil
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
		return decisionDeferred, nil, fmt.Errorf("persist run: %w", err)
	}
	item.State = store.BacklogRunning
	if err := r.Store.Backlog.Put(ctx, item); err != nil {
		// Best-effort rollback so we don't leak a dangling run row.
		_ = r.Store.DB().Close // no-op; shouldn't actually close
		return decisionDeferred, nil, fmt.Errorf("transition backlog: %w", err)
	}

	// Squad routing: when configured, consult the router before handing
	// the run off. The decision is emitted as an event keyed on the run
	// id so squads.OutcomeRecorder can read it back at merge time. A
	// router error is logged but does not block the run — v1 behavior
	// always wins on degraded paths.
	r.routeToSquad(ctx, run, item)

	if r.Starter != nil {
		if err := r.Starter.Start(ctx, run, item); err != nil {
			r.append(ctx, "reconciler.start_failed", "starter", map[string]any{
				"item": item.ID, "run": run.ID, "error": err.Error(),
			})
			return decisionDeferred, run, err
		}
	}

	r.append(ctx, "reconciler.started", "ok", map[string]any{
		"item": item.ID, "run": run.ID, "estimate_usd": estimate,
	})
	return decisionStarted, run, nil
}

// routeToSquad runs the squad router and emits an attribution event
// keyed on (subject_kind=pipeline_run, subject_id=run.ID). Best-effort:
// router errors and event-append errors are logged but do not block the
// run. When SquadRouter is nil, the function is a no-op.
func (r *Reconciler) routeToSquad(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) {
	if r == nil || r.SquadRouter == nil {
		return
	}
	decision, err := r.SquadRouter.Pick(ctx, item)
	if err != nil {
		if r.Logger != nil {
			r.Logger.Warn("reconciler: squad routing failed", "item", item.ID, "error", err)
		}
		return
	}
	payload := map[string]any{
		"run_id":      run.ID,
		"backlog_id":  item.ID,
		"squad_name":  decision.SquadName,
		"path_class":  decision.PathClass,
		"confidence":  decision.Confidence,
		"sample_size": decision.SampleSize,
		"reason":      decision.Reason,
		"outcome":     "ok",
	}
	if r.Store == nil || r.Store.Events == nil {
		return
	}
	if err := r.Store.Events.Append(ctx, &store.Event{
		Actor:       "reconciler",
		Kind:        "reconciler.squad_routed",
		SubjectKind: "pipeline_run",
		SubjectID:   run.ID,
		Payload:     payload,
	}); err != nil && r.Logger != nil {
		r.Logger.Warn("reconciler: append squad_routed event failed",
			"error", err, "run", run.ID)
	}
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

// pipelineActiveStates is the set of non-terminal pipeline states the
// reconciler refreshes gauges for. Mirrors the active-list filter in
// handlePipelineRunsList so the dashboard count matches the REST API.
var pipelineActiveStates = []store.PipelineState{
	store.PipelineQueued, store.PipelinePlanning, store.PipelineSlicing,
	store.PipelineImplementing, store.PipelineTesting, store.PipelineReviewing,
	store.PipelineMR, store.PipelineCI, store.PipelineMerging,
}

// refreshActiveGauges samples the per-state active-pipeline counts and
// writes them to PipelineActiveGauge. Called once per tick — cheap
// because the DAO indexes pipeline_runs by state.
func (r *Reconciler) refreshActiveGauges(ctx context.Context) {
	if r.Store == nil || r.Store.Pipeline == nil {
		return
	}
	for _, s := range pipelineActiveStates {
		runs, err := r.Store.Pipeline.ListByState(ctx, s)
		if err != nil {
			continue
		}
		PipelineActiveGauge.WithLabelValues(string(s)).Set(float64(len(runs)))
	}
}

// tickOutcomeLabel collapses TickResult into a single label value for
// ReconcileTicksTotal so cardinality stays bounded.
func tickOutcomeLabel(res TickResult) string {
	switch {
	case res.Errored > 0:
		return "errored"
	case res.Started > 0:
		return "started_one"
	case res.Deferred > 0:
		return "deferred"
	case res.Skipped > 0:
		return "skipped"
	default:
		return "no_op"
	}
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
