package agentcontext

import (
	"context"
	"sort"
)

// =========================================================================
// Hybrid Search (Vector + Graph) - Phase 2.4
// =========================================================================

// HybridSearchConfig configures hybrid search behavior
type HybridSearchConfig struct {
	// Weight for semantic similarity score (0.0 - 1.0)
	SemanticWeight float64 `json:"semantic_weight"`

	// Weight for graph connectivity score (0.0 - 1.0)
	GraphWeight float64 `json:"graph_weight"`

	// Maximum depth for graph traversal
	GraphMaxDepth int `json:"graph_max_depth"`

	// Minimum semantic score to consider
	MinSemanticScore float64 `json:"min_semantic_score"`

	// Whether to expand results using graph connections
	ExpandWithGraph bool `json:"expand_with_graph"`
}

// DefaultHybridSearchConfig returns default configuration
func DefaultHybridSearchConfig() HybridSearchConfig {
	return HybridSearchConfig{
		SemanticWeight:   0.7,
		GraphWeight:      0.3,
		GraphMaxDepth:    2,
		MinSemanticScore: 0.3,
		ExpandWithGraph:  true,
	}
}

// HybridSearchResult contains the result of a hybrid search
type HybridSearchResult struct {
	Entities []HybridSearchEntity      `json:"entities"`
	Subgraph *EnhancedGraphQueryResult `json:"subgraph,omitempty"`
	Stats    HybridSearchStats         `json:"stats"`
}

// HybridSearchEntity contains an entity with hybrid scoring
type HybridSearchEntity struct {
	Entity         Entity  `json:"entity"`
	SemanticScore  float64 `json:"semantic_score"`
	GraphScore     float64 `json:"graph_score"`
	CombinedScore  float64 `json:"combined_score"`
	GraphDepth     int     `json:"graph_depth,omitempty"`
	RelatedToQuery bool    `json:"related_to_query"` // True if directly related to query entities
}

// HybridSearchStats contains search statistics
type HybridSearchStats struct {
	SemanticResultsCount int     `json:"semantic_results_count"`
	GraphExpandedCount   int     `json:"graph_expanded_count"`
	TotalResultsCount    int     `json:"total_results_count"`
	AvgSemanticScore     float64 `json:"avg_semantic_score"`
	AvgGraphScore        float64 `json:"avg_graph_score"`
}

// HybridSearcher provides hybrid vector + graph search
type HybridSearcher struct {
	graph    *KnowledgeGraph
	cfg      HybridSearchConfig
	embedFn  func(ctx context.Context, text string) ([]float64, error)
	searchFn func(ctx context.Context, vector []float64, limit int) ([]*Entity, []float64, error)
}

// NewHybridSearcher creates a new hybrid searcher
func NewHybridSearcher(
	graph *KnowledgeGraph,
	cfg HybridSearchConfig,
	embedFn func(ctx context.Context, text string) ([]float64, error),
	searchFn func(ctx context.Context, vector []float64, limit int) ([]*Entity, []float64, error),
) *HybridSearcher {
	return &HybridSearcher{
		graph:    graph,
		cfg:      cfg,
		embedFn:  embedFn,
		searchFn: searchFn,
	}
}

// Search performs hybrid search combining vector similarity and graph connectivity
func (hs *HybridSearcher) Search(ctx context.Context, query string, limit int) (*HybridSearchResult, error) {
	result := &HybridSearchResult{
		Entities: []HybridSearchEntity{},
	}

	// Step 1: Get embedding for query
	queryVector, err := hs.embedFn(ctx, query)
	if err != nil {
		return nil, err
	}

	// Step 2: Semantic search
	semanticLimit := limit * 2 // Get more for graph expansion
	entities, scores, err := hs.searchFn(ctx, queryVector, semanticLimit)
	if err != nil {
		return nil, err
	}

	result.Stats.SemanticResultsCount = len(entities)

	// Build initial results with semantic scores
	entityScores := make(map[string]*HybridSearchEntity)
	seedEntityIDs := make([]string, 0, len(entities))

	for i, entity := range entities {
		score := 0.0
		if i < len(scores) {
			score = scores[i]
		}

		if score < hs.cfg.MinSemanticScore {
			continue
		}

		seedEntityIDs = append(seedEntityIDs, entity.ID)
		entityScores[entity.ID] = &HybridSearchEntity{
			Entity:         *entity,
			SemanticScore:  score,
			RelatedToQuery: true,
		}
	}

	// Step 3: Graph expansion (GraphRAG)
	if hs.cfg.ExpandWithGraph && len(seedEntityIDs) > 0 {
		expandedEntities := hs.expandWithGraph(seedEntityIDs)
		result.Stats.GraphExpandedCount = len(expandedEntities)

		for _, expanded := range expandedEntities {
			if _, exists := entityScores[expanded.entity.ID]; !exists {
				entityScores[expanded.entity.ID] = &HybridSearchEntity{
					Entity:         expanded.entity,
					GraphScore:     expanded.graphScore,
					GraphDepth:     expanded.depth,
					RelatedToQuery: false,
				}
			} else {
				// Entity exists from semantic search, add graph score
				entityScores[expanded.entity.ID].GraphScore = expanded.graphScore
				entityScores[expanded.entity.ID].GraphDepth = expanded.depth
			}
		}
	}

	// Step 4: Calculate combined scores
	var totalSemantic, totalGraph float64
	for _, es := range entityScores {
		es.CombinedScore = (es.SemanticScore * hs.cfg.SemanticWeight) + (es.GraphScore * hs.cfg.GraphWeight)
		totalSemantic += es.SemanticScore
		totalGraph += es.GraphScore
		result.Entities = append(result.Entities, *es)
	}

	// Calculate averages
	if len(result.Entities) > 0 {
		result.Stats.AvgSemanticScore = totalSemantic / float64(len(result.Entities))
		result.Stats.AvgGraphScore = totalGraph / float64(len(result.Entities))
	}

	// Step 5: Sort by combined score
	sort.Slice(result.Entities, func(i, j int) bool {
		return result.Entities[i].CombinedScore > result.Entities[j].CombinedScore
	})

	// Apply limit
	if limit > 0 && len(result.Entities) > limit {
		result.Entities = result.Entities[:limit]
	}

	result.Stats.TotalResultsCount = len(result.Entities)

	// Step 6: Extract subgraph around top results
	if len(result.Entities) > 0 {
		topEntityIDs := make([]string, 0, min(5, len(result.Entities)))
		for i := 0; i < len(result.Entities) && i < 5; i++ {
			topEntityIDs = append(topEntityIDs, result.Entities[i].Entity.ID)
		}
		result.Subgraph = hs.extractSubgraph(topEntityIDs)
	}

	return result, nil
}

type expandedEntity struct {
	entity     Entity
	graphScore float64
	depth      int
}

// expandWithGraph expands seed entities using graph connections
func (hs *HybridSearcher) expandWithGraph(seedIDs []string) []expandedEntity {
	hs.graph.mu.RLock()
	defer hs.graph.mu.RUnlock()

	visited := make(map[string]bool)
	for _, id := range seedIDs {
		visited[id] = true
	}

	var expanded []expandedEntity

	// BFS expansion
	type queueItem struct {
		entityID string
		depth    int
		weight   float64 // accumulated edge weights
	}

	queue := []queueItem{}
	for _, id := range seedIDs {
		queue = append(queue, queueItem{entityID: id, depth: 0, weight: 1.0})
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= hs.cfg.GraphMaxDepth {
			continue
		}

		// Get outgoing relations
		for relID := range hs.graph.outgoingRelations[current.entityID] {
			rel := hs.graph.relations[relID]
			if rel == nil {
				continue
			}

			targetID := rel.TargetID
			if visited[targetID] {
				continue
			}
			visited[targetID] = true

			targetEntity := hs.graph.entities[targetID]
			if targetEntity == nil {
				continue
			}

			// Calculate graph score based on distance and edge weights
			// Score decreases with depth, increases with edge weight
			graphScore := current.weight * rel.Weight / float64(current.depth+1)

			expanded = append(expanded, expandedEntity{
				entity:     *targetEntity,
				graphScore: graphScore,
				depth:      current.depth + 1,
			})

			queue = append(queue, queueItem{
				entityID: targetID,
				depth:    current.depth + 1,
				weight:   current.weight * rel.Weight * 0.8, // decay factor
			})
		}

		// Also expand incoming relations (for bidirectional search)
		for relID := range hs.graph.incomingRelations[current.entityID] {
			rel := hs.graph.relations[relID]
			if rel == nil {
				continue
			}

			sourceID := rel.SourceID
			if visited[sourceID] {
				continue
			}
			visited[sourceID] = true

			sourceEntity := hs.graph.entities[sourceID]
			if sourceEntity == nil {
				continue
			}

			graphScore := current.weight * rel.Weight / float64(current.depth+1) * 0.8 // slightly lower for incoming

			expanded = append(expanded, expandedEntity{
				entity:     *sourceEntity,
				graphScore: graphScore,
				depth:      current.depth + 1,
			})

			queue = append(queue, queueItem{
				entityID: sourceID,
				depth:    current.depth + 1,
				weight:   current.weight * rel.Weight * 0.7,
			})
		}
	}

	return expanded
}

// extractSubgraph extracts the subgraph around given entity IDs
func (hs *HybridSearcher) extractSubgraph(entityIDs []string) *EnhancedGraphQueryResult {
	hs.graph.mu.RLock()
	defer hs.graph.mu.RUnlock()

	result := &EnhancedGraphQueryResult{
		Entities:  []Entity{},
		Relations: []Relation{},
	}

	entitySet := make(map[string]bool)
	for _, id := range entityIDs {
		entitySet[id] = true
	}

	// Add entities
	for id := range entitySet {
		if entity := hs.graph.entities[id]; entity != nil {
			result.Entities = append(result.Entities, *entity)
		}
	}

	// Add relations between these entities
	for _, rel := range hs.graph.relations {
		if entitySet[rel.SourceID] && entitySet[rel.TargetID] {
			result.Relations = append(result.Relations, *rel)
		}
	}

	return result
}

// SearchWithReranking performs search with optional reranking
func (hs *HybridSearcher) SearchWithReranking(
	ctx context.Context,
	query string,
	limit int,
	reranker func(query string, entities []Entity) ([]float64, error),
) (*HybridSearchResult, error) {
	// First do hybrid search
	result, err := hs.Search(ctx, query, limit*2)
	if err != nil {
		return nil, err
	}

	if reranker == nil || len(result.Entities) == 0 {
		return result, nil
	}

	// Extract entities for reranking
	entities := make([]Entity, len(result.Entities))
	for i, he := range result.Entities {
		entities[i] = he.Entity
	}

	// Get reranking scores
	rerankScores, err := reranker(query, entities)
	if err != nil {
		// Fall back to original ranking
		return result, nil
	}

	// Apply reranking scores
	for i := range result.Entities {
		if i < len(rerankScores) {
			// Combine with rerank score
			rerankWeight := 0.4
			result.Entities[i].CombinedScore = result.Entities[i].CombinedScore*(1-rerankWeight) + rerankScores[i]*rerankWeight
		}
	}

	// Re-sort by combined score
	sort.Slice(result.Entities, func(i, j int) bool {
		return result.Entities[i].CombinedScore > result.Entities[j].CombinedScore
	})

	// Apply final limit
	if limit > 0 && len(result.Entities) > limit {
		result.Entities = result.Entities[:limit]
	}

	return result, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
