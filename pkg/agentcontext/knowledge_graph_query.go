// knowledge_graph_query.go — graph query dispatch, BFS traversal, pattern DSL, and path-finding.
package agentcontext

import (
	"fmt"
	"regexp"
	"strings"
)

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
