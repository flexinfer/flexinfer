package workflow

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockDeps struct {
	monitor *mockWorkflowMonitor
	events  []capturedEvent
}

type capturedEvent struct {
	eventType string
	payload   any
}

func newMockDeps() *mockDeps {
	return &mockDeps{monitor: &mockWorkflowMonitor{}}
}

func (m *mockDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (m *mockDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (m *mockDeps) Logger() *slog.Logger { return slog.Default() }
func (m *mockDeps) BroadcastAgentEvent(eventType string, payload any) {
	m.events = append(m.events, capturedEvent{eventType, payload})
}
func (m *mockDeps) WorkflowMonitor() WorkflowMonitorOps { return m.monitor }

type mockWorkflowMonitor struct {
	workflows    []WorkflowSummary
	detail       *WorkflowDetail
	detailErr    error
	approveErr   error
	rejectErr    error
	cancelErr    error
	refreshCount int
}

func (m *mockWorkflowMonitor) Workflows() []WorkflowSummary { return m.workflows }
func (m *mockWorkflowMonitor) Detail(_ string) (*WorkflowDetail, error) {
	return m.detail, m.detailErr
}
func (m *mockWorkflowMonitor) ApproveStep(_, _ string) error { return m.approveErr }
func (m *mockWorkflowMonitor) RejectStep(_, _ string) error  { return m.rejectErr }
func (m *mockWorkflowMonitor) CancelWorkflow(_ string) error { return m.cancelErr }
func (m *mockWorkflowMonitor) Refresh()                      { m.refreshCount++ }

func TestWorkflowDomainName(t *testing.T) {
	d := New(newMockDeps())
	if d.Name() != "workflow" {
		t.Fatalf("expected name 'workflow', got %q", d.Name())
	}
}

func TestWorkflowDomainRouteRegistration(t *testing.T) {
	deps := newMockDeps()
	deps.monitor.workflows = []WorkflowSummary{}
	deps.monitor.detail = &WorkflowDetail{Steps: []WorkflowStep{}, Events: []WorkflowEvent{}}

	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	routes := []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{"GET", "/api/workflows", "", http.StatusOK},
		{"GET", "/api/workflows/wf-1", "", http.StatusOK},
		{"POST", "/api/workflows/wf-1/approve", `{"step_id":"s1"}`, http.StatusOK},
		{"POST", "/api/workflows/wf-1/reject", `{"step_id":"s1"}`, http.StatusOK},
		{"POST", "/api/workflows/wf-1/cancel", "", http.StatusOK},
	}

	for _, rt := range routes {
		var req *http.Request
		if rt.body != "" {
			req = httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
		} else {
			req = httptest.NewRequest(rt.method, rt.path, nil)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != rt.want {
			t.Errorf("%s %s: expected %d, got %d (body: %s)", rt.method, rt.path, rt.want, rec.Code, rec.Body.String())
		}
	}
}

func TestHandleWorkflowList(t *testing.T) {
	deps := newMockDeps()
	deps.monitor.workflows = []WorkflowSummary{
		{ID: "wf-1", Name: "deploy", Status: "running", CurrentStep: "build", CreatedAt: "2025-01-01T00:00:00Z", Progress: 0.5},
		{ID: "wf-2", Name: "rollback", Status: "failed", Error: "timeout"},
	}
	d := New(deps)

	rec := httptest.NewRecorder()
	d.handleWorkflowList(rec, httptest.NewRequest("GET", "/api/workflows", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	workflows, ok := result["workflows"].([]any)
	if !ok || len(workflows) != 2 {
		t.Fatalf("expected 2 workflows, got %v", result["workflows"])
	}
	wf2 := workflows[1].(map[string]any)
	if wf2["error"] != "timeout" {
		t.Errorf("expected error 'timeout', got %v", wf2["error"])
	}
}

func TestHandleWorkflowDetail_Error(t *testing.T) {
	deps := newMockDeps()
	deps.monitor.detailErr = errors.New("not found")
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/workflows/{id}", d.handleWorkflowDetail)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/workflows/wf-99", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

func TestHandleWorkflowApprove_MissingStepID(t *testing.T) {
	d := New(newMockDeps())

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workflows/{id}/approve", d.handleWorkflowApprove)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/workflows/wf-1/approve", strings.NewReader(`{}`)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleWorkflowCancel_Error(t *testing.T) {
	deps := newMockDeps()
	deps.monitor.cancelErr = errors.New("cannot cancel")
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/workflows/{id}/cancel", d.handleWorkflowCancel)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/workflows/wf-1/cancel", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}
