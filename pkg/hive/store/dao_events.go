package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// EventDAO appends to the audit/debug event log.
type EventDAO struct {
	db *sql.DB
}

const eventColumns = `id, occurred_at, actor, kind, subject_kind, subject_id, payload_json`

// Append writes one event. Auto-fills OccurredAt if zero.
func (d *EventDAO) Append(ctx context.Context, e *Event) error {
	if e == nil || e.Actor == "" || e.Kind == "" {
		return errors.New("event: Actor + Kind required")
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	payload, err := jsonField(e.Payload)
	if err != nil {
		return fmt.Errorf("payload: %w", err)
	}
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO events (occurred_at, actor, kind, subject_kind, subject_id, payload_json)
		VALUES (?,?,?,?,?,?)
	`,
		timeRFC3339(e.OccurredAt), e.Actor, e.Kind,
		nullStr(e.SubjectKind), nullStr(e.SubjectID), payload,
	)
	if err != nil {
		return fmt.Errorf("event append: %w", err)
	}
	id, _ := res.LastInsertId()
	e.ID = id
	return nil
}

// ListBySubject returns events for the given (subject_kind, subject_id), newest-first.
func (d *EventDAO) ListBySubject(ctx context.Context, kind, id string, limit int) ([]*Event, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+eventColumns+`
		 FROM events
		 WHERE subject_kind = ? AND subject_id = ?
		 ORDER BY occurred_at DESC
		 LIMIT ?`,
		kind, id, limit)
	if err != nil {
		return nil, fmt.Errorf("event list-subject: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

// ListSince returns events occurred_at >= since, newest-first.
func (d *EventDAO) ListSince(ctx context.Context, since time.Time, limit int) ([]*Event, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+eventColumns+`
		 FROM events
		 WHERE occurred_at >= ?
		 ORDER BY occurred_at DESC
		 LIMIT ?`,
		timeRFC3339(since), limit)
	if err != nil {
		return nil, fmt.Errorf("event list-since: %w", err)
	}
	defer rows.Close()
	return scanEvents(rows)
}

func scanEvents(rows *sql.Rows) ([]*Event, error) {
	var out []*Event
	for rows.Next() {
		var (
			e           Event
			occurredAt  string
			payload     string
			subjectKind sql.NullString
			subjectID   sql.NullString
		)
		if err := rows.Scan(&e.ID, &occurredAt, &e.Actor, &e.Kind,
			&subjectKind, &subjectID, &payload); err != nil {
			return nil, fmt.Errorf("event scan: %w", err)
		}
		t, err := parseTime(occurredAt)
		if err != nil {
			return nil, fmt.Errorf("occurred_at: %w", err)
		}
		e.OccurredAt = t
		if subjectKind.Valid {
			e.SubjectKind = subjectKind.String
		}
		if subjectID.Valid {
			e.SubjectID = subjectID.String
		}
		if err := jsonInto(payload, &e.Payload); err != nil {
			return nil, fmt.Errorf("payload: %w", err)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
