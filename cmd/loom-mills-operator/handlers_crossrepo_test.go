package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// seedBacklogItem inserts a minimal backlog row + its required council
// parent so cross_repo_runs's FK constraint is satisfied. Idempotent
// across (council, backlog) pairs within one test.
func seedBacklogItem(t *testing.T, op *operator, backlogID string) {
	t.Helper()
	ctx := context.Background()
	council := "COUNCIL-XR-" + backlogID
	if err := op.store.Council.Put(ctx, &store.CouncilRun{
		ID:        council,
		Trigger:   store.CouncilTriggerCron,
		StartedAt: time.Now().UTC(),
		Outcome:   store.CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("seed council for %s: %v", backlogID, err)
	}
	if err := op.store.Backlog.Put(ctx, &store.BacklogItem{
		ID:           backlogID,
		Title:        "cross-repo-test " + backlogID,
		State:        store.BacklogQueued,
		Priority:     store.P2,
		CreatedBy:    "test",
		CouncilRunID: &council,
	}); err != nil {
		t.Fatalf("seed backlog %s: %v", backlogID, err)
	}
}

// seededBacklogIDs tracks which backlog ids a test has already created
// so seedCrossRepoRun can be called multiple times for the same backlog.
var seededBacklogIDs = map[string]bool{}

// seedCrossRepoRun is the canonical test helper for inserting a run
// row. Returns the inserted run for cases that want to assert against
// the seeded values. Auto-seeds the parent backlog row on first use.
func seedCrossRepoRun(t *testing.T, op *operator, id, backlogID string, state store.CrossRepoState) *store.CrossRepoRun {
	t.Helper()
	key := t.Name() + "|" + backlogID
	if !seededBacklogIDs[key] {
		seedBacklogItem(t, op, backlogID)
		seededBacklogIDs[key] = true
		t.Cleanup(func() { delete(seededBacklogIDs, key) })
	}
	run := &store.CrossRepoRun{
		ID:                id,
		BacklogItemID:     backlogID,
		State:             state,
		AtomicityStrategy: "all_or_revert",
		Repos: []store.CrossRepoRepoEntry{
			{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x-loom-core"},
			{ProjectID: 51, RepoName: "loom", Branch: "feat/x-loom-vscode"},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := op.store.CrossRepo.PutRun(context.Background(), run); err != nil {
		t.Fatalf("seed cross-repo run %s: %v", id, err)
	}
	return run
}

func TestHandleCrossRepoList_EmptyReturns200(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/cross-repo/runs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp crossRepoListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.Total != 0 {
		t.Errorf("Total = %d, want 0", resp.Total)
	}
	if len(resp.Runs) != 0 {
		t.Errorf("Runs len = %d, want 0", len(resp.Runs))
	}
	if resp.Limit == 0 {
		t.Errorf("Limit = 0, expected default")
	}
}

func TestHandleCrossRepoList_NoFilterReturnsAll(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	seedCrossRepoRun(t, op, "XR-1", "MILLS-A", store.CrossRepoOpen)
	seedCrossRepoRun(t, op, "XR-2", "MILLS-B", store.CrossRepoMerged)

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/cross-repo/runs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp crossRepoListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.Total != 2 {
		t.Errorf("Total = %d, want 2", resp.Total)
	}
	if len(resp.Runs) != 2 {
		t.Errorf("Runs len = %d, want 2", len(resp.Runs))
	}
}

func TestHandleCrossRepoList_StateFilter(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	seedCrossRepoRun(t, op, "XR-1", "MILLS-A", store.CrossRepoOpen)
	seedCrossRepoRun(t, op, "XR-2", "MILLS-B", store.CrossRepoMerged)

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/cross-repo/runs?state=open", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp crossRepoListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.Total != 1 {
		t.Errorf("Total = %d, want 1", resp.Total)
	}
	if len(resp.Runs) != 1 || resp.Runs[0].ID != "XR-1" {
		t.Errorf("Runs = %+v, want [XR-1]", resp.Runs)
	}
	if !strings.Contains(resp.Filter, "state=open") {
		t.Errorf("Filter = %q, want state=open echo", resp.Filter)
	}
}

func TestHandleCrossRepoList_MultiStateFilter(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	seedCrossRepoRun(t, op, "XR-1", "MILLS-A", store.CrossRepoOpen)
	seedCrossRepoRun(t, op, "XR-2", "MILLS-B", store.CrossRepoGatesGreen)
	seedCrossRepoRun(t, op, "XR-3", "MILLS-C", store.CrossRepoMerged)

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/cross-repo/runs?state=open,gates_green", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp crossRepoListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.Total != 2 {
		t.Errorf("Total = %d, want 2 (open+gates_green)", resp.Total)
	}
}

func TestHandleCrossRepoList_BadStateReturns400(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/cross-repo/runs?state=invalid_state", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"planning", "open", "merged", "failed"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing valid state %q: %s", want, body)
		}
	}
}

func TestHandleCrossRepoList_BacklogFilter(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	seedCrossRepoRun(t, op, "XR-1", "MILLS-A", store.CrossRepoOpen)
	seedCrossRepoRun(t, op, "XR-2", "MILLS-A", store.CrossRepoMerged)
	seedCrossRepoRun(t, op, "XR-3", "MILLS-B", store.CrossRepoOpen)

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/cross-repo/runs?backlog_id=MILLS-A", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp crossRepoListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.Total != 2 {
		t.Errorf("Total = %d, want 2 (both MILLS-A rows)", resp.Total)
	}
	for _, run := range resp.Runs {
		if run.BacklogItemID != "MILLS-A" {
			t.Errorf("got run for backlog %q, want MILLS-A only", run.BacklogItemID)
		}
	}
}

func TestHandleCrossRepoGet_Hit(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	seedCrossRepoRun(t, op, "XR-7", "MILLS-Z", store.CrossRepoOpen)

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/cross-repo/runs/XR-7", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var got crossRepoRunSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if got.ID != "XR-7" {
		t.Errorf("ID = %q, want XR-7", got.ID)
	}
	if got.State != store.CrossRepoOpen {
		t.Errorf("State = %q, want open", got.State)
	}
	if len(got.Repos) != 2 {
		t.Errorf("Repos len = %d, want 2", len(got.Repos))
	}
}

func TestHandleCrossRepoGet_Miss(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/cross-repo/runs/does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: %d, want 404", rec.Code)
	}
}

func TestHandleCrossRepoAbort_HappyPath(t *testing.T) {
	setAdminToken("secret-abc")
	defer setAdminToken("")

	op, cleanup := newTestOperator(t)
	defer cleanup()

	seedCrossRepoRun(t, op, "XR-A", "MILLS-Q", store.CrossRepoOpen)

	req := httptest.NewRequest(http.MethodPost,
		"/api/mills/cross-repo/runs/XR-A/abort", nil)
	req.Header.Set("Authorization", "Bearer secret-abc")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp crossRepoAbortResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.State != store.CrossRepoFailed {
		t.Errorf("State = %q, want failed", resp.State)
	}
	if resp.PreviousState != store.CrossRepoOpen {
		t.Errorf("PreviousState = %q, want open", resp.PreviousState)
	}
	if resp.AbortedAt.IsZero() {
		t.Errorf("AbortedAt should be populated")
	}

	// Verify the store row really moved.
	got, err := op.store.CrossRepo.GetRun(context.Background(), "XR-A")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.State != store.CrossRepoFailed {
		t.Errorf("store state = %q, want failed", got.State)
	}
}

func TestHandleCrossRepoAbort_TerminalRejected(t *testing.T) {
	setAdminToken("secret-abc")
	defer setAdminToken("")

	op, cleanup := newTestOperator(t)
	defer cleanup()

	seedCrossRepoRun(t, op, "XR-DONE", "MILLS-Q", store.CrossRepoMerged)

	req := httptest.NewRequest(http.MethodPost,
		"/api/mills/cross-repo/runs/XR-DONE/abort", nil)
	req.Header.Set("Authorization", "Bearer secret-abc")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status: %d, want 409 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "terminal state") {
		t.Errorf("body = %q, want terminal-state message", rec.Body.String())
	}
}

func TestHandleCrossRepoAbort_PlanningRejected(t *testing.T) {
	setAdminToken("secret-abc")
	defer setAdminToken("")

	op, cleanup := newTestOperator(t)
	defer cleanup()

	// "planning" is non-terminal but also non-abortable in this slice
	// (no live integrator to interrupt). 409 is the right answer.
	seedCrossRepoRun(t, op, "XR-PLAN", "MILLS-Q", store.CrossRepoPlanning)

	req := httptest.NewRequest(http.MethodPost,
		"/api/mills/cross-repo/runs/XR-PLAN/abort", nil)
	req.Header.Set("Authorization", "Bearer secret-abc")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status: %d, want 409 body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleCrossRepoAbort_UnknownReturns404(t *testing.T) {
	setAdminToken("secret-abc")
	defer setAdminToken("")

	op, cleanup := newTestOperator(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost,
		"/api/mills/cross-repo/runs/does-not-exist/abort", nil)
	req.Header.Set("Authorization", "Bearer secret-abc")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: %d, want 404", rec.Code)
	}
}

func TestHandleCrossRepoAbort_RequiresAdminToken(t *testing.T) {
	// Token unset → fail-closed.
	setAdminToken("")
	defer setAdminToken("")

	op, cleanup := newTestOperator(t)
	defer cleanup()

	seedCrossRepoRun(t, op, "XR-X", "MILLS-Q", store.CrossRepoOpen)

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/mills/cross-repo/runs/XR-X/abort", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token should be 401, got %d", rec.Code)
	}

	// Verify the store row did NOT move.
	got, err := op.store.CrossRepo.GetRun(context.Background(), "XR-X")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.State != store.CrossRepoOpen {
		t.Errorf("state changed despite 401: %q", got.State)
	}
}
