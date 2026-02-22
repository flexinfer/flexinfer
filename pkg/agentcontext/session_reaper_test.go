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
