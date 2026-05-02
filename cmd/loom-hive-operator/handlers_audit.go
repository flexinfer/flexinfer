package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/hive/audit"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// auditFindingsDefaultSince is the look-back window for the list endpoint
// when the caller omits ?since. Keeps the default response useful (recent
// state) without forcing every poll to walk the full table.
const auditFindingsDefaultSince = 7 * 24 * time.Hour

// auditFindingsDefaultLimit caps each list response. Mirrors the
// eval_scores list semantics so HUD pollers see consistent pagination.
const auditFindingsDefaultLimit = 200

// auditRunRequest is the POST body shape for /api/hive/audit/run. The
// admin endpoint's only required field is subject_id; subject_kind
// defaults to council_artifact (matching the most common
// re-run-on-demand path). Pool overrides let the operator try a
// different ensemble against an existing subject without touching the
// global PoolPolicy.
type auditRunRequest struct {
	SubjectKind    store.AuditSubjectKind `json:"subject_kind,omitempty"`
	SubjectID      string                 `json:"subject_id"`
	Pool           []audit.PoolMember     `json:"pool,omitempty"`
	EscalationPool []audit.PoolMember     `json:"escalation_pool,omitempty"`
}

// auditRunResponse echoes back the audit row id + score so callers can
// poll /findings/{id}/details (slice 3.4 follow-up) or render the row
// inline.
type auditRunResponse struct {
	FindingID      int64                  `json:"finding_id"`
	SubjectKind    store.AuditSubjectKind `json:"subject_kind"`
	SubjectID      string                 `json:"subject_id"`
	SurvivalScore  float64                `json:"survival_score"`
	Severity       store.AuditSeverity    `json:"severity"`
	Escalated      bool                   `json:"escalated"`
	CostUSD        float64                `json:"cost_usd"`
	SkippedMembers int                    `json:"skipped_members,omitempty"`
}

// handleAuditFindings lists audit findings, optionally filtered by
// subject. Read-only; never gates behind the admin token (HUD polls it).
//
// Query params:
//   - subject_kind (optional): "council_artifact" or "pipeline_merge"
//   - subject_id   (optional, requires subject_kind): exact match
//   - since        (optional, RFC3339): default = now-7d
//   - limit        (optional): default 200, max 1000
//
// Returns the JSON array directly (no envelope); pre-existing handlers
// follow the same shape.
func (o *operator) handleAuditFindings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	subjectKind := store.AuditSubjectKind(strings.TrimSpace(q.Get("subject_kind")))
	subjectID := strings.TrimSpace(q.Get("subject_id"))
	if subjectID != "" && subjectKind == "" {
		http.Error(w, "subject_id requires subject_kind", http.StatusBadRequest)
		return
	}
	switch subjectKind {
	case "", store.AuditSubjectCouncilArtifact, store.AuditSubjectPipelineMerge:
	default:
		http.Error(w, `subject_kind must be "council_artifact" or "pipeline_merge"`,
			http.StatusBadRequest)
		return
	}

	limit := parseLimit(q.Get("limit"), auditFindingsDefaultLimit)
	since := time.Now().Add(-auditFindingsDefaultSince)
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "since must be RFC3339", http.StatusBadRequest)
			return
		}
		since = t
	}

	ctx := r.Context()

	// Subject-pinned query: precise + uses idx_audit_findings_lookup.
	if subjectKind != "" && subjectID != "" {
		rows, err := o.store.Audit.ListForSubject(ctx, subjectKind, subjectID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, rows)
		return
	}

	// Time-window query: uses idx_audit_findings_recent. We post-filter
	// by subject_kind in-memory because Audit.ListSince doesn't (yet)
	// take a subject_kind arg — bounded by `limit` so it's always cheap.
	rows, err := o.store.Audit.ListSince(ctx, since, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if subjectKind != "" {
		filtered := rows[:0]
		for _, f := range rows {
			if f.SubjectKind == subjectKind {
				filtered = append(filtered, f)
			}
		}
		rows = filtered
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleAuditFindingDetails returns one finding by row id. Used by the
// HUD's findings drawer + the CLI to fetch the full Findings array +
// auditor pool without paginating the list endpoint.
func (o *operator) handleAuditFindingDetails(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "id must be a positive integer", http.StatusBadRequest)
		return
	}
	// AuditDAO doesn't have a GetByID today — easiest path is to walk a
	// recent window and pick by id. The list is bounded so this is
	// cheap; v2.1 can add an indexed Get when a richer drawer needs it.
	rows, err := o.store.Audit.ListSince(r.Context(),
		time.Now().Add(-30*24*time.Hour), 1000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, f := range rows {
		if f.ID == id {
			writeJSON(w, http.StatusOK, f)
			return
		}
	}
	http.NotFound(w, r)
}

// handleAuditRun is the admin-token endpoint that runs an audit on
// demand. Used by the HUD's "re-run audit" affordance + the CLI so an
// operator can test a different pool against an existing subject
// without waiting for the next trigger.
//
// The handler does not block: it enqueues the request on the QueueWorker
// and returns the worker's ack. To run synchronously the caller can pass
// `?sync=true` (mostly used by tests). The synchronous path captures the
// dispatcher result for an inline response.
func (o *operator) handleAuditRun(w http.ResponseWriter, r *http.Request) {
	if o.auditDispatcher == nil {
		http.Error(w, "audit dispatcher not configured (FLEXINFER_PROXY_URL unset)",
			http.StatusServiceUnavailable)
		return
	}
	var body auditRunRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.SubjectKind == "" {
		body.SubjectKind = store.AuditSubjectCouncilArtifact
	}
	body.SubjectID = strings.TrimSpace(body.SubjectID)
	if body.SubjectID == "" {
		http.Error(w, "subject_id required", http.StatusBadRequest)
		return
	}

	artifact, err := o.fetchAuditArtifact(r, body.SubjectKind, body.SubjectID)
	if err != nil {
		// Subject not found / unfetchable diff — surface as 404 so the
		// CLI / HUD can render an actionable error.
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	req := audit.Request{
		SubjectKind:    body.SubjectKind,
		SubjectID:      body.SubjectID,
		Artifact:       artifact,
		Pool:           body.Pool,
		EscalationPool: body.EscalationPool,
	}
	// Sync path: useful for tests + small admin re-runs. Bypasses the
	// queue so the response carries the result inline.
	if r.URL.Query().Get("sync") == "true" {
		if len(req.Pool) == 0 && o.auditPolicy != nil {
			req.Pool = o.auditPolicy.Bulk
		}
		if len(req.EscalationPool) == 0 && o.auditPolicy != nil {
			req.EscalationPool = o.auditPolicy.Escalation
		}
		res, err := o.auditDispatcher.Run(r.Context(), &req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if res.Finding == nil {
			http.Error(w, "dispatcher produced no finding", http.StatusInternalServerError)
			return
		}
		if err := o.store.Audit.RecordFinding(r.Context(), res.Finding); err != nil {
			http.Error(w, "record finding: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, auditRunResponseFor(res))
		return
	}
	// Async path: enqueue + return 202 with a queued ack. The HUD
	// polls /findings to pick up the eventual row.
	if o.auditWorker == nil {
		http.Error(w, "audit worker not configured", http.StatusServiceUnavailable)
		return
	}
	if !o.auditWorker.Enqueue(req) {
		http.Error(w, "audit queue full; retry shortly", http.StatusTooManyRequests)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":       "enqueued",
		"subject_kind": string(req.SubjectKind),
		"subject_id":   req.SubjectID,
	})
}

// fetchAuditArtifact loads the text the rubric will score. For council
// artifacts we re-render the artifact + sidecar from disk via the
// loader the operator wired into Triggers; for pipeline merges we fall
// back to whatever the GitLab client can fetch (the production path
// loads the merge diff, but if no client is wired we surface an error).
func (o *operator) fetchAuditArtifact(r *http.Request, kind store.AuditSubjectKind, id string) (string, error) {
	if o.auditTriggers == nil {
		return "", errors.New("audit triggers not configured")
	}
	switch kind {
	case store.AuditSubjectCouncilArtifact:
		run, err := o.store.Council.Get(r.Context(), id)
		if err != nil {
			return "", fmt.Errorf("council run %s: %w", id, err)
		}
		if o.auditTriggers.LoadCouncilArtifact == nil {
			return "", errors.New("council artifact loader not wired")
		}
		body, sidecar, err := o.auditTriggers.LoadCouncilArtifact(r.Context(), run, run.Artifacts)
		if err != nil {
			return "", err
		}
		if sidecar == "" {
			return body, nil
		}
		return body + "\n\n<!-- sidecar -->\n" + sidecar, nil

	case store.AuditSubjectPipelineMerge:
		run, err := o.store.Pipeline.GetRun(r.Context(), id)
		if err != nil {
			return "", fmt.Errorf("pipeline run %s: %w", id, err)
		}
		var item *store.BacklogItem
		if run.BacklogID != "" {
			item, _ = o.store.Backlog.Get(r.Context(), run.BacklogID)
		}
		if o.auditTriggers.LoadMergedDiff == nil {
			return "", errors.New("merge diff loader not wired")
		}
		return o.auditTriggers.LoadMergedDiff(r.Context(), run, item)

	default:
		return "", fmt.Errorf("unknown subject_kind %q", kind)
	}
}

func auditRunResponseFor(res *audit.Result) auditRunResponse {
	out := auditRunResponse{
		SubjectKind:    res.Finding.SubjectKind,
		SubjectID:      res.Finding.SubjectID,
		SurvivalScore:  res.Finding.SurvivalScore,
		Severity:       res.Finding.Severity,
		Escalated:      res.Escalated,
		CostUSD:        res.Finding.CostUSD,
		SkippedMembers: res.SkippedMembers,
	}
	out.FindingID = res.Finding.ID
	return out
}
