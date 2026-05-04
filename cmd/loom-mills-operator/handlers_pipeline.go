package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/crb2nu/loom/pkg/mills/pipeline"
	"github.com/crb2nu/loom/pkg/mills/store"
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

// subrunCreateRequest is the admin POST body for the Phase 6 recursion
// entry point. Field names match the JSON the spawn-driver MCP tool
// (mills_pipeline_subrun_create, slice 6.2) will produce.
type subrunCreateRequest struct {
	BacklogID   string  `json:"backlog_id"`
	Template    string  `json:"template"`
	EstimateUSD float64 `json:"estimate_usd,omitempty"`
	SliceSpec   string  `json:"slice_spec,omitempty"`
}

// subrunCreateResponse is what we return on success. Carries the new
// run id so the caller can poll status; carries the parent depth +
// computed new depth as an audit hint.
type subrunCreateResponse struct {
	RunID       string `json:"run_id"`
	ParentRunID string `json:"parent_run_id"`
	Depth       int    `json:"depth"`
}

// handlePipelineSubrunCreate is the operator's POST entry point for
// v2 bounded recursion (Phase 6 slice 6.1).
//
// Errors map cleanly to the spec's acceptance strings:
//   - GuardDepthExceeded         → 400 + body "recursion_depth_exceeded: ..."
//   - GuardBudgetSubrunTooLarge  → 400 + body "budget_subrun_too_large: ..."
//   - GuardCycleDetected         → 400 + body "recursion_cycle_detected: ..."
//   - GuardRecursionDisabled     → 403 + body "recursion_disabled: ..."
//   - GuardParentNotFound        → 404 + body "recursion_parent_not_found: ..."
//   - any other GuardError       → 400 + body "<code>: ..."
//
// The Code prefix is stable so callers can switch on a string
// response or scrape the prefix in logs.
func (o *operator) handlePipelineSubrunCreate(w http.ResponseWriter, r *http.Request) {
	if o.subrunGuard == nil {
		http.Error(w, "subrun guard not configured", http.StatusServiceUnavailable)
		return
	}
	parentID := r.PathValue("id")
	if parentID == "" {
		http.Error(w, "missing parent run id", http.StatusBadRequest)
		return
	}

	var req subrunCreateRequest
	if r.Body != nil {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	newID, err := o.subrunGuard.SubrunCreate(r.Context(), pipeline.SubrunRequest{
		ParentRunID: parentID,
		BacklogID:   req.BacklogID,
		Template:    req.Template,
		EstimateUSD: req.EstimateUSD,
		SliceSpec:   req.SliceSpec,
	})
	if err != nil {
		var ge *pipeline.GuardError
		if errors.As(err, &ge) {
			status := http.StatusBadRequest
			switch ge.Code {
			case pipeline.GuardRecursionDisabled:
				status = http.StatusForbidden
			case pipeline.GuardParentNotFound:
				status = http.StatusNotFound
			}
			http.Error(w, ge.Error(), status)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Read back depth so the response matches what's persisted.
	persisted, perr := o.store.Pipeline.GetRun(r.Context(), newID)
	depth := 0
	if perr == nil && persisted != nil {
		depth = persisted.Depth
	}
	writeJSON(w, http.StatusCreated, subrunCreateResponse{
		RunID:       newID,
		ParentRunID: parentID,
		Depth:       depth,
	})
}
