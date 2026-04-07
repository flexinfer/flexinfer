package spawn

import (
	"context"
	"encoding/json"
	"errors"
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
	// controlCalls records every SendControlMessage invocation for
	// assertions. Slice 8c tests populate this to verify the REST handler
	// forwarded the command with the expected type + text.
	controlCalls []recordedControlCall
	// controlErr is returned from SendControlMessage when non-nil, letting
	// tests exercise each sentinel-error → HTTP status mapping path.
	controlErr error
}

type recordedControlCall struct {
	spawnID string
	cmd     pkgspawn.ControlCommand
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

func (m *mockSpawner) SendControlMessage(_ context.Context, spawnID string, cmd pkgspawn.ControlCommand) error {
	m.controlCalls = append(m.controlCalls, recordedControlCall{spawnID: spawnID, cmd: cmd})
	return m.controlErr
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

// controlRouterFixture wires a mockSpawner into a fresh ServeMux so control
// message / interrupt tests get clean state per case.
func controlRouterFixture(t *testing.T, spawner *mockSpawner) *http.ServeMux {
	t.Helper()
	d := New(&mockDeps{spawner: spawner})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)
	return mux
}

// TestHandleAgentSpawnMessage_Success verifies the happy path: valid body,
// 202 response, and the spawner sees a ControlCommandMessage with the
// forwarded text.
func TestHandleAgentSpawnMessage_Success(t *testing.T) {
	spawner := &mockSpawner{}
	mux := controlRouterFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/agent/spawn/spawn-1/message",
		strings.NewReader(`{"text":"follow up turn"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if len(spawner.controlCalls) != 1 {
		t.Fatalf("expected 1 SendControlMessage call, got %d", len(spawner.controlCalls))
	}
	got := spawner.controlCalls[0]
	if got.spawnID != "spawn-1" {
		t.Errorf("spawnID = %q, want spawn-1", got.spawnID)
	}
	if got.cmd.Type != pkgspawn.ControlCommandMessage {
		t.Errorf("cmd.Type = %q, want %q", got.cmd.Type, pkgspawn.ControlCommandMessage)
	}
	if got.cmd.Text != "follow up turn" {
		t.Errorf("cmd.Text = %q, want follow up turn", got.cmd.Text)
	}
}

// TestHandleAgentSpawnMessage_InvalidBody confirms the handler rejects
// non-JSON bodies with 400 before invoking the spawner.
func TestHandleAgentSpawnMessage_InvalidBody(t *testing.T) {
	spawner := &mockSpawner{}
	mux := controlRouterFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/agent/spawn/spawn-1/message",
		strings.NewReader(`not json`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if len(spawner.controlCalls) != 0 {
		t.Errorf("expected 0 SendControlMessage calls, got %d", len(spawner.controlCalls))
	}
}

// TestHandleAgentSpawnInterrupt_Success confirms the interrupt handler
// forwards a ControlCommandInterrupt without a payload.
func TestHandleAgentSpawnInterrupt_Success(t *testing.T) {
	spawner := &mockSpawner{}
	mux := controlRouterFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/agent/spawn/spawn-2/interrupt", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if len(spawner.controlCalls) != 1 {
		t.Fatalf("expected 1 SendControlMessage call, got %d", len(spawner.controlCalls))
	}
	got := spawner.controlCalls[0]
	if got.cmd.Type != pkgspawn.ControlCommandInterrupt {
		t.Errorf("cmd.Type = %q, want %q", got.cmd.Type, pkgspawn.ControlCommandInterrupt)
	}
	if got.cmd.Text != "" {
		t.Errorf("cmd.Text = %q, want empty", got.cmd.Text)
	}
}

// TestHandleAgentSpawnControl_ErrorMapping table-tests the sentinel →
// HTTP status mapping so future regressions are caught early.
func TestHandleAgentSpawnControl_ErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
	}{
		{"not found → 404", pkgspawn.ErrSpawnNotFound, http.StatusNotFound},
		{"not running → 409", pkgspawn.ErrSpawnNotRunning, http.StatusConflict},
		{"not multi-turn → 400", pkgspawn.ErrSpawnNotMultiTurn, http.StatusBadRequest},
		{"invalid command → 400", pkgspawn.ErrInvalidControlCommand, http.StatusBadRequest},
		{"other → 500", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spawner := &mockSpawner{controlErr: tc.err}
			mux := controlRouterFixture(t, spawner)

			req := httptest.NewRequest("POST", "/api/agent/spawn/spawn-err/interrupt", nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("%s: got %d, want %d", tc.name, rec.Code, tc.wantCode)
			}
		})
	}
}
