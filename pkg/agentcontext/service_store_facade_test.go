package agentcontext

import (
	"context"
	"testing"
)

func TestDurabilityConstants(t *testing.T) {
	t.Parallel()
	if DurabilitySession != "session" {
		t.Errorf("DurabilitySession = %q, want session", DurabilitySession)
	}
	if DurabilityPersistent != "persistent" {
		t.Errorf("DurabilityPersistent = %q, want persistent", DurabilityPersistent)
	}
	if DurabilityGraph != "graph" {
		t.Errorf("DurabilityGraph = %q, want graph", DurabilityGraph)
	}
}

func TestRouteToMemory_NilHierarchy(t *testing.T) {
	t.Parallel()
	svc := &Service{}
	session := &Session{ID: "s1", AgentID: "a1", Namespace: "ns"}
	m := map[string]any{"entry_type": "finding"}

	_, err := svc.routeToMemory(context.Background(), session, m, "title", "content")
	if err == nil {
		t.Error("expected error for nil memory hierarchy")
	}
}

func TestRouteToMemory_CreatesItem(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	svc := &Service{
		persistedMemoryHierarchy: mh.SetPersistence(nil),
		metrics:                  &Metrics{},
	}
	session := &Session{ID: "s1", AgentID: "a1", Namespace: "test/ns"}
	m := map[string]any{
		"entry_type": "decision",
		"tags":       []any{"important"},
		"metadata":   map[string]any{"key": "val"},
	}

	id, err := svc.routeToMemory(context.Background(), session, m, "My Decision", "Chose approach A")
	if err != nil {
		t.Fatalf("routeToMemory: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID")
	}

	// Verify item is in the hierarchy via Recall.
	result, err := mh.Recall(MemoryRecallRequest{
		Query:       "decision",
		Namespace:   "test/ns",
		TokenBudget: 4000,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(result.Items) == 0 {
		t.Fatal("expected item in long-term tier")
	}
	item := result.Items[0]
	if item.Title != "My Decision" {
		t.Errorf("Title = %q, want 'My Decision'", item.Title)
	}
	if item.Category != "decision" {
		t.Errorf("Category = %q, want 'decision'", item.Category)
	}
	if item.Namespace != "test/ns" {
		t.Errorf("Namespace = %q, want 'test/ns'", item.Namespace)
	}
}

func TestRouteToGraph_NilGraph(t *testing.T) {
	t.Parallel()
	svc := &Service{}
	session := &Session{ID: "s1", AgentID: "a1", Namespace: "ns"}
	m := map[string]any{"entry_type": "concept"}

	_, err := svc.routeToGraph(session, m, "title", "content")
	if err == nil {
		t.Error("expected error for nil knowledge graph")
	}
}

func TestRouteToGraph_CreatesEntity(t *testing.T) {
	t.Parallel()
	kg := NewKnowledgeGraph()
	svc := &Service{
		knowledgeGraph: kg,
		metrics:        &Metrics{},
	}
	session := &Session{ID: "s1", AgentID: "a1", Namespace: "test/ns"}
	m := map[string]any{
		"entry_type": "concept",
		"file_path":  "/src/main.go",
		"tags":       []any{"architecture"},
		"metadata":   map[string]any{"layer": "domain"},
	}

	id, err := svc.routeToGraph(session, m, "DDD Pattern", "Domain-driven design pattern for services")
	if err != nil {
		t.Fatalf("routeToGraph: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID")
	}

	// Verify entity is in the graph.
	entities := kg.FindEntities(EntityTypeConcept, "test/ns", "", 10)
	if len(entities) == 0 {
		t.Fatal("expected entity in graph")
	}
	entity := entities[0]
	if entity.Name != "DDD Pattern" {
		t.Errorf("Name = %q, want 'DDD Pattern'", entity.Name)
	}
	if entity.Description != "Domain-driven design pattern for services" {
		t.Errorf("Description = %q, want 'Domain-driven design pattern for services'", entity.Description)
	}
	if entity.FilePath != "/src/main.go" {
		t.Errorf("FilePath = %q, want '/src/main.go'", entity.FilePath)
	}
}

func TestRouteToGraph_DefaultEntityType(t *testing.T) {
	t.Parallel()
	kg := NewKnowledgeGraph()
	svc := &Service{
		knowledgeGraph: kg,
		metrics:        &Metrics{},
	}
	session := &Session{ID: "s1", AgentID: "a1", Namespace: "ns"}
	// No entry_type specified — should default to "concept".
	m := map[string]any{}

	_, err := svc.routeToGraph(session, m, "Untitled", "Some content")
	if err != nil {
		t.Fatalf("routeToGraph: %v", err)
	}

	entities := kg.FindEntities(EntityTypeConcept, "ns", "", 10)
	if len(entities) == 0 {
		t.Fatal("expected entity with default concept type")
	}
}

func TestBuildContextEntry_Fields(t *testing.T) {
	t.Parallel()
	svc := &Service{cfg: Config{DefaultVisibility: VisibilityPrivate}}
	session := &Session{ID: "sess-1", AgentID: "agent-1", Namespace: "test/ns"}
	m := map[string]any{
		"entry_type": "decision",
		"file_path":  "/foo.go",
		"line_start": 10,
		"line_end":   20,
		"tags":       []any{"tag1"},
		"metadata":   map[string]any{"k": "v"},
	}

	entry := svc.buildContextEntry(session, m, "Title", "Content")

	if entry.ID == "" {
		t.Error("expected non-empty ID")
	}
	if entry.AgentID != "agent-1" {
		t.Errorf("AgentID = %q, want agent-1", entry.AgentID)
	}
	if entry.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", entry.SessionID)
	}
	if entry.Namespace != "test/ns" {
		t.Errorf("Namespace = %q, want test/ns", entry.Namespace)
	}
	if entry.EntryType != EntryTypeDecision {
		t.Errorf("EntryType = %q, want decision", entry.EntryType)
	}
	if entry.Title != "Title" {
		t.Errorf("Title = %q, want Title", entry.Title)
	}
	if entry.FilePath != "/foo.go" {
		t.Errorf("FilePath = %q, want /foo.go", entry.FilePath)
	}
	if entry.Visibility != VisibilityPrivate {
		t.Errorf("Visibility = %q, want private", entry.Visibility)
	}
	if entry.Metadata["k"] != "v" {
		t.Errorf("Metadata[k] = %v, want v", entry.Metadata["k"])
	}
}
