// knowledge_graph_entities.go — entity CRUD and indexing operations for KnowledgeGraph.
package agentcontext

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

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
