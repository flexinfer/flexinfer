package spawn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
	pkgspawn "github.com/crb2nu/loom/internal/spawn"
)

// mockSpawner satisfies SpawnerOps for testing.
type mockSpawner struct {
	spawns   []*pkgspawn.State
	projects []string
}

func (m *mockSpawner) Spawn(_ context.Context, req pkgspawn.Request) (string, error) {
	id := "spawn-001"
	m.spawns = append(m.spawns, &pkgspawn.State{
		SpawnID: id,
		AgentID: "agent-" + id,
		Status:  "pending",
		Request: req,
	})
	return id, nil
}

func (m *mockSpawner) GetSpawn(spawnID string) (*pkgspawn.State, bool) {
	for _, s := range m.spawns {
		if s.SpawnID == spawnID {
			return s, true
		}
	}
	return nil, false
}

func (m *mockSpawner) ListSpawns() []*pkgspawn.State { return m.spawns }

func (m *mockSpawner) StopSpawn(_ context.Context, _ string) error { return nil }

func (m *mockSpawner) Projects() []string { return m.projects }

func (m *mockSpawner) GetSpawnTelemetry(_ string) (*bridge.SpawnTelemetry, bool) {
	return nil, false
}

// mockDeps satisfies Deps for testing.
type mockDeps struct {
	spawner SpawnerOps
}

func (m *mockDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (m *mockDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	m.WriteJSON(w, status, map[string]string{"error": msg})
}

func (m *mockDeps) RequireAdminToken(_ http.ResponseWriter, _ *http.Request) bool {
	return true // always authorized in tests
}

func (m *mockDeps) Spawner() SpawnerOps { return m.spawner }

func TestSpawnDomainName(t *testing.T) {
	d := New(&mockDeps{})
	if d.Name() != "spawn" {
		t.Fatalf("expected name 'spawn', got %q", d.Name())
	}
}

func TestSpawnDomainRouteRegistration(t *testing.T) {
	spawner := &mockSpawner{
		spawns: []*pkgspawn.State{
			{SpawnID: "test-id", AgentID: "agent-1", Status: "running"},
		},
		projects: []string{"loom-core"},
	}
	d := New(&mockDeps{spawner: spawner})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	routes := []struct {
		method string
		path   string
		body   string
		expect int
	}{
		{"POST", "/api/agent/spawn", `{"project":"loom-core","agent_type":"claude-code","task_description":"test"}`, http.StatusAccepted},
		{"GET", "/api/agent/spawns", "", http.StatusOK},
		{"GET", "/api/agent/spawn/config", "", http.StatusOK},
		{"GET", "/api/agent/spawn/test-id", "", http.StatusOK},
		{"POST", "/api/agent/spawn/test-id/stop", "", http.StatusOK},
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
		if rec.Code != rt.expect {
			t.Errorf("%s %s: expected %d, got %d (body: %s)", rt.method, rt.path, rt.expect, rec.Code, rec.Body.String())
		}
	}
}

func TestSpawnDomainNilSpawner(t *testing.T) {
	d := New(&mockDeps{spawner: nil})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	// POST /api/agent/spawn with nil spawner should return 503.
	req := httptest.NewRequest("POST", "/api/agent/spawn", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("POST /api/agent/spawn (nil spawner): expected 503, got %d", rec.Code)
	}

	// GET /api/agent/spawns with nil spawner returns empty list.
	req = httptest.NewRequest("GET", "/api/agent/spawns", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/agent/spawns (nil spawner): expected 200, got %d", rec.Code)
	}

	// GET /api/agent/spawn/config with nil spawner should still work.
	req = httptest.NewRequest("GET", "/api/agent/spawn/config", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/agent/spawn/config (nil spawner): expected 200, got %d", rec.Code)
	}
}

func TestSpawnDomainSpawnDetail404(t *testing.T) {
	spawner := &mockSpawner{spawns: []*pkgspawn.State{}}
	d := New(&mockDeps{spawner: spawner})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/agent/spawn/nonexistent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/agent/spawn/nonexistent: expected 404, got %d", rec.Code)
	}
}
