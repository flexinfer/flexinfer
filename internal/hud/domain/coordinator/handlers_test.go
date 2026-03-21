package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	hudcoord "github.com/crb2nu/loom/internal/hud/coordinator"
)

// --- test doubles ---

type stubCoordOps struct {
	status          hudcoord.CoordinatorStatus
	summarizeResult *hudcoord.SessionSummaryResult
	summarizeErr    error
	compressResult  *hudcoord.CompactionResult
	compressErr     error
	planResult      *hudcoord.WorkflowPlan
	planErr         error
	registerID      string
	registerErr     error
}

func (s *stubCoordOps) Status() hudcoord.CoordinatorStatus { return s.status }

func (s *stubCoordOps) SummarizeSession(_ context.Context, _ string) (*hudcoord.SessionSummaryResult, error) {
	return s.summarizeResult, s.summarizeErr
}

func (s *stubCoordOps) RunCompression(_ context.Context) (*hudcoord.CompactionResult, error) {
	return s.compressResult, s.compressErr
}

func (s *stubCoordOps) PlanWorkflow(_ context.Context, _, _ string) (*hudcoord.WorkflowPlan, error) {
	return s.planResult, s.planErr
}

func (s *stubCoordOps) RegisterPlan(_ context.Context, _ *hudcoord.WorkflowPlan, _ string) (string, error) {
	return s.registerID, s.registerErr
}

type stubMetricsOps struct {
	handler http.Handler
}

func (s *stubMetricsOps) Handler() http.Handler { return s.handler }

type fakeDeps struct {
	coord          CoordinatorOps
	metrics        MetricsOps
	lastJSON       any
	lastJSONStatus int
	lastErrMsg     string
	lastErrStatus  int
	broadcastType  string
	broadcastData  any
}

func (f *fakeDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	f.lastJSON = v
	f.lastJSONStatus = status
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	f.lastErrMsg = msg
	f.lastErrStatus = status
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (f *fakeDeps) Logger() *slog.Logger {
	return slog.Default()
}

func (f *fakeDeps) BroadcastAgentEvent(eventType string, payload any) {
	f.broadcastType = eventType
	f.broadcastData = payload
}

func (f *fakeDeps) Coordinator() CoordinatorOps    { return f.coord }
func (f *fakeDeps) CoordinatorMetrics() MetricsOps { return f.metrics }

// --- tests ---

func TestHandleCoordinatorStatus_Disabled(t *testing.T) {
	deps := &fakeDeps{coord: nil}
	d := New(deps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/coordinator/status", nil)
	d.handleCoordinatorStatus(w, r)

	if deps.lastJSONStatus != http.StatusOK {
		t.Fatalf("want 200, got %d", deps.lastJSONStatus)
	}
	m, ok := deps.lastJSON.(map[string]any)
	if !ok {
		t.Fatal("expected map response")
	}
	if m["enabled"] != false {
		t.Fatalf("want enabled=false, got %v", m["enabled"])
	}
}

func TestHandleCoordinatorStatus_Enabled(t *testing.T) {
	stub := &stubCoordOps{
		status: hudcoord.CoordinatorStatus{Enabled: true, Healthy: true, Model: "test-model"},
	}
	deps := &fakeDeps{coord: stub}
	d := New(deps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/coordinator/status", nil)
	d.handleCoordinatorStatus(w, r)

	if deps.lastJSONStatus != http.StatusOK {
		t.Fatalf("want 200, got %d", deps.lastJSONStatus)
	}
}

func TestHandleCoordinatorSummarize_Disabled(t *testing.T) {
	deps := &fakeDeps{coord: nil}
	d := New(deps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/coordinator/summarize/sess-1", nil)
	d.handleCoordinatorSummarize(w, r)

	if deps.lastErrStatus != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", deps.lastErrStatus)
	}
}

func TestHandleCoordinatorSummarize_MissingSessionID(t *testing.T) {
	stub := &stubCoordOps{}
	deps := &fakeDeps{coord: stub}
	d := New(deps)

	w := httptest.NewRecorder()
	// No path value for session_id (empty).
	r := httptest.NewRequest("POST", "/api/coordinator/summarize/", nil)
	d.handleCoordinatorSummarize(w, r)

	if deps.lastErrStatus != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", deps.lastErrStatus)
	}
}

func TestHandleCoordinatorSummarize_Success(t *testing.T) {
	stub := &stubCoordOps{
		summarizeResult: &hudcoord.SessionSummaryResult{
			SessionID: "sess-1",
			Summary:   "test summary",
		},
	}
	deps := &fakeDeps{coord: stub}
	d := New(deps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/coordinator/summarize/sess-1", nil)
	r.SetPathValue("session_id", "sess-1")
	d.handleCoordinatorSummarize(w, r)

	if deps.lastJSONStatus != http.StatusOK {
		t.Fatalf("want 200, got %d", deps.lastJSONStatus)
	}
}

func TestHandleCoordinatorSummarize_ErrUnavailable(t *testing.T) {
	stub := &stubCoordOps{
		summarizeErr: hudcoord.ErrUnavailable,
	}
	deps := &fakeDeps{coord: stub}
	d := New(deps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/coordinator/summarize/sess-1", nil)
	r.SetPathValue("session_id", "sess-1")
	d.handleCoordinatorSummarize(w, r)

	if deps.lastErrStatus != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", deps.lastErrStatus)
	}
}

func TestHandleCoordinatorSummarize_OtherError(t *testing.T) {
	stub := &stubCoordOps{
		summarizeErr: fmt.Errorf("some backend error"),
	}
	deps := &fakeDeps{coord: stub}
	d := New(deps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/coordinator/summarize/sess-1", nil)
	r.SetPathValue("session_id", "sess-1")
	d.handleCoordinatorSummarize(w, r)

	if deps.lastErrStatus != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", deps.lastErrStatus)
	}
}

func TestHandleCoordinatorCompress_Disabled(t *testing.T) {
	deps := &fakeDeps{coord: nil}
	d := New(deps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/coordinator/compress", nil)
	d.handleCoordinatorCompress(w, r)

	if deps.lastErrStatus != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", deps.lastErrStatus)
	}
}

func TestHandleCoordinatorCompress_NilResult(t *testing.T) {
	stub := &stubCoordOps{compressResult: nil}
	deps := &fakeDeps{coord: stub}
	d := New(deps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/coordinator/compress", nil)
	d.handleCoordinatorCompress(w, r)

	if deps.lastJSONStatus != http.StatusOK {
		t.Fatalf("want 200, got %d", deps.lastJSONStatus)
	}
	m, ok := deps.lastJSON.(map[string]string)
	if !ok {
		t.Fatal("expected map[string]string response")
	}
	if m["status"] != "nothing_to_compress" {
		t.Fatalf("want nothing_to_compress, got %s", m["status"])
	}
}

func TestHandleCoordinatorCompress_Success(t *testing.T) {
	stub := &stubCoordOps{
		compressResult: &hudcoord.CompactionResult{Tier: "hot", CompressedCount: 5},
	}
	deps := &fakeDeps{coord: stub}
	d := New(deps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/coordinator/compress", nil)
	d.handleCoordinatorCompress(w, r)

	if deps.lastJSONStatus != http.StatusOK {
		t.Fatalf("want 200, got %d", deps.lastJSONStatus)
	}
}

func TestHandleCoordinatorPlan_Disabled(t *testing.T) {
	deps := &fakeDeps{coord: nil}
	d := New(deps)

	body := `{"goal":"build a thing"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/coordinator/plan", bytes.NewBufferString(body))
	d.handleCoordinatorPlan(w, r)

	if deps.lastErrStatus != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", deps.lastErrStatus)
	}
}

func TestHandleCoordinatorPlan_EmptyGoal(t *testing.T) {
	stub := &stubCoordOps{}
	deps := &fakeDeps{coord: stub}
	d := New(deps)

	body := `{"goal":""}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/coordinator/plan", bytes.NewBufferString(body))
	d.handleCoordinatorPlan(w, r)

	if deps.lastErrStatus != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", deps.lastErrStatus)
	}
}

func TestHandleCoordinatorPlan_InvalidJSON(t *testing.T) {
	stub := &stubCoordOps{}
	deps := &fakeDeps{coord: stub}
	d := New(deps)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/coordinator/plan", bytes.NewBufferString("{bad"))
	d.handleCoordinatorPlan(w, r)

	if deps.lastErrStatus != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", deps.lastErrStatus)
	}
}

func TestHandleCoordinatorPlan_Success(t *testing.T) {
	plan := &hudcoord.WorkflowPlan{
		Name: "test-workflow",
		Steps: []hudcoord.WorkflowPlanStep{
			{ID: "s1", Name: "Step 1", Type: "tool"},
		},
	}
	stub := &stubCoordOps{planResult: plan}
	deps := &fakeDeps{coord: stub}
	d := New(deps)

	body := `{"goal":"build a thing"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/coordinator/plan", bytes.NewBufferString(body))
	d.handleCoordinatorPlan(w, r)

	if deps.lastJSONStatus != http.StatusOK {
		t.Fatalf("want 200, got %d", deps.lastJSONStatus)
	}
	if deps.broadcastType != "coordinator.plan.complete" {
		t.Fatalf("want broadcast coordinator.plan.complete, got %s", deps.broadcastType)
	}
}

func TestHandleCoordinatorPlan_WithRegister(t *testing.T) {
	plan := &hudcoord.WorkflowPlan{
		Name: "test-workflow",
		Steps: []hudcoord.WorkflowPlanStep{
			{ID: "s1", Name: "Step 1", Type: "tool"},
		},
	}
	stub := &stubCoordOps{planResult: plan, registerID: "def-123"}
	deps := &fakeDeps{coord: stub}
	d := New(deps)

	body := `{"goal":"build a thing","register":true}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/coordinator/plan", bytes.NewBufferString(body))
	d.handleCoordinatorPlan(w, r)

	if deps.lastJSONStatus != http.StatusOK {
		t.Fatalf("want 200, got %d", deps.lastJSONStatus)
	}
	resp, ok := deps.lastJSON.(map[string]any)
	if !ok {
		t.Fatal("expected map response")
	}
	if resp["definition_id"] != "def-123" {
		t.Fatalf("want definition_id=def-123, got %v", resp["definition_id"])
	}
}

func TestHandleCoordinatorPlan_RegisterError(t *testing.T) {
	plan := &hudcoord.WorkflowPlan{
		Name: "test-workflow",
		Steps: []hudcoord.WorkflowPlanStep{
			{ID: "s1", Name: "Step 1", Type: "tool"},
		},
	}
	stub := &stubCoordOps{planResult: plan, registerErr: fmt.Errorf("register failed")}
	deps := &fakeDeps{coord: stub}
	d := New(deps)

	body := `{"goal":"build a thing","register":true}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/coordinator/plan", bytes.NewBufferString(body))
	d.handleCoordinatorPlan(w, r)

	if deps.lastJSONStatus != http.StatusOK {
		t.Fatalf("want 200 (plan succeeded), got %d", deps.lastJSONStatus)
	}
	resp, ok := deps.lastJSON.(map[string]any)
	if !ok {
		t.Fatal("expected map response")
	}
	if resp["register_error"] != "register failed" {
		t.Fatalf("want register_error, got %v", resp["register_error"])
	}
}

func TestCoordinatorErrStatus(t *testing.T) {
	if s := coordinatorErrStatus(hudcoord.ErrUnavailable); s != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", s)
	}
	if s := coordinatorErrStatus(fmt.Errorf("other")); s != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", s)
	}
}

func TestRegisterRoutes(t *testing.T) {
	metricsHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	deps := &fakeDeps{
		coord: &stubCoordOps{
			planResult: &hudcoord.WorkflowPlan{Name: "route-test", Steps: []hudcoord.WorkflowPlanStep{{ID: "s1"}}},
		},
		metrics: &stubMetricsOps{handler: metricsHandler},
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(h http.HandlerFunc) http.HandlerFunc { return h }
	d.RegisterRoutes(mux, mw)

	// Verify routes are registered by sending requests.
	tests := []struct {
		method string
		path   string
	}{
		{"GET", "/api/coordinator/status"},
		{"POST", "/api/coordinator/summarize/sess-1"},
		{"POST", "/api/coordinator/compress"},
		{"POST", "/api/coordinator/plan"},
		{"GET", "/api/coordinator/metrics"},
	}
	for _, tt := range tests {
		var body *bytes.Buffer
		if tt.method == "POST" && tt.path == "/api/coordinator/plan" {
			body = bytes.NewBufferString(`{"goal":"test"}`)
		} else {
			body = &bytes.Buffer{}
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tt.method, tt.path, body)
		mux.ServeHTTP(w, r)
		if w.Code == http.StatusNotFound {
			t.Errorf("route %s %s not registered", tt.method, tt.path)
		}
	}
}

func TestRegisterRoutes_NilMetrics(t *testing.T) {
	deps := &fakeDeps{coord: &stubCoordOps{}, metrics: nil}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(h http.HandlerFunc) http.HandlerFunc { return h }
	d.RegisterRoutes(mux, mw)

	// Metrics route should not be registered.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/coordinator/metrics", nil)
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404 for unregistered metrics, got %d", w.Code)
	}
}

func TestName(t *testing.T) {
	d := New(&fakeDeps{})
	if d.Name() != "coordinator" {
		t.Fatalf("want coordinator, got %s", d.Name())
	}
}
