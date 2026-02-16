package agentcontext

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// NewKnowledgeGraph
// ---------------------------------------------------------------------------

func TestNewKnowledgeGraph_EmptyState(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	if g == nil {
		t.Fatal("expected non-nil graph")
	}
	stats := g.Stats()
	if stats.TotalEntities != 0 {
		t.Errorf("expected 0 entities, got %d", stats.TotalEntities)
	}
	if stats.TotalRelations != 0 {
		t.Errorf("expected 0 relations, got %d", stats.TotalRelations)
	}
	if len(stats.EntitiesByType) != 0 {
		t.Errorf("expected empty EntitiesByType, got %v", stats.EntitiesByType)
	}
	if len(stats.RelationsByType) != 0 {
		t.Errorf("expected empty RelationsByType, got %v", stats.RelationsByType)
	}
}

// ---------------------------------------------------------------------------
// Entity CRUD
// ---------------------------------------------------------------------------

func TestAddEntity_GeneratesID(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	e := &Entity{Type: EntityTypeFile, Name: "main.go"}
	if err := g.AddEntity(e); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}
	if e.ID == "" {
		t.Error("expected ID to be generated")
	}
	if e.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if e.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestAddEntity_PreservesExistingID(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	e := &Entity{ID: "custom-id", Type: EntityTypeFile, Name: "main.go"}
	if err := g.AddEntity(e); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}
	if e.ID != "custom-id" {
		t.Errorf("expected ID 'custom-id', got %q", e.ID)
	}
}

func TestAddEntity_RequiresName(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	if err := g.AddEntity(&Entity{Type: EntityTypeFile}); err == nil {
		t.Error("expected error for missing name")
	}
}

func TestAddEntity_RequiresType(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	if err := g.AddEntity(&Entity{Name: "test"}); err == nil {
		t.Error("expected error for missing type")
	}
}

func TestGetEntity_Exists(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	e := &Entity{Type: EntityTypeFunction, Name: "init", Description: "Initializes the app"}
	g.AddEntity(e)

	got, err := g.GetEntity(e.ID)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}
	if got.Name != "init" {
		t.Errorf("Name: got %q", got.Name)
	}
	if got.Description != "Initializes the app" {
		t.Errorf("Description mismatch")
	}
}

func TestGetEntity_NotFound(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	_, err := g.GetEntity("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent entity")
	}
}

func TestDeleteEntity_RemovesFromGraph(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	e := &Entity{Type: EntityTypeFile, Name: "file.go"}
	g.AddEntity(e)

	if err := g.DeleteEntity(e.ID); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	_, err := g.GetEntity(e.ID)
	if err == nil {
		t.Error("expected error after deletion")
	}

	stats := g.Stats()
	if stats.TotalEntities != 0 {
		t.Errorf("expected 0 entities after delete, got %d", stats.TotalEntities)
	}
}

func TestDeleteEntity_CascadesRelations(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeFile, Name: "a.go"}
	b := &Entity{Type: EntityTypeFunction, Name: "funcB"}
	g.AddEntity(a)
	g.AddEntity(b)
	g.AddRelation(&Relation{Type: RelationContains, SourceID: a.ID, TargetID: b.ID})

	g.DeleteEntity(a.ID)

	// Relations referencing deleted entity should be cleaned up
	rels := g.GetEntityRelations(b.ID, nil, true, true)
	if len(rels) != 0 {
		t.Errorf("expected 0 relations after cascading delete, got %d", len(rels))
	}
}

func TestDeleteEntity_NotFound(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	err := g.DeleteEntity("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent entity")
	}
}

func TestFindEntities_ByType(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	g.AddEntity(&Entity{Type: EntityTypeFile, Name: "a.go"})
	g.AddEntity(&Entity{Type: EntityTypeFile, Name: "b.go"})
	g.AddEntity(&Entity{Type: EntityTypeFunction, Name: "main"})

	files := g.FindEntities(EntityTypeFile, "", "", 0)
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
}

func TestFindEntities_ByNamespace(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	g.AddEntity(&Entity{Type: EntityTypeFile, Name: "a.go", Namespace: "pkg"})
	g.AddEntity(&Entity{Type: EntityTypeFile, Name: "b.go", Namespace: "internal"})

	pkgEnts := g.FindEntities("", "pkg", "", 0)
	if len(pkgEnts) != 1 {
		t.Errorf("expected 1 entity in pkg namespace, got %d", len(pkgEnts))
	}
}

func TestFindEntities_ByNamePattern(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	g.AddEntity(&Entity{Type: EntityTypeFunction, Name: "handleRequest"})
	g.AddEntity(&Entity{Type: EntityTypeFunction, Name: "handleResponse"})
	g.AddEntity(&Entity{Type: EntityTypeFunction, Name: "processData"})

	results := g.FindEntities("", "", "handle", 0)
	if len(results) != 2 {
		t.Errorf("expected 2 entities matching 'handle', got %d", len(results))
	}
}

func TestFindEntities_WithLimit(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	for i := 0; i < 10; i++ {
		g.AddEntity(&Entity{Type: EntityTypeFile, Name: "file" + string(rune('a'+i)) + ".go"})
	}

	limited := g.FindEntities("", "", "", 3)
	if len(limited) != 3 {
		t.Errorf("expected 3 entities with limit, got %d", len(limited))
	}
}

func TestFindEntities_TypeAndNamespace(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	g.AddEntity(&Entity{Type: EntityTypeFunction, Name: "funcA", Namespace: "api"})
	g.AddEntity(&Entity{Type: EntityTypeFunction, Name: "funcB", Namespace: "internal"})
	g.AddEntity(&Entity{Type: EntityTypeClass, Name: "SvcA", Namespace: "api"})

	results := g.FindEntities(EntityTypeFunction, "api", "", 0)
	if len(results) != 1 {
		t.Errorf("expected 1 function in api, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// Relation CRUD
// ---------------------------------------------------------------------------

func TestAddRelation_GeneratesID(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeFile, Name: "a.go"}
	b := &Entity{Type: EntityTypeFunction, Name: "funcA"}
	g.AddEntity(a)
	g.AddEntity(b)

	rel := &Relation{Type: RelationContains, SourceID: a.ID, TargetID: b.ID}
	if err := g.AddRelation(rel); err != nil {
		t.Fatalf("AddRelation: %v", err)
	}
	if rel.ID == "" {
		t.Error("expected relation ID to be generated")
	}
}

func TestAddRelation_RequiresSource(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	e := &Entity{Type: EntityTypeFile, Name: "test.go"}
	g.AddEntity(e)
	err := g.AddRelation(&Relation{Type: RelationCalls, TargetID: e.ID})
	if err == nil {
		t.Error("expected error for missing source_id")
	}
}

func TestAddRelation_RequiresTarget(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	e := &Entity{Type: EntityTypeFile, Name: "test.go"}
	g.AddEntity(e)
	err := g.AddRelation(&Relation{Type: RelationCalls, SourceID: e.ID})
	if err == nil {
		t.Error("expected error for missing target_id")
	}
}

func TestAddRelation_SourceMustExist(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	e := &Entity{Type: EntityTypeFile, Name: "test.go"}
	g.AddEntity(e)
	err := g.AddRelation(&Relation{Type: RelationCalls, SourceID: "nonexistent", TargetID: e.ID})
	if err == nil {
		t.Error("expected error for nonexistent source")
	}
}

func TestAddRelation_TargetMustExist(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	e := &Entity{Type: EntityTypeFile, Name: "test.go"}
	g.AddEntity(e)
	err := g.AddRelation(&Relation{Type: RelationCalls, SourceID: e.ID, TargetID: "nonexistent"})
	if err == nil {
		t.Error("expected error for nonexistent target")
	}
}

func TestGetRelation_Exists(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeFile, Name: "a.go"}
	b := &Entity{Type: EntityTypeFunction, Name: "funcA"}
	g.AddEntity(a)
	g.AddEntity(b)
	rel := &Relation{Type: RelationContains, SourceID: a.ID, TargetID: b.ID, Weight: 0.9}
	g.AddRelation(rel)

	got, err := g.GetRelation(rel.ID)
	if err != nil {
		t.Fatalf("GetRelation: %v", err)
	}
	if got.Weight != 0.9 {
		t.Errorf("Weight: got %f", got.Weight)
	}
}

func TestGetRelation_NotFound(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	_, err := g.GetRelation("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent relation")
	}
}

func TestDeleteRelation(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeFile, Name: "a.go"}
	b := &Entity{Type: EntityTypeFunction, Name: "funcA"}
	g.AddEntity(a)
	g.AddEntity(b)
	rel := &Relation{Type: RelationContains, SourceID: a.ID, TargetID: b.ID}
	g.AddRelation(rel)

	if err := g.DeleteRelation(rel.ID); err != nil {
		t.Fatalf("DeleteRelation: %v", err)
	}

	_, err := g.GetRelation(rel.ID)
	if err == nil {
		t.Error("expected error after deletion")
	}
}

func TestGetEntityRelations_Outgoing(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeFile, Name: "a.go"}
	b := &Entity{Type: EntityTypeFunction, Name: "funcA"}
	c := &Entity{Type: EntityTypeFunction, Name: "funcB"}
	g.AddEntity(a)
	g.AddEntity(b)
	g.AddEntity(c)
	g.AddRelation(&Relation{Type: RelationContains, SourceID: a.ID, TargetID: b.ID})
	g.AddRelation(&Relation{Type: RelationContains, SourceID: a.ID, TargetID: c.ID})

	outgoing := g.GetEntityRelations(a.ID, nil, true, false)
	if len(outgoing) != 2 {
		t.Errorf("expected 2 outgoing, got %d", len(outgoing))
	}
}

func TestGetEntityRelations_Incoming(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeFunction, Name: "caller"}
	b := &Entity{Type: EntityTypeFunction, Name: "callee"}
	g.AddEntity(a)
	g.AddEntity(b)
	g.AddRelation(&Relation{Type: RelationCalls, SourceID: a.ID, TargetID: b.ID})

	incoming := g.GetEntityRelations(b.ID, nil, false, true)
	if len(incoming) != 1 {
		t.Errorf("expected 1 incoming, got %d", len(incoming))
	}
}

func TestGetEntityRelations_FilterByType(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeFile, Name: "a.go"}
	b := &Entity{Type: EntityTypeFunction, Name: "funcA"}
	c := &Entity{Type: EntityTypeModule, Name: "pkg"}
	g.AddEntity(a)
	g.AddEntity(b)
	g.AddEntity(c)
	g.AddRelation(&Relation{Type: RelationContains, SourceID: a.ID, TargetID: b.ID})
	g.AddRelation(&Relation{Type: RelationImports, SourceID: a.ID, TargetID: c.ID})

	containsOnly := g.GetEntityRelations(a.ID, []RelationType{RelationContains}, true, false)
	if len(containsOnly) != 1 {
		t.Errorf("expected 1 contains relation, got %d", len(containsOnly))
	}
}

func TestBidirectionalRelation(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeConcept, Name: "REST"}
	b := &Entity{Type: EntityTypeConcept, Name: "HTTP"}
	g.AddEntity(a)
	g.AddEntity(b)
	g.AddRelation(&Relation{Type: RelationRelatedTo, SourceID: a.ID, TargetID: b.ID, Bidirectional: true})

	// Both should have outgoing relations
	aOut := g.GetEntityRelations(a.ID, nil, true, false)
	bOut := g.GetEntityRelations(b.ID, nil, true, false)
	if len(aOut) != 1 {
		t.Errorf("expected 1 outgoing from A, got %d", len(aOut))
	}
	if len(bOut) != 1 {
		t.Errorf("expected 1 outgoing from B (bidirectional), got %d", len(bOut))
	}
}

// ---------------------------------------------------------------------------
// FindPath
// ---------------------------------------------------------------------------

func TestFindPath_ShortestPath(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeModule, Name: "A"}
	b := &Entity{Type: EntityTypeModule, Name: "B"}
	c := &Entity{Type: EntityTypeModule, Name: "C"}
	g.AddEntity(a)
	g.AddEntity(b)
	g.AddEntity(c)
	g.AddRelation(&Relation{Type: RelationDependsOn, SourceID: a.ID, TargetID: b.ID})
	g.AddRelation(&Relation{Type: RelationDependsOn, SourceID: b.ID, TargetID: c.ID})

	path, err := g.FindPath(a.ID, c.ID, 5, nil)
	if err != nil {
		t.Fatalf("FindPath: %v", err)
	}
	if path.Length != 2 {
		t.Errorf("expected path length 2, got %d", path.Length)
	}
	if path.Nodes[0] != a.ID || path.Nodes[len(path.Nodes)-1] != c.ID {
		t.Error("path should start with A and end with C")
	}
}

func TestFindPath_DirectConnection(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeModule, Name: "A"}
	b := &Entity{Type: EntityTypeModule, Name: "B"}
	g.AddEntity(a)
	g.AddEntity(b)
	g.AddRelation(&Relation{Type: RelationDependsOn, SourceID: a.ID, TargetID: b.ID})

	path, err := g.FindPath(a.ID, b.ID, 5, nil)
	if err != nil {
		t.Fatalf("FindPath: %v", err)
	}
	if path.Length != 1 {
		t.Errorf("expected path length 1, got %d", path.Length)
	}
}

func TestFindPath_NoPathExists(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeModule, Name: "A"}
	b := &Entity{Type: EntityTypeModule, Name: "B"}
	g.AddEntity(a)
	g.AddEntity(b)
	// No relation between them

	_, err := g.FindPath(a.ID, b.ID, 5, nil)
	if err == nil {
		t.Error("expected error when no path exists")
	}
}

func TestFindPath_MaxDepthRespected(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeModule, Name: "A"}
	b := &Entity{Type: EntityTypeModule, Name: "B"}
	c := &Entity{Type: EntityTypeModule, Name: "C"}
	d := &Entity{Type: EntityTypeModule, Name: "D"}
	g.AddEntity(a)
	g.AddEntity(b)
	g.AddEntity(c)
	g.AddEntity(d)
	g.AddRelation(&Relation{Type: RelationDependsOn, SourceID: a.ID, TargetID: b.ID})
	g.AddRelation(&Relation{Type: RelationDependsOn, SourceID: b.ID, TargetID: c.ID})
	g.AddRelation(&Relation{Type: RelationDependsOn, SourceID: c.ID, TargetID: d.ID})

	// Path A->D is length 3, but max depth 2 should fail
	_, err := g.FindPath(a.ID, d.ID, 2, nil)
	if err == nil {
		t.Error("expected error when path exceeds max depth")
	}
}

func TestFindPath_ChoosingShortestOfMultiple(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	// A -> B -> D (length 2)
	// A -> C -> D (length 2)
	// Both have same length, but path exists
	a := &Entity{Type: EntityTypeModule, Name: "A"}
	b := &Entity{Type: EntityTypeModule, Name: "B"}
	c := &Entity{Type: EntityTypeModule, Name: "C"}
	d := &Entity{Type: EntityTypeModule, Name: "D"}
	g.AddEntity(a)
	g.AddEntity(b)
	g.AddEntity(c)
	g.AddEntity(d)
	g.AddRelation(&Relation{Type: RelationDependsOn, SourceID: a.ID, TargetID: b.ID})
	g.AddRelation(&Relation{Type: RelationDependsOn, SourceID: b.ID, TargetID: d.ID})
	g.AddRelation(&Relation{Type: RelationDependsOn, SourceID: a.ID, TargetID: c.ID})
	g.AddRelation(&Relation{Type: RelationDependsOn, SourceID: c.ID, TargetID: d.ID})

	path, err := g.FindPath(a.ID, d.ID, 5, nil)
	if err != nil {
		t.Fatalf("FindPath: %v", err)
	}
	if path.Length != 2 {
		t.Errorf("expected path length 2 (shortest), got %d", path.Length)
	}
}

func TestFindPath_SameSourceAndTarget(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeModule, Name: "A"}
	g.AddEntity(a)

	path, err := g.FindPath(a.ID, a.ID, 5, nil)
	if err != nil {
		t.Fatalf("FindPath to self: %v", err)
	}
	if path.Length != 0 {
		t.Errorf("expected path length 0 to self, got %d", path.Length)
	}
}

// ---------------------------------------------------------------------------
// Stats
// ---------------------------------------------------------------------------

func TestStats_AfterOperations(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()

	g.AddEntity(&Entity{Type: EntityTypeFile, Name: "a.go", Namespace: "pkg"})
	g.AddEntity(&Entity{Type: EntityTypeFile, Name: "b.go", Namespace: "pkg"})
	g.AddEntity(&Entity{Type: EntityTypeFunction, Name: "main", Namespace: "cmd"})

	stats := g.Stats()
	if stats.TotalEntities != 3 {
		t.Errorf("TotalEntities: got %d, want 3", stats.TotalEntities)
	}
	if stats.EntitiesByType["file"] != 2 {
		t.Errorf("file count: got %d, want 2", stats.EntitiesByType["file"])
	}
	if stats.EntitiesByType["function"] != 1 {
		t.Errorf("function count: got %d, want 1", stats.EntitiesByType["function"])
	}
	if len(stats.Namespaces) != 2 {
		t.Errorf("expected 2 namespaces, got %d", len(stats.Namespaces))
	}
}

func TestStats_WithRelations(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeFile, Name: "a.go"}
	b := &Entity{Type: EntityTypeFunction, Name: "funcA"}
	g.AddEntity(a)
	g.AddEntity(b)
	g.AddRelation(&Relation{Type: RelationContains, SourceID: a.ID, TargetID: b.ID})
	g.AddRelation(&Relation{Type: RelationCalls, SourceID: b.ID, TargetID: a.ID})

	stats := g.Stats()
	if stats.TotalRelations != 2 {
		t.Errorf("TotalRelations: got %d, want 2", stats.TotalRelations)
	}
	if stats.RelationsByType["contains"] != 1 {
		t.Errorf("contains count: got %d, want 1", stats.RelationsByType["contains"])
	}
	if stats.RelationsByType["calls"] != 1 {
		t.Errorf("calls count: got %d, want 1", stats.RelationsByType["calls"])
	}
}

func TestStats_AfterDelete(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	e := &Entity{Type: EntityTypeFile, Name: "a.go"}
	g.AddEntity(e)
	g.DeleteEntity(e.ID)

	stats := g.Stats()
	if stats.TotalEntities != 0 {
		t.Errorf("expected 0 entities after delete, got %d", stats.TotalEntities)
	}
}

// ---------------------------------------------------------------------------
// containsEntityType / containsRelType
// ---------------------------------------------------------------------------

func TestContainsEntityType_Found(t *testing.T) {
	t.Parallel()
	types := []EntityType{EntityTypeFile, EntityTypeFunction, EntityTypeClass}
	if !containsEntityType(types, EntityTypeFunction) {
		t.Error("expected to find EntityTypeFunction")
	}
}

func TestContainsEntityType_NotFound(t *testing.T) {
	t.Parallel()
	types := []EntityType{EntityTypeFile, EntityTypeFunction}
	if containsEntityType(types, EntityTypeClass) {
		t.Error("expected not to find EntityTypeClass")
	}
}

func TestContainsEntityType_Empty(t *testing.T) {
	t.Parallel()
	if containsEntityType(nil, EntityTypeFile) {
		t.Error("expected false for nil slice")
	}
}

func TestContainsRelType_Found(t *testing.T) {
	t.Parallel()
	types := []RelationType{RelationCalls, RelationImports}
	if !containsRelType(types, RelationCalls) {
		t.Error("expected to find RelationCalls")
	}
}

func TestContainsRelType_NotFound(t *testing.T) {
	t.Parallel()
	types := []RelationType{RelationCalls}
	if containsRelType(types, RelationContains) {
		t.Error("expected not to find RelationContains")
	}
}

func TestContainsRelType_Empty(t *testing.T) {
	t.Parallel()
	if containsRelType(nil, RelationCalls) {
		t.Error("expected false for nil slice")
	}
}

// ---------------------------------------------------------------------------
// ReasoningChain CRUD
// ---------------------------------------------------------------------------

func TestReasoningChain_Add_And_Get(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	chain := &ReasoningChain{
		Query:      "Why is the service slow?",
		Conclusion: "Memory leak in handler",
		Confidence: 0.8,
		Steps: []ReasoningStep{
			{StepNumber: 1, Description: "Checked metrics"},
			{StepNumber: 2, Description: "Found memory growth"},
		},
		SessionID: "sess-1",
		AgentID:   "agent-1",
	}

	if err := g.AddReasoningChain(chain); err != nil {
		t.Fatalf("AddReasoningChain: %v", err)
	}
	if chain.ID == "" {
		t.Error("expected chain ID to be set")
	}
	if chain.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}

	got, err := g.GetReasoningChain(chain.ID)
	if err != nil {
		t.Fatalf("GetReasoningChain: %v", err)
	}
	if got.Query != "Why is the service slow?" {
		t.Errorf("Query mismatch")
	}
	if len(got.Steps) != 2 {
		t.Errorf("Steps: got %d, want 2", len(got.Steps))
	}
}

func TestReasoningChain_NotFound(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	_, err := g.GetReasoningChain("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent reasoning chain")
	}
}

func TestReasoningChain_List(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	g.AddReasoningChain(&ReasoningChain{Query: "Q1", Conclusion: "C1", SessionID: "s1"})
	g.AddReasoningChain(&ReasoningChain{Query: "Q2", Conclusion: "C2", SessionID: "s2"})

	all := g.ListReasoningChains("", "", 0)
	if len(all) != 2 {
		t.Errorf("expected 2 chains, got %d", len(all))
	}

	filtered := g.ListReasoningChains("s1", "", 0)
	if len(filtered) != 1 {
		t.Errorf("expected 1 chain for session s1, got %d", len(filtered))
	}
}

// ---------------------------------------------------------------------------
// Graph traversal (Query)
// ---------------------------------------------------------------------------

func TestQuery_ByEntityID_DefaultDepth(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	a := &Entity{Type: EntityTypeModule, Name: "A"}
	b := &Entity{Type: EntityTypeModule, Name: "B"}
	g.AddEntity(a)
	g.AddEntity(b)
	g.AddRelation(&Relation{Type: RelationDependsOn, SourceID: a.ID, TargetID: b.ID})

	result, err := g.Query(GraphQuery{EntityID: a.ID, MaxDepth: 1})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Entities) != 2 {
		t.Errorf("expected 2 entities, got %d", len(result.Entities))
	}
}

func TestQuery_EmptyGraph(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	result, err := g.Query(GraphQuery{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Entities) != 0 {
		t.Errorf("expected 0 entities in empty graph, got %d", len(result.Entities))
	}
}

// ---------------------------------------------------------------------------
// Timestamp handling
// ---------------------------------------------------------------------------

func TestEntity_TimestampsPreserved(t *testing.T) {
	t.Parallel()
	g := NewKnowledgeGraph()
	fixedTime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	e := &Entity{
		Type:      EntityTypeFile,
		Name:      "test.go",
		CreatedAt: fixedTime,
	}
	g.AddEntity(e)

	got, _ := g.GetEntity(e.ID)
	if !got.CreatedAt.Equal(fixedTime) {
		t.Errorf("CreatedAt should preserve provided time, got %v", got.CreatedAt)
	}
	// UpdatedAt should be set to now (not the fixed time)
	if got.UpdatedAt.Equal(fixedTime) {
		t.Error("UpdatedAt should be set to now, not preserved from input")
	}
}
