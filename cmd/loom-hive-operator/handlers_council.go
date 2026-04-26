package main

import (
	"errors"
	"net/http"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// handleCouncilRunsList returns the most recent N council runs.
func (o *operator) handleCouncilRunsList(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"), 50)
	runs, err := o.store.Council.List(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleCouncilRunGet returns the council run identified by the path
// component after /api/hive/council/runs/.
func (o *operator) handleCouncilRunGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	run, err := o.store.Council.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "council run not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// handleCouncilRun is the admin endpoint that triggers an ad-hoc council
// run. The implementation lands in slice 3.x once the council ensemble +
// editor + artifact writer ship; the auth gate is locked in here so the
// surface contract doesn't shift later.
func (o *operator) handleCouncilRun(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "3.x council ensemble")
}

// handleCouncilDryrun mirrors handleCouncilRun but writes artifacts to a
// scratch dir without committing or mutating GitLab.
func (o *operator) handleCouncilDryrun(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "3.x council ensemble (dryrun)")
}
