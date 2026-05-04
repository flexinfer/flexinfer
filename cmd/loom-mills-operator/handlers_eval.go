package main

import (
	"net/http"
	"strconv"
	"time"
)

// handleEvalScores lists evaluation scores since a given timestamp.
// Implementation surfaces today (queries the eval_scores table); the
// table stays empty until slice 3.5 (Loop A) starts recording council
// artifact judgments.
func (o *operator) handleEvalScores(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-7 * 24 * time.Hour)
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "since must be RFC3339", http.StatusBadRequest)
			return
		}
		since = t
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	scores, err := o.store.Eval.ListSince(r.Context(), since, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, scores)
}

// handleEvalRunCross is the admin endpoint that triggers a cross-run
// consistency eval. Lands in slice 6.4.
func (o *operator) handleEvalRunCross(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "6.4 cross-run eval")
}

// parseLimit lifts a query-string limit into a positive int with a fallback.
// Centralised because every list endpoint takes one.
func parseLimit(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	const ceiling = 1000
	if n > ceiling {
		return ceiling
	}
	return n
}
