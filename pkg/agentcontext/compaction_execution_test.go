package agentcontext

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestRunCompaction_CompressesAndPromotes(t *testing.T) {
	hierarchy := NewMemoryHierarchy()
	now := time.Now().UTC()

	if err := hierarchy.AddItem(&MemoryItem{
		ID:          "compress",
		Title:       "Compress me",
		Content:     "this content should be shortened",
		Tier:        MemoryTierWorking,
		Category:    "finding",
		CreatedAt:   now.Add(-2 * time.Hour),
		AccessCount: 1,
	}); err != nil {
		t.Fatalf("add compress item: %v", err)
	}
	if err := hierarchy.AddItem(&MemoryItem{
		ID:              "promote",
		Title:           "Promote me",
		Content:         "high signal",
		Tier:            MemoryTierWorking,
		Category:        "decision",
		CreatedAt:       now.Add(-2 * time.Hour),
		ImportanceScore: 0.9,
		AccessCount:     3,
	}); err != nil {
		t.Fatalf("add promoted item: %v", err)
	}

	scheduler := NewCompactionScheduler(CompactionConfig{
		Enabled:                  true,
		WorkingMemoryThreshold:   0,
		ShortTermMemoryThreshold: 0,
		LongTermMemoryThreshold:  0,
		TargetCapacity:           0,
		MinAgeBeforeCompaction:   0,
		SummarizationDepth:       3,
		MaxItemsPerRun:           10,
	}, hierarchy, func(ctx context.Context, content string) (string, error) {
		return "compressed", nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	stats, err := scheduler.runCompaction(context.Background())
	if err != nil {
		t.Fatalf("runCompaction: %v", err)
	}

	if stats.ItemsPromoted < 1 {
		t.Fatalf("promoted = %d, want at least 1", stats.ItemsPromoted)
	}
	if stats.ItemsCompressed < 1 {
		t.Fatalf("compressed = %d, want at least 1", stats.ItemsCompressed)
	}

	compressedItem, err := hierarchy.GetItem("compress")
	if err != nil {
		t.Fatalf("get compressed item: %v", err)
	}
	if compressedItem.Content != "compressed" {
		t.Fatalf("content = %q, want compressed", compressedItem.Content)
	}
	if compressedItem.CompressedAt == nil {
		t.Fatal("expected compressed timestamp")
	}

	promotedItem, err := hierarchy.GetItem("promote")
	if err != nil {
		t.Fatalf("get promoted item: %v", err)
	}
	if promotedItem.Tier != MemoryTierShortTerm {
		t.Fatalf("tier = %q, want %q", promotedItem.Tier, MemoryTierShortTerm)
	}
}

func TestCompactSession_CompressesOnlyOldSessionItems(t *testing.T) {
	hierarchy := NewMemoryHierarchy()
	now := time.Now().UTC()

	for _, item := range []*MemoryItem{
		{
			ID:        "old",
			Title:     "Old session item",
			Content:   "old content",
			SessionID: "sess-1",
			CreatedAt: now.Add(-time.Hour),
		},
		{
			ID:        "fresh",
			Title:     "Fresh session item",
			Content:   "fresh content",
			SessionID: "sess-1",
			CreatedAt: now.Add(-5 * time.Minute),
		},
	} {
		if err := hierarchy.AddItem(item); err != nil {
			t.Fatalf("add item %s: %v", item.ID, err)
		}
	}

	scheduler := NewCompactionScheduler(DefaultCompactionConfig(), hierarchy, func(ctx context.Context, content string) (string, error) {
		return "session-summary", nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	stats, err := scheduler.CompactSession(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("CompactSession: %v", err)
	}
	if stats.ItemsProcessed != 1 {
		t.Fatalf("processed = %d, want 1", stats.ItemsProcessed)
	}
	if stats.ItemsCompressed != 1 {
		t.Fatalf("compressed = %d, want 1", stats.ItemsCompressed)
	}

	oldItem, err := hierarchy.GetItem("old")
	if err != nil {
		t.Fatalf("get old item: %v", err)
	}
	if oldItem.Content != "session-summary" {
		t.Fatalf("old content = %q, want session-summary", oldItem.Content)
	}

	freshItem, err := hierarchy.GetItem("fresh")
	if err != nil {
		t.Fatalf("get fresh item: %v", err)
	}
	if freshItem.Content != "fresh content" {
		t.Fatalf("fresh content = %q, want unchanged", freshItem.Content)
	}
}
