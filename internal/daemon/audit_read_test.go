package daemon

import (
	"testing"
	"time"
)

func TestParseAuditTimestamp_Empty(t *testing.T) {
	ts, err := parseAuditTimestamp("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ts.IsZero() {
		t.Errorf("expected zero time, got %v", ts)
	}
}

func TestParseAuditTimestamp_RFC3339Nano(t *testing.T) {
	input := "2026-03-24T12:30:45.123456789Z"
	ts, err := parseAuditTimestamp(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts.Year() != 2026 || ts.Month() != 3 || ts.Day() != 24 {
		t.Errorf("parsed date wrong: %v", ts)
	}
	if ts.Nanosecond() != 123456789 {
		t.Errorf("nanoseconds = %d, want 123456789", ts.Nanosecond())
	}
}

func TestParseAuditTimestamp_RFC3339(t *testing.T) {
	input := "2026-03-24T12:30:45Z"
	ts, err := parseAuditTimestamp(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts.Year() != 2026 || ts.Hour() != 12 {
		t.Errorf("parsed wrong: %v", ts)
	}
}

func TestParseAuditTimestamp_Invalid(t *testing.T) {
	_, err := parseAuditTimestamp("not-a-timestamp")
	if err == nil {
		t.Error("expected error for invalid timestamp")
	}
}

func TestAppendAuditEntry_NoLimit(t *testing.T) {
	var entries []AuditEntry
	for i := range 5 {
		entries = appendAuditEntry(entries, AuditEntry{AgentID: string(rune('a' + i))}, 0)
	}
	if len(entries) != 5 {
		t.Errorf("got %d entries, want 5", len(entries))
	}
}

func TestAppendAuditEntry_WithLimit(t *testing.T) {
	var entries []AuditEntry
	for i := range 10 {
		entries = appendAuditEntry(entries, AuditEntry{AgentID: string(rune('a' + i))}, 3)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	// Should keep the last 3 entries: h, i, j
	if entries[0].AgentID != "h" {
		t.Errorf("first entry AgentID = %q, want %q", entries[0].AgentID, "h")
	}
}

func TestAppendAuditEntry_LimitOne(t *testing.T) {
	var entries []AuditEntry
	entries = appendAuditEntry(entries, AuditEntry{AgentID: "first"}, 1)
	entries = appendAuditEntry(entries, AuditEntry{AgentID: "second"}, 1)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].AgentID != "second" {
		t.Errorf("AgentID = %q, want %q", entries[0].AgentID, "second")
	}
}

func TestReadAuditEntriesFallback_Empty(t *testing.T) {
	entries, err := readAuditEntriesFallback([]byte(""), AuditReadOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

func TestReadAuditEntriesFallback_ValidJSONL(t *testing.T) {
	data := []byte(`{"agent_id":"a","server":"s","tool":"t","status":"success"}
{"agent_id":"b","server":"s","tool":"t2","status":"error","error":"fail"}
`)
	entries, err := readAuditEntriesFallback(data, AuditReadOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].AgentID != "a" {
		t.Errorf("entries[0].AgentID = %q, want %q", entries[0].AgentID, "a")
	}
	if entries[1].Error != "fail" {
		t.Errorf("entries[1].Error = %q, want %q", entries[1].Error, "fail")
	}
}

func TestReadAuditEntriesFallback_WithLimit(t *testing.T) {
	data := []byte(`{"agent_id":"a","server":"s","tool":"t","status":"s"}
{"agent_id":"b","server":"s","tool":"t","status":"s"}
{"agent_id":"c","server":"s","tool":"t","status":"s"}
`)
	entries, err := readAuditEntriesFallback(data, AuditReadOptions{Limit: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	// Should keep last 2.
	if entries[0].AgentID != "b" {
		t.Errorf("entries[0].AgentID = %q, want %q", entries[0].AgentID, "b")
	}
}

func TestReadAuditEntriesFallback_BlankLines(t *testing.T) {
	data := []byte(`{"agent_id":"a","server":"s","tool":"t","status":"s"}

{"agent_id":"b","server":"s","tool":"t","status":"s"}
`)
	entries, err := readAuditEntriesFallback(data, AuditReadOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("blank lines should be skipped, got %d entries", len(entries))
	}
}

func TestReadAuditEntriesFallback_InvalidJSON(t *testing.T) {
	data := []byte(`not json at all`)
	_, err := readAuditEntriesFallback(data, AuditReadOptions{})
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSummarizeAuditEntriesFallback_Empty(t *testing.T) {
	summary := summarizeAuditEntriesFallback(nil)
	if summary.TotalEvents != 0 {
		t.Errorf("TotalEvents = %d, want 0", summary.TotalEvents)
	}
}

func TestSummarizeAuditEntriesFallback_Counts(t *testing.T) {
	now := time.Date(2026, 3, 24, 10, 0, 0, 0, time.UTC)
	entries := []AuditEntry{
		{Timestamp: now, AgentID: "a", AgentType: "worker", Status: "success"},
		{Timestamp: now.Add(time.Second), AgentID: "a", AgentType: "worker", Status: "error"},
		{Timestamp: now.Add(2 * time.Second), AgentID: "b", AgentType: "coordinator", Status: "success"},
	}
	summary := summarizeAuditEntriesFallback(entries)

	if summary.TotalEvents != 3 {
		t.Errorf("TotalEvents = %d, want 3", summary.TotalEvents)
	}
	if summary.ByEventType["success"] != 2 {
		t.Errorf("ByEventType[success] = %d, want 2", summary.ByEventType["success"])
	}
	if summary.ByEventType["error"] != 1 {
		t.Errorf("ByEventType[error] = %d, want 1", summary.ByEventType["error"])
	}
	if summary.ByActorID["a"] != 2 {
		t.Errorf("ByActorID[a] = %d, want 2", summary.ByActorID["a"])
	}
	if summary.ByActorType["worker"] != 2 {
		t.Errorf("ByActorType[worker] = %d, want 2", summary.ByActorType["worker"])
	}
	if summary.OldestTimestamp == nil {
		t.Fatal("expected OldestTimestamp to be set")
	}
	if summary.NewestTimestamp == nil {
		t.Fatal("expected NewestTimestamp to be set")
	}
}

func TestSummarizeAuditEntriesFallback_ZeroTimestamp(t *testing.T) {
	entries := []AuditEntry{
		{AgentID: "a", Status: "success"}, // zero timestamp
	}
	summary := summarizeAuditEntriesFallback(entries)
	if summary.OldestTimestamp != nil {
		t.Error("expected nil OldestTimestamp for zero-timestamp entries")
	}
}
