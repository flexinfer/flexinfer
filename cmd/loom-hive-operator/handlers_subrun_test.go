package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// seedParentRun inserts a backlog item + pipeline run pair so the
// recursion guard has a real parent to walk. Returns the parent's id.
func seedParentRun(t *testing.T, op *operator, backlogID, runID string, depth int, parentRunID *string) {
	t.Helper()
	ctx := context.Background()
	if err := op.store.Backlog.Put(ctx, &store.BacklogItem{
		ID: backlogID, Title: backlogID + " parent", State: store.BacklogRunning,
		Priority: store.P2, CreatedBy: "test",
	}); err != nil {
		t.Fatalf("seed backlog %s: %v", backlogID, err)
	}
	if err := op.store.Pipeline.PutRun(ctx, &store.PipelineRun{
		ID: runID, BacklogID: backlogID, Template: "hive-default",
		State: store.PipelineImplementing, StartedAt: time.Now().UTC(),
		Depth: depth, ParentRunID: parentRunID,
	}); err != nil {
		t.Fatalf("seed pipeline run %s: %v", runID, err)
	}
}

// flipRecursion enables the policy.recursion section on the running
// PolicyManager so the guard's policy gate passes. Mutates the
// in-memory pointer; safe in tests because PolicyManager isn't
// hot-reloading via fsnotify here.
func flipRecursion(op *operator, maxDepth int, share float64) {
	cur := op.policy.Current()
	cur.Recursion = hive.RecursionPolicy{
		Enabled:              true,
		MaxDepth:             maxDepth,
		SubrunMaxBudgetShare: share,
	}
}

// subrunTestAdminToken is the bearer token the recursion test cases
// authenticate as. setAdminToken is package-global state, so
// postSubrun installs it on every call and the AdminTokenRequired
// test explicitly clears it again before asserting fail-closed.
const subrunTestAdminToken = "subrun-test-admin"

// postSubrun is the table-driven HTTP helper. Installs a deterministic
// admin token, attaches the matching Bearer header, and returns the
// recorder for the caller to inspect. Restores the token to "" on
// return so the AdminTokenRequired test starts from fail-closed.
func postSubrun(t *testing.T, op *operator, parentID, body string) *httptest.ResponseRecorder {
	t.Helper()
	setAdminToken(subrunTestAdminToken)
	t.Cleanup(func() { setAdminToken("") })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/hive/pipeline/runs/"+parentID+"/subrun", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+subrunTestAdminToken)
	op.httpMux().ServeHTTP(rec, req)
	return rec
}

// TestSubrunCreate_RejectsWhenRecursionDisabled pins the V2-D6 default
// — recursion is off; the endpoint must refuse with the
// `recursion_disabled` code so callers can detect "policy says no"
// distinct from "your input was bad".
func TestSubrunCreate_RejectsWhenRecursionDisabled(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	seedParentRun(t, op, "BACK-A", "PIPE-A", 0, nil)

	rec := postSubrun(t, op, "PIPE-A", `{"backlog_id":"BACK-CHILD","template":"hive-default","estimate_usd":1.0}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Body.String(), "recursion_disabled:") {
		t.Errorf("body should be `recursion_disabled:` prefix, got %s", rec.Body.String())
	}
}

// TestSubrunCreate_HappyPath_DepthOne walks the full success flow:
// parent at depth=0, request creates a child at depth=1, response
// carries the new run id + depth, persisted row matches.
func TestSubrunCreate_HappyPath_DepthOne(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	flipRecursion(op, 2, 0.5)
	seedParentRun(t, op, "BACK-A", "PIPE-A", 0, nil)
	// pipeline_runs.backlog_id is a FK; seed the subrun's target
	// backlog so the INSERT can satisfy the constraint.
	if err := op.store.Backlog.Put(context.Background(), &store.BacklogItem{
		ID: "BACK-A-CHILD", Title: "child slice", State: store.BacklogQueued,
		Priority: store.P2, CreatedBy: "test",
	}); err != nil {
		t.Fatalf("seed child backlog: %v", err)
	}

	// validPolicy fixture pins pipeline.max_usd_per_run=1.0 → with
	// share=0.5 the subrun cap is 0.50, so estimate 0.4 fits.
	rec := postSubrun(t, op, "PIPE-A",
		`{"backlog_id":"BACK-A-CHILD","template":"hive-default","estimate_usd":0.4,"slice_spec":"refactor tests"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp subrunCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.ParentRunID != "PIPE-A" {
		t.Errorf("parent_run_id: got %q want %q", resp.ParentRunID, "PIPE-A")
	}
	if resp.Depth != 1 {
		t.Errorf("depth: got %d want 1", resp.Depth)
	}
	persisted, err := op.store.Pipeline.GetRun(context.Background(), resp.RunID)
	if err != nil {
		t.Fatalf("get persisted: %v", err)
	}
	if persisted.ParentRunID == nil || *persisted.ParentRunID != "PIPE-A" {
		t.Errorf("persisted ParentRunID: got %v want PIPE-A", persisted.ParentRunID)
	}
	if persisted.BacklogID != "BACK-A-CHILD" {
		t.Errorf("persisted BacklogID: got %q want BACK-A-CHILD", persisted.BacklogID)
	}
	if persisted.Depth != 1 {
		t.Errorf("persisted Depth: got %d want 1", persisted.Depth)
	}
	// Phase 6 slice 6.2 invariant: SubrunCreate claims the target
	// backlog (transitions to Running) so the parallel reconciler
	// main loop can't double-start it.
	claimed, err := op.store.Backlog.Get(context.Background(), "BACK-A-CHILD")
	if err != nil {
		t.Fatalf("re-fetch claimed backlog: %v", err)
	}
	if claimed.State != store.BacklogRunning {
		t.Errorf("claimed backlog state: got %q want %q", claimed.State, store.BacklogRunning)
	}
}

// TestSubrunCreate_DepthExceeded pins the spec acceptance:
// "depth=3 attempt rejected with `recursion_depth_exceeded`".
// Build a chain at depth=2 and try to recurse one more.
func TestSubrunCreate_DepthExceeded(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	flipRecursion(op, 2, 0.5) // max_depth=2 → depth-3 child rejected
	seedParentRun(t, op, "BACK-A", "PIPE-A", 0, nil)
	gp := "PIPE-A"
	seedParentRun(t, op, "BACK-B", "PIPE-B", 1, &gp)
	pp := "PIPE-B"
	seedParentRun(t, op, "BACK-C", "PIPE-C", 2, &pp)

	rec := postSubrun(t, op, "PIPE-C",
		`{"backlog_id":"BACK-D","template":"hive-default","estimate_usd":0.4}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Body.String(), "recursion_depth_exceeded:") {
		t.Errorf("body should be `recursion_depth_exceeded:` prefix, got %s", rec.Body.String())
	}
}

// TestSubrunCreate_BudgetTooLarge pins the spec acceptance:
// "over-budget subrun rejected with `budget_subrun_too_large`".
// validPolicy fixture sets pipeline.max_usd_per_run=5.0; share=0.5 →
// subrun cap = 2.50. Request 3.00 → rejected.
func TestSubrunCreate_BudgetTooLarge(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	flipRecursion(op, 2, 0.5)
	seedParentRun(t, op, "BACK-A", "PIPE-A", 0, nil)

	rec := postSubrun(t, op, "PIPE-A",
		`{"backlog_id":"BACK-CHILD","template":"hive-default","estimate_usd":3.0}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Body.String(), "budget_subrun_too_large:") {
		t.Errorf("body should be `budget_subrun_too_large:` prefix, got %s", rec.Body.String())
	}
}

// TestSubrunCreate_CycleDetected pins the cycle-detector: a subrun
// targeting a backlog id that already appears in the ancestor chain
// must be rejected even when depth + budget would otherwise pass.
func TestSubrunCreate_CycleDetected(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	flipRecursion(op, 5, 0.9) // generous so only the cycle guard can trip
	seedParentRun(t, op, "BACK-A", "PIPE-A", 0, nil)
	gp := "PIPE-A"
	seedParentRun(t, op, "BACK-B", "PIPE-B", 1, &gp)

	// Try to spawn a subrun under PIPE-B that targets BACK-A — that
	// backlog item is already in the ancestor chain. Estimate 0.5
	// stays under the 0.9 cap so only the cycle guard can trip.
	rec := postSubrun(t, op, "PIPE-B",
		`{"backlog_id":"BACK-A","template":"hive-default","estimate_usd":0.5}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Body.String(), "recursion_cycle_detected:") {
		t.Errorf("body should be `recursion_cycle_detected:` prefix, got %s", rec.Body.String())
	}
}

// TestSubrunCreate_ParentNotFound pins the 404 path for an unknown
// parent run id (so callers can distinguish "you typo'd" from "policy
// said no" from "input was bad").
func TestSubrunCreate_ParentNotFound(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	flipRecursion(op, 2, 0.5)

	rec := postSubrun(t, op, "PIPE-DOES-NOT-EXIST",
		`{"backlog_id":"BACK-X","template":"hive-default","estimate_usd":0.4}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Body.String(), "recursion_parent_not_found:") {
		t.Errorf("body should be `recursion_parent_not_found:` prefix, got %s", rec.Body.String())
	}
}

// TestSubrunCreate_AdminTokenRequired pins that the route is behind
// requireAdmin — no token + no admin role → 401/403, not 201.
func TestSubrunCreate_AdminTokenRequired(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	setAdminToken("") // explicit fail-closed
	flipRecursion(op, 2, 0.5)
	seedParentRun(t, op, "BACK-A", "PIPE-A", 0, nil)

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/hive/pipeline/runs/PIPE-A/subrun",
		strings.NewReader(`{"backlog_id":"BACK-X","template":"hive-default"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401; body=%s", rec.Code, rec.Body.String())
	}
}
