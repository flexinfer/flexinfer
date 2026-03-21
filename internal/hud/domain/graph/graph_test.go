package graph

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// mockDeps provides stub implementations for the graph Deps interface.
type mockDeps struct{}

func (m *mockDeps) WriteJSON(w http.ResponseWriter, _ int, _ any) { w.WriteHeader(http.StatusOK) }
func (m *mockDeps) WriteError(w http.ResponseWriter, status int, _ string, _ error) {
	w.WriteHeader(status)
}
func (m *mockDeps) Logger() *slog.Logger                      { return slog.Default() }
func (m *mockDeps) Agent() *bridge.AgentBridge                { return nil }
func (m *mockDeps) CacheGet(_ string) (any, bool)             { return nil, false }
func (m *mockDeps) CacheSet(_ string, _ any, _ time.Duration) {}

func TestGraphDomainName(t *testing.T) {
	d := New(&mockDeps{})
	if d.Name() != "graph" {
		t.Fatalf("expected name 'graph', got %q", d.Name())
	}
}

func TestGraphDomainRouteRegistration(t *testing.T) {
	d := New(&mockDeps{})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	// Wrap with recovery to catch nil-pointer panics from handlers calling
	// Agent() on the mock (returns nil). We only care that the route is
	// registered, not that the handler succeeds.
	safeHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { recover() }()
		mux.ServeHTTP(w, r)
	})

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/graph/stats"},
		{"GET", "/api/graph/entities"},
		{"GET", "/api/graph/entities/test-id"},
		{"POST", "/api/graph/entities"},
		{"DELETE", "/api/graph/entities/test-id"},
		{"POST", "/api/graph/relations"},
		{"DELETE", "/api/graph/relations/test-id"},
		{"GET", "/api/graph/path"},
		{"GET", "/api/stream"},
		{"GET", "/api/reasoning/chains"},
		{"GET", "/api/reasoning/chains/test-id"},
		{"POST", "/api/reasoning/chains"},
	}

	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		safeHandler.ServeHTTP(rec, req)
		// The handler will likely return 400 (missing body/params) or panic
		// on nil Agent. 404/405 means the route was not registered.
		if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: route not registered (got %d)", rt.method, rt.path, rec.Code)
		}
	}
}
