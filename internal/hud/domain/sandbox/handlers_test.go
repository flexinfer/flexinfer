package sandbox

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mockDeps provides a controllable Deps implementation for testing.
type mockDeps struct {
	snapshot      map[string]any
	cacheData     map[string]any
	startResult   map[string]any
	startErr      error
	stopErr       error
	lastJSON      any
	lastJSONCode  int
	lastErrMsg    string
	lastErrCode   int
	cacheSetCalls int
}

func newMockDeps() *mockDeps {
	return &mockDeps{cacheData: make(map[string]any)}
}

func (m *mockDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	m.lastJSON = v
	m.lastJSONCode = status
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func (m *mockDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	m.lastErrMsg = msg
	m.lastErrCode = status
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}

func (m *mockDeps) SandboxSnapshot() map[string]any {
	return m.snapshot
}

func (m *mockDeps) CacheGet(key string) (any, bool) {
	v, ok := m.cacheData[key]
	return v, ok
}

func (m *mockDeps) CacheSet(key string, value any, _ time.Duration) {
	m.cacheData[key] = value
	m.cacheSetCalls++
}

func (m *mockDeps) DoSandboxStart(_, _ string) (map[string]any, error) {
	return m.startResult, m.startErr
}

func (m *mockDeps) DoSandboxStop(_ string) error {
	return m.stopErr
}

func TestSandboxDomainName(t *testing.T) {
	d := New(newMockDeps())
	if d.Name() != "sandbox" {
		t.Fatalf("expected name 'sandbox', got %q", d.Name())
	}
}

func TestSandboxDomainRouteRegistration(t *testing.T) {
	deps := newMockDeps()
	deps.snapshot = map[string]any{"status": "running"}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	// GET /api/sandbox should respond.
	req := httptest.NewRequest("GET", "/api/sandbox", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/sandbox: expected 200, got %d", rec.Code)
	}
}

func TestHandleSandbox_Available(t *testing.T) {
	deps := newMockDeps()
	deps.snapshot = map[string]any{"status": "running"}
	d := New(deps)

	req := httptest.NewRequest("GET", "/api/sandbox", nil)
	rec := httptest.NewRecorder()
	d.handleSandbox(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck
	if body["available"] != true {
		t.Errorf("expected available=true, got %v", body["available"])
	}
	if body["status"] != "running" {
		t.Errorf("expected status=running, got %v", body["status"])
	}
}

func TestHandleSandbox_Unavailable(t *testing.T) {
	deps := newMockDeps()
	deps.snapshot = nil
	d := New(deps)

	req := httptest.NewRequest("GET", "/api/sandbox", nil)
	rec := httptest.NewRecorder()
	d.handleSandbox(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck
	if body["available"] != false {
		t.Errorf("expected available=false, got %v", body["available"])
	}
}

func TestHandleSandboxPolicy_Cached(t *testing.T) {
	deps := newMockDeps()
	deps.cacheData["sandbox_policy"] = map[string]any{"configured": true, "allow": []string{"net"}}
	d := New(deps)

	req := httptest.NewRequest("GET", "/api/sandbox/policy", nil)
	rec := httptest.NewRecorder()
	d.handleSandboxPolicy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck
	if body["configured"] != true {
		t.Errorf("expected configured=true, got %v", body["configured"])
	}
}

func TestHandleSandboxPolicy_NoPolicyFile(t *testing.T) {
	deps := newMockDeps()
	d := New(deps)

	req := httptest.NewRequest("GET", "/api/sandbox/policy", nil)
	rec := httptest.NewRecorder()
	d.handleSandboxPolicy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// Should have cached the empty result.
	if deps.cacheSetCalls != 1 {
		t.Errorf("expected 1 cache set call, got %d", deps.cacheSetCalls)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck
	if body["configured"] != false {
		t.Errorf("expected configured=false, got %v", body["configured"])
	}
}

func TestHandleSandboxStart_Success(t *testing.T) {
	deps := newMockDeps()
	deps.startResult = map[string]any{"build_id": "abc123"}
	d := New(deps)

	body := `{"project":"loom-core","agent_id":"test-agent"}`
	req := httptest.NewRequest("POST", "/api/sandbox/start", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleSandboxStart(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	if resp["build_id"] != "abc123" {
		t.Errorf("expected build_id=abc123, got %v", resp["build_id"])
	}
}

func TestHandleSandboxStart_NilResult(t *testing.T) {
	deps := newMockDeps()
	deps.startResult = nil
	d := New(deps)

	body := `{"project":"loom-core"}`
	req := httptest.NewRequest("POST", "/api/sandbox/start", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleSandboxStart(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
}

func TestHandleSandboxStart_MissingProject(t *testing.T) {
	deps := newMockDeps()
	d := New(deps)

	body := `{"agent_id":"test-agent"}`
	req := httptest.NewRequest("POST", "/api/sandbox/start", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleSandboxStart(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSandboxStart_InvalidJSON(t *testing.T) {
	deps := newMockDeps()
	d := New(deps)

	req := httptest.NewRequest("POST", "/api/sandbox/start", strings.NewReader("not-json"))
	rec := httptest.NewRecorder()
	d.handleSandboxStart(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSandboxStart_Error(t *testing.T) {
	deps := newMockDeps()
	deps.startErr = http.ErrAbortHandler // any error
	d := New(deps)

	body := `{"project":"loom-core"}`
	req := httptest.NewRequest("POST", "/api/sandbox/start", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleSandboxStart(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

func TestHandleSandboxStop_Success(t *testing.T) {
	deps := newMockDeps()
	d := New(deps)

	body := `{"project":"loom-core"}`
	req := httptest.NewRequest("POST", "/api/sandbox/stop", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleSandboxStop(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	if resp["project"] != "loom-core" {
		t.Errorf("expected project=loom-core, got %v", resp["project"])
	}
}

func TestHandleSandboxStop_MissingProject(t *testing.T) {
	deps := newMockDeps()
	d := New(deps)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/sandbox/stop", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleSandboxStop(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSandboxStop_Error(t *testing.T) {
	deps := newMockDeps()
	deps.stopErr = http.ErrAbortHandler
	d := New(deps)

	body := `{"project":"loom-core"}`
	req := httptest.NewRequest("POST", "/api/sandbox/stop", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleSandboxStop(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

func TestHandleSandboxStop_InvalidJSON(t *testing.T) {
	deps := newMockDeps()
	d := New(deps)

	req := httptest.NewRequest("POST", "/api/sandbox/stop", strings.NewReader("{bad"))
	rec := httptest.NewRecorder()
	d.handleSandboxStop(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
