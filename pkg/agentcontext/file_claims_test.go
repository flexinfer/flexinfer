package agentcontext

import (
	"testing"
	"time"
)

func TestFileClaimAcquire(t *testing.T) {
	svc := newTestService()

	now := time.Now()
	claim := &FileClaim{
		ID:        GenerateID("agent-1", "main.go", "claim", now),
		AgentID:   "agent-1",
		SessionID: "session-1",
		FilePath:  "main.go",
		ClaimType: ClaimTypeEdit,
		Reason:    "implementing feature",
		CreatedAt: now,
	}

	svc.claims.claims["main.go"] = map[string]*FileClaim{
		"agent-1": claim,
	}

	if len(svc.claims.claims) != 1 {
		t.Fatalf("expected 1 file with claims, got %d", len(svc.claims.claims))
	}
	if svc.claims.claims["main.go"]["agent-1"].AgentID != "agent-1" {
		t.Error("claim agent_id mismatch")
	}
	if svc.claims.claims["main.go"]["agent-1"].ClaimType != ClaimTypeEdit {
		t.Error("claim type mismatch")
	}
}

func TestFileClaimConflict(t *testing.T) {
	svc := newTestService()

	now := time.Now()

	// Agent-1 claims service.go
	claim1 := &FileClaim{
		ID:        GenerateID("agent-1", "service.go", "claim", now),
		AgentID:   "agent-1",
		SessionID: "session-1",
		FilePath:  "service.go",
		ClaimType: ClaimTypeEdit,
		CreatedAt: now,
	}
	svc.claims.claims["service.go"] = map[string]*FileClaim{
		"agent-1": claim1,
	}

	// Agent-2 also claims service.go
	claim2 := &FileClaim{
		ID:        GenerateID("agent-2", "service.go", "claim", now),
		AgentID:   "agent-2",
		SessionID: "session-2",
		FilePath:  "service.go",
		ClaimType: ClaimTypeEdit,
		CreatedAt: now,
	}
	svc.claims.claims["service.go"]["agent-2"] = claim2

	// Check for conflicts from agent-2's perspective
	agents := svc.claims.claims["service.go"]
	hasConflict := false
	for otherAgent := range agents {
		if otherAgent != "agent-2" {
			hasConflict = true
		}
	}
	if !hasConflict {
		t.Error("expected conflict for service.go")
	}
}

func TestFileClaimRelease(t *testing.T) {
	svc := newTestService()

	now := time.Now()
	claim := &FileClaim{
		ID:        GenerateID("agent-1", "main.go", "claim", now),
		AgentID:   "agent-1",
		SessionID: "session-1",
		FilePath:  "main.go",
		ClaimType: ClaimTypeEdit,
		CreatedAt: now,
	}
	svc.claims.claims["main.go"] = map[string]*FileClaim{
		"agent-1": claim,
	}

	// Release
	svc.claims.mu.Lock()
	if agents, ok := svc.claims.claims["main.go"]; ok {
		delete(agents, "agent-1")
		if len(agents) == 0 {
			delete(svc.claims.claims, "main.go")
		}
	}
	svc.claims.mu.Unlock()

	if len(svc.claims.claims) != 0 {
		t.Errorf("expected 0 claims after release, got %d", len(svc.claims.claims))
	}
}

func TestFileClaimReleaseAll(t *testing.T) {
	svc := newTestService()

	now := time.Now()

	// Agent-1 claims 3 files
	files := []string{"main.go", "service.go", "config.go"}
	for _, f := range files {
		svc.claims.claims[f] = map[string]*FileClaim{
			"agent-1": {
				ID:        GenerateID("agent-1", f, "claim", now),
				AgentID:   "agent-1",
				SessionID: "session-1",
				FilePath:  f,
				ClaimType: ClaimTypeEdit,
				CreatedAt: now,
			},
		}
	}

	released := svc.releaseAllClaimsForAgent("agent-1")
	if released != 3 {
		t.Errorf("released = %d, want 3", released)
	}
	if len(svc.claims.claims) != 0 {
		t.Errorf("expected 0 claims after release all, got %d", len(svc.claims.claims))
	}
}

func TestFileClaimQuery(t *testing.T) {
	svc := newTestService()

	now := time.Now()

	// Agent-1 claims file-a
	svc.claims.claims["file-a.go"] = map[string]*FileClaim{
		"agent-1": {
			ID:        GenerateID("agent-1", "file-a.go", "claim", now),
			AgentID:   "agent-1",
			SessionID: "session-1",
			FilePath:  "file-a.go",
			ClaimType: ClaimTypeEdit,
			CreatedAt: now,
		},
	}

	// Agent-2 claims file-b
	svc.claims.claims["file-b.go"] = map[string]*FileClaim{
		"agent-2": {
			ID:        GenerateID("agent-2", "file-b.go", "claim", now),
			AgentID:   "agent-2",
			SessionID: "session-2",
			FilePath:  "file-b.go",
			ClaimType: ClaimTypeReview,
			CreatedAt: now,
		},
	}

	// Query file-a
	svc.claims.mu.RLock()
	agents, ok := svc.claims.claims["file-a.go"]
	svc.claims.mu.RUnlock()

	if !ok {
		t.Fatal("expected claims on file-a.go")
	}
	if _, hasAgent1 := agents["agent-1"]; !hasAgent1 {
		t.Error("expected agent-1 to hold claim on file-a.go")
	}

	// Query unclaimed file
	svc.claims.mu.RLock()
	_, ok = svc.claims.claims["unclaimed.go"]
	svc.claims.mu.RUnlock()

	if ok {
		t.Error("expected no claims on unclaimed.go")
	}
}

func TestFileClaimExpiry(t *testing.T) {
	svc := newTestService()

	past := time.Now().Add(-1 * time.Hour)
	expiresAt := time.Now().Add(-30 * time.Minute)

	svc.claims.claims["expired.go"] = map[string]*FileClaim{
		"agent-1": {
			ID:        GenerateID("agent-1", "expired.go", "claim", past),
			AgentID:   "agent-1",
			SessionID: "session-1",
			FilePath:  "expired.go",
			ClaimType: ClaimTypeEdit,
			CreatedAt: past,
			ExpiresAt: &expiresAt,
		},
	}

	// Detect conflicts — expired claim should be skipped
	// Set up agent-2 in presence to use detectFileConflicts
	svc.presence.reg["agent-2"] = newTestPresence("agent-2", 120)

	conflicts := svc.detectFileConflicts("agent-2", []string{"expired.go"})

	// The file claim is expired, so it should not show up as a conflict
	for _, c := range conflicts {
		if c["source"] == "file_claim" && c["agent_id"] == "agent-1" {
			t.Error("expired claim should not appear as conflict")
		}
	}
}

func TestFileClaimPayloadRoundtrip(t *testing.T) {
	now := time.Now().Truncate(time.Nanosecond)
	expiresAt := now.Add(1 * time.Hour)

	original := &FileClaim{
		ID:        "claim-123",
		AgentID:   "agent-1",
		SessionID: "session-abc",
		FilePath:  "pkg/service.go",
		ClaimType: ClaimTypeEdit,
		Reason:    "refactoring service layer",
		CreatedAt: now,
		ExpiresAt: &expiresAt,
	}

	payload := fileClaimToPayload(original)
	restored := payloadToFileClaim(payload)

	if restored == nil {
		t.Fatal("payloadToFileClaim returned nil")
	}
	if restored.ID != original.ID {
		t.Errorf("ID = %q, want %q", restored.ID, original.ID)
	}
	if restored.AgentID != original.AgentID {
		t.Errorf("AgentID = %q, want %q", restored.AgentID, original.AgentID)
	}
	if restored.SessionID != original.SessionID {
		t.Errorf("SessionID = %q, want %q", restored.SessionID, original.SessionID)
	}
	if restored.FilePath != original.FilePath {
		t.Errorf("FilePath = %q, want %q", restored.FilePath, original.FilePath)
	}
	if restored.ClaimType != original.ClaimType {
		t.Errorf("ClaimType = %q, want %q", restored.ClaimType, original.ClaimType)
	}
	if restored.Reason != original.Reason {
		t.Errorf("Reason = %q, want %q", restored.Reason, original.Reason)
	}
	if restored.ExpiresAt == nil {
		t.Fatal("ExpiresAt should not be nil")
	}
}
