package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PolicyProposalDAO records adaptive-policy suggestions emitted by the
// weekly Sunday job. v2.0 proposals are human-applied; v2.1 will auto-apply
// `relax` proposals (relaxations only) with a 24h revert window and a
// path-class denylist enforced by the engine, not the DAO.
type PolicyProposalDAO struct {
	db *sql.DB
}

const policyProposalColumns = `id, proposal_date, kind, target, diff,
		rationale, state, applied_at, revert_deadline, created_at`

// Create inserts a new proposal in `pending` state.
func (d *PolicyProposalDAO) Create(ctx context.Context, p *PolicyProposal) error {
	if p == nil {
		return errors.New("policy_proposal: nil")
	}
	if p.Kind == "" || p.Target == "" || p.Diff == "" {
		return errors.New("policy_proposal: Kind, Target, Diff required")
	}
	if p.ProposalDate == "" {
		p.ProposalDate = time.Now().UTC().Format("2006-01-02")
	}
	if p.State == "" {
		p.State = PolicyProposalPending
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	var (
		appliedAt sql.NullString
		deadline  sql.NullString
	)
	if p.AppliedAt != nil {
		appliedAt = sql.NullString{String: timeRFC3339(*p.AppliedAt), Valid: true}
	}
	if p.RevertDeadline != nil {
		deadline = sql.NullString{String: timeRFC3339(*p.RevertDeadline), Valid: true}
	}
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO policy_proposals (proposal_date, kind, target, diff,
			rationale, state, applied_at, revert_deadline, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)
	`,
		p.ProposalDate, string(p.Kind), p.Target, p.Diff, p.Rationale,
		string(p.State), appliedAt, deadline, timeRFC3339(p.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("policy_proposal create: %w", err)
	}
	id, _ := res.LastInsertId()
	p.ID = id
	return nil
}

// Get returns one proposal by id, or ErrNotFound.
func (d *PolicyProposalDAO) Get(ctx context.Context, id int64) (*PolicyProposal, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT `+policyProposalColumns+` FROM policy_proposals WHERE id = ?`, id)
	return scanPolicyProposal(row)
}

// ListByState returns proposals in a given state, newest-first.
func (d *PolicyProposalDAO) ListByState(ctx context.Context, state PolicyProposalState) ([]*PolicyProposal, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+policyProposalColumns+` FROM policy_proposals WHERE state = ? ORDER BY created_at DESC`,
		string(state))
	if err != nil {
		return nil, fmt.Errorf("policy_proposal list-state: %w", err)
	}
	defer rows.Close()
	return collectPolicyProposalRows(rows)
}

// ListByDate returns every proposal emitted on a given YYYY-MM-DD.
func (d *PolicyProposalDAO) ListByDate(ctx context.Context, date string) ([]*PolicyProposal, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+policyProposalColumns+` FROM policy_proposals WHERE proposal_date = ? ORDER BY created_at ASC`,
		date)
	if err != nil {
		return nil, fmt.Errorf("policy_proposal list-date: %w", err)
	}
	defer rows.Close()
	return collectPolicyProposalRows(rows)
}

// Apply transitions a pending proposal to applied_human or applied_auto and
// stamps applied_at + revert_deadline (deadline is optional; pass zero
// time.Time to leave NULL).
func (d *PolicyProposalDAO) Apply(ctx context.Context, id int64, state PolicyProposalState, deadline time.Time) error {
	if state != PolicyProposalAppliedHuman && state != PolicyProposalAppliedAuto {
		return fmt.Errorf("policy_proposal apply: state %q is not an applied state", state)
	}
	now := time.Now().UTC()
	var deadlineArg sql.NullString
	if !deadline.IsZero() {
		deadlineArg = sql.NullString{String: timeRFC3339(deadline), Valid: true}
	}
	res, err := d.db.ExecContext(ctx, `
		UPDATE policy_proposals
		SET state = ?, applied_at = ?, revert_deadline = ?
		WHERE id = ? AND state = ?
	`,
		string(state), timeRFC3339(now), deadlineArg, id, string(PolicyProposalPending),
	)
	if err != nil {
		return fmt.Errorf("policy_proposal apply: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Reject transitions a pending proposal to rejected. No-op when the
// proposal is already in a terminal state (returns ErrNotFound).
func (d *PolicyProposalDAO) Reject(ctx context.Context, id int64) error {
	res, err := d.db.ExecContext(ctx, `
		UPDATE policy_proposals SET state = ?
		WHERE id = ? AND state = ?
	`, string(PolicyProposalRejected), id, string(PolicyProposalPending))
	if err != nil {
		return fmt.Errorf("policy_proposal reject: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Revert transitions an applied proposal to reverted. Used when a v2.1
// auto-applied relaxation sees a regression within the revert window.
func (d *PolicyProposalDAO) Revert(ctx context.Context, id int64) error {
	res, err := d.db.ExecContext(ctx, `
		UPDATE policy_proposals SET state = ?
		WHERE id = ? AND state IN (?, ?)
	`,
		string(PolicyProposalReverted), id,
		string(PolicyProposalAppliedHuman), string(PolicyProposalAppliedAuto),
	)
	if err != nil {
		return fmt.Errorf("policy_proposal revert: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func collectPolicyProposalRows(rows *sql.Rows) ([]*PolicyProposal, error) {
	var out []*PolicyProposal
	for rows.Next() {
		p, err := scanPolicyProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanPolicyProposal(s scanner) (*PolicyProposal, error) {
	var (
		p         PolicyProposal
		kind      string
		state     string
		appliedAt sql.NullString
		deadline  sql.NullString
		createdAt string
	)
	err := s.Scan(
		&p.ID, &p.ProposalDate, &kind, &p.Target, &p.Diff,
		&p.Rationale, &state, &appliedAt, &deadline, &createdAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("policy_proposal scan: %w", err)
	}
	p.Kind = PolicyProposalKind(kind)
	p.State = PolicyProposalState(state)
	if p.AppliedAt, err = nullableTime(appliedAt); err != nil {
		return nil, fmt.Errorf("applied_at: %w", err)
	}
	if p.RevertDeadline, err = nullableTime(deadline); err != nil {
		return nil, fmt.Errorf("revert_deadline: %w", err)
	}
	if p.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, fmt.Errorf("created_at: %w", err)
	}
	return &p, nil
}
