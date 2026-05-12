package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PipelineDAO exposes CRUD against pipeline_runs, stage_results, gate_outcomes.
type PipelineDAO struct {
	db *sql.DB
}

const pipelineColumns = `id, backlog_id, template, state, current_stage, attempts,
		worktree_path, mr_iid, started_at, ended_at, cost_usd, parent_session_id,
		parent_run_id, depth`

// PutRun inserts or replaces a pipeline run.
func (d *PipelineDAO) PutRun(ctx context.Context, run *PipelineRun) error {
	if run == nil || run.ID == "" {
		return errors.New("pipeline: run.ID required")
	}
	if run.BacklogID == "" {
		return errors.New("pipeline: run.BacklogID required")
	}
	if run.Depth < 0 {
		return errors.New("pipeline: run.Depth must be >= 0")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	var (
		mrIID     sql.NullInt64
		endedAt   sql.NullString
		parentRun sql.NullString
	)
	if run.MRIID != nil {
		mrIID = sql.NullInt64{Int64: *run.MRIID, Valid: true}
	}
	if run.EndedAt != nil {
		endedAt = sql.NullString{String: timeRFC3339(*run.EndedAt), Valid: true}
	}
	if run.ParentRunID != nil && *run.ParentRunID != "" {
		parentRun = sql.NullString{String: *run.ParentRunID, Valid: true}
	}
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO pipeline_runs (`+pipelineColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			backlog_id        = excluded.backlog_id,
			template          = excluded.template,
			state             = excluded.state,
			current_stage     = excluded.current_stage,
			attempts          = excluded.attempts,
			worktree_path     = excluded.worktree_path,
			mr_iid            = excluded.mr_iid,
			ended_at          = excluded.ended_at,
			cost_usd          = excluded.cost_usd,
			parent_session_id = excluded.parent_session_id,
			parent_run_id     = excluded.parent_run_id,
			depth             = excluded.depth
	`,
		run.ID, run.BacklogID, run.Template, string(run.State),
		nullStr(run.CurrentStage), run.Attempts, nullStr(run.WorktreePath), mrIID,
		timeRFC3339(run.StartedAt), endedAt, run.CostUSD, nullStr(run.ParentSessionID),
		parentRun, run.Depth,
	)
	if err != nil {
		return fmt.Errorf("pipeline put %s: %w", run.ID, err)
	}
	return nil
}

// CreateSubrun inserts one new pipeline_runs row for a v2 recursion
// subrun. The caller (pkg/mills/pipeline/recursion.SubrunGuard) is
// responsible for the depth/budget/cycle guards and for filling
// run.ParentRunID + run.Depth before this call. CreateSubrun adds an
// existence check on parent_run_id (so a misuse can't silently dangle)
// and fails if the row id already exists (subruns are insert-only;
// PutRun is the upsert-friendly path for ongoing rollups).
func (d *PipelineDAO) CreateSubrun(ctx context.Context, run *PipelineRun) error {
	if run == nil || run.ID == "" {
		return errors.New("pipeline: subrun.ID required")
	}
	if run.ParentRunID == nil || *run.ParentRunID == "" {
		return errors.New("pipeline: subrun.ParentRunID required")
	}
	if run.Depth <= 0 {
		return fmt.Errorf("pipeline: subrun.Depth must be > 0 (got %d)", run.Depth)
	}
	// Defensive existence check on the parent — prevents a
	// dangling subrun row when the caller forgets to verify
	// upstream. The recursion guard already does this lookup, but
	// the DAO can be invoked directly from tests / future callers.
	row := d.db.QueryRowContext(ctx,
		`SELECT 1 FROM pipeline_runs WHERE id = ?`, *run.ParentRunID)
	var got int
	if err := row.Scan(&got); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("pipeline: subrun parent %q does not exist", *run.ParentRunID)
		}
		return fmt.Errorf("pipeline: subrun parent lookup: %w", err)
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	parentRun := sql.NullString{String: *run.ParentRunID, Valid: true}
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO pipeline_runs (`+pipelineColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`,
		run.ID, run.BacklogID, run.Template, string(run.State),
		nullStr(run.CurrentStage), run.Attempts, nullStr(run.WorktreePath), sql.NullInt64{},
		timeRFC3339(run.StartedAt), sql.NullString{}, run.CostUSD, nullStr(run.ParentSessionID),
		parentRun, run.Depth,
	)
	if err != nil {
		return fmt.Errorf("pipeline create-subrun %s: %w", run.ID, err)
	}
	return nil
}

// ListQueuedSubruns returns every pipeline run that's still in
// `queued` state AND has a non-null parent_run_id AND has not yet
// been picked up by a worker (attempts = 0). The reconciler uses
// this on each tick (Phase 6 slice 6.2) to start subruns the way it
// starts queued backlog items. Ordered by started_at so the oldest
// subrun runs first; the (state, parent_run_id) predicate naturally
// excludes non-recursive runs.
func (d *PipelineDAO) ListQueuedSubruns(ctx context.Context) ([]*PipelineRun, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT `+pipelineColumns+` FROM pipeline_runs
		WHERE state = ? AND parent_run_id IS NOT NULL AND attempts = 0
		ORDER BY started_at ASC
	`, string(PipelineQueued))
	if err != nil {
		return nil, fmt.Errorf("pipeline list-queued-subruns: %w", err)
	}
	defer rows.Close()
	var out []*PipelineRun
	for rows.Next() {
		r, err := scanPipelineRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListSubruns returns every direct child of the given parent pipeline run,
// ordered by started_at. Empty result is not an error. v2 recursion path.
func (d *PipelineDAO) ListSubruns(ctx context.Context, parentRunID string) ([]*PipelineRun, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+pipelineColumns+` FROM pipeline_runs WHERE parent_run_id = ? ORDER BY started_at ASC`,
		parentRunID)
	if err != nil {
		return nil, fmt.Errorf("pipeline list-subruns: %w", err)
	}
	defer rows.Close()
	var out []*PipelineRun
	for rows.Next() {
		r, err := scanPipelineRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRun fetches one pipeline run by id.
func (d *PipelineDAO) GetRun(ctx context.Context, id string) (*PipelineRun, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT `+pipelineColumns+` FROM pipeline_runs WHERE id = ?`, id)
	return scanPipelineRun(row)
}

// ListByState returns pipeline runs in the given state, oldest-first.
func (d *PipelineDAO) ListByState(ctx context.Context, state PipelineState) ([]*PipelineRun, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+pipelineColumns+` FROM pipeline_runs WHERE state = ? ORDER BY started_at ASC`,
		string(state))
	if err != nil {
		return nil, fmt.Errorf("pipeline list: %w", err)
	}
	defer rows.Close()
	var out []*PipelineRun
	for rows.Next() {
		r, err := scanPipelineRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListInFlight returns already-started, non-terminal pipeline runs. These are
// the rows that need to be resumed after an operator restart because their
// in-process runner goroutine died with the old pod.
func (d *PipelineDAO) ListInFlight(ctx context.Context) ([]*PipelineRun, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+pipelineColumns+` FROM pipeline_runs
		WHERE state NOT IN ('queued', 'done', 'escalated', 'paused')
		ORDER BY started_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("pipeline list in-flight: %w", err)
	}
	defer rows.Close()
	var out []*PipelineRun
	for rows.Next() {
		r, err := scanPipelineRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SumCostSince totals pipeline spend since the given timestamp.
func (d *PipelineDAO) SumCostSince(ctx context.Context, since time.Time) (float64, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cost_usd), 0) FROM pipeline_runs WHERE started_at >= ?`,
		timeRFC3339(since))
	var total float64
	if err := row.Scan(&total); err != nil {
		return 0, fmt.Errorf("pipeline sum-cost: %w", err)
	}
	return total, nil
}

// CountSince returns the number of pipeline runs started at-or-after `since`.
func (d *PipelineDAO) CountSince(ctx context.Context, since time.Time) (int, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pipeline_runs WHERE started_at >= ?`,
		timeRFC3339(since))
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("pipeline count-since: %w", err)
	}
	return n, nil
}

// CountActive returns the number of pipeline runs in any non-terminal state.
// "Terminal" = done, escalated, paused. Used by the concurrency cap.
func (d *PipelineDAO) CountActive(ctx context.Context) (int, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pipeline_runs
		WHERE state NOT IN ('done', 'escalated', 'paused')
	`)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("pipeline count-active: %w", err)
	}
	return n, nil
}

// ListByBacklog returns all pipeline runs (across attempts) for a backlog item.
// SetResearchDiff persists a JSON-encoded shadow vs. legacy research
// comparison for the pipeline run. Migration 003 adds the column; this
// is the only writer. Pass a non-nil JSON byte slice; the empty string
// is allowed and clears the column.
//
// Used by ResearchModeShadow in pkg/mills/clients/flexinfer.go during
// the soak window (MILLS_RESEARCH_VIA_WEAVER=shadow). Wiring from
// WeaverClient.DiffRecorder to this method lands when the operator
// gains a way to thread the current run id through WeaverRequest;
// until then this stays available for tests + future use.
func (d *PipelineDAO) SetResearchDiff(ctx context.Context, runID string, diffJSON string) error {
	if runID == "" {
		return errors.New("pipeline set-research-diff: run id required")
	}
	res, err := d.db.ExecContext(ctx,
		`UPDATE pipeline_runs SET research_diff = ? WHERE id = ?`,
		diffJSON, runID,
	)
	if err != nil {
		return fmt.Errorf("pipeline set-research-diff: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *PipelineDAO) ListByBacklog(ctx context.Context, backlogID string) ([]*PipelineRun, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+pipelineColumns+` FROM pipeline_runs WHERE backlog_id = ? ORDER BY attempts ASC`,
		backlogID)
	if err != nil {
		return nil, fmt.Errorf("pipeline list-backlog: %w", err)
	}
	defer rows.Close()
	var out []*PipelineRun
	for rows.Next() {
		r, err := scanPipelineRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanPipelineRun(s scanner) (*PipelineRun, error) {
	var (
		run                                                                  PipelineRun
		currentStage, worktreePath, parentSession, endedAt, state, parentRun sql.NullString
		mrIID                                                                sql.NullInt64
		startedAt                                                            string
	)
	err := s.Scan(
		&run.ID, &run.BacklogID, &run.Template, &state,
		&currentStage, &run.Attempts, &worktreePath, &mrIID,
		&startedAt, &endedAt, &run.CostUSD, &parentSession,
		&parentRun, &run.Depth,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("pipeline scan: %w", err)
	}
	if state.Valid {
		run.State = PipelineState(state.String)
	}
	if currentStage.Valid {
		run.CurrentStage = currentStage.String
	}
	if worktreePath.Valid {
		run.WorktreePath = worktreePath.String
	}
	if parentSession.Valid {
		run.ParentSessionID = parentSession.String
	}
	run.MRIID = nullableInt64(mrIID)
	run.ParentRunID = nullableString(parentRun)
	if run.StartedAt, err = parseTime(startedAt); err != nil {
		return nil, fmt.Errorf("started_at: %w", err)
	}
	if run.EndedAt, err = nullableTime(endedAt); err != nil {
		return nil, fmt.Errorf("ended_at: %w", err)
	}
	return &run, nil
}

// ----- Stage results -----

const stageColumns = `id, pipeline_run_id, stage, attempt, started_at, ended_at,
		outcome, spawn_id, cost_usd, artifacts_json, log_tail`

var ErrStageSpawnConflict = errors.New("pipeline: stage attempt already has an accepted spawn")

// PutStage inserts a stage result. The unique (pipeline_run_id, stage, attempt)
// index makes retries idempotent: re-recording the same attempt is a no-op
// upsert that updates ended_at/outcome.
func (d *PipelineDAO) PutStage(ctx context.Context, sr *StageResult) error {
	if sr == nil || sr.PipelineRunID == "" || sr.Stage == "" {
		return errors.New("pipeline: stage result requires PipelineRunID + Stage")
	}
	if sr.StartedAt.IsZero() {
		sr.StartedAt = time.Now().UTC()
	}
	artifacts, err := jsonField(sr.Artifacts)
	if err != nil {
		return fmt.Errorf("artifacts: %w", err)
	}
	var (
		endedAt sql.NullString
		outcome sql.NullString
	)
	if sr.EndedAt != nil {
		endedAt = sql.NullString{String: timeRFC3339(*sr.EndedAt), Valid: true}
	}
	if sr.Outcome != nil {
		outcome = sql.NullString{String: string(*sr.Outcome), Valid: true}
	}
	if sr.SpawnID != "" {
		var existingSpawn, existingOutcome sql.NullString
		err := d.db.QueryRowContext(ctx, `
			SELECT spawn_id, outcome
			FROM stage_results
			WHERE pipeline_run_id = ? AND stage = ? AND attempt = ?
		`, sr.PipelineRunID, sr.Stage, sr.Attempt).Scan(&existingSpawn, &existingOutcome)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("stage conflict check %s/%s/%d: %w", sr.PipelineRunID, sr.Stage, sr.Attempt, err)
		}
		if err == nil && !existingOutcome.Valid && existingSpawn.Valid && existingSpawn.String != "" && existingSpawn.String != sr.SpawnID {
			return fmt.Errorf("%w: %s/%s/%d existing=%s incoming=%s",
				ErrStageSpawnConflict, sr.PipelineRunID, sr.Stage, sr.Attempt, existingSpawn.String, sr.SpawnID)
		}
	}
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO stage_results (pipeline_run_id, stage, attempt, started_at,
			ended_at, outcome, spawn_id, cost_usd, artifacts_json, log_tail)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(pipeline_run_id, stage, attempt) DO UPDATE SET
			ended_at        = excluded.ended_at,
			outcome         = excluded.outcome,
			spawn_id        = COALESCE(NULLIF(excluded.spawn_id, ''), stage_results.spawn_id),
			cost_usd        = excluded.cost_usd,
			artifacts_json  = excluded.artifacts_json,
			log_tail        = excluded.log_tail
	`,
		sr.PipelineRunID, sr.Stage, sr.Attempt, timeRFC3339(sr.StartedAt),
		endedAt, outcome, nullStr(sr.SpawnID), sr.CostUSD, artifacts, nullStr(sr.LogTail),
	)
	if err != nil {
		return fmt.Errorf("stage put %s/%s/%d: %w", sr.PipelineRunID, sr.Stage, sr.Attempt, err)
	}
	if id, err := res.LastInsertId(); err == nil && sr.ID == 0 {
		sr.ID = id
	}
	return nil
}

// ListStages returns every stage attempt for a pipeline run, in execution order.
func (d *PipelineDAO) ListStages(ctx context.Context, pipelineRunID string) ([]*StageResult, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+stageColumns+` FROM stage_results WHERE pipeline_run_id = ? ORDER BY started_at ASC`,
		pipelineRunID)
	if err != nil {
		return nil, fmt.Errorf("stage list: %w", err)
	}
	defer rows.Close()
	var out []*StageResult
	for rows.Next() {
		var (
			sr        StageResult
			endedAt   sql.NullString
			outcome   sql.NullString
			spawnID   sql.NullString
			logTail   sql.NullString
			artifacts string
			startedAt string
		)
		if err := rows.Scan(&sr.ID, &sr.PipelineRunID, &sr.Stage, &sr.Attempt,
			&startedAt, &endedAt, &outcome, &spawnID, &sr.CostUSD, &artifacts, &logTail); err != nil {
			return nil, fmt.Errorf("stage scan: %w", err)
		}
		if sr.StartedAt, err = parseTime(startedAt); err != nil {
			return nil, fmt.Errorf("started_at: %w", err)
		}
		if sr.EndedAt, err = nullableTime(endedAt); err != nil {
			return nil, fmt.Errorf("ended_at: %w", err)
		}
		if outcome.Valid {
			o := StageOutcome(outcome.String)
			sr.Outcome = &o
		}
		if spawnID.Valid {
			sr.SpawnID = spawnID.String
		}
		if logTail.Valid {
			sr.LogTail = logTail.String
		}
		if err := jsonInto(artifacts, &sr.Artifacts); err != nil {
			return nil, fmt.Errorf("artifacts: %w", err)
		}
		out = append(out, &sr)
	}
	return out, rows.Err()
}

// ----- Gate outcomes -----

const gateColumns = `id, pipeline_run_id, after_stage, gate_name, outcome,
		reasons_json, judged_by, evaluated_at`

// PutGate appends a gate outcome record.
func (d *PipelineDAO) PutGate(ctx context.Context, g *GateOutcome) error {
	if g == nil || g.PipelineRunID == "" || g.GateName == "" {
		return errors.New("pipeline: gate outcome requires PipelineRunID + GateName")
	}
	if g.EvaluatedAt.IsZero() {
		g.EvaluatedAt = time.Now().UTC()
	}
	reasons, err := jsonField(g.Reasons)
	if err != nil {
		return fmt.Errorf("reasons: %w", err)
	}
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO gate_outcomes (pipeline_run_id, after_stage, gate_name, outcome,
			reasons_json, judged_by, evaluated_at)
		VALUES (?,?,?,?,?,?,?)
	`,
		g.PipelineRunID, g.AfterStage, g.GateName, string(g.Outcome),
		reasons, g.JudgedBy, timeRFC3339(g.EvaluatedAt),
	)
	if err != nil {
		return fmt.Errorf("gate put: %w", err)
	}
	id, _ := res.LastInsertId()
	g.ID = id
	return nil
}

// ListGates returns every gate outcome for a pipeline run, oldest-first.
func (d *PipelineDAO) ListGates(ctx context.Context, pipelineRunID string) ([]*GateOutcome, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+gateColumns+` FROM gate_outcomes WHERE pipeline_run_id = ? ORDER BY evaluated_at ASC`,
		pipelineRunID)
	if err != nil {
		return nil, fmt.Errorf("gate list: %w", err)
	}
	defer rows.Close()
	var out []*GateOutcome
	for rows.Next() {
		var (
			g           GateOutcome
			reasons     string
			outcome     string
			evaluatedAt string
		)
		if err := rows.Scan(&g.ID, &g.PipelineRunID, &g.AfterStage, &g.GateName,
			&outcome, &reasons, &g.JudgedBy, &evaluatedAt); err != nil {
			return nil, fmt.Errorf("gate scan: %w", err)
		}
		g.Outcome = GateOutcomeKind(outcome)
		if g.EvaluatedAt, err = parseTime(evaluatedAt); err != nil {
			return nil, fmt.Errorf("evaluated_at: %w", err)
		}
		if err := jsonInto(reasons, &g.Reasons); err != nil {
			return nil, fmt.Errorf("reasons: %w", err)
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}
