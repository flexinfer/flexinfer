// knowledge_graph_persistence.go — Qdrant persistence layer for KnowledgeGraph.
package agentcontext

import (
	"context"
	"fmt"
)

// GraphPersistenceConfig holds Qdrant clients for graph persistence
type GraphPersistenceConfig struct {
	EntitiesQdrant  *QdrantClient
	RelationsQdrant *QdrantClient
	EmbedModel      string
	VectorSize      int
}

// persistedGraph wraps KnowledgeGraph with Qdrant persistence
type persistedGraph struct {
	*KnowledgeGraph
	cfg *GraphPersistenceConfig
}

// SetPersistence configures Qdrant persistence for the graph
func (g *KnowledgeGraph) SetPersistence(cfg *GraphPersistenceConfig) *persistedGraph {
	return &persistedGraph{
		KnowledgeGraph: g,
		cfg:            cfg,
	}
}

// PersistEntity saves an entity to Qdrant
func (pg *persistedGraph) PersistEntity(ctx context.Context, entity *Entity, vector []float64) error {
	if pg.cfg == nil || pg.cfg.EntitiesQdrant == nil {
		return nil // No persistence configured
	}

	// Ensure collection exists
	if pg.cfg.VectorSize > 0 {
		if err := pg.cfg.EntitiesQdrant.EnsureCollection(ctx, pg.cfg.VectorSize); err != nil {
			return fmt.Errorf("ensure entities collection: %w", err)
		}
	}

	// Use zero vector if not provided
	if len(vector) == 0 && pg.cfg.VectorSize > 0 {
		vector = make([]float64, pg.cfg.VectorSize)
	}

	point := Point{
		ID:      entity.ID,
		Vector:  vector,
		Payload: EntityToPayload(*entity, pg.cfg.EmbedModel),
	}

	if err := pg.cfg.EntitiesQdrant.Upsert(ctx, []Point{point}, true); err != nil {
		return fmt.Errorf("persist entity: %w", err)
	}

	return nil
}

// PersistRelation saves a relation to Qdrant
func (pg *persistedGraph) PersistRelation(ctx context.Context, rel *Relation) error {
	if pg.cfg == nil || pg.cfg.RelationsQdrant == nil {
		return nil // No persistence configured
	}

	// Relations collection uses dummy vectors (4D for minimal storage)
	if err := pg.cfg.RelationsQdrant.EnsureCollection(ctx, 4); err != nil {
		return fmt.Errorf("ensure relations collection: %w", err)
	}

	point := Point{
		ID:      rel.ID,
		Vector:  dummyRelationsVector,
		Payload: RelationToPayload(*rel),
	}

	if err := pg.cfg.RelationsQdrant.Upsert(ctx, []Point{point}, true); err != nil {
		return fmt.Errorf("persist relation: %w", err)
	}

	return nil
}

// DeletePersistedEntity removes an entity from Qdrant
func (pg *persistedGraph) DeletePersistedEntity(ctx context.Context, id string) error {
	if pg.cfg == nil || pg.cfg.EntitiesQdrant == nil {
		return nil
	}
	return pg.cfg.EntitiesQdrant.Delete(ctx, []string{id})
}

// DeletePersistedRelation removes a relation from Qdrant
func (pg *persistedGraph) DeletePersistedRelation(ctx context.Context, id string) error {
	if pg.cfg == nil || pg.cfg.RelationsQdrant == nil {
		return nil
	}
	return pg.cfg.RelationsQdrant.Delete(ctx, []string{id})
}

// LoadGraphFromQdrant loads all entities and relations from Qdrant into memory
func (pg *persistedGraph) LoadGraphFromQdrant(ctx context.Context) error {
	if pg.cfg == nil {
		return nil
	}

	// Load entities
	if pg.cfg.EntitiesQdrant != nil {
		exists, err := pg.cfg.EntitiesQdrant.CollectionExists(ctx)
		if err != nil {
			return fmt.Errorf("check entities collection: %w", err)
		}
		if exists {
			points, err := pg.cfg.EntitiesQdrant.ScrollPoints(ctx, nil, 10000, false)
			if err != nil {
				return fmt.Errorf("load entities: %w", err)
			}

			pg.mu.Lock()
			for _, p := range points {
				entity, err := PayloadToEntity(p.Payload)
				if err != nil || entity == nil {
					continue
				}
				pg.entities[entity.ID] = entity
				pg.indexEntity(entity)
			}
			pg.mu.Unlock()
		}
	}

	// Load relations
	if pg.cfg.RelationsQdrant != nil {
		exists, err := pg.cfg.RelationsQdrant.CollectionExists(ctx)
		if err != nil {
			return fmt.Errorf("check relations collection: %w", err)
		}
		if exists {
			points, err := pg.cfg.RelationsQdrant.ScrollPoints(ctx, nil, 10000, false)
			if err != nil {
				return fmt.Errorf("load relations: %w", err)
			}

			pg.mu.Lock()
			for _, p := range points {
				rel, err := PayloadToRelation(p.Payload)
				if err != nil || rel == nil {
					continue
				}

				// Only add relations where both entities exist
				if _, ok := pg.entities[rel.SourceID]; !ok {
					continue
				}
				if _, ok := pg.entities[rel.TargetID]; !ok {
					continue
				}

				pg.relations[rel.ID] = rel

				// Update indexes
				if pg.outgoingRelations[rel.SourceID] == nil {
					pg.outgoingRelations[rel.SourceID] = make(map[string]bool)
				}
				pg.outgoingRelations[rel.SourceID][rel.ID] = true

				if pg.incomingRelations[rel.TargetID] == nil {
					pg.incomingRelations[rel.TargetID] = make(map[string]bool)
				}
				pg.incomingRelations[rel.TargetID][rel.ID] = true

				if pg.relationByType[rel.Type] == nil {
					pg.relationByType[rel.Type] = make(map[string]bool)
				}
				pg.relationByType[rel.Type][rel.ID] = true
			}
			pg.mu.Unlock()
		}
	}

	return nil
}

// AddEntityWithPersistence persists an entity to Qdrant first, then adds
// it to the in-memory graph. Persist-first ensures in-memory state never
// diverges from Qdrant.
func (pg *persistedGraph) AddEntityWithPersistence(ctx context.Context, entity *Entity, vector []float64) error {
	// Persist to Qdrant first.
	if err := pg.PersistEntity(ctx, entity, vector); err != nil {
		return err
	}

	// Add to in-memory graph.
	if err := pg.AddEntity(entity); err != nil {
		return err
	}

	return nil
}

// AddRelationWithPersistence persists a relation to Qdrant first, then adds
// it to the in-memory graph. Persist-first ensures in-memory state never
// diverges from Qdrant.
func (pg *persistedGraph) AddRelationWithPersistence(ctx context.Context, rel *Relation) error {
	// Persist to Qdrant first.
	if err := pg.PersistRelation(ctx, rel); err != nil {
		return err
	}

	// Add to in-memory graph.
	if err := pg.AddRelation(rel); err != nil {
		return err
	}

	// Also persist reverse relation if bidirectional.
	if rel.Bidirectional {
		reverseID := rel.ID + "-rev"
		pg.mu.RLock()
		reverseRel := pg.relations[reverseID]
		pg.mu.RUnlock()
		if reverseRel != nil {
			if err := pg.PersistRelation(ctx, reverseRel); err != nil {
				// Non-fatal, log and continue.
				fmt.Printf("warning: failed to persist reverse relation: %v\n", err)
			}
		}
	}

	return nil
}

// UpdateEntityWithPersistence updates an entity and persists changes
func (pg *persistedGraph) UpdateEntityWithPersistence(ctx context.Context, entity *Entity, vector []float64) error {
	if err := pg.UpdateEntity(entity); err != nil {
		return err
	}
	return pg.PersistEntity(ctx, entity, vector)
}

// DeleteEntityWithPersistence removes an entity from Qdrant first, then
// removes from in-memory graph (reverse order for deletes ensures consistency).
func (pg *persistedGraph) DeleteEntityWithPersistence(ctx context.Context, id string) error {
	// Collect relation IDs before deletion.
	pg.mu.RLock()
	outgoing := make([]string, 0, len(pg.outgoingRelations[id]))
	for relID := range pg.outgoingRelations[id] {
		outgoing = append(outgoing, relID)
	}
	incoming := make([]string, 0, len(pg.incomingRelations[id]))
	for relID := range pg.incomingRelations[id] {
		incoming = append(incoming, relID)
	}
	pg.mu.RUnlock()

	// Delete from Qdrant first.
	if err := pg.DeletePersistedEntity(ctx, id); err != nil {
		return err
	}

	// Delete relations from Qdrant.
	allRelIDs := append(outgoing, incoming...)
	if len(allRelIDs) > 0 && pg.cfg != nil && pg.cfg.RelationsQdrant != nil {
		if err := pg.cfg.RelationsQdrant.Delete(ctx, allRelIDs); err != nil {
			// Non-fatal.
			fmt.Printf("warning: failed to delete relations: %v\n", err)
		}
	}

	// Delete from in-memory graph.
	if err := pg.DeleteEntity(id); err != nil {
		return err
	}

	return nil
}

// DeleteRelationWithPersistence deletes a relation and removes from Qdrant
func (pg *persistedGraph) DeleteRelationWithPersistence(ctx context.Context, id string) error {
	if err := pg.DeleteRelation(id); err != nil {
		return err
	}
	return pg.DeletePersistedRelation(ctx, id)
}

// SearchEntitiesSemantic performs semantic search for entities using vector similarity
func (pg *persistedGraph) SearchEntitiesSemantic(ctx context.Context, vector []float64, limit int, entityType EntityType, namespace string) ([]*Entity, error) {
	if pg.cfg == nil || pg.cfg.EntitiesQdrant == nil {
		return nil, fmt.Errorf("no persistence configured for semantic search")
	}

	// Build filter
	var conds []any
	if entityType != "" {
		conds = append(conds, Match("type", string(entityType)))
	}
	if namespace != "" {
		conds = append(conds, Match("namespace", namespace))
	}

	var filter map[string]any
	if len(conds) > 0 {
		filter = FilterMust(conds...)
	}

	// Search
	type searchResult struct {
		ID      string         `json:"id"`
		Score   float64        `json:"score"`
		Payload map[string]any `json:"payload"`
	}

	path := fmt.Sprintf("/collections/%s/points/search", pg.cfg.EntitiesQdrant.collection)
	body := map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": true,
	}
	if filter != nil {
		body["filter"] = filter
	}

	var resp struct {
		Result []searchResult `json:"result"`
	}
	if err := pg.cfg.EntitiesQdrant.doJSON(ctx, "POST", path, body, &resp); err != nil {
		return nil, err
	}

	entities := make([]*Entity, 0, len(resp.Result))
	for _, hit := range resp.Result {
		entity, err := PayloadToEntity(hit.Payload)
		if err != nil || entity == nil {
			continue
		}
		entities = append(entities, entity)
	}

	return entities, nil
}

// PersistReasoningChain saves a reasoning chain to Qdrant (stored with entities)
func (pg *persistedGraph) PersistReasoningChain(ctx context.Context, chain *ReasoningChain) error {
	if pg.cfg == nil || pg.cfg.EntitiesQdrant == nil {
		return nil
	}

	// Ensure collection exists with dummy vector size
	if err := pg.cfg.EntitiesQdrant.EnsureCollection(ctx, pg.cfg.VectorSize); err != nil {
		return fmt.Errorf("ensure entities collection: %w", err)
	}

	// Use a special prefix for reasoning chain IDs
	pointID := "rc_" + chain.ID
	vector := make([]float64, pg.cfg.VectorSize)

	payload := ReasoningChainToPayload(*chain)
	payload["_type"] = "reasoning_chain" // Mark as reasoning chain

	point := Point{
		ID:      pointID,
		Vector:  vector,
		Payload: payload,
	}

	return pg.cfg.EntitiesQdrant.Upsert(ctx, []Point{point}, true)
}

// LoadReasoningChainsFromQdrant loads reasoning chains from Qdrant
func (pg *persistedGraph) LoadReasoningChainsFromQdrant(ctx context.Context) error {
	if pg.cfg == nil || pg.cfg.EntitiesQdrant == nil {
		return nil
	}

	exists, err := pg.cfg.EntitiesQdrant.CollectionExists(ctx)
	if err != nil || !exists {
		return err
	}

	// Filter for reasoning chains
	filter := Match("_type", "reasoning_chain")

	points, err := pg.cfg.EntitiesQdrant.ScrollPoints(ctx, FilterMust(filter), 1000, false)
	if err != nil {
		return err
	}

	pg.mu.Lock()
	defer pg.mu.Unlock()

	for _, p := range points {
		chain, err := PayloadToReasoningChain(p.Payload)
		if err != nil || chain == nil {
			continue
		}
		pg.reasoningChains[chain.ID] = chain
	}

	return nil
}
