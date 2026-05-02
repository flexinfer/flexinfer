package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AuditDAO records adversarial audit verdicts on council artifacts and
// pipeline merges. Audit findings are advisory in v2.0 (informational +
// follow-up issue when survival_score < 0.6); they become blocking in v2.1
// once survivability KPIs prove low-noise.
type AuditDAO struct {
	db *sql.DB
}

const auditColumns = `id, subject_kind, subject_id, severity, rubric_id,
		survival_score, findings_json, auditor_pool, cost_usd, created_at`

// RecordFinding appends an audit verdict.
func (d *AuditDAO) RecordFinding(ctx context.Context, f *AuditFinding) error {
	if f == nil || f.SubjectID == "" {
		return errors.New("audit: SubjectID required")
	}
	if f.SubjectKind == "" {
		return errors.New("audit: SubjectKind required")
	}
	if f.SurvivalScore < 0 || f.SurvivalScore > 1 {
		return errors.New("audit: SurvivalScore must be in [0,1]")
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now().UTC()
	}
	findings, err := jsonField(f.Findings)
	if err != nil {
		return fmt.Errorf("findings: %w", err)
	}
	pool, err := jsonField(f.AuditorPool)
	if err != nil {
		return fmt.Errorf("auditor_pool: %w", err)
	}
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO audit_findings (subject_kind, subject_id, severity, rubric_id,
			survival_score, findings_json, auditor_pool, cost_usd, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)
	`,
		string(f.SubjectKind), f.SubjectID, string(f.Severity), f.RubricID,
		f.SurvivalScore, findings, pool, f.CostUSD, timeRFC3339(f.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("audit record: %w", err)
	}
	id, _ := res.LastInsertId()
	f.ID = id
	return nil
}

// LatestForSubject returns the most recent finding for a (kind, id) pair, or
// ErrNotFound. Subsequent re-runs (e.g., POST /api/hive/audit/run) append new
// rows; this returns the freshest verdict.
func (d *AuditDAO) LatestForSubject(ctx context.Context, kind AuditSubjectKind, id string) (*AuditFinding, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT `+auditColumns+` FROM audit_findings
		WHERE subject_kind = ? AND subject_id = ?
		ORDER BY id DESC LIMIT 1
	`, string(kind), id)
	return scanAuditFinding(row)
}

// ListForSubject returns every finding row for a subject, oldest-first.
func (d *AuditDAO) ListForSubject(ctx context.Context, kind AuditSubjectKind, id string) ([]*AuditFinding, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT `+auditColumns+` FROM audit_findings
		WHERE subject_kind = ? AND subject_id = ?
		ORDER BY id ASC
	`, string(kind), id)
	if err != nil {
		return nil, fmt.Errorf("audit list-subject: %w", err)
	}
	defer rows.Close()
	return collectAuditRows(rows)
}

// ListSince returns audit findings created at or after `since`, newest-first.
func (d *AuditDAO) ListSince(ctx context.Context, since time.Time, limit int) ([]*AuditFinding, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT `+auditColumns+` FROM audit_findings
		WHERE created_at >= ?
		ORDER BY created_at DESC
		LIMIT ?
	`, timeRFC3339(since), limit)
	if err != nil {
		return nil, fmt.Errorf("audit list-since: %w", err)
	}
	defer rows.Close()
	return collectAuditRows(rows)
}

// SurvivalRate returns the mean survival score across findings created at-or-
// after `since` for a subject_kind. Returns (rate, sampleSize). Used by the
// audit_survival_rate KPI.
func (d *AuditDAO) SurvivalRate(ctx context.Context, kind AuditSubjectKind, since time.Time) (float64, int, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(survival_score), 0), COUNT(*)
		FROM audit_findings
		WHERE subject_kind = ? AND created_at >= ?
	`, string(kind), timeRFC3339(since))
	var rate float64
	var n int
	if err := row.Scan(&rate, &n); err != nil {
		return 0, 0, fmt.Errorf("audit survival-rate: %w", err)
	}
	return rate, n, nil
}

func collectAuditRows(rows *sql.Rows) ([]*AuditFinding, error) {
	var out []*AuditFinding
	for rows.Next() {
		f, err := scanAuditFinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanAuditFinding(s scanner) (*AuditFinding, error) {
	var (
		f         AuditFinding
		kind      string
		severity  string
		findings  string
		pool      string
		createdAt string
	)
	err := s.Scan(
		&f.ID, &kind, &f.SubjectID, &severity, &f.RubricID,
		&f.SurvivalScore, &findings, &pool, &f.CostUSD, &createdAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("audit scan: %w", err)
	}
	f.SubjectKind = AuditSubjectKind(kind)
	f.Severity = AuditSeverity(severity)
	if err := jsonInto(findings, &f.Findings); err != nil {
		return nil, fmt.Errorf("findings: %w", err)
	}
	if err := jsonInto(pool, &f.AuditorPool); err != nil {
		return nil, fmt.Errorf("auditor_pool: %w", err)
	}
	if f.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, fmt.Errorf("created_at: %w", err)
	}
	return &f, nil
}
