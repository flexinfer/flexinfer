package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/crb2nu/loom/pkg/hive/store"
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
// hive without going through the GitLab issue → sync flow.
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
