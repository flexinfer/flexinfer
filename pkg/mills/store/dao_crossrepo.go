package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CrossRepoDAO exposes CRUD against cross_repo_runs, the canonical record of
// a backlog item that spans multiple repos under atomic-merge semantics.
type CrossRepoDAO struct {
	db *sql.DB
}

const crossRepoColumns = `id, backlog_item_id, repos_json, state,
		atomicity_strategy, created_at, updated_at`

// PutRun inserts or replaces a cross-repo run.
func (d *CrossRepoDAO) PutRun(ctx context.Context, r *CrossRepoRun) error {
	if r == nil || r.ID == "" {
		return errors.New("cross_repo: ID required")
	}
	if r.BacklogItemID == "" {
		return errors.New("cross_repo: BacklogItemID required")
	}
	if r.AtomicityStrategy == "" {
		r.AtomicityStrategy = "all_or_revert"
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	repos, err := jsonField(r.Repos)
	if err != nil {
		return fmt.Errorf("repos: %w", err)
	}
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO cross_repo_runs (`+crossRepoColumns+`)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			backlog_item_id    = excluded.backlog_item_id,
			repos_json         = excluded.repos_json,
			state              = excluded.state,
			atomicity_strategy = excluded.atomicity_strategy,
			updated_at         = excluded.updated_at
	`,
		r.ID, r.BacklogItemID, repos, string(r.State),
		r.AtomicityStrategy, timeRFC3339(r.CreatedAt), timeRFC3339(r.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("cross_repo put %s: %w", r.ID, err)
	}
	return nil
}

// GetRun returns one cross-repo run by id, or ErrNotFound.
func (d *CrossRepoDAO) GetRun(ctx context.Context, id string) (*CrossRepoRun, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT `+crossRepoColumns+` FROM cross_repo_runs WHERE id = ?`, id)
	return scanCrossRepoRun(row)
}

// ListByState returns cross-repo runs in a given state, oldest-first.
func (d *CrossRepoDAO) ListByState(ctx context.Context, state CrossRepoState) ([]*CrossRepoRun, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+crossRepoColumns+` FROM cross_repo_runs WHERE state = ? ORDER BY created_at ASC`,
		string(state))
	if err != nil {
		return nil, fmt.Errorf("cross_repo list-state: %w", err)
	}
	defer rows.Close()
	return collectCrossRepoRows(rows)
}

// ListByBacklog returns every cross-repo run for a backlog item, newest-first.
func (d *CrossRepoDAO) ListByBacklog(ctx context.Context, backlogID string) ([]*CrossRepoRun, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+crossRepoColumns+` FROM cross_repo_runs WHERE backlog_item_id = ? ORDER BY created_at DESC`,
		backlogID)
	if err != nil {
		return nil, fmt.Errorf("cross_repo list-backlog: %w", err)
	}
	defer rows.Close()
	return collectCrossRepoRows(rows)
}

// SetState transitions the state of a cross-repo run.
func (d *CrossRepoDAO) SetState(ctx context.Context, id string, state CrossRepoState) error {
	res, err := d.db.ExecContext(ctx, `
		UPDATE cross_repo_runs SET state = ?, updated_at = ? WHERE id = ?
	`, string(state), timeRFC3339(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("cross_repo set-state: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func collectCrossRepoRows(rows *sql.Rows) ([]*CrossRepoRun, error) {
	var out []*CrossRepoRun
	for rows.Next() {
		r, err := scanCrossRepoRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanCrossRepoRun(s scanner) (*CrossRepoRun, error) {
	var (
		r                    CrossRepoRun
		repos                string
		state                string
		createdAt, updatedAt string
	)
	err := s.Scan(
		&r.ID, &r.BacklogItemID, &repos, &state,
		&r.AtomicityStrategy, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("cross_repo scan: %w", err)
	}
	r.State = CrossRepoState(state)
	if err := jsonInto(repos, &r.Repos); err != nil {
		return nil, fmt.Errorf("repos: %w", err)
	}
	if r.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, fmt.Errorf("created_at: %w", err)
	}
	if r.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return nil, fmt.Errorf("updated_at: %w", err)
	}
	return &r, nil
}
