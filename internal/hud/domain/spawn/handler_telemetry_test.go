package spawn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
	pkgspawn "github.com/crb2nu/loom/internal/spawn"
)

// mockTelemetrySpawner implements SpawnerOps + telemetryProvider.
type mockTelemetrySpawner struct {
	spawns    map[string]*pkgspawn.State
	telemetry map[string]*bridge.SpawnTelemetry
}

func (m *mockTelemetrySpawner) Spawn(_ context.Context, _ pkgspawn.Request) (string, error) {
	return "", nil
}

func (m *mockTelemetrySpawner) GetSpawn(id string) (*pkgspawn.State, bool) {
	s, ok := m.spawns[id]
	return s, ok
}

func (m *mockTelemetrySpawner) ListSpawns() []*pkgspawn.State { return nil }

func (m *mockTelemetrySpawner) StopSpawn(_ context.Context, _ string) error { return nil }

func (m *mockTelemetrySpawner) Projects() []string { return nil }

func (m *mockTelemetrySpawner) GetSpawnTelemetry(id string) (*bridge.SpawnTelemetry, bool) {
	t, ok := m.telemetry[id]
	return t, ok
}

// mockTelemetryDeps satisfies Deps. Its Spawner() returns a mockTelemetrySpawner
// which implements both SpawnerOps and telemetryProvider.
type mockTelemetryDeps struct {
	spawner SpawnerOps
	authed  bool
}

func (m *mockTelemetryDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // test helper
}

func (m *mockTelemetryDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	m.WriteJSON(w, status, map[string]string{"error": msg})
}

func (m *mockTelemetryDeps) RequireAdminToken(_ http.ResponseWriter, _ *http.Request) bool {
	return m.authed
}

func (m *mockTelemetryDeps) Spawner() SpawnerOps { return m.spawner }

// --- Tests ---

func TestHandleSpawnTelemetry_Success(t *testing.T) {
	spawner := &mockTelemetrySpawner{
		spawns: map[string]*pkgspawn.State{
			"spawn-1": {SpawnID: "spawn-1", AgentID: "agent-1", Status: "running"},
		},
		telemetry: map[string]*bridge.SpawnTelemetry{
			"spawn-1": {
				TurnCount:    5,
				TotalCostUSD: 0.42,
				TokenUsage: bridge.SpawnTokenUsage{
					InputTokens:  1000,
					OutputTokens: 500,
				},
				StopReason: "end_turn",
			},
		},
	}
	deps := &mockTelemetryDeps{spawner: spawner, authed: true}
	d := New(deps)

	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/agent/spawn/spawn-1/telemetry", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["spawn_id"] != "spawn-1" {
		t.Errorf("expected spawn_id=spawn-1, got %v", body["spawn_id"])
	}

	tel, ok := body["telemetry"].(map[string]any)
	if !ok {
		t.Fatalf("expected telemetry object, got %T", body["telemetry"])
	}
	if tel["turn_count"] != float64(5) {
		t.Errorf("expected turn_count=5, got %v", tel["turn_count"])
	}
	if tel["total_cost_usd"] != float64(0.42) {
		t.Errorf("expected total_cost_usd=0.42, got %v", tel["total_cost_usd"])
	}
	if tel["stop_reason"] != "end_turn" {
		t.Errorf("expected stop_reason=end_turn, got %v", tel["stop_reason"])
	}
}

func TestHandleSpawnTelemetry_SpawnNotFound(t *testing.T) {
	spawner := &mockTelemetrySpawner{
		spawns:    map[string]*pkgspawn.State{},
		telemetry: map[string]*bridge.SpawnTelemetry{},
	}
	deps := &mockTelemetryDeps{spawner: spawner, authed: true}
	d := New(deps)

	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/agent/spawn/nonexistent/telemetry", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSpawnTelemetry_NoTelemetry(t *testing.T) {
	// Spawn exists but no telemetry data (e.g., gemini agent or still building).
	spawner := &mockTelemetrySpawner{
		spawns: map[string]*pkgspawn.State{
			"spawn-2": {SpawnID: "spawn-2", AgentID: "agent-2", Status: "building"},
		},
		telemetry: map[string]*bridge.SpawnTelemetry{},
	}
	deps := &mockTelemetryDeps{spawner: spawner, authed: true}
	d := New(deps)

	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/agent/spawn/spawn-2/telemetry", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["spawn_id"] != "spawn-2" {
		t.Errorf("expected spawn_id=spawn-2, got %v", body["spawn_id"])
	}
	if body["telemetry"] != nil {
		t.Errorf("expected telemetry=nil, got %v", body["telemetry"])
	}
}

func TestHandleSpawnTelemetry_Unauthorized(t *testing.T) {
	spawner := &mockTelemetrySpawner{
		spawns:    map[string]*pkgspawn.State{},
		telemetry: map[string]*bridge.SpawnTelemetry{},
	}
	deps := &mockTelemetryDeps{spawner: spawner, authed: false}
	d := New(deps)

	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/agent/spawn/spawn-1/telemetry", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// The mockTelemetryDeps.RequireAdminToken returns false, so the handler
	// exits early. The mock does not write a status code itself, so we check
	// that the response code is the default 200 (handler returned without writing).
	// In production, RequireAdminToken writes 401 and returns false.
	// For our mock, the important thing is that no telemetry data is returned.
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body for unauthorized request, got: %s", rec.Body.String())
	}
}

func TestHandleSpawnTelemetry_NoSpawner(t *testing.T) {
	deps := &mockTelemetryDeps{spawner: nil, authed: true}
	d := New(deps)

	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/agent/spawn/spawn-1/telemetry", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleSpawnTelemetry_NoTelemetrySupport(t *testing.T) {
	// Use the basic mockSpawner from spawn_test.go which does NOT implement
	// telemetryProvider. The handler should return 501.
	spawner := &mockSpawner{
		spawns: []*pkgspawn.State{
			{SpawnID: "spawn-1", AgentID: "agent-1", Status: "running"},
		},
	}
	deps := &mockTelemetryDeps{spawner: spawner, authed: true}
	d := New(deps)

	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/agent/spawn/spawn-1/telemetry", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestTelemetryRouteRegistered(t *testing.T) {
	spawner := &mockTelemetrySpawner{
		spawns:    map[string]*pkgspawn.State{},
		telemetry: map[string]*bridge.SpawnTelemetry{},
	}
	deps := &mockTelemetryDeps{spawner: spawner, authed: true}
	d := New(deps)

	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	// Verify the route is registered (should get 404 for the spawn, not 405 for the route).
	req := httptest.NewRequest("GET", "/api/agent/spawn/test-id/telemetry", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusMethodNotAllowed {
		t.Fatal("expected telemetry route to be registered, got 405")
	}
}
