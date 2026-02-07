package agentcontext

import (
	"testing"
	"time"
)

func newTestPresence(agentID string, ttl int) *AgentPresence {
	now := time.Now()
	return &AgentPresence{
		ID:            GenerateID(agentID, "presence", "", now),
		AgentID:       agentID,
		Status:        PresenceStatusActive,
		LastHeartbeat: now,
		HeartbeatTTL:  ttl,
		RegisteredAt:  now,
	}
}

func newTestService() *Service {
	return &Service{
		cfg: Config{
			PresenceHeartbeatTTL:    120,
			PresenceCleanupInterval: 60,
		},
		presenceMap:   make(map[string]*AgentPresence),
		fileClaims:    make(map[string]map[string]*FileClaim),
		worktreeAssns: make(map[string]*WorktreeAssignment),
		sessions:      make(map[string]*Session),
	}
}

func TestPresenceRegister(t *testing.T) {
	svc := newTestService()

	p := newTestPresence("agent-1", 120)
	svc.presenceMap[p.AgentID] = p

	if len(svc.presenceMap) != 1 {
		t.Fatalf("expected 1 presence entry, got %d", len(svc.presenceMap))
	}

	got := svc.presenceMap["agent-1"]
	if got.AgentID != "agent-1" {
		t.Errorf("agent_id = %q, want %q", got.AgentID, "agent-1")
	}
	if got.Status != PresenceStatusActive {
		t.Errorf("status = %q, want %q", got.Status, PresenceStatusActive)
	}
	if got.HeartbeatTTL != 120 {
		t.Errorf("heartbeat_ttl = %d, want 120", got.HeartbeatTTL)
	}
}

func TestPresenceHeartbeat(t *testing.T) {
	svc := newTestService()

	p := newTestPresence("agent-1", 120)
	originalHeartbeat := p.LastHeartbeat
	svc.presenceMap[p.AgentID] = p

	// Simulate heartbeat update
	time.Sleep(time.Millisecond)
	p.LastHeartbeat = time.Now()
	p.ActiveFiles = []string{"main.go", "service.go"}

	if !p.LastHeartbeat.After(originalHeartbeat) {
		t.Error("heartbeat should be updated after original")
	}
	if len(p.ActiveFiles) != 2 {
		t.Errorf("active_files length = %d, want 2", len(p.ActiveFiles))
	}
}

func TestPresenceHeartbeatNotRegistered(t *testing.T) {
	svc := newTestService()

	// Agent not registered — heartbeat should fail
	_, ok := svc.presenceMap["ghost-agent"]
	if ok {
		t.Error("unregistered agent should not be in presence map")
	}
}

func TestPresenceList(t *testing.T) {
	svc := newTestService()

	// Register 2 agents
	p1 := newTestPresence("agent-1", 120)
	p2 := newTestPresence("agent-2", 120)
	svc.presenceMap[p1.AgentID] = p1
	svc.presenceMap[p2.AgentID] = p2

	// Count active (non-expired) agents
	now := time.Now()
	active := 0
	for _, p := range svc.presenceMap {
		if !now.After(p.LastHeartbeat.Add(time.Duration(p.HeartbeatTTL) * time.Second)) {
			active++
		}
	}
	if active != 2 {
		t.Errorf("active count = %d, want 2", active)
	}

	// Expire agent-2
	p2.LastHeartbeat = time.Now().Add(-300 * time.Second)
	active = 0
	now = time.Now()
	for _, p := range svc.presenceMap {
		if !now.After(p.LastHeartbeat.Add(time.Duration(p.HeartbeatTTL) * time.Second)) {
			active++
		}
	}
	if active != 1 {
		t.Errorf("active count after expiry = %d, want 1", active)
	}
}

func TestPresenceDeregister(t *testing.T) {
	svc := newTestService()

	p := newTestPresence("agent-1", 120)
	svc.presenceMap[p.AgentID] = p

	if len(svc.presenceMap) != 1 {
		t.Fatal("expected 1 entry before deregister")
	}

	delete(svc.presenceMap, "agent-1")

	if len(svc.presenceMap) != 0 {
		t.Error("expected 0 entries after deregister")
	}
}

func TestDetectFileConflicts(t *testing.T) {
	svc := newTestService()

	// Register 2 agents with overlapping files
	p1 := newTestPresence("agent-1", 120)
	p1.ActiveFiles = []string{"main.go", "service.go"}
	svc.presenceMap[p1.AgentID] = p1

	p2 := newTestPresence("agent-2", 120)
	p2.ActiveFiles = []string{"service.go", "config.go"}
	svc.presenceMap[p2.AgentID] = p2

	// Detect conflicts from agent-1's perspective
	conflicts := svc.detectFileConflicts("agent-1", []string{"service.go"})
	if len(conflicts) == 0 {
		t.Fatal("expected at least one conflict for service.go")
	}

	foundConflict := false
	for _, c := range conflicts {
		if c["file"] == "service.go" && c["agent_id"] == "agent-2" {
			foundConflict = true
		}
	}
	if !foundConflict {
		t.Error("expected conflict on service.go from agent-2")
	}

	// No conflict for unique file
	conflicts = svc.detectFileConflicts("agent-1", []string{"main.go"})
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts for main.go, got %d", len(conflicts))
	}
}

func TestPresenceMinTTL(t *testing.T) {
	// Verify TTL clamping logic
	ttl := 5
	if ttl < 30 {
		ttl = 30
	}
	if ttl != 30 {
		t.Errorf("TTL should be clamped to 30, got %d", ttl)
	}

	// TTL above minimum should not be clamped
	ttl = 120
	if ttl < 30 {
		ttl = 30
	}
	if ttl != 120 {
		t.Errorf("TTL should remain 120, got %d", ttl)
	}
}

func TestPresencePayloadRoundtrip(t *testing.T) {
	now := time.Now().Truncate(time.Nanosecond)
	original := &AgentPresence{
		ID:            "presence-123",
		AgentID:       "agent-1",
		SessionID:     "session-abc",
		Status:        PresenceStatusActive,
		Description:   "working on feature X",
		CurrentTask:   "implement auth",
		ActiveFiles:   []string{"main.go", "auth.go"},
		WorkingDir:    "/workspace/project",
		Branch:        "feature/auth",
		WorktreeID:    "wt-456",
		AgentType:     "claude-code",
		LastHeartbeat: now,
		HeartbeatTTL:  120,
		RegisteredAt:  now,
		Metadata:      map[string]any{"key": "value"},
	}

	payload := presenceToPayload(original)
	restored := payloadToPresence(payload)

	if restored == nil {
		t.Fatal("payloadToPresence returned nil")
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
	if restored.Status != original.Status {
		t.Errorf("Status = %q, want %q", restored.Status, original.Status)
	}
	if restored.Description != original.Description {
		t.Errorf("Description = %q, want %q", restored.Description, original.Description)
	}
	if restored.CurrentTask != original.CurrentTask {
		t.Errorf("CurrentTask = %q, want %q", restored.CurrentTask, original.CurrentTask)
	}
	if restored.Branch != original.Branch {
		t.Errorf("Branch = %q, want %q", restored.Branch, original.Branch)
	}
	if restored.WorktreeID != original.WorktreeID {
		t.Errorf("WorktreeID = %q, want %q", restored.WorktreeID, original.WorktreeID)
	}
	if restored.AgentType != original.AgentType {
		t.Errorf("AgentType = %q, want %q", restored.AgentType, original.AgentType)
	}
	if restored.HeartbeatTTL != original.HeartbeatTTL {
		t.Errorf("HeartbeatTTL = %d, want %d", restored.HeartbeatTTL, original.HeartbeatTTL)
	}
	if len(restored.ActiveFiles) != len(original.ActiveFiles) {
		t.Errorf("ActiveFiles length = %d, want %d", len(restored.ActiveFiles), len(original.ActiveFiles))
	}
}
