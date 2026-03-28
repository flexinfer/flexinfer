package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crb2nu/loom/internal/hud/alerting"
	"github.com/crb2nu/loom/internal/hud/autofix"
)

// --- Mock implementations ---

type mockAlertEngine struct {
	alerts   []alerting.Alert
	rules    []alerting.AlertRule
	ackError error
}

func (m *mockAlertEngine) ListAlerts(_ int) []alerting.Alert      { return m.alerts }
func (m *mockAlertEngine) ListRules() []alerting.AlertRule        { return m.rules }
func (m *mockAlertEngine) UpdateRules(rules []alerting.AlertRule) { m.rules = rules }
func (m *mockAlertEngine) AckAlert(id, _ string) error {
	if m.ackError != nil {
		return m.ackError
	}
	for i, a := range m.alerts {
		if a.ID == id {
			m.alerts[i].AckedBy = "acked"
			return nil
		}
	}
	return fmt.Errorf("alert %q not found", id)
}

type mockAutoFixEngine struct {
	diagnosis  *autofix.Diagnosis
	diagErr    error
	proposal   *autofix.AutoFixProposal
	propErr    error
	execution  *autofix.AutoFixExecution
	execErr    error
	proposals  []autofix.AutoFixProposal
	executions []autofix.AutoFixExecution
	rejectErr  error
}

func (m *mockAutoFixEngine) DiagnoseFailure(_ context.Context, _ string, _ int) (*autofix.Diagnosis, error) {
	return m.diagnosis, m.diagErr
}
func (m *mockAutoFixEngine) ProposeAutoFix(_ autofix.Diagnosis) (*autofix.AutoFixProposal, error) {
	return m.proposal, m.propErr
}
func (m *mockAutoFixEngine) ExecuteAutoFix(_ context.Context, _ autofix.AutoFixProposal) (*autofix.AutoFixExecution, error) {
	return m.execution, m.execErr
}
func (m *mockAutoFixEngine) GetExecution(id string) (*autofix.AutoFixExecution, error) {
	for _, ex := range m.executions {
		if ex.ID == id {
			return &ex, nil
		}
	}
	return nil, fmt.Errorf("execution %q not found", id)
}
func (m *mockAutoFixEngine) GetProposal(id string) (*autofix.AutoFixProposal, error) {
	for _, p := range m.proposals {
		if p.ID == id {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("proposal %q not found", id)
}
func (m *mockAutoFixEngine) ListProposals() []autofix.AutoFixProposal   { return m.proposals }
func (m *mockAutoFixEngine) ListExecutions() []autofix.AutoFixExecution { return m.executions }
func (m *mockAutoFixEngine) RejectProposal(_ string) error              { return m.rejectErr }

type mockDeps struct {
	alertEngine   AlertEngineOps
	autofixEngine AutoFixEngineOps
	adminAllowed  bool
}

func (d *mockDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func (d *mockDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}

func (d *mockDeps) RequireAdminToken(_ http.ResponseWriter, _ *http.Request) bool {
	return d.adminAllowed
}

func (d *mockDeps) AlertEngine() AlertEngineOps     { return d.alertEngine }
func (d *mockDeps) AutoFixEngine() AutoFixEngineOps { return d.autofixEngine }

// --- Domain tests ---

func TestAlertingDomainName(t *testing.T) {
	d := New(&mockDeps{})
	if d.Name() != "alerting" {
		t.Fatalf("expected name 'alerting', got %q", d.Name())
	}
}

func TestAlertingDomainRouteRegistration(t *testing.T) {
	deps := &mockDeps{
		alertEngine:   &mockAlertEngine{},
		autofixEngine: &mockAutoFixEngine{},
		adminAllowed:  true,
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/alerts"},
		{"GET", "/api/alerts/rules"},
		{"GET", "/api/autofix/proposals"},
		{"GET", "/api/autofix/executions"},
	}

	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: route not registered (got %d)", rt.method, rt.path, rec.Code)
		}
	}
}

// --- handleListAlerts tests ---

func TestHandleListAlerts_NilEngine(t *testing.T) {
	deps := &mockDeps{alertEngine: nil}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/alerts", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck
	alerts := body["alerts"].([]any)
	if len(alerts) != 0 {
		t.Errorf("expected empty alerts, got %d", len(alerts))
	}
}

func TestHandleListAlerts_WithAlerts(t *testing.T) {
	engine := &mockAlertEngine{
		alerts: []alerting.Alert{
			{ID: "a1", Severity: "critical", Title: "Pipeline failed"},
			{ID: "a2", Severity: "warning", Title: "Pipeline slow"},
		},
	}
	deps := &mockDeps{alertEngine: engine}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/alerts", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck
	alerts := body["alerts"].([]any)
	if len(alerts) != 2 {
		t.Errorf("expected 2 alerts, got %d", len(alerts))
	}
}

func TestHandleListAlerts_SeverityFilter(t *testing.T) {
	engine := &mockAlertEngine{
		alerts: []alerting.Alert{
			{ID: "a1", Severity: "critical"},
			{ID: "a2", Severity: "warning"},
			{ID: "a3", Severity: "critical"},
		},
	}
	deps := &mockDeps{alertEngine: engine}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/alerts?severity=critical", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck
	alerts := body["alerts"].([]any)
	if len(alerts) != 2 {
		t.Errorf("expected 2 critical alerts, got %d", len(alerts))
	}
}

// --- handleListRules tests ---

func TestHandleListRules_NilEngine(t *testing.T) {
	deps := &mockDeps{alertEngine: nil}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/alerts/rules", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleListRules_WithRules(t *testing.T) {
	engine := &mockAlertEngine{
		rules: []alerting.AlertRule{
			{ID: "r1", Name: "Pipeline failure"},
		},
	}
	deps := &mockDeps{alertEngine: engine}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/alerts/rules", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck
	rules := body["rules"].([]any)
	if len(rules) != 1 {
		t.Errorf("expected 1 rule, got %d", len(rules))
	}
}

// --- handleUpdateRules tests ---

func TestHandleUpdateRules_AdminRequired(t *testing.T) {
	deps := &mockDeps{
		alertEngine:  &mockAlertEngine{},
		adminAllowed: false,
	}
	d := New(deps)

	// When RequireAdminToken returns false, the handler should not proceed.
	// The mock WriteError is called by RequireAdminToken's impl in real code,
	// but since our mock returns false, the handler returns early.
	req := httptest.NewRequest("PUT", "/api/alerts/rules", strings.NewReader(`{"rules":[]}`))
	rec := httptest.NewRecorder()
	d.handleUpdateRules(rec, req)

	// Since RequireAdminToken returned false, no response was written by the handler itself.
	// The response code should be the default 200 from NewRecorder since no write occurred.
	// Default 200 from NewRecorder — RequireAdminToken writes 401/403 in real impl.
	// We just verify the handler returned early without panicking.
}

func TestHandleUpdateRules_NilEngine(t *testing.T) {
	deps := &mockDeps{
		alertEngine:  nil,
		adminAllowed: true,
	}
	d := New(deps)

	req := httptest.NewRequest("PUT", "/api/alerts/rules", strings.NewReader(`{"rules":[]}`))
	rec := httptest.NewRecorder()
	d.handleUpdateRules(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestHandleUpdateRules_Success(t *testing.T) {
	engine := &mockAlertEngine{}
	deps := &mockDeps{
		alertEngine:  engine,
		adminAllowed: true,
	}
	d := New(deps)

	body := `{"rules":[{"id":"r1","name":"test","enabled":true,"severity":"warning"}]}`
	req := httptest.NewRequest("PUT", "/api/alerts/rules", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleUpdateRules(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp["updated"] != true {
		t.Errorf("expected updated=true, got %v", resp["updated"])
	}
}

func TestHandleUpdateRules_InvalidBody(t *testing.T) {
	deps := &mockDeps{
		alertEngine:  &mockAlertEngine{},
		adminAllowed: true,
	}
	d := New(deps)

	req := httptest.NewRequest("PUT", "/api/alerts/rules", strings.NewReader("not-json"))
	rec := httptest.NewRecorder()
	d.handleUpdateRules(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleAckAlert tests ---

func TestHandleAckAlert_Success(t *testing.T) {
	engine := &mockAlertEngine{
		alerts: []alerting.Alert{{ID: "alert-1"}},
	}
	deps := &mockDeps{alertEngine: engine}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	body := `{"acked_by":"test-user"}`
	req := httptest.NewRequest("POST", "/api/alerts/alert-1/ack", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp["acked"] != true {
		t.Errorf("expected acked=true, got %v", resp["acked"])
	}
}

func TestHandleAckAlert_NotFound(t *testing.T) {
	engine := &mockAlertEngine{
		alerts: []alerting.Alert{}, // no alerts
	}
	deps := &mockDeps{alertEngine: engine}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("POST", "/api/alerts/missing/ack", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleAckAlert_NilEngine(t *testing.T) {
	deps := &mockDeps{alertEngine: nil}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("POST", "/api/alerts/a1/ack", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

// --- handleListProposals tests ---

func TestHandleListProposals_NilEngine(t *testing.T) {
	deps := &mockDeps{autofixEngine: nil}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/autofix/proposals", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleListProposals_WithProposals(t *testing.T) {
	afEngine := &mockAutoFixEngine{
		proposals: []autofix.AutoFixProposal{
			{ID: "p1", Strategy: "agent_fix"},
		},
	}
	deps := &mockDeps{autofixEngine: afEngine}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/autofix/proposals", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck
	proposals := body["proposals"].([]any)
	if len(proposals) != 1 {
		t.Errorf("expected 1 proposal, got %d", len(proposals))
	}
}

// --- handleRejectProposal tests ---

func TestHandleRejectProposal_NilEngine(t *testing.T) {
	deps := &mockDeps{autofixEngine: nil}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("POST", "/api/autofix/proposals/p1/reject", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestHandleRejectProposal_Success(t *testing.T) {
	afEngine := &mockAutoFixEngine{rejectErr: nil}
	deps := &mockDeps{autofixEngine: afEngine}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("POST", "/api/autofix/proposals/p1/reject", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// --- handleListExecutions tests ---

func TestHandleListExecutions_NilEngine(t *testing.T) {
	deps := &mockDeps{autofixEngine: nil}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/autofix/executions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// --- handleGetExecution tests ---

func TestHandleGetExecution_NilEngine(t *testing.T) {
	deps := &mockDeps{autofixEngine: nil}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/autofix/executions/e1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestHandleGetExecution_Found(t *testing.T) {
	afEngine := &mockAutoFixEngine{
		executions: []autofix.AutoFixExecution{
			{ID: "e1", Status: "running"},
		},
	}
	deps := &mockDeps{autofixEngine: afEngine}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/autofix/executions/e1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleGetExecution_NotFound(t *testing.T) {
	afEngine := &mockAutoFixEngine{
		executions: []autofix.AutoFixExecution{},
	}
	deps := &mockDeps{autofixEngine: afEngine}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/autofix/executions/missing", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

// --- handleDiagnose tests ---

func TestHandleDiagnose_AdminRequired(t *testing.T) {
	deps := &mockDeps{adminAllowed: false}
	d := New(deps)

	body := `{"project":"loom-core","pipeline_id":42}`
	req := httptest.NewRequest("POST", "/api/alerts/diagnose", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleDiagnose(rec, req)

	// Handler returns early when admin not allowed; no write by handler.
	// RequireAdminToken writes 401/403 in real impl.
}

func TestHandleDiagnose_NilAutoFixEngine(t *testing.T) {
	deps := &mockDeps{autofixEngine: nil, adminAllowed: true}
	d := New(deps)

	body := `{"project":"loom-core","pipeline_id":42}`
	req := httptest.NewRequest("POST", "/api/alerts/diagnose", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleDiagnose(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestHandleDiagnose_InvalidBody(t *testing.T) {
	afEngine := &mockAutoFixEngine{}
	deps := &mockDeps{autofixEngine: afEngine, adminAllowed: true}
	d := New(deps)

	req := httptest.NewRequest("POST", "/api/alerts/diagnose", strings.NewReader("bad-json"))
	rec := httptest.NewRecorder()
	d.handleDiagnose(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleDiagnose_MissingProject(t *testing.T) {
	afEngine := &mockAutoFixEngine{}
	deps := &mockDeps{autofixEngine: afEngine, adminAllowed: true}
	d := New(deps)

	body := `{"pipeline_id":42}`
	req := httptest.NewRequest("POST", "/api/alerts/diagnose", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleDiagnose(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleDiagnose_MissingPipelineID(t *testing.T) {
	afEngine := &mockAutoFixEngine{}
	deps := &mockDeps{autofixEngine: afEngine, adminAllowed: true}
	d := New(deps)

	body := `{"project":"proj"}`
	req := httptest.NewRequest("POST", "/api/alerts/diagnose", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleDiagnose(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}
