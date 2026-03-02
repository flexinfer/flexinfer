package agentcontext

import (
	"context"
	"testing"
	"time"
)

func TestEnrichSessionStartResult_ActiveAgents(t *testing.T) {
	svc := newTestService()

	// Register 3 agents, expire 1
	svc.presence.reg["agent-1"] = newTestPresence("agent-1", 120)
	svc.presence.reg["agent-2"] = newTestPresence("agent-2", 120)
	svc.presence.reg["agent-3"] = newTestPresence("agent-3", 120)
	svc.presence.reg["agent-3"].LastHeartbeat = time.Now().Add(-300 * time.Second) // expired

	result := map[string]any{}
	svc.enrichSessionStartResult(context.Background(), result, "agent-1", "default")

	activeAgents, ok := result["active_agents"].(int)
	if !ok {
		t.Fatal("active_agents missing or wrong type")
	}
	if activeAgents != 2 {
		t.Errorf("active_agents = %d, want 2 (agent-3 is expired)", activeAgents)
	}
}

func TestEnrichSessionStartResult_PendingHandoffsNilQdrant(t *testing.T) {
	svc := newTestService()
	// handoffsQdrant is nil in test service

	result := map[string]any{}
	svc.enrichSessionStartResult(context.Background(), result, "agent-1", "default")

	// Should still set the field (as nil slice), not panic
	if _, ok := result["pending_handoffs"]; !ok {
		t.Error("pending_handoffs field missing from result")
	}
}

func TestEnrichSessionStartResult_ZeroAgents(t *testing.T) {
	svc := newTestService()

	result := map[string]any{}
	svc.enrichSessionStartResult(context.Background(), result, "agent-1", "default")

	activeAgents := result["active_agents"].(int)
	if activeAgents != 0 {
		t.Errorf("active_agents = %d, want 0", activeAgents)
	}
}

func TestSessionEndCleanup_ReleasesFileClaims(t *testing.T) {
	svc := newTestService()

	// Create a session for agent-1
	session := &Session{
		ID:      "session-1",
		AgentID: "agent-1",
		Status:  string(SessionStatusActive),
	}
	svc.sess.sessions["session-1"] = session

	// Agent-1 has claims on 2 files
	now := time.Now()
	svc.claims.claims["main.go"] = map[string]*FileClaim{
		"agent-1": {ID: "c1", AgentID: "agent-1", FilePath: "main.go", CreatedAt: now},
	}
	svc.claims.claims["service.go"] = map[string]*FileClaim{
		"agent-1": {ID: "c2", AgentID: "agent-1", FilePath: "service.go", CreatedAt: now},
		"agent-2": {ID: "c3", AgentID: "agent-2", FilePath: "service.go", CreatedAt: now},
	}

	// Simulate cleanup
	released := svc.releaseAllClaimsForAgent("agent-1")
	if released != 2 {
		t.Errorf("released = %d, want 2", released)
	}

	// agent-2's claim should still exist
	svc.claims.mu.RLock()
	if _, ok := svc.claims.claims["service.go"]["agent-2"]; !ok {
		t.Error("agent-2 claim on service.go should still exist")
	}
	// main.go should be fully removed
	if _, ok := svc.claims.claims["main.go"]; ok {
		t.Error("main.go should have been removed (no claims left)")
	}
	svc.claims.mu.RUnlock()
}

func TestSessionEndCleanup_DeregistersPresence(t *testing.T) {
	svc := newTestService()

	svc.presence.reg["agent-1"] = newTestPresence("agent-1", 120)
	svc.presence.reg["agent-2"] = newTestPresence("agent-2", 120)

	// Simulate cleanup for agent-1
	hadPresence := svc.presence.Remove("agent-1")

	if !hadPresence {
		t.Error("agent-1 should have been registered")
	}
	if len(svc.presence.reg) != 1 {
		t.Errorf("presence registry should have 1 entry, got %d", len(svc.presence.reg))
	}
	if _, ok := svc.presence.reg["agent-2"]; !ok {
		t.Error("agent-2 should still be registered")
	}
}

func TestSessionEndCleanup_OrphansWorktrees(t *testing.T) {
	svc := newTestService()

	now := time.Now()
	svc.worktrees.assns["wt-1"] = &WorktreeAssignment{
		ID: "wt-1", AgentID: "agent-1", Status: WorktreeStatusActive, CreatedAt: now,
	}
	svc.worktrees.assns["wt-2"] = &WorktreeAssignment{
		ID: "wt-2", AgentID: "agent-1", Status: WorktreeStatusActive, CreatedAt: now,
	}
	svc.worktrees.assns["wt-3"] = &WorktreeAssignment{
		ID: "wt-3", AgentID: "agent-2", Status: WorktreeStatusActive, CreatedAt: now,
	}

	svc.orphanWorktreesForAgent("agent-1")

	if svc.worktrees.assns["wt-1"].Status != WorktreeStatusOrphaned {
		t.Errorf("wt-1 status = %q, want orphaned", svc.worktrees.assns["wt-1"].Status)
	}
	if svc.worktrees.assns["wt-2"].Status != WorktreeStatusOrphaned {
		t.Errorf("wt-2 status = %q, want orphaned", svc.worktrees.assns["wt-2"].Status)
	}
	if svc.worktrees.assns["wt-3"].Status != WorktreeStatusActive {
		t.Errorf("wt-3 status = %q, want active (different agent)", svc.worktrees.assns["wt-3"].Status)
	}
}

func TestSessionEndCleanup_FullIntegration(t *testing.T) {
	svc := newTestService()

	// Set up agent-1 with session, presence, claims, and worktrees
	session := &Session{
		ID:      "session-1",
		AgentID: "agent-1",
		Status:  string(SessionStatusActive),
	}
	svc.sess.sessions["session-1"] = session

	svc.presence.reg["agent-1"] = newTestPresence("agent-1", 120)

	now := time.Now()
	svc.claims.claims["file.go"] = map[string]*FileClaim{
		"agent-1": {ID: "c1", AgentID: "agent-1", FilePath: "file.go", CreatedAt: now},
	}
	svc.worktrees.assns["wt-1"] = &WorktreeAssignment{
		ID: "wt-1", AgentID: "agent-1", Status: WorktreeStatusActive, CreatedAt: now,
	}

	// Perform full cleanup (as HandleSessionEnd would)
	agentID := session.AgentID

	released := svc.releaseAllClaimsForAgent(agentID)

	hadPresence := svc.presence.Remove(agentID)

	svc.orphanWorktreesForAgent(agentID)

	// Verify everything is cleaned up
	if released != 1 {
		t.Errorf("released claims = %d, want 1", released)
	}
	if !hadPresence {
		t.Error("should have had presence")
	}
	if len(svc.presence.reg) != 0 {
		t.Error("presence registry should be empty")
	}
	if len(svc.claims.claims) != 0 {
		t.Error("file claims should be empty")
	}
	if svc.worktrees.assns["wt-1"].Status != WorktreeStatusOrphaned {
		t.Errorf("worktree status = %q, want orphaned", svc.worktrees.assns["wt-1"].Status)
	}
}

func TestGetSession_CacheRecheck(t *testing.T) {
	svc := newTestService()

	// Pre-populate the in-memory cache
	session := &Session{
		ID:        "session-1",
		AgentID:   "agent-1",
		Namespace: "test",
		Status:    string(SessionStatusActive),
		StartedAt: time.Now(),
	}
	svc.sess.sessions["session-1"] = session

	// getSession should return from cache (no Qdrant needed)
	got, err := svc.getSession(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("getSession() error: %v", err)
	}
	if got.ID != "session-1" {
		t.Errorf("session ID = %q, want %q", got.ID, "session-1")
	}
	if got.AgentID != "agent-1" {
		t.Errorf("agent_id = %q, want %q", got.AgentID, "agent-1")
	}
}
