package agentcontext

import (
	"context"
	"encoding/json"
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

func TestNormalizeLegacyEnhancedScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                               string
		raw                                any
		wantScope                          string
		wantContext, wantMemory, wantGraph bool
	}{
		{
			name:        "context string",
			raw:         "context",
			wantScope:   "context",
			wantContext: true,
		},
		{
			name:       "memory array any",
			raw:        []any{"memory"},
			wantScope:  "memory",
			wantMemory: true,
		},
		{
			name:        "context and graph mixed",
			raw:         []string{"context", "graph"},
			wantScope:   "context",
			wantContext: true,
			wantGraph:   true,
		},
		{
			name:      "graph only maps to context backend",
			raw:       []any{"graph"},
			wantScope: "context",
			wantGraph: true,
		},
		{
			name:      "invalid scope",
			raw:       []any{"unknown"},
			wantScope: "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scope, hasContext, hasMemory, hasGraph := normalizeLegacyEnhancedScope(tc.raw)
			if scope != tc.wantScope {
				t.Fatalf("scope = %q, want %q", scope, tc.wantScope)
			}
			if hasContext != tc.wantContext {
				t.Fatalf("hasContext = %v, want %v", hasContext, tc.wantContext)
			}
			if hasMemory != tc.wantMemory {
				t.Fatalf("hasMemory = %v, want %v", hasMemory, tc.wantMemory)
			}
			if hasGraph != tc.wantGraph {
				t.Fatalf("hasGraph = %v, want %v", hasGraph, tc.wantGraph)
			}
		})
	}
}

func TestHandleDeprecatedEnhancedRecall_DefaultRouteToUnified(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	svc := &Service{
		cfg: Config{
			DefaultTokenBudget:   4000,
			DefaultRecencyWeight: 0.2,
		},
		metrics:         GetMetrics(),
		memoryHierarchy: NewMemoryHierarchy(),
	}

	result, err := svc.HandleDeprecatedEnhancedRecall(context.Background(), map[string]any{
		"query":             "test",
		"token_budget":      0,
		"include_tasks":     false,
		"include_decisions": false,
		"include_summaries": false,
	})
	if err != nil {
		t.Fatalf("HandleDeprecatedEnhancedRecall error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected non-error result, got %#v", result)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty content")
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if got := payload["scope"]; got != "all" {
		t.Fatalf("scope = %v, want all", got)
	}
}
