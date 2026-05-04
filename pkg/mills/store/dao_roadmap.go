package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RoadmapDAO manages the roadmap_intents table — the council's machine-readable
// view of ROADMAP.md.
type RoadmapDAO struct {
	db *sql.DB
}

const roadmapColumns = `id, theme, priority, summary, constraints_json,
		last_seen_in_roadmap_sha, created_at, updated_at`

// Upsert inserts a new intent or updates the existing row keyed on (theme, summary).
func (d *RoadmapDAO) Upsert(ctx context.Context, intent *RoadmapIntent) error {
	if intent == nil || intent.Theme == "" || intent.Summary == "" {
		return errors.New("roadmap: Theme + Summary required")
	}
	now := time.Now().UTC()
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = now
	}
	intent.UpdatedAt = now
	constraints, err := jsonField(intent.Constraints)
	if err != nil {
		return fmt.Errorf("constraints: %w", err)
	}
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO roadmap_intents (theme, priority, summary, constraints_json,
			last_seen_in_roadmap_sha, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(theme, summary) DO UPDATE SET
			priority                 = excluded.priority,
			constraints_json         = excluded.constraints_json,
			last_seen_in_roadmap_sha = excluded.last_seen_in_roadmap_sha,
			updated_at               = excluded.updated_at
	`,
		intent.Theme, intent.Priority, intent.Summary, constraints,
		nullStr(intent.LastSeenInRoadmapSHA),
		timeRFC3339(intent.CreatedAt), timeRFC3339(intent.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("roadmap upsert: %w", err)
	}
	if id, err := res.LastInsertId(); err == nil && intent.ID == 0 {
		intent.ID = id
	}
	return nil
}

// List returns every intent ordered by priority asc, theme asc.
func (d *RoadmapDAO) List(ctx context.Context) ([]*RoadmapIntent, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT `+roadmapColumns+` FROM roadmap_intents ORDER BY priority ASC, theme ASC`)
	if err != nil {
		return nil, fmt.Errorf("roadmap list: %w", err)
	}
	defer rows.Close()
	var out []*RoadmapIntent
	for rows.Next() {
		ri, err := scanRoadmap(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ri)
	}
	return out, rows.Err()
}

// Delete removes intents that were not seen in the latest roadmap sha. Useful
// for council runs to retire entries that the human edited out of ROADMAP.md.
func (d *RoadmapDAO) DeleteStale(ctx context.Context, currentSHA string) (int64, error) {
	if currentSHA == "" {
		return 0, errors.New("roadmap: currentSHA required")
	}
	res, err := d.db.ExecContext(ctx,
		`DELETE FROM roadmap_intents WHERE last_seen_in_roadmap_sha IS NOT NULL AND last_seen_in_roadmap_sha != ?`,
		currentSHA)
	if err != nil {
		return 0, fmt.Errorf("roadmap delete-stale: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func scanRoadmap(s scanner) (*RoadmapIntent, error) {
	var (
		ri          RoadmapIntent
		constraints string
		sha         sql.NullString
		createdAt   string
		updatedAt   string
	)
	if err := s.Scan(&ri.ID, &ri.Theme, &ri.Priority, &ri.Summary,
		&constraints, &sha, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("roadmap scan: %w", err)
	}
	if sha.Valid {
		ri.LastSeenInRoadmapSHA = sha.String
	}
	if err := jsonInto(constraints, &ri.Constraints); err != nil {
		return nil, fmt.Errorf("constraints: %w", err)
	}
	t, err := parseTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("created_at: %w", err)
	}
	ri.CreatedAt = t
	t, err = parseTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("updated_at: %w", err)
	}
	ri.UpdatedAt = t
	return &ri, nil
}
