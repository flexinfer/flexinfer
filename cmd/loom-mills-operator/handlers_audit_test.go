package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/audit"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// fakeAuditReviewer is a deterministic Reviewer that returns canned
// rubric-shaped JSON. Keeps the handler tests free of FlexInfer HTTP.
type fakeAuditReviewer struct {
	score float64
	cost  float64
}

func (f *fakeAuditReviewer) Backend() string { return "flexinfer" }

func (f *fakeAuditReviewer) Review(_ context.Context, _, _ string, _ float64) (string, float64, error) {
	score := f.score
	if score == 0 {
		score = 0.92
	}
	cost := f.cost
	if cost == 0 {
		cost = 0.04
	}
	body := []byte(`{"survival_score":` + strconvFloat(score) +
		`,"severity":"info","findings":[]}`)
	return string(body), cost, nil
}

func strconvFloat(f float64) string {
	// Tiny FormatFloat shim so this test file stays free of strconv;
	// the helper file already exposes the intStr equivalent.
	if f == 0 {
		return "0"
	}
	// Render with 2-decimal precision; sufficient for rubric scores in
	// [0, 1].
	intPart := int(f * 100)
	whole := intPart / 100
	frac := intPart % 100
	wholeStr := intStr(whole)
	fracStr := intStr(frac)
	if len(fracStr) == 1 {
		fracStr = "0" + fracStr
	}
	return wholeStr + "." + fracStr
}

// auditOpFixture builds an operator fully wired for audit handler tests:
// a real store, a real audit dispatcher backed by the fake reviewer, a
// QueueWorker (left idle so sync-mode tests use the dispatcher
// directly), and triggers with stub loaders. Cleanup tears everything
// down deterministically.
func auditOpFixture(t *testing.T) (*operator, func()) {
	t.Helper()
	op, baseCleanup := newTestOperator(t)

	// Wire a fake-backed dispatcher + worker + triggers.
	rev := &fakeAuditReviewer{}
	d := audit.New(map[string]audit.Reviewer{rev.Backend(): rev}, audit.MustLoadRubric())
	policy := &audit.PoolPolicy{
		Bulk: []audit.PoolMember{{Backend: "flexinfer", Model: "llama-4-70b"}},
	}
	w := audit.NewQueueWorker(d, op.store.Audit, *policy, audit.QueueOptions{
		Capacity:      8,
		PerJobTimeout: 5 * time.Second,
	})
	tr := &audit.Triggers{
		Worker: w,
		LoadCouncilArtifact: func(_ context.Context, run *store.CouncilRun, _ []store.ArtifactRef) (string, string, error) {
			return "## " + run.ID, `{"slice":"A"}`, nil
		},
		LoadMergedDiff: func(_ context.Context, run *store.PipelineRun, _ *store.BacklogItem) (string, error) {
			return "diff for " + run.ID, nil
		},
	}
	op.withAudit(d, w, tr, policy)

	cleanup := func() {
		w.Stop()
		baseCleanup()
	}
	return op, cleanup
}

func TestHandleAuditFindings_EmptyReturns200(t *testing.T) {
	op, cleanup := auditOpFixture(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mills/audit/findings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" && body != "null" {
		t.Errorf("empty list: got %q want []|null", body)
	}
}

func TestHandleAuditFindings_FilterBySubject(t *testing.T) {
	op, cleanup := auditOpFixture(t)
	defer cleanup()

	ctx := context.Background()
	if err := op.store.Audit.RecordFinding(ctx, &store.AuditFinding{
		SubjectKind: store.AuditSubjectCouncilArtifact, SubjectID: "C-1",
		Severity: store.AuditSeverityInfo, RubricID: audit.RubricID,
		SurvivalScore: 0.9,
		Findings:      []map[string]any{},
		AuditorPool:   []map[string]any{},
	}); err != nil {
		t.Fatalf("seed council finding: %v", err)
	}
	if err := op.store.Audit.RecordFinding(ctx, &store.AuditFinding{
		SubjectKind: store.AuditSubjectPipelineMerge, SubjectID: "P-1",
		Severity: store.AuditSeverityCritical, RubricID: audit.RubricID,
		SurvivalScore: 0.30,
		Findings:      []map[string]any{},
		AuditorPool:   []map[string]any{},
	}); err != nil {
		t.Fatalf("seed pipeline finding: %v", err)
	}

	// Filter by subject_kind only.
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/audit/findings?subject_kind=council_artifact", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"C-1"`) {
		t.Errorf("expected C-1 in council-only filter: %s", body)
	}
	if strings.Contains(body, `"P-1"`) {
		t.Errorf("pipeline row leaked into council filter: %s", body)
	}

	// Pinned subject_kind + subject_id → indexed lookup.
	rec = httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/audit/findings?subject_kind=pipeline_merge&subject_id=P-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"P-1"`) {
		t.Errorf("expected P-1 in subject-pinned response: %s", rec.Body.String())
	}
}

func TestHandleAuditFindings_RejectsSubjectIDWithoutKind(t *testing.T) {
	op, cleanup := auditOpFixture(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/audit/findings?subject_id=C-1", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when subject_id without subject_kind, got %d", rec.Code)
	}
}

func TestHandleAuditFindings_RejectsBadSubjectKind(t *testing.T) {
	op, cleanup := auditOpFixture(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/audit/findings?subject_kind=made_up", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown subject_kind, got %d", rec.Code)
	}
}

func TestHandleAuditFindingDetails_HappyAnd404(t *testing.T) {
	op, cleanup := auditOpFixture(t)
	defer cleanup()

	finding := &store.AuditFinding{
		SubjectKind: store.AuditSubjectCouncilArtifact, SubjectID: "C-DET",
		Severity: store.AuditSeverityInfo, RubricID: audit.RubricID,
		SurvivalScore: 0.95,
		Findings:      []map[string]any{},
		AuditorPool:   []map[string]any{},
	}
	if err := op.store.Audit.RecordFinding(context.Background(), finding); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/audit/findings/"+intStr(int(finding.ID)), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"C-DET"`) {
		t.Errorf("expected subject id in response: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/audit/findings/9999999", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown id should be 404, got %d", rec.Code)
	}
}

func TestHandleAuditFindingDetails_RejectsBadID(t *testing.T) {
	op, cleanup := auditOpFixture(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/mills/audit/findings/abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-numeric id should be 400, got %d", rec.Code)
	}
}

func TestHandleAuditRun_RequiresAdminToken(t *testing.T) {
	setAdminToken("secret-abc")
	defer setAdminToken("")

	op, cleanup := auditOpFixture(t)
	defer cleanup()

	body := bytes.NewBufferString(`{"subject_kind":"council_artifact","subject_id":"COUNCIL-X"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/mills/audit/run", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing admin token should be 401, got %d", rec.Code)
	}
}

func TestHandleAuditRun_503WhenDispatcherUnconfigured(t *testing.T) {
	setAdminToken("secret-abc")
	defer setAdminToken("")

	// Operator without audit subsystem attached.
	op, cleanup := newTestOperator(t)
	defer cleanup()

	body := bytes.NewBufferString(`{"subject_kind":"council_artifact","subject_id":"COUNCIL-X"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/mills/audit/run", body)
	req.Header.Set("Authorization", "Bearer secret-abc")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unconfigured audit should be 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleAuditRun_SyncHappyPath(t *testing.T) {
	setAdminToken("secret-abc")
	defer setAdminToken("")

	op, cleanup := auditOpFixture(t)
	defer cleanup()

	// Seed a council run so fetchAuditArtifact can find it.
	if err := op.store.Council.Put(context.Background(), &store.CouncilRun{
		ID: "COUNCIL-RUN", Trigger: store.CouncilTriggerCron,
		StartedAt: time.Now().UTC(), Outcome: store.CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("seed council: %v", err)
	}

	body := bytes.NewBufferString(`{"subject_kind":"council_artifact","subject_id":"COUNCIL-RUN"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/mills/audit/run?sync=true", body)
	req.Header.Set("Authorization", "Bearer secret-abc")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp auditRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.SubjectID != "COUNCIL-RUN" {
		t.Errorf("subject_id round-trip: got %q", resp.SubjectID)
	}
	if resp.SurvivalScore != 0.92 {
		t.Errorf("survival from fake reviewer: got %v want 0.92", resp.SurvivalScore)
	}
	// Verify the row also persisted.
	rows, _ := op.store.Audit.ListForSubject(context.Background(),
		store.AuditSubjectCouncilArtifact, "COUNCIL-RUN")
	if len(rows) != 1 {
		t.Errorf("expected 1 persisted finding, got %d", len(rows))
	}
}

func TestHandleAuditRun_404ForUnknownSubject(t *testing.T) {
	setAdminToken("secret-abc")
	defer setAdminToken("")

	op, cleanup := auditOpFixture(t)
	defer cleanup()

	body := bytes.NewBufferString(`{"subject_kind":"council_artifact","subject_id":"DOES-NOT-EXIST"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/mills/audit/run", body)
	req.Header.Set("Authorization", "Bearer secret-abc")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown council run, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleAuditRun_AsyncReturns202(t *testing.T) {
	setAdminToken("secret-abc")
	defer setAdminToken("")

	op, cleanup := auditOpFixture(t)
	defer cleanup()

	// Seed a council run so fetchAuditArtifact succeeds.
	if err := op.store.Council.Put(context.Background(), &store.CouncilRun{
		ID: "COUNCIL-ASYNC", Trigger: store.CouncilTriggerCron,
		StartedAt: time.Now().UTC(), Outcome: store.CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body := bytes.NewBufferString(`{"subject_kind":"council_artifact","subject_id":"COUNCIL-ASYNC"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/mills/audit/run", body)
	req.Header.Set("Authorization", "Bearer secret-abc")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("async path should return 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"enqueued"`) {
		t.Errorf("response should report enqueued status: %s", rec.Body.String())
	}
}
