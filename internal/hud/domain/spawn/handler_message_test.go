package spawn

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkgspawn "github.com/crb2nu/loom/internal/spawn"
)

// TestHandleAgentSpawnMessage_FrozenContract verifies the 202 response body
// contains the FROZEN contract fields {spawn_id, queued_at} with queued_at as
// a parseable RFC3339 timestamp. Slice 2 (iOS) keys off this exact shape.
func TestHandleAgentSpawnMessage_FrozenContract(t *testing.T) {
	spawner := &mockSpawner{}
	mux := controlRouterFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/agent/spawn/spawn-frozen/message",
		strings.NewReader(`{"text":"hi"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got, _ := body["spawn_id"].(string); got != "spawn-frozen" {
		t.Errorf("spawn_id = %q, want spawn-frozen", got)
	}
	queuedAt, ok := body["queued_at"].(string)
	if !ok || queuedAt == "" {
		t.Fatalf("queued_at missing or empty: %v", body["queued_at"])
	}
	if _, err := time.Parse(time.RFC3339, queuedAt); err != nil {
		t.Errorf("queued_at not RFC3339: %q (%v)", queuedAt, err)
	}
}

// TestHandleAgentSpawnInterrupt_FrozenContract verifies the 202 response body
// contains the FROZEN contract fields {spawn_id, interrupted_at} with
// interrupted_at as a parseable RFC3339 timestamp.
func TestHandleAgentSpawnInterrupt_FrozenContract(t *testing.T) {
	spawner := &mockSpawner{}
	mux := controlRouterFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/agent/spawn/spawn-frozen/interrupt", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got, _ := body["spawn_id"].(string); got != "spawn-frozen" {
		t.Errorf("spawn_id = %q, want spawn-frozen", got)
	}
	interruptedAt, ok := body["interrupted_at"].(string)
	if !ok || interruptedAt == "" {
		t.Fatalf("interrupted_at missing or empty: %v", body["interrupted_at"])
	}
	if _, err := time.Parse(time.RFC3339, interruptedAt); err != nil {
		t.Errorf("interrupted_at not RFC3339: %q (%v)", interruptedAt, err)
	}
}

// TestHandleAgentSpawnInterrupt_EmptyBody confirms an empty body is accepted
// per the FROZEN contract (`body: {} (empty allowed)`).
func TestHandleAgentSpawnInterrupt_EmptyBody(t *testing.T) {
	spawner := &mockSpawner{}
	mux := controlRouterFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/agent/spawn/spawn-empty-body/interrupt",
		strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for empty {} body, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleAgentSpawnMessage_NotFound exercises the 404 mapping path.
func TestHandleAgentSpawnMessage_NotFound(t *testing.T) {
	spawner := &mockSpawner{controlErr: pkgspawn.ErrSpawnNotFound}
	mux := controlRouterFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/agent/spawn/missing/message",
		strings.NewReader(`{"text":"x"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// TestHandleAgentSpawnMessage_NotRunning exercises the 409 mapping path.
func TestHandleAgentSpawnMessage_NotRunning(t *testing.T) {
	spawner := &mockSpawner{controlErr: pkgspawn.ErrSpawnNotRunning}
	mux := controlRouterFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/agent/spawn/done/message",
		strings.NewReader(`{"text":"x"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

// TestHandleAgentSpawnInterrupt_NotFound exercises the 404 mapping path on
// the interrupt endpoint.
func TestHandleAgentSpawnInterrupt_NotFound(t *testing.T) {
	spawner := &mockSpawner{controlErr: pkgspawn.ErrSpawnNotFound}
	mux := controlRouterFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/agent/spawn/missing/interrupt", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// TestHandleAgentSpawnInterrupt_NotRunning exercises the 409 mapping path on
// the interrupt endpoint.
func TestHandleAgentSpawnInterrupt_NotRunning(t *testing.T) {
	spawner := &mockSpawner{controlErr: pkgspawn.ErrSpawnNotRunning}
	mux := controlRouterFixture(t, spawner)

	req := httptest.NewRequest("POST", "/api/agent/spawn/done/interrupt", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}
