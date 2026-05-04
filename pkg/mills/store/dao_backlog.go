package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a lookup misses.
var ErrNotFound = errors.New("mills store: not found")

// BacklogDAO exposes CRUD + queries against the backlog_items table.
type BacklogDAO struct {
	db *sql.DB
}

const backlogColumns = `id, gitlab_issue_iid, title, labels_json, state, priority,
		spec_doc, spec_anchor, success_json, budget_json, policy_json, slices_json,
		dependencies_json, council_run_id, created_by, created_at, updated_at`

// Put inserts or replaces a backlog item. CreatedAt is preserved if the row
// already exists; UpdatedAt is always set to now.
func (d *BacklogDAO) Put(ctx context.Context, item *BacklogItem) error {
	if item == nil || item.ID == "" {
		return errors.New("backlog: item.ID required")
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now

	labels, err := jsonField(item.Labels)
	if err != nil {
		return fmt.Errorf("labels: %w", err)
	}
	success, err := jsonField(item.Success)
	if err != nil {
		return fmt.Errorf("success: %w", err)
	}
	budget, err := jsonField(item.Budget)
	if err != nil {
		return fmt.Errorf("budget: %w", err)
	}
	policy, err := jsonField(item.Policy)
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	slices, err := jsonField(item.Slices)
	if err != nil {
		return fmt.Errorf("slices: %w", err)
	}
	deps, err := jsonField(item.Dependencies)
	if err != nil {
		return fmt.Errorf("dependencies: %w", err)
	}

	var iid sql.NullInt64
	if item.GitLabIssueIID != nil {
		iid = sql.NullInt64{Int64: *item.GitLabIssueIID, Valid: true}
	}
	var council sql.NullString
	if item.CouncilRunID != nil {
		council = sql.NullString{String: *item.CouncilRunID, Valid: true}
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO backlog_items (`+backlogColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			gitlab_issue_iid = excluded.gitlab_issue_iid,
			title            = excluded.title,
			labels_json      = excluded.labels_json,
			state            = excluded.state,
			priority         = excluded.priority,
			spec_doc         = excluded.spec_doc,
			spec_anchor      = excluded.spec_anchor,
			success_json     = excluded.success_json,
			budget_json      = excluded.budget_json,
			policy_json      = excluded.policy_json,
			slices_json      = excluded.slices_json,
			dependencies_json= excluded.dependencies_json,
			council_run_id   = excluded.council_run_id,
			created_by       = excluded.created_by,
			updated_at       = excluded.updated_at
	`,
		item.ID, iid, item.Title, labels, string(item.State), string(item.Priority),
		nullStr(item.SpecDoc), nullStr(item.SpecAnchor), success, budget, policy, slices,
		deps, council, item.CreatedBy, timeRFC3339(item.CreatedAt), timeRFC3339(item.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("backlog put %s: %w", item.ID, err)
	}
	return nil
}

// Get returns the backlog item with the given id, or ErrNotFound.
func (d *BacklogDAO) Get(ctx context.Context, id string) (*BacklogItem, error) {
	row := d.db.QueryRowContext(ctx, `SELECT `+backlogColumns+` FROM backlog_items WHERE id = ?`, id)
	return scanBacklog(row)
}

// List returns every backlog item, newest-first by updated_at.
func (d *BacklogDAO) List(ctx context.Context) ([]*BacklogItem, error) {
	return d.queryMany(ctx, `SELECT `+backlogColumns+` FROM backlog_items ORDER BY updated_at DESC`)
}

// ListByState returns backlog items with the given state.
func (d *BacklogDAO) ListByState(ctx context.Context, state BacklogState) ([]*BacklogItem, error) {
	return d.queryMany(ctx,
		`SELECT `+backlogColumns+` FROM backlog_items WHERE state = ? ORDER BY priority ASC, created_at ASC`,
		string(state),
	)
}

// Delete removes a backlog item by id. Returns ErrNotFound if it didn't exist.
func (d *BacklogDAO) Delete(ctx context.Context, id string) error {
	res, err := d.db.ExecContext(ctx, `DELETE FROM backlog_items WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("backlog delete %s: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *BacklogDAO) queryMany(ctx context.Context, q string, args ...any) ([]*BacklogItem, error) {
	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("backlog query: %w", err)
	}
	defer rows.Close()
	var out []*BacklogItem
	for rows.Next() {
		item, err := scanBacklog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// scanner abstracts *sql.Row and *sql.Rows for shared scan logic.
type scanner interface {
	Scan(dest ...any) error
}

func scanBacklog(s scanner) (*BacklogItem, error) {
	var (
		item             BacklogItem
		iid              sql.NullInt64
		specDoc          sql.NullString
		specAnchor       sql.NullString
		council          sql.NullString
		labels, success  string
		budget, policy   string
		slicesJSON, deps string
		createdAt        string
		updatedAt        string
	)
	err := s.Scan(
		&item.ID, &iid, &item.Title, &labels, &item.State, &item.Priority,
		&specDoc, &specAnchor, &success, &budget, &policy, &slicesJSON,
		&deps, &council, &item.CreatedBy, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("backlog scan: %w", err)
	}
	item.GitLabIssueIID = nullableInt64(iid)
	item.CouncilRunID = nullableString(council)
	if specDoc.Valid {
		item.SpecDoc = specDoc.String
	}
	if specAnchor.Valid {
		item.SpecAnchor = specAnchor.String
	}
	if err := jsonInto(labels, &item.Labels); err != nil {
		return nil, fmt.Errorf("labels: %w", err)
	}
	if err := jsonInto(success, &item.Success); err != nil {
		return nil, fmt.Errorf("success: %w", err)
	}
	if err := jsonInto(budget, &item.Budget); err != nil {
		return nil, fmt.Errorf("budget: %w", err)
	}
	if err := jsonInto(policy, &item.Policy); err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	if err := jsonInto(slicesJSON, &item.Slices); err != nil {
		return nil, fmt.Errorf("slices: %w", err)
	}
	if err := jsonInto(deps, &item.Dependencies); err != nil {
		return nil, fmt.Errorf("dependencies: %w", err)
	}
	if item.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, fmt.Errorf("created_at: %w", err)
	}
	if item.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return nil, fmt.Errorf("updated_at: %w", err)
	}
	return &item, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
