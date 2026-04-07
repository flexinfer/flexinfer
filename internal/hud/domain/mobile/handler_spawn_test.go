package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/spawn"
)

// mobileMockSpawner satisfies mobile.SpawnerOps and records every
// SendControlMessage call so tests can assert on the forwarded payload.
type mobileMockSpawner struct {
	controlCalls []recordedMobileControlCall
	controlErr   error
}

type recordedMobileControlCall struct {
	spawnID string
	cmd     spawn.ControlCommand
}

func (m *mobileMockSpawner) Spawn(_ context.Context, _ spawn.Request) (string, error) {
	return "spawn-mock", nil
}
func (m *mobileMockSpawner) GetSpawn(_ string) (*spawn.State, bool) { return nil, false }
func (m *mobileMockSpawner) ListSpawns() []*spawn.State             { return nil }
func (m *mobileMockSpawner) StopSpawn(_ context.Context, _ string) error {
	return nil
}
func (m *mobileMockSpawner) Projects() []string { return nil }
func (m *mobileMockSpawner) GetSpawnTelemetry(_ string) (*bridge.SpawnTelemetry, bool) {
	return nil, false
}
func (m *mobileMockSpawner) SendControlMessage(_ context.Context, spawnID string, cmd spawn.ControlCommand) error {
	m.controlCalls = append(m.controlCalls, recordedMobileControlCall{spawnID: spawnID, cmd: cmd})
	return m.controlErr
}

// mobileControlFixture wires a mobileMockSpawner into a fresh ServeMux with
// the mobile domain's routes registered, returning the mux and the spawner so
// tests can assert on captured calls.
func mobileControlFixture(t *testing.T, spawner *mobileMockSpawner) (*http.ServeMux, *mockDeps) {
	t.Helper()
	deps := newTestMockDeps()
	deps.spawner = spawner
	d := New(deps)
	mux := http.NewServeMux()
	d.RegisterRoutes(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })
	return mux, deps
}

// TestHandleMobileSpawnMessage_Success exercises the happy path: bearer
// token + spawn scope, valid JSON body, 202 envelope, and a captured
// ControlCommandMessage with the expected text.
func TestHandleMobileSpawnMessage_Success(t *testing.T) {
	spawner := &mobileMockSpawner{}
	mux, _ := mobileControlFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/mobile/v1/agent/spawn/spawn-1/message",
		strings.NewReader(`{"text":"another turn please"}`))
	req.Header.Set("Authorization", "Bearer test-token")

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
	if got.cmd.Type != spawn.ControlCommandMessage {
		t.Errorf("cmd.Type = %q, want %q", got.cmd.Type, spawn.ControlCommandMessage)
	}
	if got.cmd.Text != "another turn please" {
		t.Errorf("cmd.Text = %q, want 'another turn please'", got.cmd.Text)
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !env.OK {
		t.Errorf("envelope.OK = false, want true")
	}
}

// TestHandleMobileSpawnMessage_Unauthorized verifies the route exists but
// rejects requests without a bearer token (401, no spawner call).
func TestHandleMobileSpawnMessage_Unauthorized(t *testing.T) {
	spawner := &mobileMockSpawner{}
	mux, _ := mobileControlFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/mobile/v1/agent/spawn/spawn-1/message",
		strings.NewReader(`{"text":"hi"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if len(spawner.controlCalls) != 0 {
		t.Errorf("expected 0 SendControlMessage calls on unauthorized, got %d", len(spawner.controlCalls))
	}
}

// TestHandleMobileSpawnMessage_InvalidBody confirms a non-JSON body returns
// 400 with the invalid_body code and never reaches the spawner.
func TestHandleMobileSpawnMessage_InvalidBody(t *testing.T) {
	spawner := &mobileMockSpawner{}
	mux, _ := mobileControlFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/mobile/v1/agent/spawn/spawn-1/message",
		strings.NewReader(`not json`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if len(spawner.controlCalls) != 0 {
		t.Errorf("expected 0 SendControlMessage calls on invalid body, got %d", len(spawner.controlCalls))
	}
}

// TestHandleMobileSpawnInterrupt_Success confirms the interrupt route
// forwards a ControlCommandInterrupt without a payload.
func TestHandleMobileSpawnInterrupt_Success(t *testing.T) {
	spawner := &mobileMockSpawner{}
	mux, _ := mobileControlFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/mobile/v1/agent/spawn/spawn-2/interrupt", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if len(spawner.controlCalls) != 1 {
		t.Fatalf("expected 1 SendControlMessage call, got %d", len(spawner.controlCalls))
	}
	got := spawner.controlCalls[0]
	if got.cmd.Type != spawn.ControlCommandInterrupt {
		t.Errorf("cmd.Type = %q, want %q", got.cmd.Type, spawn.ControlCommandInterrupt)
	}
	if got.cmd.Text != "" {
		t.Errorf("cmd.Text = %q, want empty", got.cmd.Text)
	}
}

// TestHandleMobileSpawnControl_ErrorMapping table-tests the sentinel ->
// HTTP status + envelope code mapping for both message and interrupt
// endpoints, so future regressions are caught.
func TestHandleMobileSpawnControl_ErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantTag  string
	}{
		{"not found -> 404", spawn.ErrSpawnNotFound, http.StatusNotFound, "not_found"},
		{"not running -> 409", spawn.ErrSpawnNotRunning, http.StatusConflict, "not_running"},
		{"not multi-turn -> 400", spawn.ErrSpawnNotMultiTurn, http.StatusBadRequest, "not_multi_turn"},
		{"invalid command -> 400", spawn.ErrInvalidControlCommand, http.StatusBadRequest, "invalid_command"},
		{"other -> 500", errors.New("boom"), http.StatusInternalServerError, "control_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spawner := &mobileMockSpawner{controlErr: tc.err}
			mux, _ := mobileControlFixture(t, spawner)

			req := httptest.NewRequest("POST", "/api/mobile/v1/agent/spawn/spawn-err/interrupt", nil)
			req.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Errorf("status: got %d, want %d (body=%s)", rec.Code, tc.wantCode, rec.Body.String())
			}

			var env Envelope
			if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			if env.OK {
				t.Errorf("envelope.OK = true, want false")
			}
			errMap, ok := env.Error.(map[string]any)
			if !ok {
				t.Fatalf("envelope.Error type = %T, want map[string]any", env.Error)
			}
			if errMap["code"] != tc.wantTag {
				t.Errorf("envelope.Error.code = %v, want %q", errMap["code"], tc.wantTag)
			}
		})
	}
}

// TestRouteRegistration_SpawnControl confirms both new routes are wired
// onto the mobile mux (not 404), even when called without a body.
func TestRouteRegistration_SpawnControl(t *testing.T) {
	spawner := &mobileMockSpawner{}
	mux, _ := mobileControlFixture(t, spawner)

	for _, path := range []string{
		"/api/mobile/v1/agent/spawn/test/message",
		"/api/mobile/v1/agent/spawn/test/interrupt",
	} {
		req := httptest.NewRequest("POST", path, nil)
		// No auth header on purpose: route exists -> 401, route missing -> 404.
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s: route not registered (got 404)", path)
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: expected 401 from unauth, got %d", path, rec.Code)
		}
	}
}
