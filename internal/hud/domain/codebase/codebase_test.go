package codebase

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
)

// codebaseTestCaller implements bridge.Caller for domain tests.
type codebaseTestCaller struct {
	callToolFn func(name string, args map[string]any) (json.RawMessage, error)
}

func (s *codebaseTestCaller) Call(string, any) (json.RawMessage, error) { return nil, nil }
func (s *codebaseTestCaller) CallWithTimeout(string, any, time.Duration) (json.RawMessage, error) {
	return nil, nil
}
func (s *codebaseTestCaller) CallTool(name string, args map[string]any) (json.RawMessage, error) {
	if s.callToolFn != nil {
		return s.callToolFn(name, args)
	}
	return nil, fmt.Errorf("unexpected CallTool for %s", name)
}
func (s *codebaseTestCaller) CallToolWithTimeout(name string, args map[string]any, _ time.Duration) (json.RawMessage, error) {
	return s.CallTool(name, args)
}
func (s *codebaseTestCaller) CircuitOpen() bool { return false }
func (s *codebaseTestCaller) Close() error      { return nil }

// testDeps provides stub implementations for the codebase Deps interface.
type testDeps struct {
	agent   *bridge.AgentBridge
	monitor *monitor.CodebaseMonitor
}

func (m *testDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (m *testDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	http.Error(w, msg, status)
}

func (m *testDeps) Logger() *slog.Logger                      { return slog.Default() }
func (m *testDeps) Agent() *bridge.AgentBridge                { return m.agent }
func (m *testDeps) CodebaseMonitor() *monitor.CodebaseMonitor { return m.monitor }

func TestCodebaseDomainName(t *testing.T) {
	d := New(&testDeps{})
	if d.Name() != "codebase" {
		t.Fatalf("expected name 'codebase', got %q", d.Name())
	}
}

func TestCodebaseDomainRouteRegistration(t *testing.T) {
	caller := &codebaseTestCaller{}
	agent := bridge.NewAgentBridge(caller)
	mon := monitor.NewCodebaseMonitor(agent, slog.Default())
	deps := &testDeps{agent: agent, monitor: mon}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	// Wrap with recovery to handle nil-pointer issues from stub agent.
	safeHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { recover() }()
		mux.ServeHTTP(w, r)
	})

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/codebase/status"},
		{"GET", "/api/codebase/search?q=test"},
		{"GET", "/api/codebase/text-search?q=test"},
		{"POST", "/api/codebase/index"},
		{"GET", "/api/codebase/index/job-123"},
	}

	for _, rt := range routes {
		var body *strings.Reader
		if rt.method == "POST" {
			body = strings.NewReader(`{"path":"/workspace"}`)
		}
		var req *http.Request
		if body != nil {
			req = httptest.NewRequest(rt.method, rt.path, body)
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(rt.method, rt.path, nil)
		}
		rec := httptest.NewRecorder()
		safeHandler.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: route not registered (got %d)", rt.method, rt.path, rec.Code)
		}
	}
}

func TestHandleStatus(t *testing.T) {
	caller := &codebaseTestCaller{
		callToolFn: func(name string, _ map[string]any) (json.RawMessage, error) {
			return json.RawMessage(fmt.Sprintf(`{"content":[{"type":"text","text":"%s"}]}`,
				`{\"total_files\":50,\"total_symbols\":1000,\"languages\":{\"go\":50},\"last_indexed\":\"2026-03-24T10:00:00Z\",\"index_status\":\"ready\"}`)), nil
		},
	}
	agent := bridge.NewAgentBridge(caller)
	mon := monitor.NewCodebaseMonitor(agent, slog.Default())
	// Preload the monitor with data.
	if err := mon.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	deps := &testDeps{agent: agent, monitor: mon}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/codebase/status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var snap monitor.CodebaseSnapshot
	if err := json.NewDecoder(rec.Body).Decode(&snap); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if snap.TotalFiles != 50 {
		t.Fatalf("expected 50 files, got %d", snap.TotalFiles)
	}
}

func TestHandleSearch_MissingQuery(t *testing.T) {
	deps := &testDeps{}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/codebase/search", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing query, got %d", rec.Code)
	}
}

func TestHandleTextSearch_MissingQuery(t *testing.T) {
	deps := &testDeps{}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/codebase/text-search", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing query, got %d", rec.Code)
	}
}

func TestHandleIndex_MissingPath(t *testing.T) {
	deps := &testDeps{}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("POST", "/api/codebase/index", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing path, got %d", rec.Code)
	}
}

func TestHandleIndexPoll_RouteRegistered(t *testing.T) {
	deps := &testDeps{}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	// With a valid job_id in the path, the route should be registered.
	// The handler will panic since deps.Agent() is nil, but the route IS registered.
	req := httptest.NewRequest("GET", "/api/codebase/index/test-job", nil)
	rec := httptest.NewRecorder()

	func() {
		defer func() { recover() }()
		mux.ServeHTTP(rec, req)
	}()
	if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
		t.Fatalf("route not registered (got %d)", rec.Code)
	}
}
