// knowledge_graph.go — KnowledgeGraph struct, constructor, and aggregate statistics.
package agentcontext

import (
	"sync"
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
