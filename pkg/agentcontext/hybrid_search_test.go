package agentcontext

import (
	"context"
	"testing"
)

func TestHybridSearcherSearch_ExpandsGraphAndBuildsSubgraph(t *testing.T) {
	graph := NewKnowledgeGraph()
	for _, entity := range []*Entity{
		{ID: "seed", Name: "Seed", Type: EntityTypeFunction, Namespace: "pkg"},
		{ID: "neighbor", Name: "Neighbor", Type: EntityTypeFunction, Namespace: "pkg"},
	} {
		if err := graph.AddEntity(entity); err != nil {
			t.Fatalf("add entity %s: %v", entity.ID, err)
		}
	}
	if err := graph.AddRelation(&Relation{
		ID:       "rel-1",
		Type:     RelationCalls,
		SourceID: "seed",
		TargetID: "neighbor",
		Weight:   1.0,
	}); err != nil {
		t.Fatalf("add relation: %v", err)
	}

	searcher := NewHybridSearcher(
		graph,
		DefaultHybridSearchConfig(),
		func(ctx context.Context, text string) ([]float64, error) { return []float64{1}, nil },
		func(ctx context.Context, vector []float64, limit int) ([]*Entity, []float64, error) {
			return []*Entity{{ID: "seed", Name: "Seed", Type: EntityTypeFunction, Namespace: "pkg"}}, []float64{0.95}, nil
		},
	)

	result, err := searcher.Search(context.Background(), "seed", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Stats.SemanticResultsCount != 1 {
		t.Fatalf("semantic count = %d, want 1", result.Stats.SemanticResultsCount)
	}
	if result.Stats.GraphExpandedCount != 1 {
		t.Fatalf("graph expanded = %d, want 1", result.Stats.GraphExpandedCount)
	}
	if len(result.Entities) != 2 {
		t.Fatalf("entities = %d, want 2", len(result.Entities))
	}
	if result.Entities[0].Entity.ID != "seed" {
		t.Fatalf("top entity = %q, want seed", result.Entities[0].Entity.ID)
	}
	if result.Subgraph == nil || len(result.Subgraph.Entities) != 2 || len(result.Subgraph.Relations) != 1 {
		t.Fatalf("unexpected subgraph: %#v", result.Subgraph)
	}
}

func TestHybridSearcherSearch_FiltersLowSemanticScores(t *testing.T) {
	searcher := NewHybridSearcher(
		NewKnowledgeGraph(),
		DefaultHybridSearchConfig(),
		func(ctx context.Context, text string) ([]float64, error) { return []float64{1}, nil },
		func(ctx context.Context, vector []float64, limit int) ([]*Entity, []float64, error) {
			return []*Entity{{ID: "low", Name: "Low", Type: EntityTypeFunction}}, []float64{0.1}, nil
		},
	)

	result, err := searcher.Search(context.Background(), "low", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Entities) != 0 {
		t.Fatalf("entities = %d, want 0", len(result.Entities))
	}
	if result.Stats.TotalResultsCount != 0 {
		t.Fatalf("total results = %d, want 0", result.Stats.TotalResultsCount)
	}
}

func TestHybridSearcherSearchWithReranking_ReordersResults(t *testing.T) {
	searcher := NewHybridSearcher(
		NewKnowledgeGraph(),
		DefaultHybridSearchConfig(),
		func(ctx context.Context, text string) ([]float64, error) { return []float64{1}, nil },
		func(ctx context.Context, vector []float64, limit int) ([]*Entity, []float64, error) {
			return []*Entity{
				{ID: "a", Name: "A", Type: EntityTypeFunction},
				{ID: "b", Name: "B", Type: EntityTypeFunction},
			}, []float64{0.9, 0.8}, nil
		},
	)

	result, err := searcher.SearchWithReranking(context.Background(), "query", 2, func(query string, entities []Entity) ([]float64, error) {
		return []float64{0.1, 1.0}, nil
	})
	if err != nil {
		t.Fatalf("SearchWithReranking: %v", err)
	}
	if len(result.Entities) != 2 {
		t.Fatalf("entities = %d, want 2", len(result.Entities))
	}
	if result.Entities[0].Entity.ID != "b" {
		t.Fatalf("top entity = %q, want b after reranking", result.Entities[0].Entity.ID)
	}
}
