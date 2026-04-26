package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CouncilDAO exposes CRUD against council_runs.
type CouncilDAO struct {
	db *sql.DB
}

const councilColumns = `id, trigger, started_at, ended_at, outcome, cost_frontier_usd,
		cost_local_usd, artifacts_json, backlog_deltas_json, sidecar_json, branch_name,
		commit_sha, notes`

// Put inserts or replaces a council run record.
func (d *CouncilDAO) Put(ctx context.Context, run *CouncilRun) error {
	if run == nil || run.ID == "" {
		return errors.New("council: run.ID required")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	artifacts, err := jsonField(run.Artifacts)
	if err != nil {
		return fmt.Errorf("artifacts: %w", err)
	}
	deltas, err := jsonField(run.BacklogDeltas)
	if err != nil {
		return fmt.Errorf("backlog_deltas: %w", err)
	}
	sidecar, err := jsonField(run.Sidecar)
	if err != nil {
		return fmt.Errorf("sidecar: %w", err)
	}
	var endedAt sql.NullString
	if run.EndedAt != nil {
		endedAt = sql.NullString{String: timeRFC3339(*run.EndedAt), Valid: true}
	}
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO council_runs (`+councilColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			trigger             = excluded.trigger,
			started_at          = excluded.started_at,
			ended_at            = excluded.ended_at,
			outcome             = excluded.outcome,
			cost_frontier_usd   = excluded.cost_frontier_usd,
			cost_local_usd      = excluded.cost_local_usd,
			artifacts_json      = excluded.artifacts_json,
			backlog_deltas_json = excluded.backlog_deltas_json,
			sidecar_json        = excluded.sidecar_json,
			branch_name         = excluded.branch_name,
			commit_sha          = excluded.commit_sha,
			notes               = excluded.notes
	`,
		run.ID, string(run.Trigger), timeRFC3339(run.StartedAt), endedAt, string(run.Outcome),
		run.CostFrontierUSD, run.CostLocalUSD, artifacts, deltas, sidecar,
		nullStr(run.BranchName), nullStr(run.CommitSHA), nullStr(run.Notes),
	)
	if err != nil {
		return fmt.Errorf("council put %s: %w", run.ID, err)
	}
	return nil
}

// Get fetches a council run by id.
func (d *CouncilDAO) Get(ctx context.Context, id string) (*CouncilRun, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+councilColumns+` FROM council_runs WHERE id = ?`, id)
	return scanCouncil(row)
}

// List returns council runs, newest-first by started_at.
func (d *CouncilDAO) List(ctx context.Context, limit int) ([]*CouncilRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+councilColumns+` FROM council_runs ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("council list: %w", err)
	}
	defer rows.Close()
	var out []*CouncilRun
	for rows.Next() {
		r, err := scanCouncil(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SumCostSince returns total council spend (frontier + local) since the given
// timestamp. Used by the budget enforcer to honor per-day caps.
func (d *CouncilDAO) SumCostSince(ctx context.Context, since time.Time) (float64, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(cost_frontier_usd) + SUM(cost_local_usd), 0)
		FROM council_runs WHERE started_at >= ?
	`, timeRFC3339(since))
	var total float64
	if err := row.Scan(&total); err != nil {
		return 0, fmt.Errorf("council sum-cost: %w", err)
	}
	return total, nil
}

// CountSince returns the number of council runs started at-or-after `since`.
func (d *CouncilDAO) CountSince(ctx context.Context, since time.Time) (int, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM council_runs WHERE started_at >= ?`,
		timeRFC3339(since))
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("council count-since: %w", err)
	}
	return n, nil
}

func scanCouncil(s scanner) (*CouncilRun, error) {
	var (
		run               CouncilRun
		endedAt, branch   sql.NullString
		commit, notes     sql.NullString
		artifacts, deltas string
		sidecar           string
		startedAt         string
	)
	err := s.Scan(
		&run.ID, &run.Trigger, &startedAt, &endedAt, &run.Outcome, &run.CostFrontierUSD,
		&run.CostLocalUSD, &artifacts, &deltas, &sidecar, &branch, &commit, &notes,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("council scan: %w", err)
	}
	if run.StartedAt, err = parseTime(startedAt); err != nil {
		return nil, fmt.Errorf("started_at: %w", err)
	}
	if run.EndedAt, err = nullableTime(endedAt); err != nil {
		return nil, fmt.Errorf("ended_at: %w", err)
	}
	if err := jsonInto(artifacts, &run.Artifacts); err != nil {
		return nil, fmt.Errorf("artifacts: %w", err)
	}
	if err := jsonInto(deltas, &run.BacklogDeltas); err != nil {
		return nil, fmt.Errorf("backlog_deltas: %w", err)
	}
	if err := jsonInto(sidecar, &run.Sidecar); err != nil {
		return nil, fmt.Errorf("sidecar: %w", err)
	}
	if branch.Valid {
		run.BranchName = branch.String
	}
	if commit.Valid {
		run.CommitSHA = commit.String
	}
	if notes.Valid {
		run.Notes = notes.String
	}
	return &run, nil
}
