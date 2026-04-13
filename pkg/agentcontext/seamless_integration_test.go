package agentcontext

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// M1: Session Lifecycle Hardening
// ---------------------------------------------------------------------------

func TestTokenBudgetForPlatform(t *testing.T) {
	t.Parallel()

	const fallback = 4000

	tests := []struct {
		name            string
		platformBudgets map[string]int
		agentType       string
		want            int
	}{
		{
			name:            "known platform returns configured budget",
			platformBudgets: map[string]int{"claude-code": 8000, "gemini": 6000},
			agentType:       "claude-code",
			want:            8000,
		},
		{
			name:            "unknown platform falls back to DefaultTokenBudget",
			platformBudgets: map[string]int{"claude-code": 8000},
			agentType:       "unknown-agent",
			want:            fallback,
		},
		{
			name:            "empty agentType falls back to DefaultTokenBudget",
			platformBudgets: map[string]int{"claude-code": 8000},
			agentType:       "",
			want:            fallback,
		},
		{
			name:            "nil PlatformBudgets falls back to DefaultTokenBudget",
			platformBudgets: nil,
			agentType:       "claude-code",
			want:            fallback,
		},
		{
			name:            "empty PlatformBudgets falls back to DefaultTokenBudget",
			platformBudgets: map[string]int{},
			agentType:       "claude-code",
			want:            fallback,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Config{
				DefaultTokenBudget: fallback,
				PlatformBudgets:    tc.platformBudgets,
			}
			got := cfg.TokenBudgetForPlatform(tc.agentType)
			if got != tc.want {
				t.Errorf("TokenBudgetForPlatform(%q) = %d, want %d", tc.agentType, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// M3: Workflow Reliability — Graph BFS depth limiting
// ---------------------------------------------------------------------------

// buildEntityChain creates a linear chain of n entities (A, B, C, ...) and
// connects each adjacent pair with a RelationDependsOn relation.
func buildEntityChain(t *testing.T, g *KnowledgeGraph, n int) []string {
	t.Helper()

	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := string(rune('A' + i))
		if i >= 26 {
			id = string(rune('A'+i/26-1)) + string(rune('A'+i%26))
		}
		ids[i] = id
		if err := g.AddEntity(&Entity{
			ID:   id,
			Name: "entity-" + id,
			Type: EntityTypeFile,
		}); err != nil {
			t.Fatalf("AddEntity(%s): %v", id, err)
		}
	}

	for i := 0; i < n-1; i++ {
		if err := g.AddRelation(&Relation{
			ID:       fmt.Sprintf("rel-%s-%s", ids[i], ids[i+1]),
			SourceID: ids[i],
			TargetID: ids[i+1],
			Type:     RelationDependsOn,
		}); err != nil {
			t.Fatalf("AddRelation(%s→%s): %v", ids[i], ids[i+1], err)
		}
	}

	return ids
}

func TestGraphQuery_DepthLimit(t *testing.T) {
	t.Parallel()

	g := NewKnowledgeGraph()
	ids := buildEntityChain(t, g, 12) // A through L

	// Query from A with maxDepth=100 — should cap at 10.
	result, err := g.Query(GraphQuery{
		EntityID: ids[0],
		MaxDepth: 100,
	})
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}

	// Expect at most 11 entities (start + 10 hops).
	if len(result.Entities) > 11 {
		t.Errorf("expected at most 11 entities (depth cap 10), got %d", len(result.Entities))
	}
	if !result.Truncated {
		t.Error("expected Truncated=true when chain exceeds depth cap")
	}

	// Query with maxDepth=2 — should return exactly 3 entities (A, B, C).
	result2, err := g.Query(GraphQuery{
		EntityID: ids[0],
		MaxDepth: 2,
	})
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}

	if len(result2.Entities) != 3 {
		t.Errorf("expected 3 entities with maxDepth=2, got %d", len(result2.Entities))
	}
}

func TestGraphQuery_DepthLimitSetsTruncated(t *testing.T) {
	t.Parallel()

	g := NewKnowledgeGraph()
	buildEntityChain(t, g, 5) // A through E

	result, err := g.Query(GraphQuery{
		EntityID: "A",
		MaxDepth: 2,
	})
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}

	// With 5 entities and maxDepth=2, only A/B/C are returned — D/E are beyond.
	if !result.Truncated {
		t.Error("expected Truncated=true when nodes remain beyond maxDepth")
	}
}

func TestFindPath_DepthLimit(t *testing.T) {
	t.Parallel()

	g := NewKnowledgeGraph()
	ids := buildEntityChain(t, g, 12) // A through L

	// FindPath from A to L with maxDepth=100 — should cap at 10.
	// Since L is 11 hops away (exceeds cap of 10), path should not be found.
	path, err := g.FindPath(ids[0], ids[11], 100, nil)
	if err == nil && path != nil {
		// If a path was found despite the cap, it should not exceed 11 nodes.
		if len(path.Nodes) > 11 {
			t.Errorf("FindPath exceeded depth cap: got %d nodes", len(path.Nodes))
		}
	}
	// It is acceptable for err to be non-nil ("no path found") due to depth cap.

	// FindPath from A to E (4 hops) with maxDepth=4 — should succeed.
	path2, err := g.FindPath(ids[0], ids[4], 4, nil)
	if err != nil {
		t.Fatalf("FindPath(A→E, maxDepth=4) returned error: %v", err)
	}
	if path2 == nil {
		t.Fatal("FindPath(A→E, maxDepth=4) returned nil path")
	}
	if len(path2.Nodes) != 5 {
		t.Errorf("expected 5 nodes in path A→E, got %d", len(path2.Nodes))
	}
}

func TestFindPath_SelfPath(t *testing.T) {
	t.Parallel()

	g := NewKnowledgeGraph()
	if err := g.AddEntity(&Entity{
		ID:   "self",
		Name: "self-entity",
		Type: EntityTypeFile,
	}); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	path, err := g.FindPath("self", "self", 10, nil)
	if err != nil {
		t.Fatalf("FindPath(self→self) returned error: %v", err)
	}
	if path == nil {
		t.Fatal("FindPath(self→self) returned nil path")
	}
	if len(path.Nodes) != 1 {
		t.Errorf("expected 1 node in self-path, got %d", len(path.Nodes))
	}
	if len(path.Edges) != 0 {
		t.Errorf("expected 0 edges in self-path, got %d", len(path.Edges))
	}
	if path.Length != 0 {
		t.Errorf("expected length 0 for self-path, got %d", path.Length)
	}
}

// ---------------------------------------------------------------------------
// M4: Memory & Compaction Polish
// ---------------------------------------------------------------------------

func TestMemoryHierarchy_PromoteItem(t *testing.T) {
	t.Parallel()

	h := NewMemoryHierarchy()
	if err := h.AddItem(&MemoryItem{
		ID:         "promote-test",
		Title:      "promote test",
		Content:    "test content",
		Tier:       MemoryTierWorking,
		Status:     MemoryItemStatusActive,
		Importance: ImportanceLevelMedium,
		CreatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	// First promotion: working → short-term.
	if err := h.PromoteItem("promote-test"); err != nil {
		t.Fatalf("first PromoteItem returned error: %v", err)
	}
	got, err := h.GetItem("promote-test")
	if err != nil {
		t.Fatalf("GetItem after first promotion: %v", err)
	}
	if got.Tier != MemoryTierShortTerm {
		t.Errorf("expected tier %s after first promotion, got %s", MemoryTierShortTerm, got.Tier)
	}

	// Second promotion: short-term → long-term.
	if err := h.PromoteItem("promote-test"); err != nil {
		t.Fatalf("second PromoteItem returned error: %v", err)
	}
	got, err = h.GetItem("promote-test")
	if err != nil {
		t.Fatalf("GetItem after second promotion: %v", err)
	}
	if got.Tier != MemoryTierLongTerm {
		t.Errorf("expected tier %s after second promotion, got %s", MemoryTierLongTerm, got.Tier)
	}

	// Third promotion: already in long-term → error.
	err = h.PromoteItem("promote-test")
	if err == nil {
		t.Error("expected error when promoting from long-term, got nil")
	} else if !strings.Contains(err.Error(), "long-term") && !strings.Contains(err.Error(), "long_term") {
		t.Errorf("expected error mentioning long-term tier, got: %v", err)
	}
}

func TestMemoryHierarchy_DemoteItem(t *testing.T) {
	t.Parallel()

	h := NewMemoryHierarchy()
	if err := h.AddItem(&MemoryItem{
		ID:         "demote-test",
		Title:      "demote test",
		Content:    "test content",
		Tier:       MemoryTierLongTerm,
		Status:     MemoryItemStatusActive,
		Importance: ImportanceLevelMedium,
		CreatedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	// First demotion: long-term → short-term.
	if err := h.DemoteItem("demote-test"); err != nil {
		t.Fatalf("first DemoteItem returned error: %v", err)
	}
	got, err := h.GetItem("demote-test")
	if err != nil {
		t.Fatalf("GetItem after first demotion: %v", err)
	}
	if got.Tier != MemoryTierShortTerm {
		t.Errorf("expected tier %s after first demotion, got %s", MemoryTierShortTerm, got.Tier)
	}

	// Second demotion: short-term → working.
	if err := h.DemoteItem("demote-test"); err != nil {
		t.Fatalf("second DemoteItem returned error: %v", err)
	}
	got, err = h.GetItem("demote-test")
	if err != nil {
		t.Fatalf("GetItem after second demotion: %v", err)
	}
	if got.Tier != MemoryTierWorking {
		t.Errorf("expected tier %s after second demotion, got %s", MemoryTierWorking, got.Tier)
	}

	// Third demotion: already in working → error.
	err = h.DemoteItem("demote-test")
	if err == nil {
		t.Error("expected error when demoting from working, got nil")
	} else if !strings.Contains(err.Error(), "working") {
		t.Errorf("expected error mentioning 'working', got: %v", err)
	}
}

func TestMemoryHierarchy_CompactingFlag(t *testing.T) {
	t.Parallel()

	h := NewMemoryHierarchy()

	if h.IsCompacting() {
		t.Error("expected IsCompacting()=false on fresh hierarchy")
	}

	// RunCompression on an empty tier runs synchronously and should leave
	// the compacting flag as false when done.
	_, _ = h.RunCompression(MemoryTierWorking)

	if h.IsCompacting() {
		t.Error("expected IsCompacting()=false after synchronous RunCompression on empty tier")
	}
}

func TestMemoryImport_MergeStrategy_SameContent(t *testing.T) {
	t.Parallel()

	h := NewMemoryHierarchy()
	g := NewKnowledgeGraph()
	importer := NewMemoryImporter(h, g, nil)

	// Add an existing item.
	if err := h.AddItem(&MemoryItem{
		ID:             "merge-same",
		Title:          "merge same",
		Content:        "shared content",
		Tier:           MemoryTierWorking,
		Status:         MemoryItemStatusActive,
		Importance:     ImportanceLevelMedium,
		Tags:           []string{"original"},
		Metadata:       map[string]any{"source": "local"},
		AccessCount:    1,
		CreatedAt:      time.Now().Add(-time.Hour),
		LastAccessedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	// Import the same content with merge strategy.
	_, err := importer.Import(&UniversalMemoryFormat{
		Memories: []UniversalMemory{
			{
				ID:         "merge-same",
				Content:    "shared content",
				Tier:       string(MemoryTierWorking),
				Importance: 0.5,
				Tags:       []string{"imported"},
				Metadata:   map[string]any{"origin": "remote"},
				CreatedAt:  time.Now(),
				AccessedAt: time.Now(),
			},
		},
	}, ImportOptions{
		ImportMemories:   true,
		ConflictStrategy: "merge",
	})
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}

	got, err := h.GetItem("merge-same")
	if err != nil {
		t.Fatalf("GetItem after merge import: %v", err)
	}

	// After merge of same content, access count should have been incremented.
	if got.AccessCount < 2 {
		t.Errorf("expected AccessCount >= 2 after merge, got %d", got.AccessCount)
	}
}

func TestMemoryImport_MergeStrategy_DifferentContent_HigherTier(t *testing.T) {
	t.Parallel()

	h := NewMemoryHierarchy()
	g := NewKnowledgeGraph()
	importer := NewMemoryImporter(h, g, nil)

	// Existing item at long-term tier.
	if err := h.AddItem(&MemoryItem{
		ID:             "merge-higher",
		Title:          "merge higher",
		Content:        "existing long-term content",
		Tier:           MemoryTierLongTerm,
		Status:         MemoryItemStatusActive,
		Importance:     ImportanceLevelMedium,
		CreatedAt:      time.Now().Add(-time.Hour),
		LastAccessedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	// Import different content at working tier (lower than existing).
	_, err := importer.Import(&UniversalMemoryFormat{
		Memories: []UniversalMemory{
			{
				ID:         "merge-higher",
				Content:    "new working content",
				Tier:       string(MemoryTierWorking),
				Importance: 0.5,
				CreatedAt:  time.Now(),
				AccessedAt: time.Now(),
			},
		},
	}, ImportOptions{
		ImportMemories:   true,
		ConflictStrategy: "merge",
	})
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}

	got, err := h.GetItem("merge-higher")
	if err != nil {
		t.Fatalf("GetItem after merge import: %v", err)
	}

	// Existing item at higher tier should win.
	if got.Tier != MemoryTierLongTerm {
		t.Errorf("expected tier %s (higher tier wins), got %s", MemoryTierLongTerm, got.Tier)
	}
	if got.Content != "existing long-term content" {
		t.Errorf("expected existing content to be preserved, got %q", got.Content)
	}
}

func TestMemoryImport_MergeStrategy_DifferentContent_LowerTier(t *testing.T) {
	t.Parallel()

	h := NewMemoryHierarchy()
	g := NewKnowledgeGraph()
	importer := NewMemoryImporter(h, g, nil)

	// Existing item at working tier (lowest).
	if err := h.AddItem(&MemoryItem{
		ID:             "merge-lower",
		Title:          "merge lower",
		Content:        "existing working content",
		Tier:           MemoryTierWorking,
		Status:         MemoryItemStatusActive,
		Importance:     ImportanceLevelMedium,
		CreatedAt:      time.Now().Add(-time.Hour),
		LastAccessedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	// Import different content at long-term tier (higher than existing).
	_, err := importer.Import(&UniversalMemoryFormat{
		Memories: []UniversalMemory{
			{
				ID:         "merge-lower",
				Content:    "new long-term content",
				Tier:       string(MemoryTierLongTerm),
				Importance: 0.5,
				CreatedAt:  time.Now(),
				AccessedAt: time.Now(),
			},
		},
	}, ImportOptions{
		ImportMemories:   true,
		ConflictStrategy: "merge",
	})
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}

	got, err := h.GetItem("merge-lower")
	if err != nil {
		t.Fatalf("GetItem after merge import: %v", err)
	}

	// Imported higher tier should overwrite: the old item is deleted and a
	// new one is created at the imported tier.
	if got.Tier != MemoryTierLongTerm {
		t.Errorf("expected tier %s (imported higher tier wins), got %s", MemoryTierLongTerm, got.Tier)
	}
	if got.Content != "new long-term content" {
		t.Errorf("expected imported content to win, got %q", got.Content)
	}
}

func TestTemplateHandlers_Deprecated(t *testing.T) {
	t.Parallel()

	svc := &TemplateSvc{&Service{}}

	t.Run("HandleTemplateCreate returns deprecation result", func(t *testing.T) {
		t.Parallel()
		result, err := svc.HandleTemplateCreate(context.Background(), nil)
		if err != nil {
			t.Fatalf("HandleTemplateCreate returned unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if !result.IsError {
			t.Error("expected result.IsError=true for deprecated handler")
		}
		if len(result.Content) == 0 {
			t.Fatal("expected non-empty Content")
		}
		if !strings.Contains(strings.ToLower(result.Content[0].Text), "deprecated") {
			t.Errorf("expected result text containing 'deprecated', got: %q", result.Content[0].Text)
		}
	})

	t.Run("HandleTemplateList returns deprecation result", func(t *testing.T) {
		t.Parallel()
		result, err := svc.HandleTemplateList(context.Background(), nil)
		if err != nil {
			t.Fatalf("HandleTemplateList returned unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if !result.IsError {
			t.Error("expected result.IsError=true for deprecated handler")
		}
		if len(result.Content) == 0 {
			t.Fatal("expected non-empty Content")
		}
		if !strings.Contains(strings.ToLower(result.Content[0].Text), "deprecated") {
			t.Errorf("expected result text containing 'deprecated', got: %q", result.Content[0].Text)
		}
	})
}

func TestContentHash(t *testing.T) {
	t.Parallel()

	t.Run("same content produces same hash", func(t *testing.T) {
		t.Parallel()
		h1 := simpleContentHash("hello world")
		h2 := simpleContentHash("hello world")
		if h1 != h2 {
			t.Errorf("same content produced different hashes: %d vs %d", h1, h2)
		}
	})

	t.Run("different content produces different hash", func(t *testing.T) {
		t.Parallel()
		h1 := simpleContentHash("hello world")
		h2 := simpleContentHash("goodbye world")
		if h1 == h2 {
			t.Error("different content produced the same hash")
		}
	})

	t.Run("empty string produces consistent hash", func(t *testing.T) {
		t.Parallel()
		h1 := simpleContentHash("")
		h2 := simpleContentHash("")
		if h1 != h2 {
			t.Errorf("empty string produced inconsistent hashes: %d vs %d", h1, h2)
		}
	})
}

func TestTierRank(t *testing.T) {
	t.Parallel()

	working := tierRank(MemoryTierWorking)
	shortTerm := tierRank(MemoryTierShortTerm)
	longTerm := tierRank(MemoryTierLongTerm)

	if working >= shortTerm {
		t.Errorf("expected working (%d) < short-term (%d)", working, shortTerm)
	}
	if shortTerm >= longTerm {
		t.Errorf("expected short-term (%d) < long-term (%d)", shortTerm, longTerm)
	}
}
