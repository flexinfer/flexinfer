// knowledge_graph_relations.go — relation CRUD and helper operations for KnowledgeGraph.
package agentcontext

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

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
