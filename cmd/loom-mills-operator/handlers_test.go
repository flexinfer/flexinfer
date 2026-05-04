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

// ----- Read-only handlers -----

func TestHandlePolicy_ReturnsCurrent(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mills/policy", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"Version":1`, `"Pipeline"`, `"Council"`} {
		if !strings.Contains(body, want) {
			t.Errorf("policy missing %q: %s", want, body)
		}
	}
}

func TestHandleKPIs_NoSnapshotIs404(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mills/kpis?window=1d", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 with empty kpi_snapshots, got %d", rec.Code)
	}
}

func TestHandleKPIs_ReturnsSnapshot(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	if err := op.store.KPI.RecordSnapshot(context.Background(), &store.KPISnapshot{
		WindowSeconds: 86400,
		Metrics:       map[string]any{"cost_per_merged": 1.23},
	}); err != nil {
		t.Fatalf("seed kpi: %v", err)
	}

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mills/kpis?window=1d", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"cost_per_merged":1.23`) {
		t.Errorf("body missing metric: %s", rec.Body.String())
	}
}

func TestHandleKPIs_BadWindowIs400(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mills/kpis?window=42h", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad window, got %d", rec.Code)
	}
}

func TestHandleBacklog_ListAndGet(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	item := &store.BacklogItem{
		ID: "MILLS-T1", Title: "first", State: store.BacklogQueued,
		Priority: store.P2, CreatedBy: "test",
	}
	if err := op.store.Backlog.Put(context.Background(), item); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mills/backlog", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "MILLS-T1") {
		t.Errorf("list body missing item: %s", rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/mills/backlog/MILLS-T1", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("get: %d", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), `"Title":"first"`) {
		t.Errorf("get body missing title: %s", rec2.Body.String())
	}
}

func TestHandleBacklog_GetMissingIs404(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mills/backlog/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleBacklog_CreateRoundTrip(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	body := `{"ID":"MILLS-CREATE-1","Title":"smoke item","Labels":["docs"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/mills/backlog", strings.NewReader(body))
	op.handleBacklogCreate(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d body=%s", rec.Code, rec.Body.String())
	}

	got, err := op.store.Backlog.Get(context.Background(), "MILLS-CREATE-1")
	if err != nil {
		t.Fatalf("post-create get: %v", err)
	}
	if got.Title != "smoke item" {
		t.Errorf("title = %q, want smoke item", got.Title)
	}
	if got.State != store.BacklogQueued {
		t.Errorf("state = %q, want queued (default)", got.State)
	}
	if got.Priority != store.P3 {
		t.Errorf("priority = %q, want P3 (default)", got.Priority)
	}
	if got.CreatedBy != "api" {
		t.Errorf("created_by = %q, want api (default)", got.CreatedBy)
	}
}

func TestHandleBacklog_CreateRequiresIDAndTitle(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	cases := []struct{ name, body string }{
		{"empty body", `{}`},
		{"missing id", `{"Title":"only title"}`},
		{"missing title", `{"ID":"X"}`},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/mills/backlog", strings.NewReader(tc.body))
		op.handleBacklogCreate(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400", tc.name, rec.Code)
		}
	}
}

func TestHandleCouncilRuns_ListAndGet(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	if err := op.store.Council.Put(context.Background(), &store.CouncilRun{
		ID: "COUNCIL-T", Trigger: store.CouncilTriggerCron,
		StartedAt: time.Now().UTC(), Outcome: store.CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mills/council/runs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "COUNCIL-T") {
		t.Errorf("list missing run: %s", rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/mills/council/runs/COUNCIL-T", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("get: %d", rec2.Code)
	}
}

// TestHandleCouncilRunDebate covers the slice-5.3 debate transcript
// endpoint: 200 + populated array when debate ran, 200 + [] when the
// run had no debate, 404 when the run id itself is unknown.
func TestHandleCouncilRunDebate(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	ctx := context.Background()

	// Seed a council run + 3-row debate transcript matching the
	// slice 5.2 fixture's converge-on-round-1 shape.
	if err := op.store.Council.Put(ctx, &store.CouncilRun{
		ID: "COUNCIL-DEBATE", Trigger: store.CouncilTriggerIncident,
		StartedAt: time.Now().UTC(), Outcome: store.CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("seed council: %v", err)
	}
	rounds := []*store.CouncilDebateRound{
		{CouncilRunID: "COUNCIL-DEBATE", RoundIndex: 0, Role: store.DebateRoleEditorProposes, CostUSD: 0.42, Summary: "draft v0"},
		{CouncilRunID: "COUNCIL-DEBATE", RoundIndex: 1, Role: store.DebateRoleReviewerCritiques, CostUSD: 0.40, Summary: "critiques"},
		{CouncilRunID: "COUNCIL-DEBATE", RoundIndex: 1, Role: store.DebateRoleModeratorDecision, CostUSD: 0.05, Summary: "converged"},
	}
	for i, r := range rounds {
		if err := op.store.Debate.AppendRound(ctx, r); err != nil {
			t.Fatalf("seed round %d: %v", i, err)
		}
	}

	// Seed a single-pass run so the API can show 200 + [] for runs
	// that don't have debate.
	if err := op.store.Council.Put(ctx, &store.CouncilRun{
		ID: "COUNCIL-NODEBATE", Trigger: store.CouncilTriggerCron,
		StartedAt: time.Now().UTC(), Outcome: store.CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("seed nodebate: %v", err)
	}

	cases := []struct {
		name      string
		runID     string
		wantCode  int
		wantRows  int
		wantNotIn string // substring that must NOT appear in body
	}{
		{"with_debate", "COUNCIL-DEBATE", http.StatusOK, 3, ""},
		{"no_debate_returns_empty", "COUNCIL-NODEBATE", http.StatusOK, 0, "draft v0"},
		{"unknown_run_404", "COUNCIL-MISSING", http.StatusNotFound, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
				"/api/mills/council/runs/"+tc.runID+"/debate", nil))
			if rec.Code != tc.wantCode {
				t.Fatalf("code: got %d want %d body=%s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode != http.StatusOK {
				return
			}
			var got []*store.CouncilDebateRound
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(got) != tc.wantRows {
				t.Errorf("rows: got %d want %d", len(got), tc.wantRows)
			}
			if tc.wantNotIn != "" && strings.Contains(rec.Body.String(), tc.wantNotIn) {
				t.Errorf("body should not contain %q: %s", tc.wantNotIn, rec.Body.String())
			}
		})
	}
}

func TestHandlePipelineRuns_GetWithStagesAndGates(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	ctx := context.Background()

	if err := op.store.Backlog.Put(ctx, &store.BacklogItem{
		ID: "MILLS-P", Title: "p", State: store.BacklogRunning,
		Priority: store.P2, CreatedBy: "test",
	}); err != nil {
		t.Fatalf("seed item: %v", err)
	}
	if err := op.store.Pipeline.PutRun(ctx, &store.PipelineRun{
		ID: "PIPE-T1", BacklogID: "MILLS-P", Template: "mills-default-pipeline",
		State: store.PipelineImplementing, Attempts: 1, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	out := store.StageOutcomeSuccess
	end := time.Now().UTC().Add(time.Second)
	if err := op.store.Pipeline.PutStage(ctx, &store.StageResult{
		PipelineRunID: "PIPE-T1", Stage: "implement", Attempt: 1,
		StartedAt: time.Now().UTC(), EndedAt: &end, Outcome: &out,
	}); err != nil {
		t.Fatalf("seed stage: %v", err)
	}
	if err := op.store.Pipeline.PutGate(ctx, &store.GateOutcome{
		PipelineRunID: "PIPE-T1", AfterStage: "implement", GateName: "diff_size",
		Outcome: store.GateOutcomePass, JudgedBy: "go",
	}); err != nil {
		t.Fatalf("seed gate: %v", err)
	}

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mills/pipeline/runs/PIPE-T1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["run"] == nil {
		t.Errorf("run missing")
	}
	stages, ok := resp["stages"].([]any)
	if !ok || len(stages) != 1 {
		t.Errorf("stages: %v", resp["stages"])
	}
	gates, ok := resp["gates"].([]any)
	if !ok || len(gates) != 1 {
		t.Errorf("gates: %v", resp["gates"])
	}
}

func TestHandleEvalScores_EmptyOK(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mills/eval/scores", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with empty eval table, got %d", rec.Code)
	}
}

// ----- Admin-token gate -----

func TestRequireAdmin_NoTokenConfigured_Rejects(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	setAdminToken("") // explicit fail-closed default

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/mills/council/run", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with no admin token configured, got %d", rec.Code)
	}
}

func TestRequireAdmin_MissingHeader_Rejects(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	setAdminToken("secret-abc")
	defer setAdminToken("")

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/mills/council/run", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 missing header, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Errorf("expected WWW-Authenticate Bearer hint, got %q", got)
	}
}

func TestRequireAdmin_WrongToken_Rejects(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	setAdminToken("secret-abc")
	defer setAdminToken("")

	req := httptest.NewRequest(http.MethodPost, "/api/mills/council/run", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 wrong token, got %d", rec.Code)
	}
}

func TestRequireAdmin_CorrectTokenReachesHandler(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	setAdminToken("secret-abc")
	defer setAdminToken("")

	req := httptest.NewRequest(http.MethodPost, "/api/mills/council/run", nil)
	req.Header.Set("Authorization", "Bearer secret-abc")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)
	// newTestOperator doesn't wire a council runner, so the handler
	// short-circuits with 503. The point of this test is that the auth
	// gate *let the request through* to the handler — anything other
	// than 401 proves that. We assert 503 specifically so we'd notice
	// if the gate accidentally became authorisation-only-no-handler.
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 (no runner wired in tests), got %d body=%s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "council runner not configured") {
		t.Errorf("body should explain the 503: %s", rec.Body.String())
	}
}

func TestRequireAdmin_EveryMutatingEndpointIsGated(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	setAdminToken("") // fail-closed

	endpoints := []struct{ method, path string }{
		{http.MethodPost, "/api/mills/council/run"},
		{http.MethodPost, "/api/mills/council/dryrun"},
		{http.MethodPost, "/api/mills/pipeline/runs/MILLS-X/start"},
		{http.MethodPost, "/api/mills/pipeline/runs/PIPE-X/pause"},
		{http.MethodPost, "/api/mills/pipeline/runs/PIPE-X/resume"},
		{http.MethodPost, "/api/mills/pipeline/runs/PIPE-X/escalate"},
		{http.MethodPost, "/api/mills/backlog/sync"},
		{http.MethodPost, "/api/mills/eval/run-cross"},
	}
	for _, ep := range endpoints {
		rec := httptest.NewRecorder()
		op.httpMux().ServeHTTP(rec, httptest.NewRequest(ep.method, ep.path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: expected 401, got %d", ep.method, ep.path, rec.Code)
		}
	}
}

func TestSubtleEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"abc", "abc", true},
		{"abc", "abd", false},
		{"abc", "abcd", false},
		{"", "", true},
	}
	for _, c := range cases {
		if got := subtleEqual(c.a, c.b); got != c.want {
			t.Errorf("subtleEqual(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestParseLimit(t *testing.T) {
	cases := []struct {
		raw      string
		fallback int
		want     int
	}{
		{"", 50, 50},
		{"abc", 50, 50},
		{"-1", 50, 50},
		{"0", 50, 50},
		{"25", 50, 25},
		{"5000", 50, 1000},
	}
	for _, c := range cases {
		if got := parseLimit(c.raw, c.fallback); got != c.want {
			t.Errorf("parseLimit(%q, %d) = %d, want %d", c.raw, c.fallback, got, c.want)
		}
	}
}
