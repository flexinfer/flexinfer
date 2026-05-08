package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud"
	"github.com/crb2nu/loom/internal/spawn"
)

// fakeHUDApp satisfies hudAppStopper for /api/hud/sessions tests. Returns
// whatever SpawnOrchestrator was wired in at construction time (may be nil).
type fakeHUDApp struct {
	orch *hud.SpawnOrchestrator
}

func (f *fakeHUDApp) StopMonitors()                             {}
func (f *fakeHUDApp) RefreshMonitors()                          {}
func (f *fakeHUDApp) SpawnOrchestrator() *hud.SpawnOrchestrator { return f.orch }
func (f *fakeHUDApp) SetAdminToken(string)                      {}

func TestHandleHUDSessions_EmptyWhenNoSessionManager(t *testing.T) {
	d := &Daemon{daemonEpoch: 7, logger: slog.Default()}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hud/sessions", nil)
	d.handleHUDSessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp hudSessionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DaemonEpoch != 7 {
		t.Errorf("DaemonEpoch = %d, want 7", resp.DaemonEpoch)
	}
	if len(resp.Sessions) != 0 {
		t.Errorf("expected empty sessions, got %d", len(resp.Sessions))
	}
}

func TestHandleHUDSessions_ListsSessionsWithoutSpawns(t *testing.T) {
	d := newTestDaemonWithSessions(t)

	sess := d.sessions.Open(SessionClientInfo{
		AgentHint:       "claude-code",
		PresenceAgentID: "claude-host-1",
	}, "")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hud/sessions", nil)
	d.handleHUDSessions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp hudSessionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(resp.Sessions))
	}
	s := resp.Sessions[0]
	if s.ID != sess.ID {
		t.Errorf("ID = %q, want %q", s.ID, sess.ID)
	}
	if s.AgentHint != "claude-code" {
		t.Errorf("AgentHint = %q, want claude-code", s.AgentHint)
	}
	if s.PresenceAgentID != "claude-host-1" {
		t.Errorf("PresenceAgentID = %q, want claude-host-1", s.PresenceAgentID)
	}
	if s.State != string(SessionActive) {
		t.Errorf("State = %q, want %q", s.State, SessionActive)
	}
	if len(s.Spawns) != 0 {
		t.Errorf("expected no spawns (hudApp nil), got %d", len(s.Spawns))
	}
}

func TestHandleHUDSessions_JoinsSpawnsByParentSessionID(t *testing.T) {
	d := newTestDaemonWithSessions(t)

	sess := d.sessions.Open(SessionClientInfo{AgentHint: "parent"}, "")

	// Build a SpawnOrchestrator backed by an in-memory controller and seed
	// two spawns — one child of our session, one unrelated.
	ctrl := spawn.NewK8sController(nil, "test", nil, slog.Default())
	ctrl.UpdateState(context.Background(), &spawn.State{
		SpawnID: "spawn-child-1",
		AgentID: "spawn-claude-child1",
		Status:  spawn.StatusRunning,
		Request: spawn.Request{
			AgentType:       "claude-code",
			ParentSessionID: sess.ID,
			Metadata: map[string]string{
				"weaver_query_id": "qid-abc",
				"weaver_domain":   "cluster-ops-claude",
			},
		},
	})
	ctrl.UpdateState(context.Background(), &spawn.State{
		SpawnID: "spawn-orphan-1",
		AgentID: "spawn-codex-orphan1",
		Status:  spawn.StatusRunning,
		Request: spawn.Request{AgentType: "codex"}, // ParentSessionID empty
	})

	orch := hud.NewSpawnOrchestratorForTest(ctrl)
	d.hudApp = &fakeHUDApp{orch: orch}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hud/sessions", nil)
	d.handleHUDSessions(rr, req)

	var resp hudSessionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(resp.Sessions))
	}
	s := resp.Sessions[0]
	if len(s.Spawns) != 1 {
		t.Fatalf("expected 1 spawn joined to session, got %d", len(s.Spawns))
	}
	child := s.Spawns[0]
	if child.SpawnID != "spawn-child-1" {
		t.Errorf("SpawnID = %q, want spawn-child-1", child.SpawnID)
	}
	if child.WeaverQueryID != "qid-abc" {
		t.Errorf("WeaverQueryID = %q, want qid-abc", child.WeaverQueryID)
	}
	if child.WeaverDomain != "cluster-ops-claude" {
		t.Errorf("WeaverDomain = %q, want cluster-ops-claude", child.WeaverDomain)
	}
	if child.AgentType != "claude-code" {
		t.Errorf("AgentType = %q, want claude-code", child.AgentType)
	}
	// Orphan must NOT be folded into this session.
	for _, sp := range s.Spawns {
		if sp.SpawnID == "spawn-orphan-1" {
			t.Error("orphan spawn (no parent_session_id) leaked into session response")
		}
	}
}

func TestHandleHUDSessions_ReportsDrainingFlag(t *testing.T) {
	d := newTestDaemonWithSessions(t)
	d.draining.Store(true)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hud/sessions", nil)
	d.handleHUDSessions(rr, req)

	var resp hudSessionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Draining {
		t.Error("expected Draining=true")
	}
}

// quiet unused-import guard for time (used via httptest indirectly and
// newTestDaemonWithSessions's time.Duration constructor).
var _ = time.Second
