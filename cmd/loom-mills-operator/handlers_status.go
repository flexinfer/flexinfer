package main

import (
	"context"
	"net/http"
	"time"
)

// handleStatusFull replaces the slice-1.2 stub with a fully populated
// snapshot. Fields the mills doesn't yet have data for (queue depth,
// active pipeline runs, last council) are sourced from the canonical
// store, so as soon as the reconciler starts producing rows the values
// become non-nil automatically — no further handler changes needed.
func (o *operator) handleStatusFull(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	policy := o.policy.Current()

	queueDepth := 0
	if items, err := o.store.Backlog.ListByState(ctx, "queued"); err == nil {
		queueDepth = len(items)
	}
	active, _ := o.store.Pipeline.CountActive(ctx)

	var lastCouncil *time.Time
	if runs, err := o.store.Council.List(ctx, 1); err == nil && len(runs) > 0 {
		t := runs[0].StartedAt
		lastCouncil = &t
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"db_ok":                o.dbOK(ctx),
		"policy_enabled":       policy.IsEnabled(),
		"policy_version":       policy.Version,
		"queue_depth":          queueDepth,
		"active_pipeline_runs": active,
		"last_council_at":      lastCouncil,
		"slice":                "2.4-rest-surface",
	})
}

// handlePolicy returns the current effective policy. Read-only — the
// policy is mutated by ConfigMap writes + the operator's fsnotify
// hot-reload, never via this endpoint.
func (o *operator) handlePolicy(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, o.policy.Current())
}

// handleKPIs returns the most recent KPI snapshot for the requested
// rolling window. Slice 5.1 wires the reconciler to record snapshots; the
// handler is correct today but returns 404 until a snapshot exists.
func (o *operator) handleKPIs(w http.ResponseWriter, r *http.Request) {
	window := r.URL.Query().Get("window")
	seconds := windowSeconds(window)
	if seconds == 0 {
		http.Error(w, "window must be one of 1d, 7d, 30d", http.StatusBadRequest)
		return
	}
	snap, err := o.store.KPI.Latest(r.Context(), seconds)
	if err != nil {
		// ErrNotFound surfaces as 404 with a clear "no snapshot yet" body
		// so HUD can render a placeholder card rather than an error toast.
		http.Error(w, "no kpi snapshot for window "+window, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// windowSeconds maps the user-friendly window names to seconds. Keep the
// set tight; arbitrary windows would let a caller cardinality-bomb the
// kpi_snapshots table.
func windowSeconds(window string) int {
	switch window {
	case "1d", "":
		return 86400
	case "7d":
		return 7 * 86400
	case "30d":
		return 30 * 86400
	}
	return 0
}

func (o *operator) dbOK(ctx context.Context) bool {
	if o.store == nil {
		return false
	}
	if db := o.store.DB(); db != nil {
		return db.PingContext(ctx) == nil
	}
	return false
}
