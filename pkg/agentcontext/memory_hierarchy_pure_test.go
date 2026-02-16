package agentcontext

import (
	"math"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// NewMemoryHierarchy
// ---------------------------------------------------------------------------

func TestNewMemoryHierarchy_EmptyState(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	if mh == nil {
		t.Fatal("expected non-nil memory hierarchy")
	}

	stats := mh.Stats()
	if stats.TotalItems != 0 {
		t.Errorf("expected 0 total items, got %d", stats.TotalItems)
	}
	if stats.TotalTokens != 0 {
		t.Errorf("expected 0 total tokens, got %d", stats.TotalTokens)
	}
}

func TestNewMemoryHierarchy_DefaultPolicies(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()

	working := mh.GetRetentionPolicy(MemoryTierWorking)
	if working == nil {
		t.Fatal("expected default working policy")
	}
	if working.DefaultTTL != 24 {
		t.Errorf("working TTL: got %d, want 24", working.DefaultTTL)
	}
	if working.MaxItems != 1000 {
		t.Errorf("working MaxItems: got %d, want 1000", working.MaxItems)
	}

	shortTerm := mh.GetRetentionPolicy(MemoryTierShortTerm)
	if shortTerm == nil {
		t.Fatal("expected default short-term policy")
	}
	if shortTerm.DefaultTTL != 168 {
		t.Errorf("short-term TTL: got %d, want 168", shortTerm.DefaultTTL)
	}

	longTerm := mh.GetRetentionPolicy(MemoryTierLongTerm)
	if longTerm == nil {
		t.Fatal("expected default long-term policy")
	}
	if longTerm.DefaultTTL != 0 {
		t.Errorf("long-term TTL: got %d, want 0 (no expiry)", longTerm.DefaultTTL)
	}
}

// ---------------------------------------------------------------------------
// importanceLevelToScore
// ---------------------------------------------------------------------------

func TestImportanceLevelToScore(t *testing.T) {
	t.Parallel()
	tests := []struct {
		level ImportanceLevel
		want  float64
	}{
		{ImportanceLevelCritical, 1.0},
		{ImportanceLevelHigh, 0.75},
		{ImportanceLevelMedium, 0.5},
		{ImportanceLevelLow, 0.25},
		{ImportanceLevel("unknown"), 0.5},
		{ImportanceLevel(""), 0.5},
	}
	for _, tc := range tests {
		t.Run(string(tc.level), func(t *testing.T) {
			t.Parallel()
			got := importanceLevelToScore(tc.level)
			if got != tc.want {
				t.Errorf("importanceLevelToScore(%q) = %f, want %f", tc.level, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// cosineSimilarity
// ---------------------------------------------------------------------------

func TestCosineSimilarity_IdenticalVectors(t *testing.T) {
	t.Parallel()
	v := []float64{1.0, 2.0, 3.0}
	sim := cosineSimilarity(v, v)
	if math.Abs(sim-1.0) > 1e-10 {
		t.Errorf("expected similarity ~1.0 for identical vectors, got %f", sim)
	}
}

func TestCosineSimilarity_OrthogonalVectors(t *testing.T) {
	t.Parallel()
	a := []float64{1.0, 0.0}
	b := []float64{0.0, 1.0}
	sim := cosineSimilarity(a, b)
	if math.Abs(sim) > 1e-10 {
		t.Errorf("expected similarity ~0 for orthogonal vectors, got %f", sim)
	}
}

func TestCosineSimilarity_OppositeVectors(t *testing.T) {
	t.Parallel()
	a := []float64{1.0, 0.0}
	b := []float64{-1.0, 0.0}
	sim := cosineSimilarity(a, b)
	if math.Abs(sim-(-1.0)) > 1e-10 {
		t.Errorf("expected similarity ~-1.0 for opposite vectors, got %f", sim)
	}
}

func TestCosineSimilarity_ZeroVectorA(t *testing.T) {
	t.Parallel()
	a := []float64{0.0, 0.0}
	b := []float64{1.0, 2.0}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("expected 0 for zero vector, got %f", sim)
	}
}

func TestCosineSimilarity_ZeroVectorB(t *testing.T) {
	t.Parallel()
	a := []float64{1.0, 2.0}
	b := []float64{0.0, 0.0}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("expected 0 for zero vector, got %f", sim)
	}
}

func TestCosineSimilarity_BothZero(t *testing.T) {
	t.Parallel()
	a := []float64{0.0, 0.0}
	b := []float64{0.0, 0.0}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("expected 0 for both zero vectors, got %f", sim)
	}
}

func TestCosineSimilarity_DifferentLengths(t *testing.T) {
	t.Parallel()
	a := []float64{1.0, 2.0, 3.0}
	b := []float64{1.0, 2.0}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("expected 0 for different length vectors, got %f", sim)
	}
}

func TestCosineSimilarity_EmptyVectors(t *testing.T) {
	t.Parallel()
	sim := cosineSimilarity([]float64{}, []float64{})
	if sim != 0 {
		t.Errorf("expected 0 for empty vectors, got %f", sim)
	}
}

func TestCosineSimilarity_ScaledVectors(t *testing.T) {
	t.Parallel()
	a := []float64{1.0, 2.0, 3.0}
	b := []float64{2.0, 4.0, 6.0} // 2x of a
	sim := cosineSimilarity(a, b)
	if math.Abs(sim-1.0) > 1e-10 {
		t.Errorf("expected similarity ~1.0 for scaled vectors, got %f", sim)
	}
}

// ---------------------------------------------------------------------------
// textSimilarity
// ---------------------------------------------------------------------------

func TestTextSimilarity_IdenticalText(t *testing.T) {
	t.Parallel()
	sim := textSimilarity("hello world", "hello world")
	if sim != 1.0 {
		t.Errorf("expected 1.0 for identical text, got %f", sim)
	}
}

func TestTextSimilarity_CompletelyDifferent(t *testing.T) {
	t.Parallel()
	sim := textSimilarity("apples oranges", "zebras elephants")
	if sim != 0 {
		t.Errorf("expected 0 for completely different text, got %f", sim)
	}
}

func TestTextSimilarity_BothEmpty(t *testing.T) {
	t.Parallel()
	sim := textSimilarity("", "")
	if sim != 1.0 {
		t.Errorf("expected 1.0 for both empty strings, got %f", sim)
	}
}

func TestTextSimilarity_OneEmpty(t *testing.T) {
	t.Parallel()
	sim := textSimilarity("hello world", "")
	if sim != 0.0 {
		t.Errorf("expected 0.0 when one string is empty, got %f", sim)
	}
}

func TestTextSimilarity_PartialOverlap(t *testing.T) {
	t.Parallel()
	// "hello world test" has 3 words, "hello world other" has 3 words
	// Intersection: {"hello", "world"} = 2, Union: 4
	// Jaccard = 2/4 = 0.5
	sim := textSimilarity("hello world test", "hello world other")
	if math.Abs(sim-0.5) > 0.01 {
		t.Errorf("expected similarity ~0.5 for partial overlap, got %f", sim)
	}
}

func TestTextSimilarity_CaseInsensitive(t *testing.T) {
	t.Parallel()
	sim := textSimilarity("Hello World", "hello world")
	if sim != 1.0 {
		t.Errorf("expected 1.0 for case-insensitive match, got %f", sim)
	}
}

func TestTextSimilarity_PunctuationIgnored(t *testing.T) {
	t.Parallel()
	// tokenize strips non-alphanumeric characters
	sim := textSimilarity("hello, world!", "hello world")
	if sim != 1.0 {
		t.Errorf("expected 1.0 ignoring punctuation, got %f", sim)
	}
}

// ---------------------------------------------------------------------------
// EstimateTokens (via estimateTokenCount / EstimateTokens)
// ---------------------------------------------------------------------------

func TestEstimateTokens_Empty(t *testing.T) {
	t.Parallel()
	tokens := EstimateTokens("")
	if tokens != 0 {
		t.Errorf("expected 0 tokens for empty string, got %d", tokens)
	}
}

func TestEstimateTokens_ShortContent(t *testing.T) {
	t.Parallel()
	// "test" = 4 chars => (4+3)/4 = 1 token (fallback heuristic)
	tokens := EstimateTokens("test")
	if tokens < 1 {
		t.Errorf("expected at least 1 token for 'test', got %d", tokens)
	}
}

func TestEstimateTokens_LongerContent(t *testing.T) {
	t.Parallel()
	content := "This is a longer piece of content that should produce a reasonable token count estimate."
	tokens := EstimateTokens(content)
	if tokens < 10 {
		t.Errorf("expected at least 10 tokens for longer content, got %d", tokens)
	}
}

func TestEstimateTokens_ProportionalToLength(t *testing.T) {
	t.Parallel()
	short := EstimateTokens("hello")
	long := EstimateTokens("hello hello hello hello hello hello hello hello hello hello")
	if long <= short {
		t.Errorf("expected longer content to have more tokens: short=%d, long=%d", short, long)
	}
}

// ---------------------------------------------------------------------------
// MemoryHierarchy.Stats
// ---------------------------------------------------------------------------

func TestMemoryHierarchyStats_EmptyHierarchy(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	stats := mh.Stats()

	if stats.TotalItems != 0 {
		t.Errorf("TotalItems: got %d, want 0", stats.TotalItems)
	}
	if stats.TotalTokens != 0 {
		t.Errorf("TotalTokens: got %d, want 0", stats.TotalTokens)
	}
	if stats.WorkingMemory.ItemCount != 0 {
		t.Errorf("WorkingMemory.ItemCount: got %d, want 0", stats.WorkingMemory.ItemCount)
	}
	if stats.ShortTermMemory.ItemCount != 0 {
		t.Errorf("ShortTermMemory.ItemCount: got %d, want 0", stats.ShortTermMemory.ItemCount)
	}
	if stats.LongTermMemory.ItemCount != 0 {
		t.Errorf("LongTermMemory.ItemCount: got %d, want 0", stats.LongTermMemory.ItemCount)
	}
}

func TestMemoryHierarchyStats_PerTierCounts(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	mh.AddItem(&MemoryItem{Title: "w1", Content: "working content one", Tier: MemoryTierWorking, Importance: ImportanceLevelHigh})
	mh.AddItem(&MemoryItem{Title: "w2", Content: "working content two", Tier: MemoryTierWorking, Importance: ImportanceLevelLow})
	mh.AddItem(&MemoryItem{Title: "s1", Content: "short term content", Tier: MemoryTierShortTerm, Importance: ImportanceLevelMedium})
	mh.AddItem(&MemoryItem{Title: "l1", Content: "long term content", Tier: MemoryTierLongTerm, Importance: ImportanceLevelCritical})

	stats := mh.Stats()
	if stats.TotalItems != 4 {
		t.Errorf("TotalItems: got %d, want 4", stats.TotalItems)
	}
	if stats.WorkingMemory.ItemCount != 2 {
		t.Errorf("WorkingMemory.ItemCount: got %d, want 2", stats.WorkingMemory.ItemCount)
	}
	if stats.ShortTermMemory.ItemCount != 1 {
		t.Errorf("ShortTermMemory.ItemCount: got %d, want 1", stats.ShortTermMemory.ItemCount)
	}
	if stats.LongTermMemory.ItemCount != 1 {
		t.Errorf("LongTermMemory.ItemCount: got %d, want 1", stats.LongTermMemory.ItemCount)
	}
}

func TestMemoryHierarchyStats_CategoryCounts(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	mh.AddItem(&MemoryItem{Title: "d1", Content: "decision content", Category: "decision"})
	mh.AddItem(&MemoryItem{Title: "d2", Content: "another decision", Category: "decision"})
	mh.AddItem(&MemoryItem{Title: "f1", Content: "finding content", Category: "finding"})

	stats := mh.Stats()
	if stats.WorkingMemory.ByCategory["decision"] != 2 {
		t.Errorf("decision count: got %d, want 2", stats.WorkingMemory.ByCategory["decision"])
	}
	if stats.WorkingMemory.ByCategory["finding"] != 1 {
		t.Errorf("finding count: got %d, want 1", stats.WorkingMemory.ByCategory["finding"])
	}
}

func TestMemoryHierarchyStats_ImportanceCounts(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	mh.AddItem(&MemoryItem{Title: "h1", Content: "content", Importance: ImportanceLevelHigh})
	mh.AddItem(&MemoryItem{Title: "h2", Content: "content", Importance: ImportanceLevelHigh})
	mh.AddItem(&MemoryItem{Title: "l1", Content: "content", Importance: ImportanceLevelLow})

	stats := mh.Stats()
	if stats.WorkingMemory.ByImportance["high"] != 2 {
		t.Errorf("high importance count: got %d, want 2", stats.WorkingMemory.ByImportance["high"])
	}
	if stats.WorkingMemory.ByImportance["low"] != 1 {
		t.Errorf("low importance count: got %d, want 1", stats.WorkingMemory.ByImportance["low"])
	}
}

func TestMemoryHierarchyStats_AvgImportance(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	mh.AddItem(&MemoryItem{Title: "a", Content: "c", Importance: ImportanceLevelHigh})   // 0.75
	mh.AddItem(&MemoryItem{Title: "b", Content: "c", Importance: ImportanceLevelLow})    // 0.25
	mh.AddItem(&MemoryItem{Title: "c", Content: "c", Importance: ImportanceLevelMedium}) // 0.50

	stats := mh.Stats()
	// Average: (0.75 + 0.25 + 0.50) / 3 = 0.5
	if math.Abs(stats.WorkingMemory.AvgImportance-0.5) > 0.01 {
		t.Errorf("AvgImportance: got %f, want ~0.5", stats.WorkingMemory.AvgImportance)
	}
}

func TestMemoryHierarchyStats_ExcludesArchivedExpired(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	mh.AddItem(&MemoryItem{Title: "active", Content: "content"})

	// Manually add an archived item
	archivedItem := &MemoryItem{
		ID:      "archived-1",
		Title:   "archived",
		Content: "content",
		Tier:    MemoryTierWorking,
		Status:  MemoryItemStatusArchived,
	}
	mh.mu.Lock()
	mh.working[archivedItem.ID] = archivedItem
	mh.mu.Unlock()

	stats := mh.Stats()
	// Stats should count only active items
	if stats.WorkingMemory.ItemCount != 1 {
		t.Errorf("expected 1 active item in stats, got %d", stats.WorkingMemory.ItemCount)
	}
}

func TestMemoryHierarchyStats_TokenCount(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	mh.AddItem(&MemoryItem{Title: "item1", Content: "hello world"})
	mh.AddItem(&MemoryItem{Title: "item2", Content: "foo bar baz qux"})

	stats := mh.Stats()
	if stats.TotalTokens <= 0 {
		t.Errorf("expected positive total tokens, got %d", stats.TotalTokens)
	}
}

func TestMemoryHierarchyStats_OldestNewestItem(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()

	early := time.Now().UTC().Add(-2 * time.Hour)
	late := time.Now().UTC().Add(-1 * time.Hour)

	mh.AddItem(&MemoryItem{Title: "early", Content: "c", CreatedAt: early})
	mh.AddItem(&MemoryItem{Title: "late", Content: "c", CreatedAt: late})

	stats := mh.Stats()
	if stats.WorkingMemory.OldestItem == nil {
		t.Fatal("expected OldestItem to be set")
	}
	if stats.WorkingMemory.NewestItem == nil {
		t.Fatal("expected NewestItem to be set")
	}
	if !stats.WorkingMemory.OldestItem.Equal(early) {
		t.Errorf("OldestItem: got %v, want %v", stats.WorkingMemory.OldestItem, early)
	}
	if !stats.WorkingMemory.NewestItem.Equal(late) {
		t.Errorf("NewestItem: got %v, want %v", stats.WorkingMemory.NewestItem, late)
	}
}

func TestMemoryHierarchyStats_CompressionRatio(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()

	item := &MemoryItem{
		Title:            "compressed",
		Content:          "This is some original content that is much longer than the compressed version",
		OriginalTokens:   100,
		CompressedTokens: 30,
		Status:           MemoryItemStatusCompressed,
	}
	mh.AddItem(item)

	// Override the auto-calculated tokens
	mh.mu.Lock()
	item.OriginalTokens = 100
	item.CompressedTokens = 30
	mh.mu.Unlock()

	stats := mh.Stats()
	if stats.CompressionRatio < 0.29 || stats.CompressionRatio > 0.31 {
		t.Errorf("CompressionRatio: got %f, want ~0.3", stats.CompressionRatio)
	}
}

// ---------------------------------------------------------------------------
// Deduplication support (text similarity for dedup)
// ---------------------------------------------------------------------------

func TestCheckDuplicate_NoPolicyReturnsAdd(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()

	// Remove the default policy to test no-policy case
	mh.mu.Lock()
	delete(mh.policies, MemoryTierWorking)
	mh.mu.Unlock()

	result := mh.CheckDuplicate(&MemoryItem{
		Title:   "test",
		Content: "content",
		Tier:    MemoryTierWorking,
	}, nil)
	if result.Action != "add" {
		t.Errorf("expected action=add when no policy, got %q", result.Action)
	}
}

func TestCheckDuplicate_DedupeDisabledReturnsAdd(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()

	mh.SetRetentionPolicy(&RetentionPolicy{
		Tier:          MemoryTierWorking,
		DedupeEnabled: false,
	})

	result := mh.CheckDuplicate(&MemoryItem{
		Title:   "test",
		Content: "content",
		Tier:    MemoryTierWorking,
	}, nil)
	if result.Action != "add" {
		t.Errorf("expected action=add when dedupe disabled, got %q", result.Action)
	}
}

func TestCheckDuplicate_IdenticalContent(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()

	mh.AddItem(&MemoryItem{
		Title:     "existing",
		Content:   "the quick brown fox jumps over the lazy dog",
		Namespace: "ns",
	})

	result := mh.CheckDuplicate(&MemoryItem{
		Title:     "duplicate",
		Content:   "the quick brown fox jumps over the lazy dog",
		Tier:      MemoryTierWorking,
		Namespace: "ns",
	}, nil)
	if !result.IsDuplicate {
		t.Error("expected identical content to be flagged as duplicate")
	}
	if result.Action != "skip" {
		t.Errorf("expected action=skip, got %q", result.Action)
	}
}

// ---------------------------------------------------------------------------
// Add / Get / Delete lifecycle
// ---------------------------------------------------------------------------

func TestAddItem_SetsDefaults(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	item := &MemoryItem{Title: "test", Content: "content here"}
	if err := mh.AddItem(item); err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	if item.ID == "" {
		t.Error("expected ID to be set")
	}
	if item.Tier != MemoryTierWorking {
		t.Errorf("default tier: got %q, want working", item.Tier)
	}
	if item.Status != MemoryItemStatusActive {
		t.Errorf("default status: got %q, want active", item.Status)
	}
	if item.Importance != ImportanceLevelMedium {
		t.Errorf("default importance: got %q, want medium", item.Importance)
	}
	if item.ImportanceScore != 0.5 {
		t.Errorf("default importance score: got %f, want 0.5", item.ImportanceScore)
	}
	if item.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if item.LastAccessedAt.IsZero() {
		t.Error("expected LastAccessedAt to be set")
	}
	if item.OriginalTokens == 0 {
		t.Error("expected OriginalTokens to be calculated")
	}
	if item.ExpiresAt == nil {
		t.Error("expected ExpiresAt to be set based on working tier policy")
	}
}

func TestGetItem_UpdatesAccessTracking(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	item := &MemoryItem{Title: "test", Content: "content"}
	mh.AddItem(item)

	got, _ := mh.GetItem(item.ID)
	if got.AccessCount != 1 {
		t.Errorf("AccessCount after first get: got %d, want 1", got.AccessCount)
	}

	got, _ = mh.GetItem(item.ID)
	if got.AccessCount != 2 {
		t.Errorf("AccessCount after second get: got %d, want 2", got.AccessCount)
	}
}

func TestDeleteItem_RemovesFromAllIndexes(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	item := &MemoryItem{
		Title:     "test",
		Content:   "content",
		Namespace: "ns",
		Category:  "cat",
		SessionID: "sess",
	}
	mh.AddItem(item)
	mh.DeleteItem(item.ID)

	// Should not appear in recall
	result, _ := mh.Recall(MemoryRecallRequest{Namespace: "ns"})
	if len(result.Items) != 0 {
		t.Error("expected 0 items after delete")
	}
}

// ---------------------------------------------------------------------------
// Promote / Demote
// ---------------------------------------------------------------------------

func TestPromoteItem_WorkingToShortTerm(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	item := &MemoryItem{Title: "test", Content: "content", Tier: MemoryTierWorking}
	mh.AddItem(item)

	mh.PromoteItem(item.ID)
	got, _ := mh.GetItem(item.ID)
	if got.Tier != MemoryTierShortTerm {
		t.Errorf("expected short_term after promotion, got %q", got.Tier)
	}
}

func TestPromoteItem_ShortTermToLongTerm(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	item := &MemoryItem{Title: "test", Content: "content", Tier: MemoryTierShortTerm}
	mh.AddItem(item)

	mh.PromoteItem(item.ID)
	got, _ := mh.GetItem(item.ID)
	if got.Tier != MemoryTierLongTerm {
		t.Errorf("expected long_term after promotion, got %q", got.Tier)
	}
	if got.ExpiresAt != nil {
		t.Error("expected no expiry for long-term items")
	}
}

func TestPromoteItem_LongTermFails(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	item := &MemoryItem{Title: "test", Content: "content", Tier: MemoryTierLongTerm}
	mh.AddItem(item)

	err := mh.PromoteItem(item.ID)
	if err == nil {
		t.Error("expected error when promoting from long-term")
	}
}

func TestDemoteItem_LongTermToShortTerm(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	item := &MemoryItem{Title: "test", Content: "content", Tier: MemoryTierLongTerm}
	mh.AddItem(item)

	mh.DemoteItem(item.ID)
	got, _ := mh.GetItem(item.ID)
	if got.Tier != MemoryTierShortTerm {
		t.Errorf("expected short_term after demotion, got %q", got.Tier)
	}
}

func TestDemoteItem_WorkingFails(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	item := &MemoryItem{Title: "test", Content: "content", Tier: MemoryTierWorking}
	mh.AddItem(item)

	err := mh.DemoteItem(item.ID)
	if err == nil {
		t.Error("expected error when demoting from working")
	}
}
