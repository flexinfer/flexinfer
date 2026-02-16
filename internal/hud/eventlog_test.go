package hud

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewEventLog_DefaultCapacity(t *testing.T) {
	// Zero capacity should default to 1000.
	el := NewEventLog(0)
	if el.cap != 1000 {
		t.Fatalf("expected default capacity 1000, got %d", el.cap)
	}

	// Negative capacity should also default to 1000.
	el = NewEventLog(-5)
	if el.cap != 1000 {
		t.Fatalf("expected default capacity 1000, got %d", el.cap)
	}
}

func TestNewEventLog_CustomCapacity(t *testing.T) {
	el := NewEventLog(50)
	if el.cap != 50 {
		t.Fatalf("expected capacity 50, got %d", el.cap)
	}
}

func TestEventLog_AppendAndLen(t *testing.T) {
	el := NewEventLog(10)

	if el.Len() != 0 {
		t.Fatalf("expected 0 entries, got %d", el.Len())
	}

	el.Append(TimelineEntry{
		EventType: "session.start",
		AgentID:   "claude-1",
		Timestamp: time.Now(),
	})
	if el.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", el.Len())
	}

	el.Append(TimelineEntry{
		EventType: "session.end",
		AgentID:   "claude-1",
		Timestamp: time.Now(),
	})
	if el.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", el.Len())
	}
}

func TestEventLog_AllReturnsNewestFirst(t *testing.T) {
	el := NewEventLog(10)

	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)

	el.Append(TimelineEntry{EventType: "first", Timestamp: t1})
	el.Append(TimelineEntry{EventType: "second", Timestamp: t2})
	el.Append(TimelineEntry{EventType: "third", Timestamp: t3})

	all := el.All(0)
	if len(all) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(all))
	}

	// Newest-first order: third, second, first.
	if all[0].EventType != "third" {
		t.Errorf("expected newest entry first, got %q", all[0].EventType)
	}
	if all[1].EventType != "second" {
		t.Errorf("expected second entry, got %q", all[1].EventType)
	}
	if all[2].EventType != "first" {
		t.Errorf("expected oldest entry last, got %q", all[2].EventType)
	}
}

func TestEventLog_AllWithLimit(t *testing.T) {
	el := NewEventLog(10)

	for i := 0; i < 5; i++ {
		el.Append(TimelineEntry{EventType: "event", Timestamp: time.Now()})
	}

	limited := el.All(3)
	if len(limited) != 3 {
		t.Fatalf("expected 3 entries with limit, got %d", len(limited))
	}
}

func TestEventLog_AllWithZeroLimit(t *testing.T) {
	el := NewEventLog(10)

	for i := 0; i < 5; i++ {
		el.Append(TimelineEntry{EventType: "event", Timestamp: time.Now()})
	}

	all := el.All(0)
	if len(all) != 5 {
		t.Fatalf("expected 5 entries with zero limit, got %d", len(all))
	}
}

func TestEventLog_AllEmpty(t *testing.T) {
	el := NewEventLog(10)

	all := el.All(10)
	if all != nil {
		t.Fatalf("expected nil for empty log, got %v", all)
	}
}

func TestEventLog_WrapAround(t *testing.T) {
	el := NewEventLog(3)

	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	t4 := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	t5 := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)

	el.Append(TimelineEntry{EventType: "a", Timestamp: t1})
	el.Append(TimelineEntry{EventType: "b", Timestamp: t2})
	el.Append(TimelineEntry{EventType: "c", Timestamp: t3})
	el.Append(TimelineEntry{EventType: "d", Timestamp: t4})
	el.Append(TimelineEntry{EventType: "e", Timestamp: t5})

	if el.Len() != 3 {
		t.Fatalf("expected 3 entries after wrap, got %d", el.Len())
	}

	all := el.All(0)
	// Should contain most recent 3: e, d, c (newest-first).
	if all[0].EventType != "e" {
		t.Errorf("expected 'e' first, got %q", all[0].EventType)
	}
	if all[1].EventType != "d" {
		t.Errorf("expected 'd' second, got %q", all[1].EventType)
	}
	if all[2].EventType != "c" {
		t.Errorf("expected 'c' third, got %q", all[2].EventType)
	}
}

func TestEventLog_SinceFilters(t *testing.T) {
	el := NewEventLog(10)

	t1 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
	t4 := time.Date(2026, 1, 4, 12, 0, 0, 0, time.UTC)

	el.Append(TimelineEntry{EventType: "a", Timestamp: t1})
	el.Append(TimelineEntry{EventType: "b", Timestamp: t2})
	el.Append(TimelineEntry{EventType: "c", Timestamp: t3})
	el.Append(TimelineEntry{EventType: "d", Timestamp: t4})

	// Since t2 should return t3 and t4 only (entries strictly after t2).
	since := el.Since(t2, 10)
	if len(since) != 2 {
		t.Fatalf("expected 2 entries since t2, got %d", len(since))
	}
	if since[0].EventType != "d" {
		t.Errorf("expected 'd' first, got %q", since[0].EventType)
	}
	if since[1].EventType != "c" {
		t.Errorf("expected 'c' second, got %q", since[1].EventType)
	}
}

func TestEventLog_SinceWithLimit(t *testing.T) {
	el := NewEventLog(10)

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		el.Append(TimelineEntry{
			EventType: "event",
			Timestamp: base.Add(time.Duration(i) * time.Hour),
		})
	}

	// Since the beginning with limit 3 should return only 3 entries.
	since := el.Since(base.Add(-time.Hour), 3)
	if len(since) != 3 {
		t.Fatalf("expected 3 entries with limit, got %d", len(since))
	}
}

func TestEventLog_SinceNoResults(t *testing.T) {
	el := NewEventLog(10)

	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	el.Append(TimelineEntry{EventType: "old", Timestamp: past})

	// Since a future time should return no entries.
	future := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	since := el.Since(future, 10)
	if len(since) != 0 {
		t.Fatalf("expected 0 entries since future time, got %d", len(since))
	}
}

func TestTimelineEntry_JSONSerialization(t *testing.T) {
	entry := TimelineEntry{
		Timestamp: time.Date(2026, 2, 16, 10, 30, 0, 0, time.UTC),
		EventType: "agent.session.start",
		AgentID:   "claude-code",
		AgentType: "claude-code",
		Data:      json.RawMessage(`{"session_id":"sess-1"}`),
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded TimelineEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.EventType != "agent.session.start" {
		t.Errorf("expected event_type 'agent.session.start', got %q", decoded.EventType)
	}
	if decoded.AgentID != "claude-code" {
		t.Errorf("expected agent_id 'claude-code', got %q", decoded.AgentID)
	}
}
