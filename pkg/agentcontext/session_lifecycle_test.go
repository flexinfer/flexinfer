package agentcontext

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func decodeToolPayload(t *testing.T, resultText string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(resultText), &payload); err != nil {
		t.Fatalf("unmarshal tool payload: %v", err)
	}
	return payload
}

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

func TestSessionEnd_SummarizeSyncTransitionsToSummarized(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	svc := newTestService()
	svc.sess.cfg.AutoSummarize = true

	svc.sess.sessions["s1"] = &Session{
		ID:        "s1",
		AgentID:   "agent-1",
		Status:    string(SessionStatusActive),
		StartedAt: time.Now().Add(-time.Hour),
	}

	summaryCalls := 0
	svc.sess.generateSummary = func(_ context.Context, session *Session) error {
		summaryCalls++
		if session.ID != "s1" {
			t.Fatalf("summary called for session %q, want s1", session.ID)
		}
		return nil
	}

	result, err := svc.sess.End(context.Background(), map[string]any{
		"session_id":    "s1",
		"summarize":     true,
		"summary_async": false,
		"cleanup":       false,
	})
	if err != nil {
		t.Fatalf("End returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error result: %+v", result)
	}

	payload := decodeToolPayload(t, result.Content[0].Text)
	if payload["summarized"] != true {
		t.Fatalf("payload summarized = %v, want true", payload["summarized"])
	}
	if summaryCalls != 1 {
		t.Fatalf("summary calls = %d, want 1", summaryCalls)
	}

	ended := svc.sess.sessions["s1"]
	if ended.Status != string(SessionStatusSummarized) {
		t.Fatalf("session status = %q, want %q", ended.Status, SessionStatusSummarized)
	}
}

func TestSessionEnd_SummaryAsyncQueuesWork(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	svc := newTestService()
	svc.sess.cfg.AutoSummarize = true

	svc.sess.sessions["s1"] = &Session{
		ID:        "s1",
		AgentID:   "agent-1",
		Status:    string(SessionStatusActive),
		StartedAt: time.Now().Add(-time.Hour),
	}

	queued := make(chan string, 1)
	svc.sess.runSummaryAsync = func(session *Session) {
		queued <- session.ID
	}

	result, err := svc.sess.End(context.Background(), map[string]any{
		"session_id":    "s1",
		"summarize":     true,
		"summary_async": true,
		"cleanup":       false,
	})
	if err != nil {
		t.Fatalf("End returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error result: %+v", result)
	}

	payload := decodeToolPayload(t, result.Content[0].Text)
	if payload["summary_queued"] != true {
		t.Fatalf("summary_queued = %v, want true", payload["summary_queued"])
	}
	if payload["summarized"] != false {
		t.Fatalf("summarized = %v, want false", payload["summarized"])
	}

	select {
	case sessionID := <-queued:
		if sessionID != "s1" {
			t.Fatalf("queued session id = %q, want s1", sessionID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async summary callback")
	}

	ended := svc.sess.sessions["s1"]
	if ended.Status != string(SessionStatusEnded) {
		t.Fatalf("session status = %q, want %q", ended.Status, SessionStatusEnded)
	}
}

func TestSessionEnd_CleanupContract(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	svc := newTestService()
	svc.sess.sessions["s1"] = &Session{
		ID:        "s1",
		AgentID:   "agent-1",
		Status:    string(SessionStatusActive),
		StartedAt: time.Now().Add(-time.Hour),
	}

	releaseCalled := 0
	removeCalled := 0
	deleteCalled := 0
	orphanCalled := 0
	staleCalled := 0

	svc.sess.releaseClaimsForAgent = func(agentID string) int {
		releaseCalled++
		if agentID != "agent-1" {
			t.Fatalf("releaseClaimsForAgent agentID = %q, want agent-1", agentID)
		}
		return 2
	}
	svc.sess.removePresence = func(agentID string) bool {
		removeCalled++
		if agentID != "agent-1" {
			t.Fatalf("removePresence agentID = %q, want agent-1", agentID)
		}
		return true
	}
	svc.sess.deletePresenceFromQdrant = func(_ context.Context, agentID string) error {
		deleteCalled++
		if agentID != "agent-1" {
			t.Fatalf("deletePresenceFromQdrant agentID = %q, want agent-1", agentID)
		}
		return nil
	}
	svc.sess.orphanWorktrees = func(agentID string) {
		orphanCalled++
		if agentID != "agent-1" {
			t.Fatalf("orphanWorktrees agentID = %q, want agent-1", agentID)
		}
	}
	svc.sess.markTasksStale = func(_ context.Context, sessionID string) int {
		staleCalled++
		if sessionID != "s1" {
			t.Fatalf("markTasksStale sessionID = %q, want s1", sessionID)
		}
		return 5
	}

	result, err := svc.sess.End(context.Background(), map[string]any{
		"session_id": "s1",
		"summarize":  false,
		"cleanup":    true,
	})
	if err != nil {
		t.Fatalf("End returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error result: %+v", result)
	}

	payload := decodeToolPayload(t, result.Content[0].Text)
	cleanup, ok := payload["cleanup"].(map[string]any)
	if !ok {
		t.Fatalf("cleanup payload missing or wrong type: %T", payload["cleanup"])
	}

	if cleanup["file_claims_released"] != float64(2) {
		t.Fatalf("file_claims_released = %v, want 2", cleanup["file_claims_released"])
	}
	if cleanup["presence_deregistered"] != true {
		t.Fatalf("presence_deregistered = %v, want true", cleanup["presence_deregistered"])
	}
	if cleanup["worktrees_orphaned"] != true {
		t.Fatalf("worktrees_orphaned = %v, want true", cleanup["worktrees_orphaned"])
	}
	if cleanup["tasks_marked_stale"] != float64(5) {
		t.Fatalf("tasks_marked_stale = %v, want 5", cleanup["tasks_marked_stale"])
	}

	if releaseCalled != 1 || removeCalled != 1 || deleteCalled != 1 || orphanCalled != 1 || staleCalled != 1 {
		t.Fatalf("callback counts = release:%d remove:%d delete:%d orphan:%d stale:%d; want all 1",
			releaseCalled, removeCalled, deleteCalled, orphanCalled, staleCalled)
	}
}

func TestSessionStart_IdempotentForSameAgentAndNamespace(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	svc := newTestService()
	started := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	svc.sess.sessions["s1"] = &Session{
		ID:        "s1",
		AgentID:   "agent-1",
		Namespace: "loom-core/main",
		Status:    string(SessionStatusActive),
		StartedAt: started,
	}

	result, err := svc.sess.Start(context.Background(), map[string]any{
		"agent_id":    "agent-1",
		"namespace":   "loom-core/main",
		"description": "should be idempotent",
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error result: %+v", result)
	}

	payload := decodeToolPayload(t, result.Content[0].Text)
	if payload["session_id"] != "s1" {
		t.Fatalf("session_id = %v, want s1", payload["session_id"])
	}
	if payload["already_existed"] != true {
		t.Fatalf("already_existed = %v, want true", payload["already_existed"])
	}
	if payload["started_at"] != started.Format(time.RFC3339) {
		t.Fatalf("started_at = %v, want %s", payload["started_at"], started.Format(time.RFC3339))
	}

	svc.sess.mu.RLock()
	defer svc.sess.mu.RUnlock()
	if len(svc.sess.sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(svc.sess.sessions))
	}
	if svc.sess.sessions["s1"].Status != string(SessionStatusActive) {
		t.Fatalf("existing session status = %q, want active", svc.sess.sessions["s1"].Status)
	}
	if svc.sess.sessions["s1"].EndedAt != nil {
		t.Fatalf("existing session EndedAt should remain nil, got %v", svc.sess.sessions["s1"].EndedAt)
	}
}

func TestSessionStart_NewNamespaceEndsPriorActiveSession(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	svc := newTestService()
	svc.sess.sessions["old"] = &Session{
		ID:        "old",
		AgentID:   "agent-1",
		Namespace: "loom-core/old",
		Status:    string(SessionStatusActive),
		StartedAt: time.Now().Add(-time.Hour),
	}

	result, err := svc.sess.Start(context.Background(), map[string]any{
		"agent_id":  "agent-1",
		"namespace": "loom-core/new",
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error result: %+v", result)
	}

	payload := decodeToolPayload(t, result.Content[0].Text)
	newID, _ := payload["session_id"].(string)
	if newID == "" || newID == "old" {
		t.Fatalf("expected new session id, got %q", newID)
	}

	svc.sess.mu.RLock()
	defer svc.sess.mu.RUnlock()
	old := svc.sess.sessions["old"]
	if old.Status != string(SessionStatusEnded) {
		t.Fatalf("old session status = %q, want ended", old.Status)
	}
	if old.EndedAt == nil {
		t.Fatal("old session EndedAt should be set")
	}
	newSess := svc.sess.sessions[newID]
	if newSess == nil {
		t.Fatalf("new session %q missing from cache", newID)
	}
	if newSess.Namespace != "loom-core/new" {
		t.Fatalf("new session namespace = %q, want loom-core/new", newSess.Namespace)
	}
	if newSess.Status != string(SessionStatusActive) {
		t.Fatalf("new session status = %q, want active", newSess.Status)
	}
}
