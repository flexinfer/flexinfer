package agentcontext

import (
	"testing"
	"time"
)

// TestFilterEntriesAfter_PartitionInvariant asserts that chaining the
// cursor returned from one page into the next page produces a partition
// of the underlying entry set: every entry surfaces exactly once, no
// overlap, no gap.
func TestFilterEntriesAfter_PartitionInvariant(t *testing.T) {
	t.Parallel()

	base := time.Unix(1700000000, 0)
	mk := func(id string, offset time.Duration) ContextEntry {
		return ContextEntry{ID: id, Timestamp: base.Add(offset)}
	}

	// Unordered on purpose — helper must sort.
	entries := []ContextEntry{
		mk("e5", 5*time.Second),
		mk("e1", 1*time.Second),
		mk("e4", 4*time.Second),
		mk("e2", 2*time.Second),
		mk("e3", 3*time.Second),
		mk("e6", 6*time.Second),
		mk("e7", 7*time.Second),
	}

	limit := 3
	seen := map[string]int{}

	var cursorNs int64
	var pages int
	for pages = 0; pages < 10; pages++ {
		page := filterEntriesAfter(entries, cursorNs, limit)
		if len(page) == 0 {
			break
		}
		for _, e := range page {
			seen[e.ID]++
		}
		cursorNs = page[len(page)-1].Timestamp.UnixNano()
	}

	if pages == 10 {
		t.Fatalf("infinite loop: pages did not terminate")
	}
	if len(seen) != len(entries) {
		t.Fatalf("partition missed entries: seen=%d entries=%d", len(seen), len(entries))
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("entry %s appeared %d times; want 1 (no overlap)", id, count)
		}
	}
}

// TestFilterEntriesAfter_StrictlyGreater asserts the comparison is strict:
// an entry whose Timestamp equals the cursor is NOT returned.
func TestFilterEntriesAfter_StrictlyGreater(t *testing.T) {
	t.Parallel()

	base := time.Unix(1700000000, 0)
	entries := []ContextEntry{
		{ID: "a", Timestamp: base},
		{ID: "b", Timestamp: base.Add(time.Second)},
	}
	out := filterEntriesAfter(entries, base.UnixNano(), 10)
	if len(out) != 1 || out[0].ID != "b" {
		t.Fatalf("strict-greater violated: got %v", out)
	}
}

// TestFilterEntriesAfter_LimitCaps ensures limit truncates the sorted result.
func TestFilterEntriesAfter_LimitCaps(t *testing.T) {
	t.Parallel()

	base := time.Unix(1700000000, 0)
	entries := []ContextEntry{
		{ID: "c", Timestamp: base.Add(3 * time.Second)},
		{ID: "a", Timestamp: base.Add(1 * time.Second)},
		{ID: "b", Timestamp: base.Add(2 * time.Second)},
	}
	out := filterEntriesAfter(entries, 0, 2)
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
	if out[0].ID != "a" || out[1].ID != "b" {
		t.Fatalf("unexpected order: %+v", out)
	}
}

// TestFilterEntriesAfter_Empty returns an empty slice when no entries are
// newer than the cursor.
func TestFilterEntriesAfter_Empty(t *testing.T) {
	t.Parallel()

	base := time.Unix(1700000000, 0)
	entries := []ContextEntry{
		{ID: "a", Timestamp: base},
	}
	out := filterEntriesAfter(entries, base.Add(time.Hour).UnixNano(), 10)
	if len(out) != 0 {
		t.Fatalf("expected empty slice, got %v", out)
	}
}
