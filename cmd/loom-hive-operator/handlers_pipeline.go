package main

import (
	"errors"
	"net/http"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// handlePipelineRunsList returns active runs (anything not in done /
// escalated / paused) so the HUD pipeline panel renders only what's
// in-flight by default. Slice 5.2 will add a query param to surface
// recent terminal runs.
func (o *operator) handlePipelineRunsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// "Active" is the union of every non-terminal state. Listing each
	// state separately would race a state-transition mid-call; the DAO's
	// CountActive uses the same predicate so the two endpoints agree.
	states := []store.PipelineState{
		store.PipelineQueued, store.PipelinePlanning, store.PipelineSlicing,
		store.PipelineImplementing, store.PipelineTesting, store.PipelineReviewing,
		store.PipelineMR, store.PipelineCI, store.PipelineMerging,
	}
	var all []*store.PipelineRun
	for _, s := range states {
		runs, err := o.store.Pipeline.ListByState(ctx, s)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		all = append(all, runs...)
	}
	writeJSON(w, http.StatusOK, all)
}

// handlePipelineRunGet returns a pipeline run with its stage results
// and gate outcomes nested inline. One call replaces three so HUD can
// render a detail drawer in a single request.
func (o *operator) handlePipelineRunGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	run, err := o.store.Pipeline.GetRun(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "pipeline run not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stages, _ := o.store.Pipeline.ListStages(ctx, id)
	gates, _ := o.store.Pipeline.ListGates(ctx, id)
	writeJSON(w, http.StatusOK, map[string]any{
		"run":    run,
		"stages": stages,
		"gates":  gates,
	})
}

// Mutating actions. All wired to admin-token middleware in server.go;
// the actual implementations land in slice 4.x with the pipeline runner.
func (o *operator) handlePipelineStart(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "4.x pipeline runner")
}

func (o *operator) handlePipelinePause(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "4.x pipeline runner")
}

func (o *operator) handlePipelineResume(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "4.x pipeline runner")
}

func (o *operator) handlePipelineEscalate(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "4.x pipeline runner")
}
