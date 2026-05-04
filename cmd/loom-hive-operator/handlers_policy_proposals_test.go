package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// proposalTestAdminToken is the bearer token used by the apply/reject
// admin tests. Installed/cleared inside each test via setAdminToken so
// the auth-required test starts from a known fail-closed default.
const proposalTestAdminToken = "policy-proposal-test-admin"

// seedProposal inserts one proposal with the given state and returns the
// generated id. The DAO Create() path drives default timestamp wiring,
// which keeps this helper pleasingly small.
func seedProposal(t *testing.T, op *operator, kind store.PolicyProposalKind, target string, state store.PolicyProposalState) int64 {
	t.Helper()
	p := &store.PolicyProposal{
		Kind:      kind,
		Target:    target,
		Diff:      "delta: example",
		Rationale: "test fixture",
		State:     state,
	}
	if err := op.store.PolicyProposals.Create(context.Background(), p); err != nil {
		t.Fatalf("seed proposal: %v", err)
	}
	return p.ID
}

// TestPolicyProposalsList_FiltersToPendingByDefault verifies the default
// (no ?state=) filter returns pending rows only. Seeds three proposals
// in distinct states; expects exactly the pending one back.
func TestPolicyProposalsList_FiltersToPendingByDefault(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	pendingID := seedProposal(t, op, store.PolicyProposalRelax, "council.editor.judge", store.PolicyProposalPending)
	seedProposal(t, op, store.PolicyProposalTighten, "pipeline.diff_size", store.PolicyProposalAppliedHuman)
	seedProposal(t, op, store.PolicyProposalRotateEnsemble, "audit.pool", store.PolicyProposalRejected)

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hive/policy/proposals", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}

	var got []*store.PolicyProposal
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("rows: got %d want 1; body=%s", len(got), rec.Body.String())
	}
	if got[0].ID != pendingID {
		t.Errorf("id: got %d want %d", got[0].ID, pendingID)
	}
	if got[0].State != store.PolicyProposalPending {
		t.Errorf("state: got %q want pending", got[0].State)
	}
}

// TestPolicyProposalApply_Admin walks the happy path for applying a
// pending proposal. Asserts status, persisted state, and that the
// revert_deadline lands inside a sane 23h..25h window from now.
func TestPolicyProposalApply_Admin(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	setAdminToken(proposalTestAdminToken)
	t.Cleanup(func() { setAdminToken("") })

	id := seedProposal(t, op, store.PolicyProposalRelax, "council.editor.judge", store.PolicyProposalPending)

	before := time.Now().UTC()
	req := httptest.NewRequest(http.MethodPost,
		"/api/hive/policy/proposals/"+itoa(id)+"/apply", nil)
	req.Header.Set("Authorization", "Bearer "+proposalTestAdminToken)
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got store.PolicyProposal
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.State != store.PolicyProposalAppliedHuman {
		t.Errorf("state: got %q want applied_human", got.State)
	}
	if got.RevertDeadline == nil {
		t.Fatalf("revert_deadline: got nil")
	}
	low := before.Add(23 * time.Hour)
	high := before.Add(25 * time.Hour)
	if got.RevertDeadline.Before(low) || got.RevertDeadline.After(high) {
		t.Errorf("revert_deadline %v not in [%v, %v]", got.RevertDeadline, low, high)
	}
}

// TestPolicyProposalApply_RequiresAdmin proves the admin gate fails
// closed when no Authorization header is attached. Token is cleared
// from any prior test via t.Cleanup chains.
func TestPolicyProposalApply_RequiresAdmin(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	setAdminToken(proposalTestAdminToken)
	t.Cleanup(func() { setAdminToken("") })

	id := seedProposal(t, op, store.PolicyProposalRelax, "council.editor.judge", store.PolicyProposalPending)

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/hive/policy/proposals/"+itoa(id)+"/apply", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPolicyProposalApply_404Unknown verifies an unknown id with a
// valid admin token returns 404 (DAO's UPDATE-where-pending affects
// zero rows → ErrNotFound → 404).
func TestPolicyProposalApply_404Unknown(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	setAdminToken(proposalTestAdminToken)
	t.Cleanup(func() { setAdminToken("") })

	req := httptest.NewRequest(http.MethodPost,
		"/api/hive/policy/proposals/9999/apply", nil)
	req.Header.Set("Authorization", "Bearer "+proposalTestAdminToken)
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// itoa is a tiny strconv-free helper to avoid introducing an import
// for one base-10 conversion.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
