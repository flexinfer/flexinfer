package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// EvalDAO records evaluation scores for council/pipeline/cross-run subjects.
type EvalDAO struct {
	db *sql.DB
}

const evalColumns = `id, subject_kind, subject_id, rubric, score,
		breakdown_json, judged_by, evaluated_at, notes`

// RecordScore appends a score row.
func (d *EvalDAO) RecordScore(ctx context.Context, sc *EvalScore) error {
	if sc == nil || sc.SubjectID == "" {
		return errors.New("eval: subject_id required")
	}
	if sc.SubjectKind == "" {
		return errors.New("eval: subject_kind required")
	}
	if sc.EvaluatedAt.IsZero() {
		sc.EvaluatedAt = time.Now().UTC()
	}
	breakdown, err := jsonField(sc.Breakdown)
	if err != nil {
		return fmt.Errorf("breakdown: %w", err)
	}
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO eval_scores (subject_kind, subject_id, rubric, score,
			breakdown_json, judged_by, evaluated_at, notes)
		VALUES (?,?,?,?,?,?,?,?)
	`,
		string(sc.SubjectKind), sc.SubjectID, sc.Rubric, sc.Score,
		breakdown, sc.JudgedBy, timeRFC3339(sc.EvaluatedAt), nullStr(sc.Notes),
	)
	if err != nil {
		return fmt.Errorf("eval record: %w", err)
	}
	id, _ := res.LastInsertId()
	sc.ID = id
	return nil
}

// LatestPerSubject returns the most recent score for each rubric on the given subject.
func (d *EvalDAO) LatestPerSubject(ctx context.Context, kind EvalSubjectKind, id string) ([]*EvalScore, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT `+evalColumns+`
		FROM eval_scores
		WHERE subject_kind = ? AND subject_id = ?
		AND id IN (
			SELECT MAX(id) FROM eval_scores
			WHERE subject_kind = ? AND subject_id = ?
			GROUP BY rubric
		)
		ORDER BY rubric ASC
	`, string(kind), id, string(kind), id)
	if err != nil {
		return nil, fmt.Errorf("eval latest: %w", err)
	}
	defer rows.Close()
	var out []*EvalScore
	for rows.Next() {
		s, err := scanEval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListSince returns every eval score evaluated_at >= since, newest-first.
func (d *EvalDAO) ListSince(ctx context.Context, since time.Time, limit int) ([]*EvalScore, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT `+evalColumns+`
		FROM eval_scores
		WHERE evaluated_at >= ?
		ORDER BY evaluated_at DESC
		LIMIT ?
	`, timeRFC3339(since), limit)
	if err != nil {
		return nil, fmt.Errorf("eval since: %w", err)
	}
	defer rows.Close()
	var out []*EvalScore
	for rows.Next() {
		s, err := scanEval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanEval(s scanner) (*EvalScore, error) {
	var (
		sc          EvalScore
		breakdown   string
		evaluatedAt string
		notes       sql.NullString
		kind        string
	)
	if err := s.Scan(&sc.ID, &kind, &sc.SubjectID, &sc.Rubric, &sc.Score,
		&breakdown, &sc.JudgedBy, &evaluatedAt, &notes); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("eval scan: %w", err)
	}
	sc.SubjectKind = EvalSubjectKind(kind)
	t, err := parseTime(evaluatedAt)
	if err != nil {
		return nil, fmt.Errorf("evaluated_at: %w", err)
	}
	sc.EvaluatedAt = t
	if err := jsonInto(breakdown, &sc.Breakdown); err != nil {
		return nil, fmt.Errorf("breakdown: %w", err)
	}
	if notes.Valid {
		sc.Notes = notes.String
	}
	return &sc, nil
}
