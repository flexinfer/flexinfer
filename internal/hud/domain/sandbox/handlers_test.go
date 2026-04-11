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
	execResult    map[string]any
	execErr       error
	pollResult    map[string]any
	pollErr       error
	allowAdmin    bool
	lastJSON      any
	lastJSONCode  int
	lastErrMsg    string
	lastErrCode   int
	cacheSetCalls int
	broadcasts    []sandboxBroadcast
}

type sandboxBroadcast struct {
	eventType string
	payload   any
}

func newMockDeps() *mockDeps {
	return &mockDeps{cacheData: make(map[string]any), allowAdmin: true}
}

func (m *mockDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	m.lastJSON = v
	m.lastJSONCode = status
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // test helper; assertion failures catch issues
}

func (m *mockDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	m.lastErrMsg = msg
	m.lastErrCode = status
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck // test helper; assertion failures catch issues
}

func (m *mockDeps) RequireAdminToken(w http.ResponseWriter, _ *http.Request) bool {
	if m.allowAdmin {
		return true
	}
	w.WriteHeader(http.StatusUnauthorized)
	return false
}

func (m *mockDeps) BroadcastAgentEvent(eventType string, payload any) {
	m.broadcasts = append(m.broadcasts, sandboxBroadcast{eventType: eventType, payload: payload})
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

func (m *mockDeps) DoSandboxExecAsync(_, _, _, _ string) (map[string]any, error) {
	return m.execResult, m.execErr
}

func (m *mockDeps) DoSandboxExecPoll(_ string) (map[string]any, error) {
	return m.pollResult, m.pollErr
}

func (m *mockDeps) DoSandboxStatus(_ string) ([]map[string]any, error) {
	return nil, nil
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
	deps.snapshot = map[string]any{"status": "running", "backend": "k8s", "projects": []any{"loom-core"}}
	d := New(deps)

	req := httptest.NewRequest("GET", "/api/sandbox", nil)
	rec := httptest.NewRecorder()
	d.handleSandbox(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck // test helper; assertion failures catch issues
	if body["available"] != true {
		t.Errorf("expected available=true, got %v", body["available"])
	}
	if body["status"] != "running" {
		t.Errorf("expected status=running, got %v", body["status"])
	}
}

func TestHandleSandboxCapabilities_Available(t *testing.T) {
	deps := newMockDeps()
	deps.snapshot = map[string]any{"status": "running", "backend": "k8s", "projects": []any{"loom-core", "platform/gitops"}}
	d := New(deps)

	req := httptest.NewRequest("GET", "/api/sandbox/capabilities", nil)
	rec := httptest.NewRecorder()
	d.handleSandboxCapabilities(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck // test helper; assertion failures catch issues
	if body["available"] != true {
		t.Fatalf("expected available=true, got %v", body["available"])
	}
	if body["backend"] != "k8s" {
		t.Fatalf("expected backend=k8s, got %v", body["backend"])
	}
	if body["auth_required"] != true {
		t.Fatalf("expected auth_required=true, got %v", body["auth_required"])
	}
	if got, _ := body["project_count"].(float64); got != 2 {
		t.Fatalf("expected project_count=2, got %v", body["project_count"])
	}
}

func TestHandleSandboxCapabilities_Unavailable(t *testing.T) {
	deps := newMockDeps()
	d := New(deps)

	req := httptest.NewRequest("GET", "/api/sandbox/capabilities", nil)
	rec := httptest.NewRecorder()
	d.handleSandboxCapabilities(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck // test helper; assertion failures catch issues
	if body["available"] != false {
		t.Fatalf("expected available=false, got %v", body["available"])
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
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck // test helper; assertion failures catch issues
	if body["available"] != false {
		t.Errorf("expected available=false, got %v", body["available"])
	}
	if body["status"] != "offline" {
		t.Errorf("expected status=offline, got %v", body["status"])
	}
	if body["start_command"] != "loom start devbox" {
		t.Errorf("expected start_command, got %v", body["start_command"])
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
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck // test helper; assertion failures catch issues
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
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck // test helper; assertion failures catch issues
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
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck // test helper; assertion failures catch issues
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	if resp["build_id"] != "abc123" {
		t.Errorf("expected build_id=abc123, got %v", resp["build_id"])
	}
	if resp["project"] != "loom-core" {
		t.Errorf("expected project=loom-core, got %v", resp["project"])
	}
	if resp["message"] != "sandbox start requested" {
		t.Errorf("expected start message, got %v", resp["message"])
	}
	if len(deps.broadcasts) != 1 {
		t.Fatalf("expected 1 sandbox event broadcast, got %d", len(deps.broadcasts))
	}
	if deps.broadcasts[0].eventType != "hud.sandbox.event" {
		t.Fatalf("expected hud.sandbox.event broadcast, got %q", deps.broadcasts[0].eventType)
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
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck // test helper; assertion failures catch issues
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	if resp["project"] != "loom-core" {
		t.Errorf("expected project=loom-core, got %v", resp["project"])
	}
	if resp["message"] != "sandbox start requested" {
		t.Errorf("expected start message, got %v", resp["message"])
	}
}

func TestHandleSandboxStart_RequiresAdminToken(t *testing.T) {
	deps := newMockDeps()
	deps.allowAdmin = false
	d := New(deps)

	req := httptest.NewRequest("POST", "/api/sandbox/start", strings.NewReader(`{"project":"loom-core"}`))
	rec := httptest.NewRecorder()
	d.handleSandboxStart(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if len(deps.broadcasts) != 0 {
		t.Fatalf("expected no broadcasts when unauthorized, got %d", len(deps.broadcasts))
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
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck // test helper; assertion failures catch issues
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	if resp["project"] != "loom-core" {
		t.Errorf("expected project=loom-core, got %v", resp["project"])
	}
	if resp["message"] != "sandbox stop requested" {
		t.Errorf("expected stop message, got %v", resp["message"])
	}
	if len(deps.broadcasts) != 1 {
		t.Fatalf("expected 1 sandbox event broadcast, got %d", len(deps.broadcasts))
	}
	if deps.broadcasts[0].eventType != "hud.sandbox.event" {
		t.Fatalf("expected hud.sandbox.event broadcast, got %q", deps.broadcasts[0].eventType)
	}
}

func TestHandleSandboxStop_RequiresAdminToken(t *testing.T) {
	deps := newMockDeps()
	deps.allowAdmin = false
	d := New(deps)

	req := httptest.NewRequest("POST", "/api/sandbox/stop", strings.NewReader(`{"project":"loom-core"}`))
	rec := httptest.NewRecorder()
	d.handleSandboxStop(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if len(deps.broadcasts) != 0 {
		t.Fatalf("expected no broadcasts when unauthorized, got %d", len(deps.broadcasts))
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

func TestHandleSandboxExec_Success(t *testing.T) {
	deps := newMockDeps()
	deps.execResult = map[string]any{
		"exec_id": "exec-123",
		"status":  "running",
		"project": "loom-core",
		"command": "make test",
	}
	d := New(deps)

	req := httptest.NewRequest("POST", "/api/sandbox/exec", strings.NewReader(`{"project":"loom-core","command":"make test","timeout":"15m"}`))
	rec := httptest.NewRecorder()
	d.handleSandboxExec(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck // test helper; assertion failures catch issues
	if body["exec_id"] != "exec-123" {
		t.Fatalf("expected exec_id=exec-123, got %v", body["exec_id"])
	}
	if body["ok"] != true {
		t.Fatalf("expected ok=true, got %v", body["ok"])
	}
	if len(deps.broadcasts) != 1 {
		t.Fatalf("expected 1 broadcast, got %d", len(deps.broadcasts))
	}
}

func TestHandleSandboxExec_RequiresAdminToken(t *testing.T) {
	deps := newMockDeps()
	deps.allowAdmin = false
	d := New(deps)

	req := httptest.NewRequest("POST", "/api/sandbox/exec", strings.NewReader(`{"project":"loom-core","command":"make test"}`))
	rec := httptest.NewRecorder()
	d.handleSandboxExec(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleSandboxExec_Validation(t *testing.T) {
	deps := newMockDeps()
	d := New(deps)

	req := httptest.NewRequest("POST", "/api/sandbox/exec", strings.NewReader(`{"project":"loom-core"}`))
	rec := httptest.NewRecorder()
	d.handleSandboxExec(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSandboxExecPoll_Success(t *testing.T) {
	deps := newMockDeps()
	deps.pollResult = map[string]any{
		"exec_id":     "exec-123",
		"status":      "completed",
		"project":     "loom-core",
		"command":     "make test",
		"exit_code":   0,
		"stdout_tail": "ok",
	}
	d := New(deps)

	req := httptest.NewRequest("GET", "/api/sandbox/exec/exec-123", nil)
	req.SetPathValue("exec_id", "exec-123")
	rec := httptest.NewRecorder()
	d.handleSandboxExecPoll(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck // test helper; assertion failures catch issues
	if body["status"] != "completed" {
		t.Fatalf("expected completed status, got %v", body["status"])
	}
}

func TestHandleSandboxExecPoll_RequiresAdminToken(t *testing.T) {
	deps := newMockDeps()
	deps.allowAdmin = false
	d := New(deps)

	req := httptest.NewRequest("GET", "/api/sandbox/exec/exec-123", nil)
	req.SetPathValue("exec_id", "exec-123")
	rec := httptest.NewRecorder()
	d.handleSandboxExecPoll(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}
