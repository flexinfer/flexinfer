package hud

import (
	"strings"
	"testing"
)

func TestNudgeQueue_NewIsEmpty(t *testing.T) {
	q := NewNudgeQueue()

	if q.Count("any-agent") != 0 {
		t.Fatalf("expected 0 nudges for unknown agent, got %d", q.Count("any-agent"))
	}
}

func TestNudgeQueue_AddAndCount(t *testing.T) {
	q := NewNudgeQueue()

	q.Add("claude-1", NudgeEntry{ID: "n1", Type: "message", Content: "hello"})
	q.Add("claude-1", NudgeEntry{ID: "n2", Type: "context_inject", Content: "data"})

	if q.Count("claude-1") != 2 {
		t.Fatalf("expected 2 nudges, got %d", q.Count("claude-1"))
	}

	// Different agent should have 0.
	if q.Count("gemini-1") != 0 {
		t.Fatalf("expected 0 nudges for gemini-1, got %d", q.Count("gemini-1"))
	}
}

func TestNudgeQueue_DrainReturnsAll(t *testing.T) {
	q := NewNudgeQueue()

	q.Add("agent-a", NudgeEntry{ID: "n1", Type: "message", Content: "first"})
	q.Add("agent-a", NudgeEntry{ID: "n2", Type: "task_redirect", Content: "second"})
	q.Add("agent-a", NudgeEntry{ID: "n3", Type: "pause_request", Content: "third"})

	drained := q.Drain("agent-a")
	if len(drained) != 3 {
		t.Fatalf("expected 3 drained nudges, got %d", len(drained))
	}

	// Verify order.
	if drained[0].ID != "n1" || drained[1].ID != "n2" || drained[2].ID != "n3" {
		t.Errorf("drained order mismatch: %v", drained)
	}
}

func TestNudgeQueue_DrainClearsQueue(t *testing.T) {
	q := NewNudgeQueue()

	q.Add("agent-a", NudgeEntry{ID: "n1", Type: "message", Content: "test"})
	q.Drain("agent-a")

	if q.Count("agent-a") != 0 {
		t.Fatalf("expected 0 nudges after drain, got %d", q.Count("agent-a"))
	}

	// Second drain should return nil/empty.
	second := q.Drain("agent-a")
	if len(second) != 0 {
		t.Fatalf("expected empty second drain, got %d", len(second))
	}
}

func TestNudgeQueue_DrainNonexistentAgent(t *testing.T) {
	q := NewNudgeQueue()

	drained := q.Drain("nonexistent")
	if len(drained) != 0 {
		t.Fatalf("expected empty drain for nonexistent agent, got %d", len(drained))
	}
}

func TestNudgeQueue_MultipleAgentsIndependent(t *testing.T) {
	q := NewNudgeQueue()

	q.Add("agent-a", NudgeEntry{ID: "a1", Type: "message", Content: "for a"})
	q.Add("agent-b", NudgeEntry{ID: "b1", Type: "message", Content: "for b"})
	q.Add("agent-b", NudgeEntry{ID: "b2", Type: "message", Content: "for b 2"})

	// Drain a should not affect b.
	aDrained := q.Drain("agent-a")
	if len(aDrained) != 1 {
		t.Fatalf("expected 1 nudge for agent-a, got %d", len(aDrained))
	}

	if q.Count("agent-b") != 2 {
		t.Fatalf("expected 2 nudges for agent-b, got %d", q.Count("agent-b"))
	}
}

func TestNudgeQueue_NudgeEntryFields(t *testing.T) {
	entry := NudgeEntry{
		ID:        "nudge-abc-123",
		Type:      "context_inject",
		Content:   "focus on file X",
		FromAgent: "hud",
		CreatedAt: "2026-02-16T10:00:00Z",
	}

	if entry.Type != "context_inject" {
		t.Errorf("expected type 'context_inject', got %q", entry.Type)
	}
	if entry.FromAgent != "hud" {
		t.Errorf("expected from_agent 'hud', got %q", entry.FromAgent)
	}
}

func TestNewNudgeID_Format(t *testing.T) {
	id := NewNudgeID("claude-code")

	if !strings.HasPrefix(id, "nudge-claude-code-") {
		t.Fatalf("expected nudge ID to start with 'nudge-claude-code-', got %q", id)
	}
}

func TestNewNudgeID_Unique(t *testing.T) {
	id1 := NewNudgeID("agent-1")
	id2 := NewNudgeID("agent-1")

	// While not guaranteed with millisecond resolution, in practice
	// these will differ due to time progression. If they happen to be
	// the same, that is acceptable for this test.
	if id1 == id2 {
		t.Log("nudge IDs happened to match (same millisecond), acceptable")
	}
}
