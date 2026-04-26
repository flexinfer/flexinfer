package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/crb2nu/loom/pkg/hive/runner"
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

// councilRunRequest is the admin POST body shared by /council/run and
// /council/dryrun. Both fields optional; absent reason logs as the empty
// string into council_runs.notes.
type councilRunRequest struct {
	Trigger string `json:"trigger,omitempty"` // "manual" | "cron" | "roadmap" | "incident"
	Reason  string `json:"reason,omitempty"`
}

// handleCouncilRun triggers an ad-hoc council pass via the wired runner.
// Returns the run summary (id, score, partial flag, created backlog ids).
// Returns 503 if the operator is running without a runner (e.g. testing).
func (o *operator) handleCouncilRun(w http.ResponseWriter, r *http.Request) {
	o.runCouncil(w, r, false)
}

// handleCouncilDryrun mirrors handleCouncilRun but writes artifacts to a
// scratch dir under .loom/dryrun/<runID>/, skips canonical-store writes,
// and skips backlog mutation. Useful for previewing what a run *would*
// produce.
func (o *operator) handleCouncilDryrun(w http.ResponseWriter, r *http.Request) {
	o.runCouncil(w, r, true)
}

// runCouncil is the shared implementation of handleCouncilRun /
// handleCouncilDryrun. The dryrun bool flips behaviour through to the
// runner.
func (o *operator) runCouncil(w http.ResponseWriter, r *http.Request, dryrun bool) {
	if o.runner == nil {
		http.Error(w, "council runner not configured on this operator instance",
			http.StatusServiceUnavailable)
		return
	}

	// Body is optional; empty body parses to a zero councilRunRequest.
	// json.Decode returns io.EOF on empty input — treat that as no body.
	var req councilRunRequest
	if r.Body != nil {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	// Council runs can take minutes when the editor is a real spawn.
	// Cap at 10 minutes per request; the operator's scheduler runs them
	// without an HTTP-side cap.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	res, err := o.runner.Run(ctx, runner.RunInput{
		Trigger: store.CouncilTrigger(req.Trigger),
		Dryrun:  dryrun,
		Reason:  req.Reason,
	})
	if err != nil {
		http.Error(w, "council run failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, councilRunResponse(res))
}

// councilRunResponse projects a runner.RunResult into the JSON shape the
// CLI + HUD consume. Avoids leaking the full Brief / EditorOutput which
// can be many KB.
func councilRunResponse(res *runner.RunResult) map[string]any {
	resp := map[string]any{
		"run_id":          res.RunID,
		"dryrun":          res.Dryrun,
		"started_at":      res.StartedAt,
		"ended_at":        res.EndedAt,
		"cost_usd_approx": res.CostUSDApprox,
	}
	if res.Verdict != nil {
		resp["score"] = res.Verdict.Score
		resp["partial"] = res.Verdict.Partial
		resp["judged_by"] = res.Verdict.JudgedBy
	}
	if res.Mutation != nil {
		resp["backlog_proposed"] = res.Mutation.TotalProposed
		resp["backlog_created"] = res.Mutation.CreatedIDs()
		resp["backlog_truncated"] = res.Mutation.Truncated
		resp["backlog_skipped"] = res.Mutation.Skipped
		resp["backlog_skip_reason"] = res.Mutation.SkipReason
	}
	if res.Write != nil {
		resp["artifacts"] = res.Write.ArtifactRefs
		resp["sidecar_path"] = res.Write.SidecarPath
	}
	if res.Brief != nil {
		resp["brief_sources"] = res.Brief.SourceCounts
	}
	return resp
}
