package agentcontext

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// KnowledgeGraph provides an in-memory knowledge graph with entity and relation management
type KnowledgeGraph struct {
	mu sync.RWMutex

	// Primary storage
	entities  map[string]*Entity   // entity ID -> entity
	relations map[string]*Relation // relation ID -> relation

	// Indexes for efficient queries
	entityByType      map[EntityType]map[string]bool   // type -> set of entity IDs
	entityByNamespace map[string]map[string]bool       // namespace -> set of entity IDs
	entityByName      map[string]map[string]bool       // name (lowercase) -> set of entity IDs
	outgoingRelations map[string]map[string]bool       // source entity ID -> set of relation IDs
	incomingRelations map[string]map[string]bool       // target entity ID -> set of relation IDs
	relationByType    map[RelationType]map[string]bool // type -> set of relation IDs

	// Reasoning chains
	reasoningChains map[string]*ReasoningChain
}

// NewKnowledgeGraph creates a new knowledge graph
func NewKnowledgeGraph() *KnowledgeGraph {
	return &KnowledgeGraph{
		entities:          make(map[string]*Entity),
		relations:         make(map[string]*Relation),
		entityByType:      make(map[EntityType]map[string]bool),
		entityByNamespace: make(map[string]map[string]bool),
		entityByName:      make(map[string]map[string]bool),
		outgoingRelations: make(map[string]map[string]bool),
		incomingRelations: make(map[string]map[string]bool),
		relationByType:    make(map[RelationType]map[string]bool),
		reasoningChains:   make(map[string]*ReasoningChain),
	}
}

// AddEntity adds an entity to the graph
func (g *KnowledgeGraph) AddEntity(entity *Entity) error {
	if entity.Name == "" {
		return fmt.Errorf("entity name is required")
	}
	if entity.Type == "" {
		return fmt.Errorf("entity type is required")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Generate ID if not provided
	if entity.ID == "" {
		entity.ID = uuid.New().String()[:12]
	}

	// Set timestamps
	now := time.Now().UTC()
	if entity.CreatedAt.IsZero() {
		entity.CreatedAt = now
	}
	entity.UpdatedAt = now

	// Store entity
	g.entities[entity.ID] = entity

	// Update indexes
	g.indexEntity(entity)

	return nil
}

// indexEntity updates all indexes for an entity
func (g *KnowledgeGraph) indexEntity(entity *Entity) {
	// Index by type
	if g.entityByType[entity.Type] == nil {
		g.entityByType[entity.Type] = make(map[string]bool)
	}
	g.entityByType[entity.Type][entity.ID] = true

	// Index by namespace
	ns := entity.Namespace
	if ns == "" {
		ns = "_default"
	}
	if g.entityByNamespace[ns] == nil {
		g.entityByNamespace[ns] = make(map[string]bool)
	}
	g.entityByNamespace[ns][entity.ID] = true

	// Index by name (lowercase for case-insensitive search)
	nameLower := strings.ToLower(entity.Name)
	if g.entityByName[nameLower] == nil {
		g.entityByName[nameLower] = make(map[string]bool)
	}
	g.entityByName[nameLower][entity.ID] = true
}

// removeEntityFromIndexes removes an entity from all indexes
func (g *KnowledgeGraph) removeEntityFromIndexes(entity *Entity) {
	delete(g.entityByType[entity.Type], entity.ID)
	ns := entity.Namespace
	if ns == "" {
		ns = "_default"
	}
	delete(g.entityByNamespace[ns], entity.ID)
	nameLower := strings.ToLower(entity.Name)
	delete(g.entityByName[nameLower], entity.ID)
}

// GetEntity retrieves an entity by ID
func (g *KnowledgeGraph) GetEntity(id string) (*Entity, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	entity, ok := g.entities[id]
	if !ok {
		return nil, fmt.Errorf("entity not found: %s", id)
	}
	return entity, nil
}

// UpdateEntity updates an existing entity
func (g *KnowledgeGraph) UpdateEntity(entity *Entity) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	existing, ok := g.entities[entity.ID]
	if !ok {
		return fmt.Errorf("entity not found: %s", entity.ID)
	}

	// Remove from old indexes
	g.removeEntityFromIndexes(existing)

	// Update
	entity.UpdatedAt = time.Now().UTC()
	entity.CreatedAt = existing.CreatedAt // Preserve creation time
	g.entities[entity.ID] = entity

	// Re-index
	g.indexEntity(entity)

	return nil
}

// DeleteEntity removes an entity and its relations
func (g *KnowledgeGraph) DeleteEntity(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	entity, ok := g.entities[id]
	if !ok {
		return fmt.Errorf("entity not found: %s", id)
	}

	// Remove from indexes
	g.removeEntityFromIndexes(entity)

	// Remove all relations involving this entity
	for relID := range g.outgoingRelations[id] {
		g.deleteRelationUnsafe(relID)
	}
	for relID := range g.incomingRelations[id] {
		g.deleteRelationUnsafe(relID)
	}

	// Remove entity
	delete(g.entities, id)
	delete(g.outgoingRelations, id)
	delete(g.incomingRelations, id)

	return nil
}

// FindEntities searches for entities matching criteria
func (g *KnowledgeGraph) FindEntities(entityType EntityType, namespace, namePattern string, limit int) []*Entity {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var candidates map[string]bool

	// Start with type filter if specified
	if entityType != "" {
		candidates = g.entityByType[entityType]
	} else {
		// All entities
		candidates = make(map[string]bool)
		for id := range g.entities {
			candidates[id] = true
		}
	}

	// Apply namespace filter
	if namespace != "" {
		nsEntities := g.entityByNamespace[namespace]
		if nsEntities == nil {
			return nil
		}
		if candidates != nil {
			// Intersect
			newCandidates := make(map[string]bool)
			for id := range candidates {
				if nsEntities[id] {
					newCandidates[id] = true
				}
			}
			candidates = newCandidates
		} else {
			candidates = nsEntities
		}
	}

	// Compile name pattern (cached)
	var nameRE *regexp.Regexp
	if namePattern != "" {
		nameRE = getCachedRegexp("(?i)" + namePattern)
	}

	// Build result
	var result []*Entity
	for id := range candidates {
		entity := g.entities[id]
		if entity == nil {
			continue
		}

		// Apply name filter
		if namePattern != "" {
			if nameRE != nil {
				if !nameRE.MatchString(entity.Name) {
					continue
				}
			} else if !strings.Contains(strings.ToLower(entity.Name), strings.ToLower(namePattern)) {
				continue
			}
		}

		result = append(result, entity)
		if limit > 0 && len(result) >= limit {
			break
		}
	}

	return result
}

// AddRelation adds a relation to the graph
func (g *KnowledgeGraph) AddRelation(rel *Relation) error {
	if rel.SourceID == "" || rel.TargetID == "" {
		return fmt.Errorf("source_id and target_id are required")
	}
	if rel.Type == "" {
		return fmt.Errorf("relation type is required")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Verify entities exist
	if _, ok := g.entities[rel.SourceID]; !ok {
		return fmt.Errorf("source entity not found: %s", rel.SourceID)
	}
	if _, ok := g.entities[rel.TargetID]; !ok {
		return fmt.Errorf("target entity not found: %s", rel.TargetID)
	}

	// Generate ID if not provided
	if rel.ID == "" {
		rel.ID = uuid.New().String()[:12]
	}

	// Set timestamp
	if rel.CreatedAt.IsZero() {
		rel.CreatedAt = time.Now().UTC()
	}

	// Default weight
	if rel.Weight == 0 {
		rel.Weight = 1.0
	}

	// Store relation
	g.relations[rel.ID] = rel

	// Update indexes
	if g.outgoingRelations[rel.SourceID] == nil {
		g.outgoingRelations[rel.SourceID] = make(map[string]bool)
	}
	g.outgoingRelations[rel.SourceID][rel.ID] = true

	if g.incomingRelations[rel.TargetID] == nil {
		g.incomingRelations[rel.TargetID] = make(map[string]bool)
	}
	g.incomingRelations[rel.TargetID][rel.ID] = true

	if g.relationByType[rel.Type] == nil {
		g.relationByType[rel.Type] = make(map[string]bool)
	}
	g.relationByType[rel.Type][rel.ID] = true

	// If bidirectional, add reverse relation
	if rel.Bidirectional {
		reverseID := rel.ID + "-rev"
		reverseRel := &Relation{
			ID:            reverseID,
			Type:          rel.Type,
			SourceID:      rel.TargetID,
			TargetID:      rel.SourceID,
			Weight:        rel.Weight,
			Bidirectional: false, // Don't recurse
			Properties:    rel.Properties,
			Evidence:      rel.Evidence,
			SessionID:     rel.SessionID,
			AgentID:       rel.AgentID,
			CreatedAt:     rel.CreatedAt,
		}
		g.relations[reverseID] = reverseRel
		if g.outgoingRelations[rel.TargetID] == nil {
			g.outgoingRelations[rel.TargetID] = make(map[string]bool)
		}
		g.outgoingRelations[rel.TargetID][reverseID] = true
		if g.incomingRelations[rel.SourceID] == nil {
			g.incomingRelations[rel.SourceID] = make(map[string]bool)
		}
		g.incomingRelations[rel.SourceID][reverseID] = true
		g.relationByType[rel.Type][reverseID] = true
	}

	return nil
}

// GetRelation retrieves a relation by ID
func (g *KnowledgeGraph) GetRelation(id string) (*Relation, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	rel, ok := g.relations[id]
	if !ok {
		return nil, fmt.Errorf("relation not found: %s", id)
	}
	return rel, nil
}

// DeleteRelation removes a relation
func (g *KnowledgeGraph) DeleteRelation(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.deleteRelationUnsafe(id)
}

func (g *KnowledgeGraph) deleteRelationUnsafe(id string) error {
	rel, ok := g.relations[id]
	if !ok {
		return fmt.Errorf("relation not found: %s", id)
	}

	// Remove from indexes
	delete(g.outgoingRelations[rel.SourceID], id)
	delete(g.incomingRelations[rel.TargetID], id)
	delete(g.relationByType[rel.Type], id)

	// Remove relation
	delete(g.relations, id)

	return nil
}

// GetEntityRelations gets all relations for an entity
func (g *KnowledgeGraph) GetEntityRelations(entityID string, relTypes []RelationType, outgoing, incoming bool) []*Relation {
	g.mu.RLock()
	defer g.mu.RUnlock()

	relTypeSet := make(map[RelationType]bool)
	for _, t := range relTypes {
		relTypeSet[t] = true
	}

	var result []*Relation

	if outgoing {
		for relID := range g.outgoingRelations[entityID] {
			rel := g.relations[relID]
			if rel != nil {
				if len(relTypes) == 0 || relTypeSet[rel.Type] {
					result = append(result, rel)
				}
			}
		}
	}

	if incoming {
		for relID := range g.incomingRelations[entityID] {
			rel := g.relations[relID]
			if rel != nil {
				if len(relTypes) == 0 || relTypeSet[rel.Type] {
					result = append(result, rel)
				}
			}
		}
	}

	return result
}

// Query executes a graph query
func (g *KnowledgeGraph) Query(q GraphQuery) (*GraphQueryResult, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := &GraphQueryResult{
		Entities:  []Entity{},
		Relations: []Relation{},
		Paths:     []GraphPath{},
	}

	// If entity ID specified, do neighbor traversal
	if q.EntityID != "" {
		return g.traverseFromEntity(q)
	}

	// If pattern specified, parse and execute
	if q.Pattern != "" {
		return g.executePatternQuery(q)
	}

	// Otherwise, do filtered search
	entities := g.findEntitiesForQuery(q)
	for _, e := range entities {
		result.Entities = append(result.Entities, *e)
	}

	// Find relations between returned entities
	entitySet := make(map[string]bool)
	for _, e := range result.Entities {
		entitySet[e.ID] = true
	}

	for _, rel := range g.relations {
		if entitySet[rel.SourceID] && entitySet[rel.TargetID] {
			if len(q.RelationTypes) == 0 || containsRelType(q.RelationTypes, rel.Type) {
				result.Relations = append(result.Relations, *rel)
			}
		}
	}

	return result, nil
}

func (g *KnowledgeGraph) findEntitiesForQuery(q GraphQuery) []*Entity {
	var result []*Entity

	for _, entity := range g.entities {
		// Type filter
		if len(q.SourceTypes) > 0 && !containsEntityType(q.SourceTypes, entity.Type) {
			continue
		}

		// Namespace filter
		if q.Namespace != "" && entity.Namespace != q.Namespace {
			continue
		}

		// Session filter
		if q.SessionID != "" && entity.SessionID != q.SessionID {
			continue
		}

		// Agent filter
		if q.AgentID != "" && entity.AgentID != q.AgentID {
			continue
		}

		result = append(result, entity)
		if q.Limit > 0 && len(result) >= q.Limit {
			break
		}
	}

	return result
}

// traverseFromEntity does a BFS/DFS from a starting entity
func (g *KnowledgeGraph) traverseFromEntity(q GraphQuery) (*GraphQueryResult, error) {
	result := &GraphQueryResult{
		Entities:  []Entity{},
		Relations: []Relation{},
		Paths:     []GraphPath{},
	}

	startEntity, ok := g.entities[q.EntityID]
	if !ok {
		return nil, fmt.Errorf("entity not found: %s", q.EntityID)
	}

	result.Entities = append(result.Entities, *startEntity)

	maxDepth := q.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}

	visited := make(map[string]bool)
	visited[q.EntityID] = true

	// BFS
	currentLevel := []string{q.EntityID}
	for depth := 0; depth < maxDepth && len(currentLevel) > 0; depth++ {
		nextLevel := []string{}

		for _, entityID := range currentLevel {
			// Outgoing relations
			for relID := range g.outgoingRelations[entityID] {
				rel := g.relations[relID]
				if rel == nil {
					continue
				}

				// Filter by relation type
				if len(q.RelationTypes) > 0 && !containsRelType(q.RelationTypes, rel.Type) {
					continue
				}

				result.Relations = append(result.Relations, *rel)

				if !visited[rel.TargetID] {
					visited[rel.TargetID] = true
					targetEntity := g.entities[rel.TargetID]
					if targetEntity != nil {
						// Filter by target type
						if len(q.TargetTypes) == 0 || containsEntityType(q.TargetTypes, targetEntity.Type) {
							result.Entities = append(result.Entities, *targetEntity)
							nextLevel = append(nextLevel, rel.TargetID)
						}
					}
				}
			}

			// Incoming relations (if bidirectional query)
			if q.Bidirectional {
				for relID := range g.incomingRelations[entityID] {
					rel := g.relations[relID]
					if rel == nil {
						continue
					}

					if len(q.RelationTypes) > 0 && !containsRelType(q.RelationTypes, rel.Type) {
						continue
					}

					result.Relations = append(result.Relations, *rel)

					if !visited[rel.SourceID] {
						visited[rel.SourceID] = true
						sourceEntity := g.entities[rel.SourceID]
						if sourceEntity != nil {
							if len(q.SourceTypes) == 0 || containsEntityType(q.SourceTypes, sourceEntity.Type) {
								result.Entities = append(result.Entities, *sourceEntity)
								nextLevel = append(nextLevel, rel.SourceID)
							}
						}
					}
				}
			}
		}

		currentLevel = nextLevel

		// Apply limit
		if q.Limit > 0 && len(result.Entities) >= q.Limit {
			break
		}
	}

	return result, nil
}

// executePatternQuery executes a simple pattern query
// Supports patterns like: (file)-[calls]->(function)
func (g *KnowledgeGraph) executePatternQuery(q GraphQuery) (*GraphQueryResult, error) {
	result := &GraphQueryResult{
		Entities:  []Entity{},
		Relations: []Relation{},
	}

	// Parse simple pattern: (type)-[relation]->(type)
	pattern := strings.TrimSpace(q.Pattern)

	// Regex to match pattern
	re := regexp.MustCompile(`\((\w*)\)\s*-\[(\w*)\]->\s*\((\w*)\)`)
	matches := re.FindStringSubmatch(pattern)

	if len(matches) != 4 {
		return nil, fmt.Errorf("invalid pattern: %s (expected format: (type)-[relation]->(type))", pattern)
	}

	sourceType := EntityType(matches[1])
	relType := RelationType(matches[2])
	targetType := EntityType(matches[3])

	entitySet := make(map[string]bool)

	for _, rel := range g.relations {
		// Filter by relation type
		if relType != "" && rel.Type != relType {
			continue
		}

		sourceEntity := g.entities[rel.SourceID]
		targetEntity := g.entities[rel.TargetID]

		if sourceEntity == nil || targetEntity == nil {
			continue
		}

		// Filter by entity types
		if sourceType != "" && sourceEntity.Type != sourceType {
			continue
		}
		if targetType != "" && targetEntity.Type != targetType {
			continue
		}

		// Add to results
		result.Relations = append(result.Relations, *rel)

		if !entitySet[sourceEntity.ID] {
			entitySet[sourceEntity.ID] = true
			result.Entities = append(result.Entities, *sourceEntity)
		}
		if !entitySet[targetEntity.ID] {
			entitySet[targetEntity.ID] = true
			result.Entities = append(result.Entities, *targetEntity)
		}

		if q.Limit > 0 && len(result.Relations) >= q.Limit {
			break
		}
	}

	return result, nil
}

// FindPath finds a path between two entities
func (g *KnowledgeGraph) FindPath(sourceID, targetID string, maxDepth int, relTypes []RelationType) (*GraphPath, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if maxDepth <= 0 {
		maxDepth = 5
	}

	// BFS to find shortest path
	type queueItem struct {
		entityID string
		path     []string // entity IDs
		edges    []string // relation IDs
	}

	// Self-path: source == target is a zero-length path.
	if sourceID == targetID {
		return &GraphPath{
			Nodes:  []string{sourceID},
			Edges:  nil,
			Length: 0,
		}, nil
	}

	visited := make(map[string]bool)
	queue := []queueItem{{entityID: sourceID, path: []string{sourceID}, edges: []string{}}}
	visited[sourceID] = true

	relTypeSet := make(map[RelationType]bool)
	for _, t := range relTypes {
		relTypeSet[t] = true
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// Hops from source = len(path)-1. Stop exploring if already at maxDepth.
		if len(current.path)-1 >= maxDepth {
			continue
		}

		// Check outgoing relations
		for relID := range g.outgoingRelations[current.entityID] {
			rel := g.relations[relID]
			if rel == nil {
				continue
			}

			if len(relTypes) > 0 && !relTypeSet[rel.Type] {
				continue
			}

			if rel.TargetID == targetID {
				// Found path
				return &GraphPath{
					Nodes:  append(current.path, targetID),
					Edges:  append(current.edges, relID),
					Length: len(current.path),
				}, nil
			}

			if !visited[rel.TargetID] {
				visited[rel.TargetID] = true
				queue = append(queue, queueItem{
					entityID: rel.TargetID,
					path:     append(append([]string{}, current.path...), rel.TargetID),
					edges:    append(append([]string{}, current.edges...), relID),
				})
			}
		}
	}

	return nil, fmt.Errorf("no path found between %s and %s", sourceID, targetID)
}

// AddReasoningChain stores a reasoning chain
func (g *KnowledgeGraph) AddReasoningChain(chain *ReasoningChain) error {
	if chain.Query == "" {
		return fmt.Errorf("query is required")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if chain.ID == "" {
		chain.ID = uuid.New().String()[:12]
	}
	if chain.CreatedAt.IsZero() {
		chain.CreatedAt = time.Now().UTC()
	}

	g.reasoningChains[chain.ID] = chain
	return nil
}

// GetReasoningChain retrieves a reasoning chain
func (g *KnowledgeGraph) GetReasoningChain(id string) (*ReasoningChain, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	chain, ok := g.reasoningChains[id]
	if !ok {
		return nil, fmt.Errorf("reasoning chain not found: %s", id)
	}
	return chain, nil
}

// ListReasoningChains lists reasoning chains
func (g *KnowledgeGraph) ListReasoningChains(sessionID, agentID string, limit int) []*ReasoningChain {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var result []*ReasoningChain
	for _, chain := range g.reasoningChains {
		if sessionID != "" && chain.SessionID != sessionID {
			continue
		}
		if agentID != "" && chain.AgentID != agentID {
			continue
		}
		result = append(result, chain)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

// Stats returns graph statistics
func (g *KnowledgeGraph) Stats() GraphStats {
	g.mu.RLock()
	defer g.mu.RUnlock()

	stats := GraphStats{
		TotalEntities:   len(g.entities),
		TotalRelations:  len(g.relations),
		EntitiesByType:  make(map[string]int),
		RelationsByType: make(map[string]int),
	}

	for t, ids := range g.entityByType {
		stats.EntitiesByType[string(t)] = len(ids)
	}

	for t, ids := range g.relationByType {
		stats.RelationsByType[string(t)] = len(ids)
	}

	nsSet := make(map[string]bool)
	for ns := range g.entityByNamespace {
		if ns != "_default" {
			nsSet[ns] = true
		}
	}
	for ns := range nsSet {
		stats.Namespaces = append(stats.Namespaces, ns)
	}

	return stats
}

// Helper functions

func containsEntityType(types []EntityType, t EntityType) bool {
	for _, et := range types {
		if et == t {
			return true
		}
	}
	return false
}

func containsRelType(types []RelationType, t RelationType) bool {
	for _, rt := range types {
		if rt == t {
			return true
		}
	}
	return false
}

// =========================================================================
// Persistence Layer (Phase 1.1)
// =========================================================================

// PersistenceConfig holds Qdrant clients for graph persistence
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

// AddEntityWithPersistence adds an entity and persists it to Qdrant
func (pg *persistedGraph) AddEntityWithPersistence(ctx context.Context, entity *Entity, vector []float64) error {
	// Add to in-memory graph first
	if err := pg.AddEntity(entity); err != nil {
		return err
	}

	// Persist to Qdrant
	if err := pg.PersistEntity(ctx, entity, vector); err != nil {
		// Rollback in-memory change on persistence failure
		pg.mu.Lock()
		pg.removeEntityFromIndexes(entity)
		delete(pg.entities, entity.ID)
		pg.mu.Unlock()
		return err
	}

	return nil
}

// AddRelationWithPersistence adds a relation and persists it to Qdrant
func (pg *persistedGraph) AddRelationWithPersistence(ctx context.Context, rel *Relation) error {
	// Add to in-memory graph first
	if err := pg.AddRelation(rel); err != nil {
		return err
	}

	// Persist to Qdrant
	if err := pg.PersistRelation(ctx, rel); err != nil {
		// Rollback in-memory change
		pg.mu.Lock()
		pg.deleteRelationUnsafe(rel.ID)
		pg.mu.Unlock()
		return err
	}

	// Also persist reverse relation if bidirectional
	if rel.Bidirectional {
		reverseID := rel.ID + "-rev"
		pg.mu.RLock()
		reverseRel := pg.relations[reverseID]
		pg.mu.RUnlock()
		if reverseRel != nil {
			if err := pg.PersistRelation(ctx, reverseRel); err != nil {
				// Non-fatal, log and continue
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

// DeleteEntityWithPersistence deletes an entity and removes from Qdrant
func (pg *persistedGraph) DeleteEntityWithPersistence(ctx context.Context, id string) error {
	// Get relations to delete from Qdrant
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

	// Delete from in-memory graph
	if err := pg.DeleteEntity(id); err != nil {
		return err
	}

	// Delete from Qdrant
	if err := pg.DeletePersistedEntity(ctx, id); err != nil {
		return err
	}

	// Delete relations from Qdrant
	allRelIDs := append(outgoing, incoming...)
	if len(allRelIDs) > 0 && pg.cfg != nil && pg.cfg.RelationsQdrant != nil {
		if err := pg.cfg.RelationsQdrant.Delete(ctx, allRelIDs); err != nil {
			// Non-fatal
			fmt.Printf("warning: failed to delete relations: %v\n", err)
		}
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

// regexpCache caches compiled regexps to avoid recompilation on repeated queries.
var regexpCache sync.Map // map[string]*regexp.Regexp

func getCachedRegexp(pattern string) *regexp.Regexp {
	if v, ok := regexpCache.Load(pattern); ok {
		return v.(*regexp.Regexp)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	regexpCache.Store(pattern, re)
	return re
}
