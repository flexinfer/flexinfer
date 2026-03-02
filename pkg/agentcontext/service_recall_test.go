package agentcontext

import (
	"context"
	"testing"
)

func TestScopeIncludes_EmptyIncludesAll(t *testing.T) {
	t.Parallel()
	var empty RecallScope
	if !scopeIncludes(empty, RecallSourceContext) {
		t.Error("empty scope should include context")
	}
	if !scopeIncludes(empty, RecallSourceMemory) {
		t.Error("empty scope should include memory")
	}
	if !scopeIncludes(empty, RecallSourceGraph) {
		t.Error("empty scope should include graph")
	}
}

func TestScopeIncludes_FilteredScope(t *testing.T) {
	t.Parallel()
	scope := RecallScope{RecallSourceContext, RecallSourceGraph}
	if !scopeIncludes(scope, RecallSourceContext) {
		t.Error("scope should include context")
	}
	if scopeIncludes(scope, RecallSourceMemory) {
		t.Error("scope should not include memory")
	}
	if !scopeIncludes(scope, RecallSourceGraph) {
		t.Error("scope should include graph")
	}
}

func TestScopeIncludes_SingleBackend(t *testing.T) {
	t.Parallel()
	scope := RecallScope{RecallSourceMemory}
	if scopeIncludes(scope, RecallSourceContext) {
		t.Error("scope should not include context")
	}
	if !scopeIncludes(scope, RecallSourceMemory) {
		t.Error("scope should include memory")
	}
	if scopeIncludes(scope, RecallSourceGraph) {
		t.Error("scope should not include graph")
	}
}

func TestRecallSourceConstants(t *testing.T) {
	t.Parallel()
	if RecallSourceContext != "context" {
		t.Errorf("RecallSourceContext = %q, want context", RecallSourceContext)
	}
	if RecallSourceMemory != "memory" {
		t.Errorf("RecallSourceMemory = %q, want memory", RecallSourceMemory)
	}
	if RecallSourceGraph != "graph" {
		t.Errorf("RecallSourceGraph = %q, want graph", RecallSourceGraph)
	}
}

func TestMemoryRecallToEntries_NilHierarchy(t *testing.T) {
	t.Parallel()
	svc := &Service{
		memoryHierarchy: nil,
	}

	opts := EnhancedRecallOptions{
		RecallOptions: RecallOptions{
			Query:       "test query",
			TokenBudget: 4000,
		},
	}
	// Should not panic with nil memoryHierarchy.
	entries := svc.memoryRecallToEntries(opts, "", "")
	if entries != nil {
		t.Errorf("expected nil entries for nil hierarchy, got %d", len(entries))
	}
}

func TestMemoryRecallToEntries_ConvertsItems(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	svc := &Service{
		memoryHierarchy: mh,
	}

	// Add a memory item to working tier.
	item := &MemoryItem{
		ID:              "mem-1",
		Tier:            MemoryTierWorking,
		Status:          MemoryItemStatusActive,
		Importance:      ImportanceLevelMedium,
		ImportanceScore: 0.5,
		Title:           "Test Memory",
		Content:         "some recalled content",
		Category:        "finding",
		Namespace:       "test/ns",
		AgentID:         "agent-1",
		OriginalTokens:  100,
	}
	if err := mh.AddItem(item); err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	opts := EnhancedRecallOptions{
		RecallOptions: RecallOptions{
			Query:       "test",
			Namespace:   "test/ns",
			TokenBudget: 4000,
		},
	}

	entries := svc.memoryRecallToEntries(opts, "agent-1", "")
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}

	entry := entries[0]
	if entry.ID != "mem-1" {
		t.Errorf("ID = %q, want mem-1", entry.ID)
	}
	if entry.Content != "some recalled content" {
		t.Errorf("Content = %q, want 'some recalled content'", entry.Content)
	}
	if entry.Metadata == nil {
		t.Fatal("Metadata is nil")
	}
	if tier, ok := entry.Metadata["memory_tier"].(string); !ok || tier != "working" {
		t.Errorf("memory_tier = %v, want working", entry.Metadata["memory_tier"])
	}
	if entry.TokenCount != 100 {
		t.Errorf("TokenCount = %d, want 100", entry.TokenCount)
	}
}

func TestEnhancedRecallOptions_ScopeDefaults(t *testing.T) {
	t.Parallel()
	opts := EnhancedRecallOptions{
		IncludeMemory: true,
		IncludeGraph:  true,
	}
	if len(opts.Scope) != 0 {
		t.Errorf("default Scope should be empty, got %v", opts.Scope)
	}
	if !opts.IncludeMemory {
		t.Error("IncludeMemory should be true")
	}
	if !opts.IncludeGraph {
		t.Error("IncludeGraph should be true")
	}
}

func TestGraphSearchToEntries_NilGraph(t *testing.T) {
	t.Parallel()
	svc := &Service{
		persistedKnowledgeGraph: nil,
	}
	// Should not panic with nil graph.
	entries := svc.graphSearchToEntries(context.Background(), nil, "")
	if entries != nil {
		t.Errorf("expected nil for nil graph, got %d entries", len(entries))
	}
}
