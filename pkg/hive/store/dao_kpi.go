package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// KPIDAO records and reads rolled-up metric snapshots.
type KPIDAO struct {
	db *sql.DB
}

// RecordSnapshot appends a snapshot row.
func (d *KPIDAO) RecordSnapshot(ctx context.Context, snap *KPISnapshot) error {
	if snap == nil {
		return errors.New("kpi: snapshot required")
	}
	if snap.SnapshotAt.IsZero() {
		snap.SnapshotAt = time.Now().UTC()
	}
	if snap.WindowSeconds <= 0 {
		return errors.New("kpi: WindowSeconds must be positive")
	}
	metrics, err := jsonField(snap.Metrics)
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO kpi_snapshots (snapshot_at, window_seconds, metrics_json)
		VALUES (?, ?, ?)
	`, timeRFC3339(snap.SnapshotAt), snap.WindowSeconds, metrics)
	if err != nil {
		return fmt.Errorf("kpi record: %w", err)
	}
	id, _ := res.LastInsertId()
	snap.ID = id
	return nil
}

// Latest returns the most recent snapshot for the given window, or ErrNotFound.
func (d *KPIDAO) Latest(ctx context.Context, windowSeconds int) (*KPISnapshot, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT id, snapshot_at, window_seconds, metrics_json
		FROM kpi_snapshots
		WHERE window_seconds = ?
		ORDER BY snapshot_at DESC
		LIMIT 1
	`, windowSeconds)
	return scanKPI(row)
}

// Range returns snapshots for the window between [from, to], oldest-first.
func (d *KPIDAO) Range(ctx context.Context, windowSeconds int, from, to time.Time) ([]*KPISnapshot, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, snapshot_at, window_seconds, metrics_json
		FROM kpi_snapshots
		WHERE window_seconds = ? AND snapshot_at BETWEEN ? AND ?
		ORDER BY snapshot_at ASC
	`, windowSeconds, timeRFC3339(from), timeRFC3339(to))
	if err != nil {
		return nil, fmt.Errorf("kpi range: %w", err)
	}
	defer rows.Close()
	var out []*KPISnapshot
	for rows.Next() {
		s, err := scanKPI(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanKPI(s scanner) (*KPISnapshot, error) {
	var (
		snap       KPISnapshot
		snapshotAt string
		metrics    string
	)
	if err := s.Scan(&snap.ID, &snapshotAt, &snap.WindowSeconds, &metrics); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("kpi scan: %w", err)
	}
	t, err := parseTime(snapshotAt)
	if err != nil {
		return nil, fmt.Errorf("snapshot_at: %w", err)
	}
	snap.SnapshotAt = t
	if err := jsonInto(metrics, &snap.Metrics); err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	return &snap, nil
}
