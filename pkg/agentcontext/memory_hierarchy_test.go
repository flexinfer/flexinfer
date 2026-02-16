package agentcontext

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestMemoryHierarchy_AddItem(t *testing.T) {
	mh := NewMemoryHierarchy()

	item := &MemoryItem{
		Title:      "Test Decision",
		Content:    "We decided to use PostgreSQL for the database.",
		Category:   "decision",
		Importance: ImportanceLevelHigh,
		Tags:       []string{"database", "architecture"},
	}

	err := mh.AddItem(item)
	if err != nil {
		t.Fatalf("AddItem failed: %v", err)
	}

	if item.ID == "" {
		t.Error("expected item ID to be set")
	}

	if item.Tier != MemoryTierWorking {
		t.Errorf("expected default tier to be working, got %q", item.Tier)
	}

	if item.Status != MemoryItemStatusActive {
		t.Errorf("expected status to be active, got %q", item.Status)
	}

	// Verify we can retrieve it
	retrieved, err := mh.GetItem(item.ID)
	if err != nil {
		t.Fatalf("GetItem failed: %v", err)
	}

	if retrieved.Title != "Test Decision" {
		t.Errorf("expected title 'Test Decision', got %q", retrieved.Title)
	}

	if retrieved.AccessCount != 1 {
		t.Errorf("expected access count 1, got %d", retrieved.AccessCount)
	}
}

func TestMemoryHierarchy_AddItem_Validation(t *testing.T) {
	mh := NewMemoryHierarchy()

	// Missing title
	err := mh.AddItem(&MemoryItem{Content: "content"})
	if err == nil {
		t.Error("expected error for missing title")
	}

	// Missing content
	err = mh.AddItem(&MemoryItem{Title: "title"})
	if err == nil {
		t.Error("expected error for missing content")
	}
}

func TestMemoryHierarchy_Recall(t *testing.T) {
	mh := NewMemoryHierarchy()

	// Add items to different tiers
	mh.AddItem(&MemoryItem{
		Title:      "Working Item 1",
		Content:    "Content for working item 1",
		Tier:       MemoryTierWorking,
		Importance: ImportanceLevelHigh,
		Category:   "insight",
	})
	mh.AddItem(&MemoryItem{
		Title:      "Working Item 2",
		Content:    "Content for working item 2",
		Tier:       MemoryTierWorking,
		Importance: ImportanceLevelMedium,
		Category:   "decision",
	})
	mh.AddItem(&MemoryItem{
		Title:      "Short Term Item",
		Content:    "Content for short term item",
		Tier:       MemoryTierShortTerm,
		Importance: ImportanceLevelLow,
		Category:   "insight",
	})

	// Recall all
	result, err := mh.Recall(MemoryRecallRequest{})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}

	if len(result.Items) != 3 {
		t.Errorf("expected 3 items, got %d", len(result.Items))
	}

	// Items should be sorted by importance (high first)
	if result.Items[0].Importance != ImportanceLevelHigh {
		t.Error("expected highest importance item first")
	}

	// Recall by tier
	result, err = mh.Recall(MemoryRecallRequest{
		Tiers: []MemoryTier{MemoryTierWorking},
	})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}

	if len(result.Items) != 2 {
		t.Errorf("expected 2 working memory items, got %d", len(result.Items))
	}

	// Recall by category
	result, err = mh.Recall(MemoryRecallRequest{
		Categories: []string{"insight"},
	})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}

	if len(result.Items) != 2 {
		t.Errorf("expected 2 insight items, got %d", len(result.Items))
	}
}

func TestMemoryHierarchy_Recall_TokenBudget(t *testing.T) {
	mh := NewMemoryHierarchy()

	// Add items with known token counts
	for i := 0; i < 10; i++ {
		mh.AddItem(&MemoryItem{
			Title:   "Item",
			Content: "This is a test content that takes up some tokens for the memory test.",
		})
	}

	// Recall with small token budget
	result, err := mh.Recall(MemoryRecallRequest{
		TokenBudget: 50,
	})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}

	if !result.Truncated {
		t.Error("expected results to be truncated due to token budget")
	}

	if len(result.Items) >= 10 {
		t.Error("expected fewer items due to token budget")
	}
}

func TestMemoryHierarchy_Recall_Query(t *testing.T) {
	mh := NewMemoryHierarchy()

	mh.AddItem(&MemoryItem{
		Title:   "Database Design",
		Content: "We chose PostgreSQL for its reliability.",
	})
	mh.AddItem(&MemoryItem{
		Title:   "API Architecture",
		Content: "RESTful API with JSON responses.",
	})

	// Search for PostgreSQL
	result, err := mh.Recall(MemoryRecallRequest{
		Query: "PostgreSQL",
	})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}

	if len(result.Items) != 1 {
		t.Errorf("expected 1 item matching PostgreSQL, got %d", len(result.Items))
	}
}

func TestMemoryHierarchy_Promote(t *testing.T) {
	mh := NewMemoryHierarchy()

	item := &MemoryItem{
		Title:   "Promotable Item",
		Content: "This item will be promoted.",
		Tier:    MemoryTierWorking,
	}
	mh.AddItem(item)

	// Promote to short-term
	err := mh.PromoteItem(item.ID)
	if err != nil {
		t.Fatalf("PromoteItem failed: %v", err)
	}

	retrieved, _ := mh.GetItem(item.ID)
	if retrieved.Tier != MemoryTierShortTerm {
		t.Errorf("expected tier short_term, got %q", retrieved.Tier)
	}

	// Promote to long-term
	err = mh.PromoteItem(item.ID)
	if err != nil {
		t.Fatalf("PromoteItem failed: %v", err)
	}

	retrieved, _ = mh.GetItem(item.ID)
	if retrieved.Tier != MemoryTierLongTerm {
		t.Errorf("expected tier long_term, got %q", retrieved.Tier)
	}

	// Cannot promote beyond long-term
	err = mh.PromoteItem(item.ID)
	if err == nil {
		t.Error("expected error when promoting from long-term")
	}
}

func TestMemoryHierarchy_Demote(t *testing.T) {
	mh := NewMemoryHierarchy()

	item := &MemoryItem{
		Title:   "Demotable Item",
		Content: "This item will be demoted.",
		Tier:    MemoryTierLongTerm,
	}
	mh.AddItem(item)

	// Demote to short-term
	err := mh.DemoteItem(item.ID)
	if err != nil {
		t.Fatalf("DemoteItem failed: %v", err)
	}

	retrieved, _ := mh.GetItem(item.ID)
	if retrieved.Tier != MemoryTierShortTerm {
		t.Errorf("expected tier short_term, got %q", retrieved.Tier)
	}

	// Demote to working
	err = mh.DemoteItem(item.ID)
	if err != nil {
		t.Fatalf("DemoteItem failed: %v", err)
	}

	retrieved, _ = mh.GetItem(item.ID)
	if retrieved.Tier != MemoryTierWorking {
		t.Errorf("expected tier working, got %q", retrieved.Tier)
	}

	// Cannot demote from working
	err = mh.DemoteItem(item.ID)
	if err == nil {
		t.Error("expected error when demoting from working memory")
	}
}

func TestMemoryHierarchy_Compress(t *testing.T) {
	mh := NewMemoryHierarchy()

	item := &MemoryItem{
		Title:   "Compressible Item",
		Content: "This is a longer piece of content that should be compressed to reduce token usage. It contains multiple sentences and various details that can be summarized.",
	}
	mh.AddItem(item)

	originalTokens := item.OriginalTokens

	err := mh.CompressItem(item.ID)
	if err != nil {
		t.Fatalf("CompressItem failed: %v", err)
	}

	retrieved, _ := mh.GetItem(item.ID)
	if retrieved.Status != MemoryItemStatusCompressed {
		t.Errorf("expected status compressed, got %q", retrieved.Status)
	}

	if retrieved.Summary == "" {
		t.Error("expected summary to be set")
	}

	if retrieved.CompressedTokens >= originalTokens {
		t.Error("expected compressed tokens to be less than original")
	}

	if retrieved.CompressedAt == nil {
		t.Error("expected CompressedAt to be set")
	}
}

func TestMemoryHierarchy_Merge(t *testing.T) {
	mh := NewMemoryHierarchy()

	item1 := &MemoryItem{
		Title:      "Item 1",
		Content:    "Content of item 1",
		Importance: ImportanceLevelMedium,
		Tags:       []string{"tag1", "tag2"},
		Category:   "decision",
	}
	item2 := &MemoryItem{
		Title:      "Item 2",
		Content:    "Content of item 2",
		Importance: ImportanceLevelHigh,
		Tags:       []string{"tag2", "tag3"},
		Category:   "decision",
	}
	mh.AddItem(item1)
	mh.AddItem(item2)

	merged, err := mh.MergeItems([]string{item1.ID, item2.ID}, "Merged Decision")
	if err != nil {
		t.Fatalf("MergeItems failed: %v", err)
	}

	if merged.Title != "Merged Decision" {
		t.Errorf("expected title 'Merged Decision', got %q", merged.Title)
	}

	// Should have highest importance
	if merged.Importance != ImportanceLevelHigh {
		t.Errorf("expected importance high, got %q", merged.Importance)
	}

	// Should have all unique tags
	if len(merged.Tags) != 3 {
		t.Errorf("expected 3 unique tags, got %d", len(merged.Tags))
	}

	// Original items should be archived
	orig1, _ := mh.GetItem(item1.ID)
	if orig1.Status != MemoryItemStatusArchived {
		t.Errorf("expected original item to be archived, got %q", orig1.Status)
	}

	// Should track child IDs
	if len(merged.ChildIDs) != 2 {
		t.Errorf("expected 2 child IDs, got %d", len(merged.ChildIDs))
	}
}

func TestMemoryHierarchy_Merge_MinItems(t *testing.T) {
	mh := NewMemoryHierarchy()

	item := &MemoryItem{Title: "Single", Content: "content"}
	mh.AddItem(item)

	_, err := mh.MergeItems([]string{item.ID}, "Merged")
	if err == nil {
		t.Error("expected error when merging less than 2 items")
	}
}

func TestMemoryHierarchy_Delete(t *testing.T) {
	mh := NewMemoryHierarchy()

	item := &MemoryItem{
		Title:   "Deletable Item",
		Content: "This item will be deleted.",
	}
	mh.AddItem(item)

	err := mh.DeleteItem(item.ID)
	if err != nil {
		t.Fatalf("DeleteItem failed: %v", err)
	}

	_, err = mh.GetItem(item.ID)
	if err == nil {
		t.Error("expected error when getting deleted item")
	}
}

func TestMemoryHierarchy_Stats(t *testing.T) {
	mh := NewMemoryHierarchy()

	// Add items to different tiers
	mh.AddItem(&MemoryItem{
		Title:      "Working 1",
		Content:    "content",
		Tier:       MemoryTierWorking,
		Importance: ImportanceLevelHigh,
		Category:   "decision",
	})
	mh.AddItem(&MemoryItem{
		Title:      "Working 2",
		Content:    "content",
		Tier:       MemoryTierWorking,
		Importance: ImportanceLevelLow,
		Category:   "insight",
	})
	mh.AddItem(&MemoryItem{
		Title:      "Short Term 1",
		Content:    "content",
		Tier:       MemoryTierShortTerm,
		Importance: ImportanceLevelMedium,
		Category:   "decision",
	})
	mh.AddItem(&MemoryItem{
		Title:      "Long Term 1",
		Content:    "content",
		Tier:       MemoryTierLongTerm,
		Importance: ImportanceLevelCritical,
		Category:   "pattern",
	})

	stats := mh.Stats()

	if stats.TotalItems != 4 {
		t.Errorf("expected 4 total items, got %d", stats.TotalItems)
	}

	if stats.WorkingMemory.ItemCount != 2 {
		t.Errorf("expected 2 working items, got %d", stats.WorkingMemory.ItemCount)
	}

	if stats.ShortTermMemory.ItemCount != 1 {
		t.Errorf("expected 1 short term item, got %d", stats.ShortTermMemory.ItemCount)
	}

	if stats.LongTermMemory.ItemCount != 1 {
		t.Errorf("expected 1 long term item, got %d", stats.LongTermMemory.ItemCount)
	}

	if stats.WorkingMemory.ByCategory["decision"] != 1 {
		t.Errorf("expected 1 decision in working, got %d", stats.WorkingMemory.ByCategory["decision"])
	}
}

func TestMemoryHierarchy_RetentionPolicy(t *testing.T) {
	mh := NewMemoryHierarchy()

	// Default policies exist
	policy := mh.GetRetentionPolicy(MemoryTierWorking)
	if policy == nil {
		t.Fatal("expected default working memory policy")
		return
	}

	if policy.DefaultTTL != 24 {
		t.Errorf("expected default TTL 24, got %d", policy.DefaultTTL)
	}

	// Update policy
	newPolicy := &RetentionPolicy{
		ID:               "custom-working",
		Name:             "Custom Working",
		Tier:             MemoryTierWorking,
		DefaultTTL:       12,
		CompressionRatio: 0.3,
		MaxItems:         500,
	}
	mh.SetRetentionPolicy(newPolicy)

	updated := mh.GetRetentionPolicy(MemoryTierWorking)
	if updated.DefaultTTL != 12 {
		t.Errorf("expected updated TTL 12, got %d", updated.DefaultTTL)
	}
}

func TestMemoryHierarchy_Expiry(t *testing.T) {
	mh := NewMemoryHierarchy()

	// Create an item that's already expired
	expiredTime := time.Now().Add(-1 * time.Hour)
	item := &MemoryItem{
		Title:     "Expired Item",
		Content:   "This item has expired.",
		ExpiresAt: &expiredTime,
	}
	mh.AddItem(item)

	// Recall should not return expired items
	result, err := mh.Recall(MemoryRecallRequest{})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}

	if len(result.Items) != 0 {
		t.Errorf("expected 0 items (expired filtered out), got %d", len(result.Items))
	}
}

func TestMemoryHierarchy_RunCompression(t *testing.T) {
	mh := NewMemoryHierarchy()

	// Set a policy with immediate compression
	policy := &RetentionPolicy{
		ID:                 "test-working",
		Tier:               MemoryTierWorking,
		CompressAfterHours: 0, // Will compress items created in the past
		CompressionRatio:   0.5,
	}
	mh.SetRetentionPolicy(policy)

	// Add items with CreatedAt in the past
	item := &MemoryItem{
		Title:   "Old Item",
		Content: "This is an older item with some content that should be compressed during the compression job.",
	}
	mh.AddItem(item)

	// Manually backdate the item
	mh.mu.Lock()
	item.CreatedAt = time.Now().Add(-5 * time.Hour)
	mh.mu.Unlock()

	// Run compression
	job, err := mh.RunCompression(MemoryTierWorking)
	if err != nil {
		t.Fatalf("RunCompression failed: %v", err)
	}

	if job.Status != "completed" {
		t.Errorf("expected job status completed, got %q", job.Status)
	}
}

func TestMemoryHierarchy_IndexByNamespace(t *testing.T) {
	mh := NewMemoryHierarchy()

	mh.AddItem(&MemoryItem{
		Title:     "Item 1",
		Content:   "content",
		Namespace: "project-a",
	})
	mh.AddItem(&MemoryItem{
		Title:     "Item 2",
		Content:   "content",
		Namespace: "project-a",
	})
	mh.AddItem(&MemoryItem{
		Title:     "Item 3",
		Content:   "content",
		Namespace: "project-b",
	})

	// Recall by namespace
	result, err := mh.Recall(MemoryRecallRequest{
		Namespace: "project-a",
	})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}

	if len(result.Items) != 2 {
		t.Errorf("expected 2 items in project-a, got %d", len(result.Items))
	}
}

func TestMemoryHierarchy_IndexByTags(t *testing.T) {
	mh := NewMemoryHierarchy()

	mh.AddItem(&MemoryItem{
		Title:   "Item 1",
		Content: "content",
		Tags:    []string{"golang", "backend"},
	})
	mh.AddItem(&MemoryItem{
		Title:   "Item 2",
		Content: "content",
		Tags:    []string{"typescript", "frontend"},
	})
	mh.AddItem(&MemoryItem{
		Title:   "Item 3",
		Content: "content",
		Tags:    []string{"golang", "frontend"},
	})

	// Recall by tag
	result, err := mh.Recall(MemoryRecallRequest{
		Tags: []string{"golang"},
	})
	if err != nil {
		t.Fatalf("Recall failed: %v", err)
	}

	if len(result.Items) != 2 {
		t.Errorf("expected 2 items with golang tag, got %d", len(result.Items))
	}
}

func TestMemoryHierarchy_PromoteNonExistent(t *testing.T) {
	mh := NewMemoryHierarchy()

	err := mh.PromoteItem("nonexistent-id")
	if err == nil {
		t.Error("expected error when promoting non-existent item")
	}
}

func TestMemoryHierarchy_DemoteNonExistent(t *testing.T) {
	mh := NewMemoryHierarchy()

	err := mh.DemoteItem("nonexistent-id")
	if err == nil {
		t.Error("expected error when demoting non-existent item")
	}
}

func TestMemoryHierarchy_DeleteNonExistent(t *testing.T) {
	mh := NewMemoryHierarchy()

	err := mh.DeleteItem("nonexistent-id")
	if err == nil {
		t.Error("expected error when deleting non-existent item")
	}
}

func TestMemoryHierarchy_GetNonExistent(t *testing.T) {
	mh := NewMemoryHierarchy()

	_, err := mh.GetItem("nonexistent-id")
	if err == nil {
		t.Error("expected error when getting non-existent item")
	}
}

func TestMemoryHierarchy_CompressNonExistent(t *testing.T) {
	mh := NewMemoryHierarchy()

	err := mh.CompressItem("nonexistent-id")
	if err == nil {
		t.Error("expected error when compressing non-existent item")
	}
}

func TestMemoryHierarchy_MergeNonExistent(t *testing.T) {
	mh := NewMemoryHierarchy()

	_, err := mh.MergeItems([]string{"nonexistent-1", "nonexistent-2"}, "merged")
	if err == nil {
		t.Error("expected error when merging non-existent items")
	}
}

func TestMemoryHierarchy_ConcurrentAddAndRecall(t *testing.T) {
	mh := NewMemoryHierarchy()

	var wg sync.WaitGroup

	// Launch 50 goroutines each adding an item
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			err := mh.AddItem(&MemoryItem{
				Title:   fmt.Sprintf("Concurrent Item %d", idx),
				Content: fmt.Sprintf("Content for concurrent item %d", idx),
			})
			if err != nil {
				t.Errorf("AddItem failed for item %d: %v", idx, err)
			}
		}(i)
	}

	// Simultaneously launch 50 goroutines each doing Recall
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = mh.Recall(MemoryRecallRequest{})
		}()
	}

	wg.Wait()

	// Final Recall to verify all 50 items exist
	result, err := mh.Recall(MemoryRecallRequest{})
	if err != nil {
		t.Fatalf("final Recall failed: %v", err)
	}

	if len(result.Items) != 50 {
		t.Errorf("expected 50 items after concurrent adds, got %d", len(result.Items))
	}
}

func TestMemoryHierarchy_RecallEmptyHierarchy(t *testing.T) {
	mh := NewMemoryHierarchy()

	result, err := mh.Recall(MemoryRecallRequest{})
	if err != nil {
		t.Fatalf("Recall on empty hierarchy failed: %v", err)
	}

	if len(result.Items) != 0 {
		t.Errorf("expected 0 items from empty hierarchy, got %d", len(result.Items))
	}
}

func TestMemoryHierarchy_StatsEmpty(t *testing.T) {
	mh := NewMemoryHierarchy()

	stats := mh.Stats()

	if stats.TotalItems != 0 {
		t.Errorf("expected TotalItems=0, got %d", stats.TotalItems)
	}
	if stats.WorkingMemory.ItemCount != 0 {
		t.Errorf("expected WorkingMemory.ItemCount=0, got %d", stats.WorkingMemory.ItemCount)
	}
	if stats.ShortTermMemory.ItemCount != 0 {
		t.Errorf("expected ShortTermMemory.ItemCount=0, got %d", stats.ShortTermMemory.ItemCount)
	}
	if stats.LongTermMemory.ItemCount != 0 {
		t.Errorf("expected LongTermMemory.ItemCount=0, got %d", stats.LongTermMemory.ItemCount)
	}
}
