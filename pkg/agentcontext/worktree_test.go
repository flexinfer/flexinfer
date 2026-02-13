package agentcontext

import (
	"testing"
	"time"
)

func TestWorktreeCleanupSafeTypeAssertions(t *testing.T) {
	// Verify that toString() on missing/nil map values doesn't panic
	// This tests the fix for the unsafe type assertions in HandleWorktreeCleanup

	m := map[string]any{}

	// These should return empty string, not panic
	path := toString(m["worktree_path"])
	if path != "" {
		t.Errorf("toString(nil) = %q, want empty string", path)
	}

	assignmentID := toString(m["assignment_id"])
	if assignmentID != "" {
		t.Errorf("toString(nil) = %q, want empty string", assignmentID)
	}

	// Also test with wrong type
	m["worktree_path"] = 42
	path = toString(m["worktree_path"])
	if path != "" {
		t.Errorf("toString(int) = %q, want empty string", path)
	}

	// And with actual string
	m["worktree_path"] = "/path/to/worktree"
	path = toString(m["worktree_path"])
	if path != "/path/to/worktree" {
		t.Errorf("toString(string) = %q, want %q", path, "/path/to/worktree")
	}
}

func TestWorktreePayloadRoundtrip(t *testing.T) {
	now := time.Now().Truncate(time.Nanosecond)
	releasedAt := now.Add(1 * time.Hour)

	original := &WorktreeAssignment{
		ID:           "wt-123",
		AgentID:      "agent-1",
		SessionID:    "session-abc",
		WorktreePath: "/workspace/.worktrees/feature-x",
		Branch:       "feature/x",
		BaseBranch:   "main",
		Purpose:      "implement feature X",
		Status:       WorktreeStatusActive,
		CreatedAt:    now,
		ReleasedAt:   &releasedAt,
	}

	payload := worktreeAssignmentToPayload(original)
	restored := payloadToWorktreeAssignment(payload)

	if restored == nil {
		t.Fatal("payloadToWorktreeAssignment returned nil")
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
	if restored.WorktreePath != original.WorktreePath {
		t.Errorf("WorktreePath = %q, want %q", restored.WorktreePath, original.WorktreePath)
	}
	if restored.Branch != original.Branch {
		t.Errorf("Branch = %q, want %q", restored.Branch, original.Branch)
	}
	if restored.BaseBranch != original.BaseBranch {
		t.Errorf("BaseBranch = %q, want %q", restored.BaseBranch, original.BaseBranch)
	}
	if restored.Purpose != original.Purpose {
		t.Errorf("Purpose = %q, want %q", restored.Purpose, original.Purpose)
	}
	if restored.Status != original.Status {
		t.Errorf("Status = %q, want %q", restored.Status, original.Status)
	}
	if restored.ReleasedAt == nil {
		t.Fatal("ReleasedAt should not be nil")
	}
}

func TestOrphanWorktreesForAgent(t *testing.T) {
	svc := newTestService()

	now := time.Now()

	// Create active worktrees for agent-1
	wt1 := &WorktreeAssignment{
		ID:           "wt-1",
		AgentID:      "agent-1",
		SessionID:    "session-1",
		WorktreePath: "/workspace/.worktrees/branch-a",
		Branch:       "branch-a",
		Status:       WorktreeStatusActive,
		CreatedAt:    now,
	}
	wt2 := &WorktreeAssignment{
		ID:           "wt-2",
		AgentID:      "agent-1",
		SessionID:    "session-1",
		WorktreePath: "/workspace/.worktrees/branch-b",
		Branch:       "branch-b",
		Status:       WorktreeStatusActive,
		CreatedAt:    now,
	}
	// Worktree for a different agent — should not be affected
	wt3 := &WorktreeAssignment{
		ID:           "wt-3",
		AgentID:      "agent-2",
		SessionID:    "session-2",
		WorktreePath: "/workspace/.worktrees/branch-c",
		Branch:       "branch-c",
		Status:       WorktreeStatusActive,
		CreatedAt:    now,
	}

	svc.worktreeAssns["wt-1"] = wt1
	svc.worktreeAssns["wt-2"] = wt2
	svc.worktreeAssns["wt-3"] = wt3

	svc.orphanWorktreesForAgent("agent-1")

	if wt1.Status != WorktreeStatusOrphaned {
		t.Errorf("wt-1 status = %q, want %q", wt1.Status, WorktreeStatusOrphaned)
	}
	if wt2.Status != WorktreeStatusOrphaned {
		t.Errorf("wt-2 status = %q, want %q", wt2.Status, WorktreeStatusOrphaned)
	}
	if wt3.Status != WorktreeStatusActive {
		t.Errorf("wt-3 status = %q, want %q (should be unaffected)", wt3.Status, WorktreeStatusActive)
	}
}

func TestOrphanWorktreesForAgent_SetsOrphanedAt(t *testing.T) {
	svc := newTestService()

	wt := &WorktreeAssignment{
		ID:        "wt-orphan-at",
		AgentID:   "agent-1",
		SessionID: "session-1",
		Branch:    "branch-x",
		Status:    WorktreeStatusActive,
		CreatedAt: time.Now(),
	}
	svc.worktreeAssns["wt-orphan-at"] = wt

	before := time.Now()
	svc.orphanWorktreesForAgent("agent-1")
	after := time.Now()

	if wt.OrphanedAt == nil {
		t.Fatal("OrphanedAt should be set after orphaning")
	}
	if wt.OrphanedAt.Before(before) || wt.OrphanedAt.After(after) {
		t.Errorf("OrphanedAt = %v, should be between %v and %v", *wt.OrphanedAt, before, after)
	}
}

func TestWorktreePayloadRoundtrip_NewFields(t *testing.T) {
	now := time.Now().Truncate(time.Nanosecond)
	orphanedAt := now.Add(30 * time.Minute)
	measuredAt := now.Add(1 * time.Hour)

	original := &WorktreeAssignment{
		ID:             "wt-new-fields",
		AgentID:        "agent-1",
		SessionID:      "session-1",
		WorktreePath:   "/workspace/.worktrees/test",
		Branch:         "test-branch",
		Status:         WorktreeStatusOrphaned,
		CreatedAt:      now,
		OrphanedAt:     &orphanedAt,
		TTL:            24,
		DiskUsage:      1048576, // 1 MiB
		DiskMeasuredAt: &measuredAt,
	}

	payload := worktreeAssignmentToPayload(original)
	restored := payloadToWorktreeAssignment(payload)

	if restored == nil {
		t.Fatal("payloadToWorktreeAssignment returned nil")
	}
	if restored.OrphanedAt == nil {
		t.Fatal("OrphanedAt should not be nil")
	}
	if restored.TTL != 24 {
		t.Errorf("TTL = %d, want 24", restored.TTL)
	}
	if restored.DiskUsage != 1048576 {
		t.Errorf("DiskUsage = %d, want 1048576", restored.DiskUsage)
	}
	if restored.DiskMeasuredAt == nil {
		t.Fatal("DiskMeasuredAt should not be nil")
	}
}
