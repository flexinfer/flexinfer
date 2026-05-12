// Package pipeline drives a backlog item through the mills-default-pipeline
// DAG defined in cmd/mcp-mentatlab/templates/mills-default-pipeline.yaml.
//
// The Runner is the operator-side state machine that materialises a
// pipeline_runs row through every stage, persists stage_results +
// gate_outcomes after each step, and either reaches a terminal state
// (done, escalated, paused) or returns control so the reconciler can
// resume on the next tick.
//
// Slice 4.1 ships the engine: stage iteration, gate evaluation, retry on
// gate fail, and resume-on-restart. Worker dispatch is behind the
// WorkerDispatcher interface so slice 4.2 can drop in spawn/devbox/MCP
// implementations without touching this file.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/crb2nu/loom/pkg/mills"
	"github.com/crb2nu/loom/pkg/mills/gates"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// Stage describes one node in the pipeline DAG. It captures only the
// metadata the runner needs; the actual worker logic for non-gate stages
// is resolved through WorkerDispatcher.
type Stage struct {
	// ID matches the node id in mills-default-pipeline.yaml.
	ID string
	// Type is one of "llm", "agent_spawn", "shell", "auto_gate".
	Type string
	// State is the pipeline_runs.state to record while this stage runs.
	// Gate stages inherit the state of the upstream non-gate stage.
	State store.PipelineState
	// Gates is the ordered list of gate names to evaluate (auto_gate only).
	Gates []string
	// RetryFrom names the upstream stage to re-run when a gate fails.
	// Empty for non-gate stages. The static contract is captured in
	// DefaultStages; runtime can override for custom DAGs later.
	RetryFrom string
}

// DefaultStages mirrors mills-default-pipeline.yaml. Order is significant.
//
// The set of gates per auto_gate matches §"Pipeline flow template" and
// §"Stage gates — required v1 set" in .loom/90-…; LLM-judged gates
// (spec_conformance, pr_self_review) are listed here but only fire when
// slice 4.5 registers them on the gate registry.
var DefaultStages = []Stage{
	{ID: "plan_slice", Type: "llm", State: store.PipelinePlanning},
	{ID: "research", Type: "llm", State: store.PipelinePlanning},
	{ID: "implement", Type: "agent_spawn", State: store.PipelineImplementing},
	{
		ID:        "post_implement_gate",
		Type:      "auto_gate",
		State:     store.PipelineImplementing,
		RetryFrom: "implement",
		Gates:     []string{"diff_size", "scope", "path_policy", "secret_scan", "commit_format"},
	},
	{ID: "tests", Type: "shell", State: store.PipelineTesting},
	{
		ID:        "post_tests_gate",
		Type:      "auto_gate",
		State:     store.PipelineTesting,
		RetryFrom: "implement",
		Gates:     []string{},
	},
	{ID: "pr_self_review", Type: "agent_spawn", State: store.PipelineReviewing},
	{
		ID:        "post_review_gate",
		Type:      "auto_gate",
		State:     store.PipelineReviewing,
		RetryFrom: "pr_self_review",
		Gates:     []string{"spec_conformance", "pr_self_review"},
	},
	{ID: "mr", Type: "shell", State: store.PipelineMR},
	{
		ID:        "post_mr_gate",
		Type:      "auto_gate",
		State:     store.PipelineMR,
		RetryFrom: "mr",
		Gates:     []string{},
	},
	{ID: "ci_watch", Type: "shell", State: store.PipelineCI},
	{
		ID:        "post_ci_gate",
		Type:      "auto_gate",
		State:     store.PipelineCI,
		RetryFrom: "implement",
		Gates:     []string{},
	},
	{ID: "merge", Type: "shell", State: store.PipelineMerging},
	{
		ID:        "post_merge_gate",
		Type:      "auto_gate",
		State:     store.PipelineMerging,
		RetryFrom: "merge",
		Gates:     []string{},
	},
	{ID: "cleanup", Type: "shell", State: store.PipelineMerging},
}

// StageOutput is the bundle every worker returns to the runner. Fields
// are loosely typed because the dispatcher in slice 4.2 wraps a mix of
// spawn calls, MCP tool calls, and shell commands; the runner only needs
// what gates and downstream stages will consume.
type StageOutput struct {
	CostUSD        float64
	SpawnID        string
	LogTail        string
	Artifacts      map[string]any
	FilesChanged   []string
	LinesAdded     int
	LinesRemoved   int
	DiffPatch      []byte
	CommitMessages []string
	// MRIID, when non-zero, is propagated up onto pipeline_runs.mr_iid.
	MRIID int64
	// WorktreePath, when set, is propagated up onto pipeline_runs.worktree_path.
	WorktreePath string
	// MergedSHA is populated by the merge stage; the runner stores it on
	// the run row so eval Loop B can attribute outcomes.
	MergedSHA string
}

type stageAcceptRecorderKey struct{}
type resumeSpawnIDKey struct{}

var errStagePending = errors.New("pipeline: stage remains pending")

func withStageAcceptRecorder(ctx context.Context, fn func(spawnID string) error) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, stageAcceptRecorderKey{}, fn)
}

func stageAcceptRecorderFromContext(ctx context.Context) func(spawnID string) error {
	fn, _ := ctx.Value(stageAcceptRecorderKey{}).(func(string) error)
	return fn
}

func withResumeSpawnID(ctx context.Context, spawnID string) context.Context {
	if spawnID == "" {
		return ctx
	}
	return context.WithValue(ctx, resumeSpawnIDKey{}, spawnID)
}

func resumeSpawnIDFromContext(ctx context.Context) string {
	spawnID, _ := ctx.Value(resumeSpawnIDKey{}).(string)
	return spawnID
}

// WorkerDispatcher executes one non-gate stage. Slice 4.2 supplies the
// real implementation (spawn / weaver / devbox / mcp); slice 4.1 ships
// the runner against this interface and tests use a fake.
type WorkerDispatcher interface {
	Dispatch(
		ctx context.Context,
		run *store.PipelineRun,
		item *store.BacklogItem,
		stage Stage,
		prior map[string]StageOutput,
	) (StageOutput, error)
}

// Runner drives one pipeline run end-to-end. It is safe for concurrent
// Drive calls against different runs; a single run is serialised by the
// caller (the reconciler issues one Start per queued item per tick).
type Runner struct {
	Store      *store.Store
	Gates      *gates.Registry
	Dispatcher WorkerDispatcher
	Policy     *mills.PolicyManager
	Stages     []Stage
	Clock      func() time.Time
	Logger     *slog.Logger
	// Escalator, when set, is invoked after the runner transitions a
	// run to PipelineEscalated. Failure-record + issue + handoff
	// publication is best-effort: an Escalator error is logged but does
	// not undo the state transition.
	Escalator EscalationHandler
	// OnMerged, when set, is invoked synchronously after a run reaches
	// PipelineDone (the merge stage + cleanup completed). Slice 4.7
	// wires this to eval.OutcomeAttributor.OnMerged so each merge
	// produces exactly one pipeline_outcome eval row. Errors are logged
	// but do not undo the state transition.
	OnMerged func(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error
	active   sync.Map
	// CrossRepoIntegrator, when set, switches the runner into the
	// cross-repo path for any backlog item that has an open
	// cross_repo_run row. Unset means single-repo behaviour for every
	// item; an open cross_repo_run with no integrator wired returns a
	// clear error rather than silently routing through the single-repo
	// flow. See slice 4.2/4.3 in
	// .loom/94-implementation-plan-mills-v2-…2026-05-02.md.
	CrossRepoIntegrator CrossRepoIntegrator
}

// CrossRepoIntegrator is the subset of crossrepo.Integrator the pipeline
// runner depends on. Defined here so the runner stays agnostic to the
// concrete crossrepo package and tests can supply a fake without pulling
// in the GitLab/policy wiring.
type CrossRepoIntegrator interface {
	WaitForGreen(ctx context.Context, run *store.CrossRepoRun) (store.CrossRepoState, error)
	AtomicMerge(ctx context.Context, run *store.CrossRepoRun) (store.CrossRepoState, error)
}

// New constructs a Runner with sensible defaults. A nil PolicyManager is
// treated as "no policy snapshot" — gate retries default to 3 attempts.
func New(s *store.Store, gr *gates.Registry, d WorkerDispatcher, pm *mills.PolicyManager) *Runner {
	return &Runner{
		Store:      s,
		Gates:      gr,
		Dispatcher: d,
		Policy:     pm,
		Stages:     DefaultStages,
		Clock:      time.Now,
		Logger:     slog.Default(),
	}
}

// Start satisfies mills.PipelineStarter. It validates inputs, kicks off
// Drive in a goroutine, and returns nil on accept. The reconciler relies
// on the contract that progress is reported via stage_results + events.
func (r *Runner) Start(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error {
	if r == nil || r.Store == nil || r.Dispatcher == nil {
		return errors.New("pipeline: runner not configured")
	}
	if run == nil || run.ID == "" {
		return errors.New("pipeline: run.ID required")
	}
	if item == nil || item.ID == "" {
		return errors.New("pipeline: item.ID required")
	}
	if _, loaded := r.active.LoadOrStore(run.ID, struct{}{}); loaded {
		r.logger().Warn("pipeline start skipped; run already active in this operator", "run", run.ID)
		return nil
	}
	go func() {
		defer r.active.Delete(run.ID)
		// Drive uses a detached context; a reconciler tick that returns
		// must not cancel an in-flight run.
		bg := context.Background()
		if err := r.Drive(bg, run, item); err != nil {
			r.logger().Error("pipeline drive failed", "run", run.ID, "error", err)
			if eerr := r.escalateWithItem(bg, run, item, fmt.Sprintf("pipeline drive failed: %v", err)); eerr != nil {
				r.logger().Error("pipeline drive failure escalation failed", "run", run.ID, "error", eerr)
			}
		}
	}()
	return nil
}

// Drive runs the state machine synchronously to a terminal state. It is
// the test entry point and the unit under test for slice 4.1.
//
// Drive is resume-safe: if run.CurrentStage is set and matches a stage
// in r.Stages, execution picks up at that index. New runs (CurrentStage
// empty) start at index 0.
//
// Cross-repo branch (slice 4.2/4.3): when the backlog item has an open
// cross_repo_run row the Runner hands off to handleCrossRepoRun instead
// of stepping through r.Stages. The detection is "open run exists" —
// the planner's caller is responsible for materialising that row before
// dispatching the pipeline. See .loom/94-…2026-05-02.md slice 4.2.
func (r *Runner) Drive(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error {
	if r.Store == nil || r.Dispatcher == nil {
		return errors.New("pipeline: runner not configured")
	}
	if cross, err := r.openCrossRepoRun(ctx, item); err != nil {
		return err
	} else if cross != nil {
		return r.handleCrossRepoRun(ctx, cross, run, item)
	}
	startIdx, err := r.resumeIndex(run)
	if err != nil {
		return err
	}
	prior, err := r.loadPriorOutputs(ctx, run.ID)
	if err != nil {
		return fmt.Errorf("pipeline: load prior outputs: %w", err)
	}

	policy := r.policy()
	maxAttempts := policy.Pipeline.Retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	// attempts tracks per-stage attempt count for the live Drive call.
	// On resume we seed it from the persisted stage_results so retry
	// caps survive operator restarts.
	attempts, err := r.seedAttempts(ctx, run.ID)
	if err != nil {
		return fmt.Errorf("pipeline: seed attempts: %w", err)
	}

	for i := startIdx; i < len(r.Stages); i++ {
		stage := r.Stages[i]
		if err := ctx.Err(); err != nil {
			return err
		}

		if stage.Type == "auto_gate" {
			pass, err := r.runGate(ctx, run, item, stage, prior, policy)
			if err != nil {
				return r.escalateWithItem(ctx, run, item, fmt.Sprintf("gate %s: %v", stage.ID, err))
			}
			if pass {
				continue
			}
			// Gate failure: rewind to RetryFrom and retry, bumping
			// the upstream stage's attempt counter. Cap at maxAttempts.
			rewindIdx, ok := r.indexOf(stage.RetryFrom)
			if !ok || stage.RetryFrom == "" {
				return r.escalateWithItem(ctx, run, item, fmt.Sprintf("gate %s failed and no RetryFrom defined", stage.ID))
			}
			if attempts[stage.RetryFrom]+1 > maxAttempts {
				return r.escalateWithItem(ctx, run, item, fmt.Sprintf("gate %s failed; %s exceeded %d attempts", stage.ID, stage.RetryFrom, maxAttempts))
			}
			r.logger().Info("pipeline retry", "run", run.ID, "from", stage.RetryFrom, "attempt", attempts[stage.RetryFrom]+1)
			i = rewindIdx - 1 // -1 so the for-loop ++ lands on rewindIdx
			continue
		}

		// Non-gate stage: dispatch the worker.
		attempt := attempts[stage.ID] + 1
		pending, err := r.pendingStage(ctx, run.ID, stage.ID)
		if err != nil {
			return fmt.Errorf("pipeline: load pending stage: %w", err)
		}
		if pending != nil {
			attempt = pending.Attempt
		}
		attempts[stage.ID] = attempt
		out, err := r.runStage(ctx, run, item, stage, prior, attempt, pending)
		if err != nil {
			if errors.Is(err, errStagePending) {
				r.logger().Info("pipeline drive stopped; stage remains pending", "run", run.ID, "stage", stage.ID, "attempt", attempt)
				return nil
			}
			if errors.Is(err, store.ErrStageSpawnConflict) {
				r.logger().Info("pipeline drive stopped; stage attempt already has an accepted spawn", "run", run.ID, "stage", stage.ID, "attempt", attempt)
				return nil
			}
			if attempts[stage.ID] >= maxAttempts {
				return r.escalateWithItem(ctx, run, item, fmt.Sprintf("stage %s errored after %d attempts: %v", stage.ID, attempts[stage.ID], err))
			}
			// Retry the same stage by stepping back one (loop will ++).
			i--
			continue
		}
		prior[stage.ID] = out
	}

	return r.markDone(ctx, run, item)
}

// resumeIndex returns the stage index to start (or restart) at. A run
// with no CurrentStage starts at 0; a run mid-flight resumes at the
// stage that was in flight when the operator stopped.
func (r *Runner) resumeIndex(run *store.PipelineRun) (int, error) {
	if run.CurrentStage == "" {
		return 0, nil
	}
	if i, ok := r.indexOf(run.CurrentStage); ok {
		return i, nil
	}
	return 0, fmt.Errorf("pipeline: run %s current_stage %q not in DAG", run.ID, run.CurrentStage)
}

// indexOf returns the position of stage id in r.Stages, or (0, false).
func (r *Runner) indexOf(id string) (int, bool) {
	for i, s := range r.Stages {
		if s.ID == id {
			return i, true
		}
	}
	return 0, false
}

// loadPriorOutputs rehydrates the most-recent successful output per stage
// from stage_results. Used on resume so downstream stages still see their
// inputs after an operator restart.
func (r *Runner) loadPriorOutputs(ctx context.Context, runID string) (map[string]StageOutput, error) {
	rows, err := r.Store.Pipeline.ListStages(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]StageOutput, len(rows))
	for _, sr := range rows {
		if sr.Outcome == nil || *sr.Outcome != store.StageOutcomeSuccess {
			continue
		}
		so := StageOutput{
			CostUSD:   sr.CostUSD,
			SpawnID:   sr.SpawnID,
			LogTail:   sr.LogTail,
			Artifacts: sr.Artifacts,
		}
		// The dispatcher may have stashed structured fields under
		// well-known keys. Surface the ones gates care about.
		if sr.Artifacts != nil {
			if v, ok := sr.Artifacts["files_changed"].([]any); ok {
				for _, f := range v {
					if s, ok := f.(string); ok {
						so.FilesChanged = append(so.FilesChanged, s)
					}
				}
			}
			if v, ok := sr.Artifacts["diff_patch"].(string); ok {
				so.DiffPatch = []byte(v)
			}
			if v, ok := sr.Artifacts["lines_added"].(float64); ok {
				so.LinesAdded = int(v)
			}
			if v, ok := sr.Artifacts["lines_removed"].(float64); ok {
				so.LinesRemoved = int(v)
			}
			if v, ok := sr.Artifacts["mr_iid"].(float64); ok {
				so.MRIID = int64(v)
			}
		}
		out[sr.Stage] = so
	}
	return out, nil
}

// seedAttempts loads the persisted attempt count for every stage of a
// run so retry caps survive operator restarts.
func (r *Runner) seedAttempts(ctx context.Context, runID string) (map[string]int, error) {
	out := make(map[string]int)
	if r.Store == nil || runID == "" {
		return out, nil
	}
	rows, err := r.Store.Pipeline.ListStages(ctx, runID)
	if err != nil {
		return nil, err
	}
	for _, sr := range rows {
		if sr.Outcome == nil {
			continue
		}
		if sr.Attempt > out[sr.Stage] {
			out[sr.Stage] = sr.Attempt
		}
	}
	return out, nil
}

func (r *Runner) pendingStage(ctx context.Context, runID, stageID string) (*store.StageResult, error) {
	if r.Store == nil || runID == "" || stageID == "" {
		return nil, nil
	}
	rows, err := r.Store.Pipeline.ListStages(ctx, runID)
	if err != nil {
		return nil, err
	}
	for i := len(rows) - 1; i >= 0; i-- {
		sr := rows[i]
		if sr.Stage == stageID && sr.Outcome == nil && sr.SpawnID != "" {
			return sr, nil
		}
	}
	return nil, nil
}

// runStage executes one non-gate stage: persist current_stage, dispatch
// the worker, persist the stage_result row, propagate side-effects (cost,
// mr_iid, worktree_path) up onto the run row.
func (r *Runner) runStage(
	ctx context.Context,
	run *store.PipelineRun,
	item *store.BacklogItem,
	stage Stage,
	prior map[string]StageOutput,
	attempt int,
	pending *store.StageResult,
) (StageOutput, error) {
	now := r.now()
	resumeSpawnID := ""
	if pending != nil {
		now = pending.StartedAt
		resumeSpawnID = pending.SpawnID
	}
	run.CurrentStage = stage.ID
	run.State = stage.State
	if err := r.Store.Pipeline.PutRun(ctx, run); err != nil {
		return StageOutput{}, fmt.Errorf("persist run head: %w", err)
	}
	r.event(ctx, "pipeline.stage.start", "ok", map[string]any{
		"run": run.ID, "stage": stage.ID, "attempt": attempt,
	})

	acceptedSpawnID := resumeSpawnID
	stageCtx := withResumeSpawnID(ctx, resumeSpawnID)
	stageCtx = withStageAcceptRecorder(stageCtx, func(spawnID string) error {
		if spawnID == "" {
			return nil
		}
		acceptedSpawnID = spawnID
		return r.Store.Pipeline.PutStage(ctx, &store.StageResult{
			PipelineRunID: run.ID,
			Stage:         stage.ID,
			Attempt:       attempt,
			StartedAt:     now,
			SpawnID:       spawnID,
			Artifacts:     map[string]any{"stage_id": stage.ID},
		})
	})
	out, derr := r.Dispatcher.Dispatch(stageCtx, run, item, stage, prior)
	if out.SpawnID == "" && acceptedSpawnID != "" {
		out.SpawnID = acceptedSpawnID
	}
	if derr != nil && out.SpawnID != "" && !hasTerminalSpawnStatus(out) {
		if perr := r.Store.Pipeline.PutStage(ctx, &store.StageResult{
			PipelineRunID: run.ID,
			Stage:         stage.ID,
			Attempt:       attempt,
			StartedAt:     now,
			SpawnID:       out.SpawnID,
			Artifacts:     map[string]any{"stage_id": stage.ID},
			LogTail:       out.LogTail,
		}); perr != nil {
			return out, fmt.Errorf("persist pending stage: %w", perr)
		}
		r.event(ctx, "pipeline.stage.pending", "ok", map[string]any{
			"run": run.ID, "stage": stage.ID, "attempt": attempt, "spawn_id": out.SpawnID, "error": derr.Error(),
		})
		return out, errStagePending
	}
	endedAt := r.now()

	mills.PipelineStageDurationSeconds.WithLabelValues(stage.ID).Observe(endedAt.Sub(now).Seconds())
	outcome := store.StageOutcomeSuccess
	if derr != nil {
		outcome = store.StageOutcomeError
	}
	mills.PipelineStageAttemptsTotal.WithLabelValues(stage.ID, string(outcome)).Inc()
	logTail := out.LogTail
	if derr != nil && strings.TrimSpace(logTail) == "" {
		logTail = derr.Error()
	}
	sr := &store.StageResult{
		PipelineRunID: run.ID,
		Stage:         stage.ID,
		Attempt:       attempt,
		StartedAt:     now,
		EndedAt:       &endedAt,
		Outcome:       &outcome,
		SpawnID:       out.SpawnID,
		CostUSD:       out.CostUSD,
		Artifacts:     mergeArtifacts(stage.ID, out),
		LogTail:       logTail,
	}
	if perr := r.Store.Pipeline.PutStage(ctx, sr); perr != nil {
		// PutStage failure is unrecoverable for audit purposes.
		return out, fmt.Errorf("persist stage: %w", perr)
	}
	if derr != nil {
		r.event(ctx, "pipeline.stage.error", "error", map[string]any{
			"run": run.ID, "stage": stage.ID, "attempt": attempt, "error": derr.Error(),
		})
		return out, derr
	}

	// Roll up side effects onto the run row.
	run.CostUSD += out.CostUSD
	if out.MRIID != 0 {
		v := out.MRIID
		run.MRIID = &v
	}
	if out.WorktreePath != "" {
		run.WorktreePath = out.WorktreePath
	}
	if err := r.Store.Pipeline.PutRun(ctx, run); err != nil {
		return out, fmt.Errorf("persist run rollup: %w", err)
	}

	r.event(ctx, "pipeline.stage.done", "ok", map[string]any{
		"run": run.ID, "stage": stage.ID, "attempt": attempt, "cost_usd": out.CostUSD,
	})
	return out, nil
}

func hasTerminalSpawnStatus(out StageOutput) bool {
	if out.Artifacts == nil {
		return false
	}
	status, _ := out.Artifacts["status"].(string)
	switch status {
	case "completed", "failed", "stopped":
		return true
	default:
		return false
	}
}

// runGate evaluates an auto_gate stage against the gate registry. Returns
// (true, nil) on aggregate pass, (false, nil) on aggregate fail (caller
// triggers retry), and (_, err) only on infrastructure errors.
func (r *Runner) runGate(
	ctx context.Context,
	run *store.PipelineRun,
	item *store.BacklogItem,
	stage Stage,
	prior map[string]StageOutput,
	policy *mills.Policy,
) (bool, error) {
	if r.Gates == nil || len(stage.Gates) == 0 {
		// No gates registered → vacuously pass. Logged for audit.
		r.event(ctx, "pipeline.gate.skip", "ok", map[string]any{
			"run": run.ID, "gate": stage.ID,
		})
		return true, nil
	}
	in := r.gateInputFor(stage, item, policy, prior)

	// Filter to gates that are actually registered. An unregistered gate
	// (e.g. spec_conformance before slice 4.5 lands) is treated as skip,
	// not fail — the static template lists future gates by name.
	known := r.Gates.Names()
	knownSet := make(map[string]bool, len(known))
	for _, n := range known {
		knownSet[n] = true
	}
	var toRun []string
	for _, g := range stage.Gates {
		if knownSet[g] {
			toRun = append(toRun, g)
		}
	}
	if len(toRun) == 0 {
		r.event(ctx, "pipeline.gate.skip", "ok", map[string]any{
			"run": run.ID, "gate": stage.ID, "reason": "no registered gates",
		})
		return true, nil
	}

	outcomes, allPass, err := r.Gates.EvaluateAll(ctx, toRun, in)
	if err != nil {
		return false, err
	}
	for _, no := range outcomes {
		row := &store.GateOutcome{
			PipelineRunID: run.ID,
			AfterStage:    stage.ID,
			GateName:      no.Name,
			Outcome:       store.GateOutcomePass,
			Reasons:       no.Outcome.Reasons,
			JudgedBy:      no.Outcome.JudgedBy,
			EvaluatedAt:   r.now(),
		}
		if !no.Outcome.Pass {
			row.Outcome = store.GateOutcomeFail
		}
		mills.GateEvaluationsTotal.WithLabelValues(no.Name, string(row.Outcome)).Inc()
		if perr := r.Store.Pipeline.PutGate(ctx, row); perr != nil {
			r.logger().Warn("pipeline gate persist failed", "error", perr)
		}
	}
	r.event(ctx, "pipeline.gate.eval", boolStr(allPass, "ok", "fail"), map[string]any{
		"run": run.ID, "gate": stage.ID, "gates_run": toRun, "pass": allPass,
	})
	return allPass, nil
}

// gateInputFor builds the StageInput passed to gates. It walks `prior`
// for the most recent diff/file/test artifact regardless of which stage
// produced it.
func (r *Runner) gateInputFor(stage Stage, item *store.BacklogItem, policy *mills.Policy, prior map[string]StageOutput) gates.StageInput {
	in := gates.StageInput{Item: item, Policy: policy}
	if impl, ok := prior["implement"]; ok {
		in.FilesChanged = impl.FilesChanged
		in.LinesAdded = impl.LinesAdded
		in.LinesRemoved = impl.LinesRemoved
		in.DiffPatch = impl.DiffPatch
		in.CommitMessages = impl.CommitMessages
	}
	return in
}

// markDone closes out a run that completed cleanup successfully and
// fires the OnMerged hook for downstream eval Loop B attribution.
func (r *Runner) markDone(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error {
	t := r.now()
	run.State = store.PipelineDone
	run.CurrentStage = ""
	run.EndedAt = &t
	if err := r.Store.Pipeline.PutRun(ctx, run); err != nil {
		return fmt.Errorf("persist run done: %w", err)
	}
	if item != nil {
		item.State = store.BacklogMerged
		if err := r.Store.Backlog.Put(ctx, item); err != nil {
			return fmt.Errorf("persist backlog merged: %w", err)
		}
	}
	mills.PipelineRunsTotal.WithLabelValues(string(store.PipelineDone)).Inc()
	mills.PipelineCostUSDTotal.WithLabelValues(string(store.PipelineDone)).Add(run.CostUSD)
	r.event(ctx, "pipeline.run.done", "ok", map[string]any{
		"run": run.ID, "cost_usd": run.CostUSD,
	})
	if r.OnMerged != nil && item != nil {
		if err := r.OnMerged(ctx, run, item); err != nil {
			r.logger().Warn("pipeline OnMerged hook failed", "run", run.ID, "error", err)
		}
	}
	return nil
}

// escalateWithItem transitions a run to escalated, records the reason,
// and invokes the optional EscalationHandler for issue+handoff
// publication. Handler failures are logged but don't undo the state
// transition.
func (r *Runner) escalateWithItem(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem, reason string) error {
	t := r.now()
	run.State = store.PipelineEscalated
	run.EndedAt = &t
	if err := r.Store.Pipeline.PutRun(ctx, run); err != nil {
		return fmt.Errorf("persist run escalated: %w", err)
	}
	if item != nil {
		item.State = store.BacklogEscalated
		if err := r.Store.Backlog.Put(ctx, item); err != nil {
			return fmt.Errorf("persist backlog escalated: %w", err)
		}
	}
	mills.PipelineRunsTotal.WithLabelValues(string(store.PipelineEscalated)).Inc()
	mills.PipelineCostUSDTotal.WithLabelValues(string(store.PipelineEscalated)).Add(run.CostUSD)
	mills.EscalationsTotal.WithLabelValues(classifyEscalationReason(reason)).Inc()
	r.event(ctx, "pipeline.run.escalated", "error", map[string]any{
		"run": run.ID, "reason": reason,
	})
	if r.Escalator != nil && item != nil {
		if err := r.Escalator.Handle(ctx, run, item, reason); err != nil {
			r.logger().Warn("pipeline escalator failed", "run", run.ID, "error", err)
		}
	}
	return nil
}

// mergeArtifacts flattens a StageOutput into the JSON map persisted into
// stage_results.artifacts_json, retaining whatever the dispatcher set
// plus the typed fields gates rely on.
func mergeArtifacts(stageID string, out StageOutput) map[string]any {
	dst := map[string]any{}
	for k, v := range out.Artifacts {
		dst[k] = v
	}
	if len(out.FilesChanged) > 0 {
		dst["files_changed"] = out.FilesChanged
	}
	if out.LinesAdded != 0 {
		dst["lines_added"] = out.LinesAdded
	}
	if out.LinesRemoved != 0 {
		dst["lines_removed"] = out.LinesRemoved
	}
	if len(out.DiffPatch) > 0 {
		dst["diff_patch"] = string(out.DiffPatch)
	}
	if len(out.CommitMessages) > 0 {
		dst["commit_messages"] = out.CommitMessages
	}
	if out.MRIID != 0 {
		dst["mr_iid"] = out.MRIID
	}
	if out.MergedSHA != "" {
		dst["merged_sha"] = out.MergedSHA
	}
	if out.WorktreePath != "" {
		dst["worktree_path"] = out.WorktreePath
	}
	dst["stage_id"] = stageID
	return dst
}

func (r *Runner) event(ctx context.Context, kind, outcome string, payload map[string]any) {
	if r.Store == nil || r.Store.Events == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["outcome"] = outcome
	if err := r.Store.Events.Append(ctx, &store.Event{
		Actor:   "pipeline",
		Kind:    kind,
		Payload: payload,
	}); err != nil {
		r.logger().Warn("pipeline append event failed", "error", err, "kind", kind)
	}
}

func (r *Runner) policy() *mills.Policy {
	if r.Policy == nil {
		return mills.Default()
	}
	return r.Policy.Current()
}

func (r *Runner) now() time.Time {
	if r.Clock != nil {
		return r.Clock()
	}
	return time.Now()
}

func (r *Runner) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

func boolStr(b bool, t, f string) string {
	if b {
		return t
	}
	return f
}

// openCrossRepoRun returns the most-recent cross_repo_runs row for the
// backlog item if it sits in a non-terminal state the runner should
// drive. Returns (nil, nil) for the common single-repo case.
//
// "Non-terminal" today is open + gates_green + merging — anything before
// the integrator finishes its job. Merged/reverted/failed rows are
// historical artifacts and should not re-enter the pipeline.
func (r *Runner) openCrossRepoRun(ctx context.Context, item *store.BacklogItem) (*store.CrossRepoRun, error) {
	if r.Store == nil || r.Store.CrossRepo == nil || item == nil {
		return nil, nil
	}
	rows, err := r.Store.CrossRepo.ListByBacklog(ctx, item.ID)
	if err != nil {
		return nil, fmt.Errorf("pipeline: lookup cross_repo for %s: %w", item.ID, err)
	}
	for _, row := range rows {
		if isCrossRepoActive(row.State) {
			return row, nil
		}
	}
	return nil, nil
}

func isCrossRepoActive(s store.CrossRepoState) bool {
	switch s {
	case store.CrossRepoOpen, store.CrossRepoGatesGreen, store.CrossRepoMerging:
		return true
	default:
		return false
	}
}

// handleCrossRepoRun drives a cross-repo run through WaitForGreen +
// AtomicMerge, persisting state transitions on cross_repo_runs and
// closing out the *store.PipelineRun envelope when the integrator
// reaches a terminal state. Per-repo MR creation is intentionally
// out-of-band today (see TODO below).
func (r *Runner) handleCrossRepoRun(
	ctx context.Context,
	cross *store.CrossRepoRun,
	run *store.PipelineRun,
	item *store.BacklogItem,
) error {
	if r.CrossRepoIntegrator == nil {
		return r.escalateWithItem(ctx, run, item, fmt.Sprintf(
			"cross-repo run %s present but integrator not configured", cross.ID))
	}
	// TODO(slice 4.2 followup): fan out per-repo plan stages; for now
	// assume MRs are created out-of-band by the planner caller.
	greenState, err := r.CrossRepoIntegrator.WaitForGreen(ctx, cross)
	if perr := r.persistCrossState(ctx, cross, greenState); perr != nil {
		r.logger().Warn("crossrepo persist gates_green failed",
			"cross_repo_run", cross.ID, "error", perr)
	}
	if err != nil {
		return r.escalateWithItem(ctx, run, item, fmt.Sprintf(
			"cross-repo wait_for_green: %v", err))
	}
	mergeState, err := r.CrossRepoIntegrator.AtomicMerge(ctx, cross)
	if perr := r.persistCrossState(ctx, cross, mergeState); perr != nil {
		r.logger().Warn("crossrepo persist merge state failed",
			"cross_repo_run", cross.ID, "state", mergeState, "error", perr)
	}
	if err != nil {
		return r.escalateWithItem(ctx, run, item, fmt.Sprintf(
			"cross-repo atomic_merge: %v", err))
	}
	if mergeState != store.CrossRepoMerged {
		return r.escalateWithItem(ctx, run, item, fmt.Sprintf(
			"cross-repo terminal state %s", mergeState))
	}
	return r.markDone(ctx, run, item)
}

// persistCrossState pushes a state transition onto cross_repo_runs.
// Wraps the DAO so the runner doesn't grow conditional nil checks at
// every call site.
func (r *Runner) persistCrossState(ctx context.Context, cross *store.CrossRepoRun, state store.CrossRepoState) error {
	if r.Store == nil || r.Store.CrossRepo == nil || cross == nil || state == "" {
		return nil
	}
	cross.State = state
	return r.Store.CrossRepo.SetState(ctx, cross.ID, state)
}

// classifyEscalationReason maps the free-form escalation reason string
// into one of a small set of label values bounded enough to keep
// Prometheus cardinality predictable. Anything we don't recognise gets
// "other" so dashboards always see a complete partition.
func classifyEscalationReason(reason string) string {
	switch {
	case strings.Contains(reason, "exceeded"):
		return "retry_cap_exceeded"
	case strings.Contains(reason, "merge conflict"):
		return "integrator_conflict"
	case strings.Contains(reason, "allocate worktree") || strings.Contains(reason, "alloc"):
		return "integrator_alloc_fail"
	case strings.Contains(reason, "gate ") || strings.Contains(reason, "gate:"):
		return "gate_fail"
	case strings.Contains(reason, "errored") || strings.Contains(reason, "stage error"):
		return "stage_error"
	case strings.Contains(reason, "cross-repo"):
		return "cross_repo"
	default:
		return "other"
	}
}
