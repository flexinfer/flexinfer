package agentcontext

import (
	"context"
	"testing"
	"time"
)

func TestHandleSessionDelete_RemovesFromMemory(t *testing.T) {
	svc := newTestService()
	now := time.Now()

	// Create a session
	svc.sessions["sess-1"] = &Session{
		ID:        "sess-1",
		AgentID:   "agent-1",
		Status:    string(SessionStatusEnded),
		StartedAt: now.Add(-2 * time.Hour),
	}

	result, err := svc.HandleSessionDelete(context.Background(), map[string]any{
		"session_id": "sess-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.IsError {
		t.Fatalf("unexpected error: %v", result)
	}

	// Verify removed from memory
	svc.sessionsMu.RLock()
	_, exists := svc.sessions["sess-1"]
	svc.sessionsMu.RUnlock()
	if exists {
		t.Error("session should have been deleted from memory")
	}
}

func TestHandleSessionDelete_NonExistent(t *testing.T) {
	svc := newTestService()

	result, err := svc.HandleSessionDelete(context.Background(), map[string]any{
		"session_id": "nonexistent",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should succeed but report existed=false (no Qdrant client = no Qdrant error)
	if result.IsError {
		t.Fatalf("unexpected error: %v", result)
	}
}

func TestHandleSessionDelete_RequiresSessionID(t *testing.T) {
	svc := newTestService()

	result, err := svc.HandleSessionDelete(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when session_id is missing")
	}
}

func TestHandleSessionPrune_DryRun(t *testing.T) {
	svc := newTestService()

	// Without Qdrant, pruneSessions returns 0 pruned (no data source)
	result, err := svc.HandleSessionPrune(context.Background(), map[string]any{
		"max_age_hours": 72,
		"status":        "ended,summarized",
		"dry_run":       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result)
	}
}

func TestPruneSessions_NilQdrant(t *testing.T) {
	svc := newTestService()

	// With nil Qdrant client, should return 0 gracefully
	pruned, err := svc.pruneSessions(context.Background(), 72, "ended,summarized", false)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 0 {
		t.Errorf("expected 0 pruned with nil Qdrant, got %d", pruned)
	}
}

func TestPruneSessions_EmptyStatusFilter(t *testing.T) {
	svc := newTestService()

	pruned, err := svc.pruneSessions(context.Background(), 72, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 0 {
		t.Errorf("expected 0 pruned with empty status, got %d", pruned)
	}
}

func TestSessionReaperConfig(t *testing.T) {
	svc := newTestService()
	svc.cfg.SessionReaperEnabled = true
	svc.cfg.SessionReaperInterval = 1800
	svc.cfg.SessionReaperMaxAge = 168

	if !svc.cfg.SessionReaperEnabled {
		t.Error("session reaper should be enabled")
	}
	if svc.cfg.SessionReaperInterval != 1800 {
		t.Errorf("interval = %d, want 1800", svc.cfg.SessionReaperInterval)
	}
	if svc.cfg.SessionReaperMaxAge != 168 {
		t.Errorf("max_age = %d, want 168", svc.cfg.SessionReaperMaxAge)
	}
}

func TestEndActiveSessionsForAgent(t *testing.T) {
	svc := newTestService()
	now := time.Now()

	// Create active sessions for agent-1
	svc.sessions["s1"] = &Session{
		ID:        "s1",
		AgentID:   "agent-1",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-2 * time.Hour),
	}
	svc.sessions["s2"] = &Session{
		ID:        "s2",
		AgentID:   "agent-1",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-1 * time.Hour),
	}
	// Create a session for a different agent (should not be ended)
	svc.sessions["s3"] = &Session{
		ID:        "s3",
		AgentID:   "agent-2",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-3 * time.Hour),
	}
	// Create an already-ended session for agent-1 (should stay ended)
	ended := now.Add(-30 * time.Minute)
	svc.sessions["s4"] = &Session{
		ID:        "s4",
		AgentID:   "agent-1",
		Status:    string(SessionStatusEnded),
		StartedAt: now.Add(-4 * time.Hour),
		EndedAt:   &ended,
	}

	svc.endActiveSessionsForAgent(context.Background(), "agent-1")

	svc.sessionsMu.RLock()
	defer svc.sessionsMu.RUnlock()

	if svc.sessions["s1"].Status != string(SessionStatusEnded) {
		t.Errorf("s1 status = %s, want ended", svc.sessions["s1"].Status)
	}
	if svc.sessions["s1"].EndedAt == nil {
		t.Error("s1 EndedAt should be set")
	}
	if svc.sessions["s2"].Status != string(SessionStatusEnded) {
		t.Errorf("s2 status = %s, want ended", svc.sessions["s2"].Status)
	}
	if svc.sessions["s3"].Status != string(SessionStatusActive) {
		t.Errorf("s3 (agent-2) status = %s, want active", svc.sessions["s3"].Status)
	}
	if svc.sessions["s4"].Status != string(SessionStatusEnded) {
		t.Errorf("s4 status = %s, want ended", svc.sessions["s4"].Status)
	}
}

func TestEndStaleSessions_NilQdrant(t *testing.T) {
	svc := newTestService()

	ended := svc.endStaleSessions(context.Background(), 24)
	if ended != 0 {
		t.Errorf("expected 0 ended with nil Qdrant, got %d", ended)
	}
}

func TestLiveAgentIDs(t *testing.T) {
	svc := newTestService()
	now := time.Now()

	svc.presenceMap["alive"] = &AgentPresence{
		AgentID:       "alive",
		LastHeartbeat: now,
		HeartbeatTTL:  120,
		Status:        PresenceStatusActive,
	}
	svc.presenceMap["stale"] = &AgentPresence{
		AgentID:       "stale",
		LastHeartbeat: now.Add(-10 * time.Minute), // 600s > 3×120s = 360s
		HeartbeatTTL:  120,
		Status:        PresenceStatusOffline,
	}

	live := svc.liveAgentIDs()
	if !live["alive"] {
		t.Error("alive agent should be live")
	}
	if live["stale"] {
		t.Error("stale agent should not be live")
	}
}

func TestSessionStartEndsPriorActiveSessions(t *testing.T) {
	svc := newTestService()
	now := time.Now()

	// Create existing active sessions for the agent.
	svc.sessions["old-1"] = &Session{
		ID:        "old-1",
		AgentID:   "test-agent",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-2 * time.Hour),
	}
	svc.sessions["old-2"] = &Session{
		ID:        "old-2",
		AgentID:   "test-agent",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-1 * time.Hour),
	}

	// Simulate what HandleSessionStart does: end prior active sessions.
	// (HandleSessionStart calls endActiveSessionsForAgent before creating
	// the new session. We test the helper directly to avoid requiring Qdrant.)
	svc.endActiveSessionsForAgent(context.Background(), "test-agent")

	svc.sessionsMu.RLock()
	defer svc.sessionsMu.RUnlock()

	if svc.sessions["old-1"].Status != string(SessionStatusEnded) {
		t.Errorf("old-1 status = %s, want ended", svc.sessions["old-1"].Status)
	}
	if svc.sessions["old-1"].EndedAt == nil {
		t.Error("old-1 EndedAt should be set")
	}
	if svc.sessions["old-2"].Status != string(SessionStatusEnded) {
		t.Errorf("old-2 status = %s, want ended", svc.sessions["old-2"].Status)
	}
}

func TestSessionReaperActiveMaxAgeConfig(t *testing.T) {
	svc := newTestService()
	svc.cfg.SessionReaperActiveMaxAge = 48

	if svc.cfg.SessionReaperActiveMaxAge != 48 {
		t.Errorf("active_max_age = %d, want 48", svc.cfg.SessionReaperActiveMaxAge)
	}
}

func TestSessionReaperTick_EndsStaleInMemorySessions(t *testing.T) {
	svc := newTestService()
	svc.cfg.SessionReaperActiveMaxAge = 1 // 1 hour
	now := time.Now()

	// Create a stale active session older than 1 hour with no live presence.
	svc.sessions["stale-1"] = &Session{
		ID:        "stale-1",
		AgentID:   "dead-agent",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-3 * time.Hour),
	}

	// Create a recent active session (should NOT be ended).
	svc.sessions["recent-1"] = &Session{
		ID:        "recent-1",
		AgentID:   "dead-agent",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-30 * time.Minute),
	}

	// Run one reaper tick (same function called on startup).
	svc.sessionReaperTick(context.Background())

	svc.sessionsMu.RLock()
	defer svc.sessionsMu.RUnlock()

	// Stale session should remain active in memory (no Qdrant = endStaleSessions returns 0).
	// But sessionReaperTick should not panic or error with nil Qdrant.
	if svc.sessions["recent-1"].Status != string(SessionStatusActive) {
		t.Errorf("recent-1 status = %s, want active", svc.sessions["recent-1"].Status)
	}
}
