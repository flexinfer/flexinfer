package codebase

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// handleStatus returns the current codebase index snapshot from the monitor.
func (d *CodebaseDomain) handleStatus(w http.ResponseWriter, _ *http.Request) {
	snap := d.deps.CodebaseMonitor().Status()
	d.deps.WriteJSON(w, http.StatusOK, snap)
}

// handleSearch performs a semantic search across the codebase index.
func (d *CodebaseDomain) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing query parameter 'q'", nil)
		return
	}
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	results, err := d.deps.Agent().CodebaseSearch(query, limit)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "codebase search failed", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

// handleTextSearch performs a text (grep-like) search across the codebase index.
func (d *CodebaseDomain) handleTextSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing query parameter 'q'", nil)
		return
	}
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	results, err := d.deps.Agent().CodebaseTextSearch(query, limit)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "codebase text search failed", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

// handleIndex triggers a new indexing job for the given path.
func (d *CodebaseDomain) handleIndex(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Path == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "path is required", nil)
		return
	}
	job, err := d.deps.Agent().CodebaseIndexStart(body.Path)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to start indexing", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusAccepted, job)
}

// handleIndexPoll checks the status of an in-progress indexing job.
func (d *CodebaseDomain) handleIndexPoll(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("job_id")
	if jobID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing job_id", nil)
		return
	}
	job, err := d.deps.Agent().CodebaseIndexPoll(jobID)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to poll index job", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, job)
}
