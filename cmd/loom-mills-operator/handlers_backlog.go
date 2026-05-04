package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/crb2nu/loom/pkg/mills/budget"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// handleBacklogList returns every backlog item, newest-first by updated_at.
// Pagination is intentionally absent for v1 — operator humans will browse
// O(100) items, not O(10k); when scale demands it we add ?limit/?offset
// without breaking the existing shape.
func (o *operator) handleBacklogList(w http.ResponseWriter, r *http.Request) {
	items, err := o.store.Backlog.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// handleBacklogCreate accepts a JSON BacklogItem body and upserts it into
// the canonical store. Required fields: id, title. Defaults applied when
// unset: state=queued, priority=P3, created_by="api". Always returns the
// persisted item (so callers can see normalized timestamps).
//
// Until slice 3.x lands the council-driven backlog mutator + GitLab sync,
// this endpoint is the only mutation path — used by smoke tests, manual
// queue insertions, and any external automation that wants to feed the
// mills without going through the GitLab issue → sync flow.
func (o *operator) handleBacklogCreate(w http.ResponseWriter, r *http.Request) {
	var item store.BacklogItem
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&item); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(item.Title) == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if item.State == "" {
		item.State = store.BacklogQueued
	}
	if item.Priority == "" {
		item.Priority = store.P3
	}
	if item.CreatedBy == "" {
		item.CreatedBy = "api"
	}
	if err := o.store.Backlog.Put(r.Context(), &item); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	persisted, err := o.store.Backlog.Get(r.Context(), item.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, persisted)
}

// handleBacklogGet returns one backlog item.
func (o *operator) handleBacklogGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	item, err := o.store.Backlog.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "backlog item not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// handleBacklogSync triggers the canonical-store ↔ GitLab sync. Implementation
// lands in slice 3.x (council backlog mutator); the auth gate is locked
// in here.
func (o *operator) handleBacklogSync(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "3.x backlog mutator + GitLab sync")
}

// handleCostPreview returns a Phase 7 slice 7.3 cost preview for one
// backlog item. Read-only: no admin token required. Required query
// param: ?backlog_id=. Responses:
//   - 200 + CostEstimate JSON on the happy path
//   - 400 when backlog_id is missing
//   - 404 when the backlog id is unknown
//   - 503 when the policy manager isn't configured (operator boot race)
//
// The estimator is constructed per-request because it's just two pointer
// wires; profiling didn't justify caching it on the operator struct.
func (o *operator) handleCostPreview(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("backlog_id"))
	if id == "" {
		http.Error(w, "backlog_id is required", http.StatusBadRequest)
		return
	}
	if o.policy == nil {
		http.Error(w, "policy manager not ready", http.StatusServiceUnavailable)
		return
	}
	est := &budget.Estimator{
		Store:      o.store,
		PolicyFunc: o.policy.Current,
	}
	preview, err := est.Preview(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "backlog item not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Echo a small marker on the body so HUD callers can distinguish
	// preview vs. live spend in their own logging without re-parsing.
	type previewEnvelope struct {
		*budget.CostEstimate
		Source string `json:"source"`
	}
	writeJSON(w, http.StatusOK, previewEnvelope{
		CostEstimate: preview,
		Source:       "estimator/v1",
	})
}
