package main

import (
	"errors"
	"net/http"

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
