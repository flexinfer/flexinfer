package agentcontext

import (
	"testing"
)

func TestKnowledgeGraph_AddEntity(t *testing.T) {
	g := NewKnowledgeGraph()

	entity := &Entity{
		Type:        EntityTypeFunction,
		Name:        "handleRequest",
		Description: "Handles HTTP requests",
		Namespace:   "api",
		FilePath:    "/src/handler.go",
		LineStart:   10,
		LineEnd:     50,
		Language:    "go",
	}

	err := g.AddEntity(entity)
	if err != nil {
		t.Fatalf("AddEntity failed: %v", err)
	}

	if entity.ID == "" {
		t.Error("expected entity ID to be set")
	}

	// Verify we can retrieve it
	retrieved, err := g.GetEntity(entity.ID)
	if err != nil {
		t.Fatalf("GetEntity failed: %v", err)
	}

	if retrieved.Name != "handleRequest" {
		t.Errorf("expected name 'handleRequest', got %q", retrieved.Name)
	}
	if retrieved.Type != EntityTypeFunction {
		t.Errorf("expected type 'function', got %q", retrieved.Type)
	}
}

func TestKnowledgeGraph_AddEntity_Validation(t *testing.T) {
	g := NewKnowledgeGraph()

	// Missing name
	err := g.AddEntity(&Entity{Type: EntityTypeFile})
	if err == nil {
		t.Error("expected error for missing name")
	}

	// Missing type
	err = g.AddEntity(&Entity{Name: "test"})
	if err == nil {
		t.Error("expected error for missing type")
	}
}

func TestKnowledgeGraph_FindEntities(t *testing.T) {
	g := NewKnowledgeGraph()

	// Add multiple entities
	g.AddEntity(&Entity{Type: EntityTypeFunction, Name: "funcA", Namespace: "api"})
	g.AddEntity(&Entity{Type: EntityTypeFunction, Name: "funcB", Namespace: "api"})
	g.AddEntity(&Entity{Type: EntityTypeClass, Name: "UserService", Namespace: "api"})
	g.AddEntity(&Entity{Type: EntityTypeFunction, Name: "funcC", Namespace: "internal"})

	// Find by type
	funcs := g.FindEntities(EntityTypeFunction, "", "", 0)
	if len(funcs) != 3 {
		t.Errorf("expected 3 functions, got %d", len(funcs))
	}

	// Find by namespace
	apiEntities := g.FindEntities("", "api", "", 0)
	if len(apiEntities) != 3 {
		t.Errorf("expected 3 api entities, got %d", len(apiEntities))
	}

	// Find by type and namespace
	apiFuncs := g.FindEntities(EntityTypeFunction, "api", "", 0)
	if len(apiFuncs) != 2 {
		t.Errorf("expected 2 api functions, got %d", len(apiFuncs))
	}

	// Find by name pattern
	userEntities := g.FindEntities("", "", "User", 0)
	if len(userEntities) != 1 {
		t.Errorf("expected 1 entity matching 'User', got %d", len(userEntities))
	}
}

func TestKnowledgeGraph_AddRelation(t *testing.T) {
	g := NewKnowledgeGraph()

	// Add entities
	file := &Entity{Type: EntityTypeFile, Name: "handler.go"}
	func1 := &Entity{Type: EntityTypeFunction, Name: "handleRequest"}
	g.AddEntity(file)
	g.AddEntity(func1)

	// Add relation
	rel := &Relation{
		Type:     RelationContains,
		SourceID: file.ID,
		TargetID: func1.ID,
	}

	err := g.AddRelation(rel)
	if err != nil {
		t.Fatalf("AddRelation failed: %v", err)
	}

	if rel.ID == "" {
		t.Error("expected relation ID to be set")
	}

	// Verify we can retrieve it
	retrieved, err := g.GetRelation(rel.ID)
	if err != nil {
		t.Fatalf("GetRelation failed: %v", err)
	}

	if retrieved.Type != RelationContains {
		t.Errorf("expected type 'contains', got %q", retrieved.Type)
	}
}

func TestKnowledgeGraph_AddRelation_Validation(t *testing.T) {
	g := NewKnowledgeGraph()

	entity := &Entity{Type: EntityTypeFile, Name: "test.go"}
	g.AddEntity(entity)

	// Missing source
	err := g.AddRelation(&Relation{Type: RelationCalls, TargetID: entity.ID})
	if err == nil {
		t.Error("expected error for missing source_id")
	}

	// Non-existent source
	err = g.AddRelation(&Relation{Type: RelationCalls, SourceID: "nonexistent", TargetID: entity.ID})
	if err == nil {
		t.Error("expected error for non-existent source")
	}
}

func TestKnowledgeGraph_GetEntityRelations(t *testing.T) {
	g := NewKnowledgeGraph()

	// Create entities
	file := &Entity{Type: EntityTypeFile, Name: "handler.go"}
	func1 := &Entity{Type: EntityTypeFunction, Name: "handleRequest"}
	func2 := &Entity{Type: EntityTypeFunction, Name: "validateInput"}
	g.AddEntity(file)
	g.AddEntity(func1)
	g.AddEntity(func2)

	// Create relations
	g.AddRelation(&Relation{Type: RelationContains, SourceID: file.ID, TargetID: func1.ID})
	g.AddRelation(&Relation{Type: RelationContains, SourceID: file.ID, TargetID: func2.ID})
	g.AddRelation(&Relation{Type: RelationCalls, SourceID: func1.ID, TargetID: func2.ID})

	// Get outgoing relations from file
	fileRels := g.GetEntityRelations(file.ID, nil, true, false)
	if len(fileRels) != 2 {
		t.Errorf("expected 2 outgoing relations from file, got %d", len(fileRels))
	}

	// Get incoming relations to func2
	func2Rels := g.GetEntityRelations(func2.ID, nil, false, true)
	if len(func2Rels) != 2 {
		t.Errorf("expected 2 incoming relations to func2, got %d", len(func2Rels))
	}

	// Filter by type
	callRels := g.GetEntityRelations(func1.ID, []RelationType{RelationCalls}, true, true)
	if len(callRels) != 1 {
		t.Errorf("expected 1 calls relation, got %d", len(callRels))
	}
}

func TestKnowledgeGraph_Query_Pattern(t *testing.T) {
	g := NewKnowledgeGraph()

	// Build a small graph
	file1 := &Entity{Type: EntityTypeFile, Name: "handler.go"}
	file2 := &Entity{Type: EntityTypeFile, Name: "service.go"}
	func1 := &Entity{Type: EntityTypeFunction, Name: "handleRequest"}
	func2 := &Entity{Type: EntityTypeFunction, Name: "processData"}
	g.AddEntity(file1)
	g.AddEntity(file2)
	g.AddEntity(func1)
	g.AddEntity(func2)

	g.AddRelation(&Relation{Type: RelationContains, SourceID: file1.ID, TargetID: func1.ID})
	g.AddRelation(&Relation{Type: RelationContains, SourceID: file2.ID, TargetID: func2.ID})
	g.AddRelation(&Relation{Type: RelationCalls, SourceID: func1.ID, TargetID: func2.ID})

	// Query: Find all file->function containment
	result, err := g.Query(GraphQuery{Pattern: "(file)-[contains]->(function)"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(result.Relations) != 2 {
		t.Errorf("expected 2 contains relations, got %d", len(result.Relations))
	}
	if len(result.Entities) != 4 {
		t.Errorf("expected 4 entities, got %d", len(result.Entities))
	}

	// Query: Find all function->function calls
	result, err = g.Query(GraphQuery{Pattern: "(function)-[calls]->(function)"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(result.Relations) != 1 {
		t.Errorf("expected 1 calls relation, got %d", len(result.Relations))
	}
}

func TestKnowledgeGraph_Query_Traversal(t *testing.T) {
	g := NewKnowledgeGraph()

	// Build a dependency chain: A -> B -> C -> D
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

	// Traverse from A with depth 2
	result, err := g.Query(GraphQuery{EntityID: a.ID, MaxDepth: 2})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// Should find A, B, C (but not D at depth 3)
	if len(result.Entities) != 3 {
		t.Errorf("expected 3 entities at depth 2, got %d", len(result.Entities))
	}

	// Traverse with depth 3
	result, err = g.Query(GraphQuery{EntityID: a.ID, MaxDepth: 3})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(result.Entities) != 4 {
		t.Errorf("expected 4 entities at depth 3, got %d", len(result.Entities))
	}
}

func TestKnowledgeGraph_FindPath(t *testing.T) {
	g := NewKnowledgeGraph()

	// Build graph: A -> B -> C, A -> D -> C
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
	g.AddRelation(&Relation{Type: RelationDependsOn, SourceID: a.ID, TargetID: d.ID})
	g.AddRelation(&Relation{Type: RelationDependsOn, SourceID: d.ID, TargetID: c.ID})

	// Find path from A to C
	path, err := g.FindPath(a.ID, c.ID, 5, nil)
	if err != nil {
		t.Fatalf("FindPath failed: %v", err)
	}

	if path.Length != 2 {
		t.Errorf("expected path length 2, got %d", path.Length)
	}

	// Path should start with A and end with C
	if path.Nodes[0] != a.ID {
		t.Errorf("expected path to start with A")
	}
	if path.Nodes[len(path.Nodes)-1] != c.ID {
		t.Errorf("expected path to end with C")
	}
}

func TestKnowledgeGraph_FindPath_NoPath(t *testing.T) {
	g := NewKnowledgeGraph()

	// Disconnected entities
	a := &Entity{Type: EntityTypeModule, Name: "A"}
	b := &Entity{Type: EntityTypeModule, Name: "B"}
	g.AddEntity(a)
	g.AddEntity(b)

	// No relations between them
	_, err := g.FindPath(a.ID, b.ID, 5, nil)
	if err == nil {
		t.Error("expected error for no path")
	}
}

func TestKnowledgeGraph_BidirectionalRelation(t *testing.T) {
	g := NewKnowledgeGraph()

	a := &Entity{Type: EntityTypeConcept, Name: "REST API"}
	b := &Entity{Type: EntityTypeConcept, Name: "HTTP"}
	g.AddEntity(a)
	g.AddEntity(b)

	// Add bidirectional relation
	g.AddRelation(&Relation{Type: RelationRelatedTo, SourceID: a.ID, TargetID: b.ID, Bidirectional: true})

	// Should have relations in both directions
	aOutgoing := g.GetEntityRelations(a.ID, nil, true, false)
	bOutgoing := g.GetEntityRelations(b.ID, nil, true, false)

	if len(aOutgoing) != 1 {
		t.Errorf("expected 1 outgoing from A, got %d", len(aOutgoing))
	}
	if len(bOutgoing) != 1 {
		t.Errorf("expected 1 outgoing from B, got %d", len(bOutgoing))
	}
}

func TestKnowledgeGraph_DeleteEntity(t *testing.T) {
	g := NewKnowledgeGraph()

	a := &Entity{Type: EntityTypeFile, Name: "test.go"}
	b := &Entity{Type: EntityTypeFunction, Name: "testFunc"}
	g.AddEntity(a)
	g.AddEntity(b)
	g.AddRelation(&Relation{Type: RelationContains, SourceID: a.ID, TargetID: b.ID})

	// Delete entity should also delete its relations
	err := g.DeleteEntity(a.ID)
	if err != nil {
		t.Fatalf("DeleteEntity failed: %v", err)
	}

	// Entity should be gone
	_, err = g.GetEntity(a.ID)
	if err == nil {
		t.Error("expected entity to be deleted")
	}

	// Relation should be gone
	bRels := g.GetEntityRelations(b.ID, nil, true, true)
	if len(bRels) != 0 {
		t.Errorf("expected 0 relations after deleting source, got %d", len(bRels))
	}
}

func TestKnowledgeGraph_ReasoningChain(t *testing.T) {
	g := NewKnowledgeGraph()

	// Add a reasoning chain
	chain := &ReasoningChain{
		Query: "Why did the deployment fail?",
		Steps: []ReasoningStep{
			{StepNumber: 1, Description: "Found deployment error in logs", Conclusion: "Container failed to start"},
			{StepNumber: 2, Description: "Checked container image", Conclusion: "Image tag was wrong"},
		},
		Conclusion: "Deployment failed due to incorrect image tag",
		Confidence: 0.9,
		SessionID:  "session-1",
		AgentID:    "agent-1",
	}

	err := g.AddReasoningChain(chain)
	if err != nil {
		t.Fatalf("AddReasoningChain failed: %v", err)
	}

	if chain.ID == "" {
		t.Error("expected chain ID to be set")
	}

	// Retrieve
	retrieved, err := g.GetReasoningChain(chain.ID)
	if err != nil {
		t.Fatalf("GetReasoningChain failed: %v", err)
	}

	if retrieved.Conclusion != "Deployment failed due to incorrect image tag" {
		t.Errorf("unexpected conclusion: %s", retrieved.Conclusion)
	}

	if len(retrieved.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(retrieved.Steps))
	}
}

func TestKnowledgeGraph_Stats(t *testing.T) {
	g := NewKnowledgeGraph()

	// Add some entities and relations
	g.AddEntity(&Entity{Type: EntityTypeFile, Name: "a.go", Namespace: "pkg"})
	g.AddEntity(&Entity{Type: EntityTypeFile, Name: "b.go", Namespace: "pkg"})
	g.AddEntity(&Entity{Type: EntityTypeFunction, Name: "func1", Namespace: "pkg"})
	g.AddEntity(&Entity{Type: EntityTypeClass, Name: "Service", Namespace: "api"})

	stats := g.Stats()

	if stats.TotalEntities != 4 {
		t.Errorf("expected 4 entities, got %d", stats.TotalEntities)
	}

	if stats.EntitiesByType["file"] != 2 {
		t.Errorf("expected 2 files, got %d", stats.EntitiesByType["file"])
	}

	if stats.EntitiesByType["function"] != 1 {
		t.Errorf("expected 1 function, got %d", stats.EntitiesByType["function"])
	}

	if len(stats.Namespaces) != 2 {
		t.Errorf("expected 2 namespaces, got %d", len(stats.Namespaces))
	}
}
