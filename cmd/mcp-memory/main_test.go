package main

import (
	"context"
	"strings"
	"testing"
)

func newTestMemoryServer() *memoryServer {
	return &memoryServer{
		graph: &KnowledgeGraph{
			Entities:  make(map[string]*Entity),
			Relations: make([]*Relation, 0),
		},
		autoSave: false, // skip disk writes in tests
	}
}

func TestHandleCreateEntities(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		m := newTestMemoryServer()
		result, err := m.handleCreateEntities(context.Background(), map[string]any{
			"entities": []any{
				map[string]any{
					"name":         "Go",
					"entityType":   "language",
					"observations": []any{"compiled", "statically typed"},
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error: %v", result.Content)
		}
		if len(result.Content) == 0 {
			t.Fatal("expected content")
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "Go") {
			t.Errorf("expected entity name in result, got: %s", text)
		}
	})

	t.Run("missing entities param", func(t *testing.T) {
		m := newTestMemoryServer()
		result, err := m.handleCreateEntities(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for missing entities")
		}
	})

	t.Run("entities not an array", func(t *testing.T) {
		m := newTestMemoryServer()
		result, err := m.handleCreateEntities(context.Background(), map[string]any{
			"entities": "not-an-array",
		})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for non-array entities")
		}
	})

	t.Run("no duplicate entities", func(t *testing.T) {
		m := newTestMemoryServer()
		m.graph.Entities["Go"] = &Entity{Name: "Go", EntityType: "language"}

		result, err := m.handleCreateEntities(context.Background(), map[string]any{
			"entities": []any{
				map[string]any{"name": "Go", "entityType": "language"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error")
		}
		// Count should be 0 since entity already exists
		text := result.Content[0].Text
		if !strings.Contains(text, "count: 0") && !strings.Contains(text, `"count":0`) && !strings.Contains(text, `"count": 0`) {
			t.Errorf("expected count 0 for duplicate, got: %s", text)
		}
	})
}

func TestHandleCreateRelations(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		m := newTestMemoryServer()
		result, err := m.handleCreateRelations(context.Background(), map[string]any{
			"relations": []any{
				map[string]any{
					"from":         "Go",
					"to":           "Kubernetes",
					"relationType": "used_by",
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error")
		}
	})

	t.Run("missing relations param", func(t *testing.T) {
		m := newTestMemoryServer()
		result, err := m.handleCreateRelations(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result")
		}
	})

	t.Run("relations not an array", func(t *testing.T) {
		m := newTestMemoryServer()
		result, err := m.handleCreateRelations(context.Background(), map[string]any{
			"relations": "bad",
		})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result")
		}
	})
}

func TestHandleReadGraph(t *testing.T) {
	t.Parallel()

	t.Run("empty graph", func(t *testing.T) {
		m := newTestMemoryServer()
		result, err := m.handleReadGraph(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error")
		}
		if len(result.Content) == 0 {
			t.Fatal("expected content")
		}
	})

	t.Run("graph with data", func(t *testing.T) {
		m := newTestMemoryServer()
		m.graph.Entities["Go"] = &Entity{Name: "Go", EntityType: "language", Observations: []string{"fast"}}
		m.graph.Relations = []*Relation{{From: "Go", To: "K8s", RelationType: "used_by"}}

		result, err := m.handleReadGraph(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "Go") {
			t.Errorf("expected entity in result, got: %s", text)
		}
	})
}

func TestHandleSearchNodes(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		m := newTestMemoryServer()
		m.graph.Entities["Go"] = &Entity{Name: "Go", EntityType: "language", Observations: []string{"fast"}}

		result, err := m.handleSearchNodes(context.Background(), map[string]any{
			"query": "Go",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
	})

	t.Run("missing query", func(t *testing.T) {
		m := newTestMemoryServer()
		result, err := m.handleSearchNodes(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for missing query")
		}
	})
}

func TestHandleOpenNodes(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		m := newTestMemoryServer()
		m.graph.Entities["Go"] = &Entity{Name: "Go", EntityType: "language"}

		result, err := m.handleOpenNodes(context.Background(), map[string]any{
			"names": []any{"Go"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
	})

	t.Run("missing names", func(t *testing.T) {
		m := newTestMemoryServer()
		result, err := m.handleOpenNodes(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for missing names")
		}
	})
}

func TestHandleDeleteEntities(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		m := newTestMemoryServer()
		m.graph.Entities["Go"] = &Entity{Name: "Go", EntityType: "language"}
		m.graph.Relations = []*Relation{{From: "Go", To: "K8s", RelationType: "used_by"}}

		result, err := m.handleDeleteEntities(context.Background(), map[string]any{
			"names": []any{"Go"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
		if _, exists := m.graph.Entities["Go"]; exists {
			t.Error("expected entity to be deleted")
		}
		if len(m.graph.Relations) != 0 {
			t.Error("expected related relations to be cleaned up")
		}
	})

	t.Run("missing names", func(t *testing.T) {
		m := newTestMemoryServer()
		result, err := m.handleDeleteEntities(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for missing names")
		}
	})
}

func TestHandleAddObservations(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		m := newTestMemoryServer()
		m.graph.Entities["Go"] = &Entity{Name: "Go", EntityType: "language", Observations: []string{}}

		result, err := m.handleAddObservations(context.Background(), map[string]any{
			"observations": []any{
				map[string]any{"entityName": "Go", "observation": "concurrent"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
		if len(m.graph.Entities["Go"].Observations) != 1 {
			t.Errorf("expected 1 observation, got %d", len(m.graph.Entities["Go"].Observations))
		}
	})

	t.Run("missing observations", func(t *testing.T) {
		m := newTestMemoryServer()
		result, err := m.handleAddObservations(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result")
		}
	})
}
