package spawn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockAppHandlers struct{}

func (m *mockAppHandlers) HandleAgentSpawn(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentSpawnList(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentSpawnConfig(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentSpawnDetail(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentSpawnStop(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func TestSpawnDomainName(t *testing.T) {
	d := New(&mockAppHandlers{})
	if d.Name() != "spawn" {
		t.Fatalf("expected name 'spawn', got %q", d.Name())
	}
}

func TestSpawnDomainRouteRegistration(t *testing.T) {
	d := New(&mockAppHandlers{})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/agent/spawn"},
		{"GET", "/api/agent/spawns"},
		{"GET", "/api/agent/spawn/config"},
		{"GET", "/api/agent/spawn/test-id"},
		{"POST", "/api/agent/spawn/test-id/stop"},
	}

	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s: expected 200, got %d", rt.method, rt.path, rec.Code)
		}
	}
}

func TestSpawnDomainLifecycle(t *testing.T) {
	d := New(&mockAppHandlers{})
	if err := d.Start(context.TODO()); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop: unexpected error: %v", err)
	}
}
