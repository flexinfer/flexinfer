package agentcontext

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// stubSummarizer is a minimal CompactionSummarizer used in tests.
type stubSummarizer struct {
	fn func(ctx context.Context, entries []ContextEntry) (string, error)
}

func (s stubSummarizer) Summarize(ctx context.Context, entries []ContextEntry) (string, error) {
	return s.fn(ctx, entries)
}

// TestLLMCompaction_ReplacesEntriesWithSummary verifies the happy path: when
// Mode == "llm" and a summarizer is configured, the scheduler collects
// working-memory items, synthesizes a single summary, pins originals, and
// removes them from working memory.
func TestLLMCompaction_ReplacesEntriesWithSummary(t *testing.T) {
	hierarchy := NewMemoryHierarchy()
	now := time.Now().UTC()

	for _, id := range []string{"one", "two", "three"} {
		if err := hierarchy.AddItem(&MemoryItem{
			ID:             id,
			Title:          "t-" + id,
			Content:        "content-" + id,
			Tier:           MemoryTierWorking,
			Category:       "finding",
			CreatedAt:      now.Add(-2 * time.Hour),
			OriginalTokens: 10,
		}); err != nil {
			t.Fatalf("add item %s: %v", id, err)
		}
	}

	cfg := DefaultCompactionConfig()
	cfg.Mode = "llm"
	cfg.PinRawFor = 30 * time.Minute
	cfg.MaxItemsPerRun = 10

	scheduler := NewCompactionScheduler(cfg, hierarchy, func(_ context.Context, _ string) (string, error) {
		return "unused", nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	scheduler.SetSummarizer(stubSummarizer{
		fn: func(_ context.Context, entries []ContextEntry) (string, error) {
			if len(entries) != 3 {
				t.Errorf("expected 3 entries, got %d", len(entries))
			}
			return "a condensed summary of three items", nil
		},
	})
	pinStore := NewMemoryPinnedStore()
	scheduler.SetPinnedStore(pinStore)

	stats, err := scheduler.runCompaction(context.Background())
	if err != nil {
		t.Fatalf("runCompaction: %v", err)
	}
	if stats.ItemsCompressed != 3 {
		t.Fatalf("ItemsCompressed = %d, want 3", stats.ItemsCompressed)
	}

	// Originals should be gone from working memory.
	for _, id := range []string{"one", "two", "three"} {
		if _, err := hierarchy.GetItem(id); err == nil {
			t.Errorf("expected item %s to be removed", id)
		}
	}

	// Pins should be live.
	for _, id := range []string{"one", "two", "three"} {
		pinned, perr := pinStore.IsPinned(context.Background(), id)
		if perr != nil {
			t.Fatalf("IsPinned: %v", perr)
		}
		if !pinned {
			t.Errorf("expected %s to be pinned", id)
		}
	}
}

// TestLLMCompaction_FallsBackToExtractive verifies that a summarizer error
// causes the scheduler to fall through to the existing extractive path and
// bump the fallback metric.
func TestLLMCompaction_FallsBackToExtractive(t *testing.T) {
	hierarchy := NewMemoryHierarchy()
	now := time.Now().UTC()

	if err := hierarchy.AddItem(&MemoryItem{
		ID:             "falls-back",
		Title:          "original",
		Content:        "original content",
		Tier:           MemoryTierWorking,
		Category:       "finding",
		CreatedAt:      now.Add(-2 * time.Hour),
		OriginalTokens: 10,
	}); err != nil {
		t.Fatalf("add item: %v", err)
	}

	cfg := CompactionConfig{
		Enabled:                  true,
		WorkingMemoryThreshold:   0,
		ShortTermMemoryThreshold: 0,
		LongTermMemoryThreshold:  0,
		TargetCapacity:           0,
		MinAgeBeforeCompaction:   0,
		SummarizationDepth:       3,
		MaxItemsPerRun:           10,
		Mode:                     "llm",
		PinRawFor:                time.Minute,
	}

	before := GetMetrics().CompactionFallbacks.Load()

	scheduler := NewCompactionScheduler(cfg, hierarchy, func(_ context.Context, _ string) (string, error) {
		return "extractive-out", nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	scheduler.SetSummarizer(stubSummarizer{
		fn: func(_ context.Context, _ []ContextEntry) (string, error) {
			return "", errors.New("llm unavailable")
		},
	})

	if _, err := scheduler.runCompaction(context.Background()); err != nil {
		t.Fatalf("runCompaction: %v", err)
	}

	if got := GetMetrics().CompactionFallbacks.Load(); got <= before {
		t.Fatalf("CompactionFallbacks = %d, want > %d", got, before)
	}

	// Extractive path should have rewritten the content.
	item, err := hierarchy.GetItem("falls-back")
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if item.Content != "extractive-out" {
		t.Fatalf("content = %q, want extractive-out", item.Content)
	}
}

// TestMemoryPinnedStore_PinLifecycle verifies that MemoryPinnedStore retains
// pins until their expiry and rejects lookups thereafter.
func TestMemoryPinnedStore_PinLifecycle(t *testing.T) {
	store := NewMemoryPinnedStore()
	ctx := context.Background()

	until := time.Now().Add(50 * time.Millisecond)
	if err := store.Pin(ctx, []string{"alpha", "beta"}, until); err != nil {
		t.Fatalf("Pin: %v", err)
	}

	pinned, err := store.IsPinned(ctx, "alpha")
	if err != nil {
		t.Fatalf("IsPinned: %v", err)
	}
	if !pinned {
		t.Fatal("expected alpha pinned right after Pin")
	}

	// Wait past expiry.
	time.Sleep(75 * time.Millisecond)

	pinned, err = store.IsPinned(ctx, "alpha")
	if err != nil {
		t.Fatalf("IsPinned after expiry: %v", err)
	}
	if pinned {
		t.Fatal("expected alpha unpinned after expiry")
	}

	// Purge is a no-op on already-lazy-purged entries.
	if err := store.Purge(ctx); err != nil {
		t.Fatalf("Purge: %v", err)
	}
}
