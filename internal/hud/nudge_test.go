package hud

import (
	"strings"
	"testing"
	"time"
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
		Lane:      "control",
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
	if entry.Lane != "control" {
		t.Errorf("expected lane 'control', got %q", entry.Lane)
	}
}

func TestNewNudgeID_Format(t *testing.T) {
	id := NewNudgeID("claude-code")

	if !strings.HasPrefix(id, "nudge-claude-code-") {
		t.Fatalf("expected nudge ID to start with 'nudge-claude-code-', got %q", id)
	}
}

func TestNewNudgeID_Unique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id := NewNudgeID("agent-1")
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate nudge ID generated: %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNudgeQueue_DrainRespectsLanePriority(t *testing.T) {
	q := NewNudgeQueueWithConfig(NudgeQueueConfig{
		Cap:        20,
		DropPolicy: DropPolicySummarize,
		LanePriority: []string{
			"control",
			"handoff",
			"advice",
			"default",
		},
	})

	q.Add("agent-1", NudgeEntry{ID: "n1", Type: "message", Lane: "advice", Content: "advice"})
	q.Add("agent-1", NudgeEntry{ID: "n2", Type: "task_redirect", Lane: "handoff", Content: "handoff"})
	q.Add("agent-1", NudgeEntry{ID: "n3", Type: "context_inject", Lane: "control", Content: "control"})

	drained := q.Drain("agent-1")
	if len(drained) != 3 {
		t.Fatalf("expected 3 nudges, got %d", len(drained))
	}
	if drained[0].ID != "n3" || drained[1].ID != "n2" || drained[2].ID != "n1" {
		t.Fatalf("unexpected drain order: %+v", drained)
	}
}

func TestNudgeQueue_DropPolicyDropNew(t *testing.T) {
	q := NewNudgeQueueWithConfig(NudgeQueueConfig{
		Cap:        1,
		DropPolicy: DropPolicyDropNew,
	})

	q.Add("agent-1", NudgeEntry{ID: "n1", Type: "message", Content: "keep"})
	q.Add("agent-1", NudgeEntry{ID: "n2", Type: "message", Content: "drop"})

	drained := q.Drain("agent-1")
	if len(drained) != 1 {
		t.Fatalf("expected 1 nudge after drop_new, got %d", len(drained))
	}
	if drained[0].ID != "n1" {
		t.Fatalf("expected first item to be retained, got %q", drained[0].ID)
	}
}

func TestNudgeQueue_DropPolicySummarizeAddsSummaryEntry(t *testing.T) {
	q := NewNudgeQueueWithConfig(NudgeQueueConfig{
		Cap:        1,
		DropPolicy: DropPolicySummarize,
	})

	q.Add("agent-1", NudgeEntry{ID: "n1", Type: "message", Content: "first"})
	time.Sleep(2 * time.Millisecond)
	q.Add("agent-1", NudgeEntry{ID: "n2", Type: "message", Content: "second"})

	drained := q.Drain("agent-1")
	if len(drained) != 2 {
		t.Fatalf("expected summary + retained nudge, got %d", len(drained))
	}
	if drained[0].Type != "queue_summary" {
		t.Fatalf("expected first nudge to be queue_summary, got %q", drained[0].Type)
	}
	if drained[1].ID != "n2" {
		t.Fatalf("expected newest item to remain after summarize overflow, got %q", drained[1].ID)
	}
	if !strings.Contains(drained[0].Content, "dropped 1 item") {
		t.Fatalf("expected summary content to mention dropped count, got %q", drained[0].Content)
	}
}

func TestNudgeQueue_DebounceDelaysDrain(t *testing.T) {
	q := NewNudgeQueueWithConfig(NudgeQueueConfig{
		Debounce:   50 * time.Millisecond,
		Cap:        10,
		DropPolicy: DropPolicySummarize,
	})

	q.Add("agent-1", NudgeEntry{ID: "n1", Type: "message", Content: "wait"})
	if got := q.Drain("agent-1"); len(got) != 0 {
		t.Fatalf("expected debounced drain to return 0, got %d", len(got))
	}

	time.Sleep(60 * time.Millisecond)
	if got := q.Drain("agent-1"); len(got) != 1 {
		t.Fatalf("expected drain after debounce to return 1, got %d", len(got))
	}
}

func TestNudgeQueue_StatusIncludesByLane(t *testing.T) {
	q := NewNudgeQueueWithConfig(NudgeQueueConfig{
		Cap:        10,
		DropPolicy: DropPolicySummarize,
	})

	q.Add("agent-1", NudgeEntry{ID: "c1", Type: "context_inject", Lane: "control", Content: "c"})
	q.Add("agent-1", NudgeEntry{ID: "a1", Type: "message", Lane: "advice", Content: "a"})

	status := q.Status("agent-1")
	if status.Pending != 2 {
		t.Fatalf("expected pending=2, got %d", status.Pending)
	}
	if status.ByLane["control"] != 1 || status.ByLane["advice"] != 1 {
		t.Fatalf("unexpected lane counts: %+v", status.ByLane)
	}
	if status.Cap != 10 {
		t.Fatalf("expected cap=10, got %d", status.Cap)
	}
}

func TestNudgeQueue_UpdateConfigRejectsInvalidDropPolicy(t *testing.T) {
	q := NewNudgeQueue()
	policy := "invalid"

	_, err := q.UpdateConfig(NudgeQueuePolicyUpdate{DropPolicy: &policy})
	if err == nil {
		t.Fatalf("expected error for invalid drop policy")
	}
}

func TestNudgeQueue_UpdateConfigAppliesAndNormalizes(t *testing.T) {
	q := NewNudgeQueue()

	capValue := 8
	debounceMs := 15
	dropPolicy := "drop_new"

	updated, err := q.UpdateConfig(NudgeQueuePolicyUpdate{
		Cap:        &capValue,
		DebounceMs: &debounceMs,
		DropPolicy: &dropPolicy,
		LanePriority: []string{
			" Control ",
			"handoff",
			"control",
			"advice",
		},
	})
	if err != nil {
		t.Fatalf("update config failed: %v", err)
	}

	if updated.Cap != 8 {
		t.Fatalf("expected cap=8, got %d", updated.Cap)
	}
	if updated.Debounce != 15*time.Millisecond {
		t.Fatalf("expected debounce=15ms, got %s", updated.Debounce)
	}
	if updated.DropPolicy != DropPolicyDropNew {
		t.Fatalf("expected drop policy drop_new, got %q", updated.DropPolicy)
	}
	if len(updated.LanePriority) != 3 {
		t.Fatalf("expected deduplicated lane priority length 3, got %d", len(updated.LanePriority))
	}
	if updated.LanePriority[0] != "control" || updated.LanePriority[1] != "handoff" || updated.LanePriority[2] != "advice" {
		t.Fatalf("unexpected lane priority: %+v", updated.LanePriority)
	}
}
