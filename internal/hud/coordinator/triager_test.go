package coordinator

import (
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestTriager_FallbackTriage(t *testing.T) {
	triager := &Triager{}
	entries := []bridge.ContextEntryInfo{
		{Entry: bridge.ContextEntry{ID: "e1"}},
		{Entry: bridge.ContextEntry{ID: "e2"}},
		{Entry: bridge.ContextEntry{ID: "e3"}},
	}

	results := triager.fallbackTriage(entries)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Importance != "medium" {
			t.Errorf("result[%d] importance = %q, want medium", i, r.Importance)
		}
		if r.EntryID != entries[i].Entry.ID {
			t.Errorf("result[%d] entry_id = %q, want %q", i, r.EntryID, entries[i].Entry.ID)
		}
	}
}

func TestMemoryToContextEntries(t *testing.T) {
	items := []bridge.MemoryItem{
		{
			ID:       "m1",
			Title:    "Test Item",
			Content:  "Some content",
			Category: "decision",
		},
	}

	entries := memoryToContextEntries(items)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Entry.ID != "m1" {
		t.Errorf("expected ID m1, got %s", entries[0].Entry.ID)
	}
	if entries[0].Entry.Title != "Test Item" {
		t.Errorf("expected title 'Test Item', got %s", entries[0].Entry.Title)
	}
	if entries[0].Entry.EntryType != "decision" {
		t.Errorf("expected entry_type 'decision', got %s", entries[0].Entry.EntryType)
	}
}
