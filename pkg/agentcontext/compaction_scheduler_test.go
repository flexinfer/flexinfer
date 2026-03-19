package agentcontext

import (
	"testing"
	"time"
)

func TestDefaultCompactionConfig(t *testing.T) {
	cfg := DefaultCompactionConfig()

	if !cfg.Enabled {
		t.Error("expected Enabled to be true")
	}
	if cfg.CheckInterval != 5*time.Minute {
		t.Errorf("expected CheckInterval 5m, got %v", cfg.CheckInterval)
	}
	if cfg.WorkingMemoryThreshold != 0.95 {
		t.Errorf("expected WorkingMemoryThreshold 0.95, got %f", cfg.WorkingMemoryThreshold)
	}
	if cfg.ShortTermMemoryThreshold != 0.90 {
		t.Errorf("expected ShortTermMemoryThreshold 0.90, got %f", cfg.ShortTermMemoryThreshold)
	}
	if cfg.LongTermMemoryThreshold != 0.85 {
		t.Errorf("expected LongTermMemoryThreshold 0.85, got %f", cfg.LongTermMemoryThreshold)
	}
	if cfg.TargetCapacity != 0.70 {
		t.Errorf("expected TargetCapacity 0.70, got %f", cfg.TargetCapacity)
	}
	if cfg.SummarizationDepth != 3 {
		t.Errorf("expected SummarizationDepth 3, got %d", cfg.SummarizationDepth)
	}
	if cfg.MaxItemsPerRun != 100 {
		t.Errorf("expected MaxItemsPerRun 100, got %d", cfg.MaxItemsPerRun)
	}
}

func TestDefaultCompactionPolicy(t *testing.T) {
	policy := DefaultCompactionPolicy()

	if policy.WorkingPolicy.MaxAge != 24*time.Hour {
		t.Errorf("working MaxAge = %v, want 24h", policy.WorkingPolicy.MaxAge)
	}
	if policy.WorkingPolicy.MaxItems != 100 {
		t.Errorf("working MaxItems = %d, want 100", policy.WorkingPolicy.MaxItems)
	}
	if policy.WorkingPolicy.CompressionStrategy != "summarize" {
		t.Errorf("working strategy = %q, want summarize", policy.WorkingPolicy.CompressionStrategy)
	}
	if policy.WorkingPolicy.FullyCompressedAction != "demote" {
		t.Errorf("working action = %q, want demote", policy.WorkingPolicy.FullyCompressedAction)
	}

	if policy.ShortTermPolicy.MaxAge != 7*24*time.Hour {
		t.Errorf("short_term MaxAge = %v, want 7d", policy.ShortTermPolicy.MaxAge)
	}
	if policy.ShortTermPolicy.CompressionStrategy != "hybrid" {
		t.Errorf("short_term strategy = %q, want hybrid", policy.ShortTermPolicy.CompressionStrategy)
	}

	if policy.LongTermPolicy.MaxAge != 30*24*time.Hour {
		t.Errorf("long_term MaxAge = %v, want 30d", policy.LongTermPolicy.MaxAge)
	}
	if policy.LongTermPolicy.FullyCompressedAction != "archive" {
		t.Errorf("long_term action = %q, want archive", policy.LongTermPolicy.FullyCompressedAction)
	}
}

func TestEstimateTokenCount(t *testing.T) {
	tests := []struct {
		content  string
		expected int
	}{
		{"", 0},
		{"test", 1},        // 4 chars / 4 = 1
		{"hello world", 2}, // 11 chars / 4 = 2 (integer division)
		{"a longer piece of content here", 7},
	}

	for _, tc := range tests {
		got := estimateTokenCount(tc.content)
		if got != tc.expected {
			t.Errorf("estimateTokenCount(%q) = %d, want %d", tc.content, got, tc.expected)
		}
	}
}

func TestCompactionScore(t *testing.T) {
	cfg := DefaultCompactionConfig()
	cs := &CompactionScheduler{config: cfg}

	now := time.Now()

	// Older item with low importance should score higher
	oldItem := MemoryItem{
		CreatedAt:       now.Add(-60 * 24 * time.Hour), // 60 days old
		LastAccessedAt:  now.Add(-30 * 24 * time.Hour), // accessed 30 days ago
		ImportanceScore: 0.1,
		OriginalTokens:  500,
	}

	// Recent item with high importance should score lower
	newItem := MemoryItem{
		CreatedAt:       now.Add(-1 * time.Hour),
		LastAccessedAt:  now.Add(-10 * time.Minute),
		ImportanceScore: 0.9,
		OriginalTokens:  100,
	}

	oldScore := cs.compactionScore(oldItem, nil)
	newScore := cs.compactionScore(newItem, nil)

	if oldScore <= newScore {
		t.Errorf("expected old/low-importance item to score higher (%f) than new/high-importance (%f)", oldScore, newScore)
	}
}

func TestCompactionScore_CompressedTokens(t *testing.T) {
	cfg := DefaultCompactionConfig()
	cs := &CompactionScheduler{config: cfg}

	now := time.Now()
	item := MemoryItem{
		CreatedAt:        now.Add(-24 * time.Hour),
		LastAccessedAt:   now.Add(-12 * time.Hour),
		ImportanceScore:  0.5,
		OriginalTokens:   1000,
		CompressedTokens: 200,
	}

	score := cs.compactionScore(item, nil)
	// With compressed tokens, should use CompressedTokens (200) not OriginalTokens (1000)
	// tokenScore = 200/1000 = 0.2
	if score <= 0 {
		t.Errorf("expected positive score, got %f", score)
	}
}

func TestSortItemsForCompaction(t *testing.T) {
	cfg := DefaultCompactionConfig()
	cs := &CompactionScheduler{config: cfg}

	now := time.Now()
	items := []MemoryItem{
		{ID: "new-important", CreatedAt: now.Add(-1 * time.Hour), LastAccessedAt: now, ImportanceScore: 0.9},
		{ID: "old-unimportant", CreatedAt: now.Add(-60 * 24 * time.Hour), LastAccessedAt: now.Add(-30 * 24 * time.Hour), ImportanceScore: 0.1},
	}

	sorted := cs.sortItemsForCompaction(items)

	if len(sorted) != 2 {
		t.Fatalf("expected 2 items, got %d", len(sorted))
	}
	// old-unimportant should be first (highest compaction score = compact first)
	if sorted[0].ID != "old-unimportant" {
		t.Errorf("expected old-unimportant first, got %s", sorted[0].ID)
	}
	if sorted[1].ID != "new-important" {
		t.Errorf("expected new-important last, got %s", sorted[1].ID)
	}

	// Verify original slice is not modified
	if items[0].ID != "new-important" {
		t.Error("sortItemsForCompaction modified the original slice")
	}
}

func TestCalculateCapacityForTier_Defaults(t *testing.T) {
	cfg := DefaultCompactionConfig()
	cs := &CompactionScheduler{config: cfg}

	// With default maxItems=1000, maxTokens=500000
	// 500 items, 100000 tokens
	capacity := cs.calculateCapacityForTier(500, 100000, "")

	// itemCapacity = 500/1000 = 0.5
	// tokenCapacity = 100000/500000 = 0.2
	// Should return max(0.5, 0.2) = 0.5
	if capacity != 0.5 {
		t.Errorf("expected capacity 0.5, got %f", capacity)
	}
}

func TestCalculateCapacityForTier_TokenDominated(t *testing.T) {
	cfg := DefaultCompactionConfig()
	cs := &CompactionScheduler{config: cfg}

	// 100 items, 400000 tokens
	capacity := cs.calculateCapacityForTier(100, 400000, "")

	// itemCapacity = 100/1000 = 0.1
	// tokenCapacity = 400000/500000 = 0.8
	// Should return max(0.1, 0.8) = 0.8
	if capacity != 0.8 {
		t.Errorf("expected capacity 0.8, got %f", capacity)
	}
}

func TestNewCompactionScheduler(t *testing.T) {
	cfg := DefaultCompactionConfig()
	cs := NewCompactionScheduler(cfg, nil, nil, nil)

	if cs == nil {
		t.Fatal("expected non-nil scheduler")
	}
	if cs.IsRunning() {
		t.Error("expected scheduler not running initially")
	}
}

func TestSchedulerStatus_Initial(t *testing.T) {
	cfg := DefaultCompactionConfig()
	cs := NewCompactionScheduler(cfg, nil, nil, nil)

	status := cs.Status()
	if status.Running {
		t.Error("expected not running")
	}
	if status.RunCount != 0 {
		t.Errorf("expected RunCount 0, got %d", status.RunCount)
	}
	if status.ErrorCount != 0 {
		t.Errorf("expected ErrorCount 0, got %d", status.ErrorCount)
	}
	if status.LastRunStats != nil {
		t.Error("expected nil LastRunStats initially")
	}
}
