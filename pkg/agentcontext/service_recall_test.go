package agentcontext

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
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

func TestHandleUnifiedRecall_GraphScope(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	kg := NewKnowledgeGraph()
	_ = kg.AddEntity(&Entity{
		ID:          "ent-router-1",
		Type:        EntityTypeFunction,
		Name:        "OrchestraRouter",
		Description: "Routes MCP tool calls to orchestra backends",
		Namespace:   "loom/orchestra",
	})
	_ = kg.AddEntity(&Entity{
		ID:          "ent-router-2",
		Type:        EntityTypeService,
		Name:        "RouterService",
		Description: "HTTP router for the orchestra service",
		Namespace:   "loom/orchestra",
	})
	_ = kg.AddEntity(&Entity{
		ID:          "ent-unrelated",
		Type:        EntityTypeConcept,
		Name:        "MemoryHierarchy",
		Description: "Tiered memory for agent context",
		Namespace:   "loom/memory",
	})

	svc := &Service{
		cfg: Config{
			DefaultTokenBudget:   4000,
			DefaultRecencyWeight: 0.2,
		},
		metrics:        GetMetrics(),
		knowledgeGraph: kg,
	}

	result, err := svc.HandleUnifiedRecall(context.Background(), map[string]any{
		"query":             "Router",
		"scope":             "graph",
		"token_budget":      0,
		"include_tasks":     false,
		"include_decisions": false,
		"include_summaries": false,
	})
	if err != nil {
		t.Fatalf("HandleUnifiedRecall error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected non-error result, got %#v", result)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if payload["scope"] != "graph" {
		t.Fatalf("scope = %v, want graph", payload["scope"])
	}

	graphEntities, ok := payload["graph_entities"].([]any)
	if !ok {
		t.Fatalf("graph_entities missing or wrong type: %T", payload["graph_entities"])
	}
	if len(graphEntities) != 2 {
		t.Fatalf("graph_entities count = %d, want 2", len(graphEntities))
	}

	// Verify count includes graph entities.
	count := int(payload["count"].(float64))
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}

	graphCount := int(payload["graph_count"].(float64))
	if graphCount != 2 {
		t.Fatalf("graph_count = %d, want 2", graphCount)
	}

	// Verify no context entries returned (scope=graph should skip context).
	if _, exists := payload["entries"]; exists {
		t.Fatal("scope=graph should not return context entries")
	}
}

func TestHandleUnifiedRecall_AllScopeIncludesGraph(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	kg := NewKnowledgeGraph()
	_ = kg.AddEntity(&Entity{
		ID:          "ent-all-1",
		Type:        EntityTypeFunction,
		Name:        "AllScopeTarget",
		Description: "Entity for all-scope test",
		Namespace:   "test",
	})

	svc := &Service{
		cfg: Config{
			DefaultTokenBudget:   4000,
			DefaultRecencyWeight: 0.2,
		},
		metrics:         GetMetrics(),
		memoryHierarchy: NewMemoryHierarchy(),
		knowledgeGraph:  kg,
	}

	result, err := svc.HandleUnifiedRecall(context.Background(), map[string]any{
		"query":             "AllScopeTarget",
		"scope":             "all",
		"token_budget":      0,
		"include_tasks":     false,
		"include_decisions": false,
		"include_summaries": false,
	})
	if err != nil {
		t.Fatalf("HandleUnifiedRecall error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if payload["scope"] != "all" {
		t.Fatalf("scope = %v, want all", payload["scope"])
	}

	graphEntities, ok := payload["graph_entities"].([]any)
	if !ok {
		t.Fatalf("graph_entities missing or wrong type: %T", payload["graph_entities"])
	}
	if len(graphEntities) != 1 {
		t.Fatalf("graph_entities count = %d, want 1", len(graphEntities))
	}

	// Verify count includes graph entity.
	count := int(payload["count"].(float64))
	if count < 1 {
		t.Fatalf("count = %d, want >= 1", count)
	}
}

func TestHandleUnifiedRecall_GraphScopeNilGraph(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	svc := &Service{
		cfg: Config{
			DefaultTokenBudget:   4000,
			DefaultRecencyWeight: 0.2,
		},
		metrics:        GetMetrics(),
		knowledgeGraph: nil,
	}

	result, err := svc.HandleUnifiedRecall(context.Background(), map[string]any{
		"query":             "anything",
		"scope":             "graph",
		"token_budget":      0,
		"include_tasks":     false,
		"include_decisions": false,
		"include_summaries": false,
	})
	if err != nil {
		t.Fatalf("HandleUnifiedRecall error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected non-error result, got %#v", result)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// Should have zero count and no graph_entities key.
	count := int(payload["count"].(float64))
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
	if _, exists := payload["graph_entities"]; exists {
		t.Fatal("expected no graph_entities for nil knowledge graph")
	}
}

func TestHandleUnifiedRecall_GraphScopeWithNamespace(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	kg := NewKnowledgeGraph()
	_ = kg.AddEntity(&Entity{
		ID:          "ent-ns-1",
		Type:        EntityTypeFunction,
		Name:        "HandleRouter",
		Description: "Handler in orchestra namespace",
		Namespace:   "loom/orchestra",
	})
	_ = kg.AddEntity(&Entity{
		ID:          "ent-ns-2",
		Type:        EntityTypeFunction,
		Name:        "HandleRouter",
		Description: "Handler in different namespace",
		Namespace:   "loom/other",
	})

	svc := &Service{
		cfg: Config{
			DefaultTokenBudget:   4000,
			DefaultRecencyWeight: 0.2,
		},
		metrics:        GetMetrics(),
		knowledgeGraph: kg,
	}

	result, err := svc.HandleUnifiedRecall(context.Background(), map[string]any{
		"query":             "Router",
		"scope":             "graph",
		"namespace":         "loom/orchestra",
		"token_budget":      0,
		"include_tasks":     false,
		"include_decisions": false,
		"include_summaries": false,
	})
	if err != nil {
		t.Fatalf("HandleUnifiedRecall error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	graphEntities, ok := payload["graph_entities"].([]any)
	if !ok {
		t.Fatalf("graph_entities missing: %T", payload["graph_entities"])
	}
	if len(graphEntities) != 1 {
		t.Fatalf("graph_entities count = %d, want 1 (namespace filtered)", len(graphEntities))
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

func TestHandleUnifiedRecall_RecallMetaFields(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	kg := NewKnowledgeGraph()
	_ = kg.AddEntity(&Entity{
		ID:          "ent-meta-1",
		Type:        EntityTypeFunction,
		Name:        "AllScopeTarget",
		Description: "Entity for recall meta test",
		Namespace:   "test",
	})

	svc := &Service{
		cfg: Config{
			DefaultTokenBudget:   4000,
			DefaultRecencyWeight: 0.2,
		},
		metrics:         NewMetrics(),
		memoryHierarchy: NewMemoryHierarchy(),
		knowledgeGraph:  kg,
	}

	result, err := svc.HandleUnifiedRecall(context.Background(), map[string]any{
		"query":             "AllScopeTarget",
		"scope":             "all",
		"token_budget":      0,
		"include_tasks":     false,
		"include_decisions": false,
		"include_summaries": false,
	})
	if err != nil {
		t.Fatalf("HandleUnifiedRecall error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	meta := requireRecallMeta(t, payload)
	assertStringSlice(t, meta["backends_queried"], []string{"context", "memory", "graph"})
	assertStringSlice(t, meta["backends_failed"], nil)
	assertIntField(t, meta, "total_candidates", 1)
	assertIntField(t, meta, "returned", 1)
	assertIntField(t, meta, "token_budget_used", 0)
	assertNumberField(t, meta, "latency_ms")

	if got := int(payload["count"].(float64)); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
	if got := int(payload["graph_count"].(float64)); got != 1 {
		t.Fatalf("graph_count = %d, want 1", got)
	}

	snap := svc.metrics.Snapshot()
	for _, backend := range []string{"context", "memory", "graph"} {
		stats, ok := snap.RecallLatencyByBackend[backend]
		if !ok {
			t.Fatalf("missing recall latency stats for backend %q", backend)
		}
		if stats.Count != 1 {
			t.Fatalf("recall latency count for %q = %d, want 1", backend, stats.Count)
		}
	}
}

func TestHandleUnifiedRecall_DegradedMemoryBackendWarns(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	kg := NewKnowledgeGraph()
	_ = kg.AddEntity(&Entity{
		ID:          "ent-warning-1",
		Type:        EntityTypeFunction,
		Name:        "WarningTarget",
		Description: "Entity for warning path test",
		Namespace:   "test",
	})

	svc := &Service{
		cfg: Config{
			DefaultTokenBudget:   4000,
			DefaultRecencyWeight: 0.2,
		},
		metrics:         NewMetrics(),
		memoryHierarchy: nil,
		knowledgeGraph:  kg,
	}

	result, err := svc.HandleUnifiedRecall(context.Background(), map[string]any{
		"query":             "WarningTarget",
		"scope":             "all",
		"token_budget":      0,
		"include_tasks":     false,
		"include_decisions": false,
		"include_summaries": false,
	})
	if err != nil {
		t.Fatalf("HandleUnifiedRecall error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	meta := requireRecallMeta(t, payload)
	assertStringSlice(t, meta["backends_queried"], []string{"context", "memory", "graph"})
	assertStringSlice(t, meta["backends_failed"], []string{"memory"})
	assertIntField(t, meta, "total_candidates", 1)
	assertIntField(t, meta, "returned", 1)

	warnings := assertStringSlice(t, payload["_warnings"], []string{
		"memory backend unavailable: memory hierarchy is nil",
	})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "memory backend unavailable") {
		t.Fatalf("warnings = %v, want memory backend warning", warnings)
	}

	if got := int(payload["count"].(float64)); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
	if got := int(payload["graph_count"].(float64)); got != 1 {
		t.Fatalf("graph_count = %d, want 1", got)
	}

	snap := svc.metrics.Snapshot()
	for _, backend := range []string{"context", "memory", "graph"} {
		stats, ok := snap.RecallLatencyByBackend[backend]
		if !ok {
			t.Fatalf("missing recall latency stats for backend %q", backend)
		}
		if stats.Count != 1 {
			t.Fatalf("recall latency count for %q = %d, want 1", backend, stats.Count)
		}
	}
}

func requireRecallMeta(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()

	meta, ok := payload["recall_meta"].(map[string]any)
	if !ok {
		t.Fatalf("recall_meta missing or wrong type: %T", payload["recall_meta"])
	}
	return meta
}

func assertStringSlice(t *testing.T, value any, want []string) []string {
	t.Helper()

	if want == nil {
		want = []string{}
	}

	var got []string
	switch typed := value.(type) {
	case []any:
		got = make([]string, len(typed))
		for i, raw := range typed {
			str, ok := raw.(string)
			if !ok {
				t.Fatalf("slice item %d has type %T, want string", i, raw)
			}
			got[i] = str
		}
	case nil:
		got = []string{}
	default:
		t.Fatalf("value has type %T, want []any or nil", value)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slice = %v, want %v", got, want)
	}
	return got
}

func assertIntField(t *testing.T, payload map[string]any, key string, want int) {
	t.Helper()

	got, ok := payload[key].(float64)
	if !ok {
		t.Fatalf("%s has type %T, want numeric", key, payload[key])
	}
	if int(got) != want {
		t.Fatalf("%s = %v, want %d", key, payload[key], want)
	}
}

func assertNumberField(t *testing.T, payload map[string]any, key string) {
	t.Helper()

	if _, ok := payload[key].(float64); !ok {
		t.Fatalf("%s has type %T, want numeric", key, payload[key])
	}
}
