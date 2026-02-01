package agentcontext

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// =========================================================================
// Enhanced Graph Query Language (Phase 2.3)
// =========================================================================

// EnhancedGraphQuery provides advanced graph query capabilities
type EnhancedGraphQuery struct {
	// Multi-hop patterns like: (file)-[imports]->(module)-[defines]->(function)
	Pattern string `json:"pattern,omitempty"`

	// Property filters like: (function{language: "go"})-[calls]->()
	PropertyFilters map[string]map[string]any `json:"property_filters,omitempty"`

	// Optional relations (marked with ?)
	OptionalRelations []string `json:"optional_relations,omitempty"`

	// Path length constraints
	MinDepth int `json:"min_depth,omitempty"`
	MaxDepth int `json:"max_depth,omitempty"`

	// Starting entity
	StartEntityID string `json:"start_entity_id,omitempty"`

	// Result options
	Limit             int  `json:"limit,omitempty"`
	IncludeProperties bool `json:"include_properties,omitempty"`
}

// EnhancedGraphQueryResult contains the enhanced query result
type EnhancedGraphQueryResult struct {
	Entities       []Entity       `json:"entities"`
	Relations      []Relation     `json:"relations"`
	Paths          []EnhancedPath `json:"paths,omitempty"`
	PatternMatches []PatternMatch `json:"pattern_matches,omitempty"`
}

// EnhancedPath represents a path with full entity/relation details
type EnhancedPath struct {
	Nodes       []Entity   `json:"nodes"`
	Edges       []Relation `json:"edges"`
	Length      int        `json:"length"`
	TotalWeight float64    `json:"total_weight,omitempty"`
}

// PatternMatch represents a single match of a multi-hop pattern
type PatternMatch struct {
	Steps      []PatternStep `json:"steps"`
	TotalScore float64       `json:"total_score,omitempty"`
}

// PatternStep represents one step in a pattern match
type PatternStep struct {
	Entity   Entity    `json:"entity"`
	Relation *Relation `json:"relation,omitempty"` // nil for the last step
}

// ParsedPatternStep represents a parsed step from the pattern string
type ParsedPatternStep struct {
	EntityType    EntityType     `json:"entity_type,omitempty"`
	EntityFilters map[string]any `json:"entity_filters,omitempty"`
	RelationType  RelationType   `json:"relation_type,omitempty"`
	Optional      bool           `json:"optional"`
}

// ParsePattern parses an enhanced pattern string
// Supports: (type{prop: "value"})-[relation?]->(type)
func ParsePattern(pattern string) ([]ParsedPatternStep, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, fmt.Errorf("empty pattern")
	}

	// Pattern: (entity1)-[rel1]->(entity2)-[rel2]->(entity3)
	// Each segment: (type{filters})-[relation?]->

	var steps []ParsedPatternStep

	// Split by ->
	segments := strings.Split(pattern, "->")

	for i, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}

		step := ParsedPatternStep{
			EntityFilters: make(map[string]any),
		}

		// Extract entity part: (type{filters})
		entityMatch := regexp.MustCompile(`\(([^)]*)\)`).FindStringSubmatch(segment)
		if len(entityMatch) > 1 {
			entityStr := entityMatch[1]

			// Check for filters: type{key: "value", ...}
			filterMatch := regexp.MustCompile(`(\w*)\{([^}]*)\}`).FindStringSubmatch(entityStr)
			if len(filterMatch) > 2 {
				step.EntityType = EntityType(filterMatch[1])
				// Parse filters
				filterStr := filterMatch[2]
				step.EntityFilters = parseFilters(filterStr)
			} else {
				step.EntityType = EntityType(strings.TrimSpace(entityStr))
			}
		}

		// Extract relation part: -[relation?]-
		// Only if not the last segment
		if i < len(segments)-1 {
			relMatch := regexp.MustCompile(`-\[(\w*)\??]\s*$`).FindStringSubmatch(segment)
			if len(relMatch) > 1 {
				step.RelationType = RelationType(relMatch[1])
				step.Optional = strings.Contains(segment, "?]")
			}
		}

		steps = append(steps, step)
	}

	if len(steps) == 0 {
		return nil, fmt.Errorf("no valid steps in pattern")
	}

	return steps, nil
}

// parseFilters parses filter expressions like: language: "go", lines: 100
func parseFilters(filterStr string) map[string]any {
	filters := make(map[string]any)

	// Split by comma
	parts := strings.Split(filterStr, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Split by colon
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}

		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])

		// Try to parse value
		if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
			// String value
			filters[key] = value[1 : len(value)-1]
		} else if num, err := strconv.Atoi(value); err == nil {
			// Integer value
			filters[key] = num
		} else if flt, err := strconv.ParseFloat(value, 64); err == nil {
			// Float value
			filters[key] = flt
		} else if value == "true" {
			filters[key] = true
		} else if value == "false" {
			filters[key] = false
		} else {
			// Treat as string
			filters[key] = value
		}
	}

	return filters
}

// ExecuteEnhancedQuery executes an enhanced graph query
func (g *KnowledgeGraph) ExecuteEnhancedQuery(q EnhancedGraphQuery) (*EnhancedGraphQueryResult, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := &EnhancedGraphQueryResult{
		Entities:  []Entity{},
		Relations: []Relation{},
		Paths:     []EnhancedPath{},
	}

	// Parse pattern if provided
	if q.Pattern != "" {
		steps, err := ParsePattern(q.Pattern)
		if err != nil {
			return nil, fmt.Errorf("parse pattern: %w", err)
		}

		matches, err := g.executeMultiHopPattern(steps, q)
		if err != nil {
			return nil, err
		}
		result.PatternMatches = matches

		// Extract unique entities and relations from matches
		entitySet := make(map[string]bool)
		relationSet := make(map[string]bool)

		for _, match := range matches {
			for _, step := range match.Steps {
				if !entitySet[step.Entity.ID] {
					entitySet[step.Entity.ID] = true
					result.Entities = append(result.Entities, step.Entity)
				}
				if step.Relation != nil && !relationSet[step.Relation.ID] {
					relationSet[step.Relation.ID] = true
					result.Relations = append(result.Relations, *step.Relation)
				}
			}
		}
	} else if q.StartEntityID != "" {
		// Path-based query from starting entity
		paths, err := g.findPaths(q)
		if err != nil {
			return nil, err
		}
		result.Paths = paths

		// Extract unique entities and relations
		entitySet := make(map[string]bool)
		relationSet := make(map[string]bool)

		for _, path := range paths {
			for _, entity := range path.Nodes {
				if !entitySet[entity.ID] {
					entitySet[entity.ID] = true
					result.Entities = append(result.Entities, entity)
				}
			}
			for _, rel := range path.Edges {
				if !relationSet[rel.ID] {
					relationSet[rel.ID] = true
					result.Relations = append(result.Relations, rel)
				}
			}
		}
	}

	// Apply limit
	if q.Limit > 0 && len(result.Entities) > q.Limit {
		result.Entities = result.Entities[:q.Limit]
	}

	return result, nil
}

// executeMultiHopPattern executes a multi-hop pattern query
func (g *KnowledgeGraph) executeMultiHopPattern(steps []ParsedPatternStep, q EnhancedGraphQuery) ([]PatternMatch, error) {
	if len(steps) == 0 {
		return nil, nil
	}

	var matches []PatternMatch

	// Find all entities matching the first step
	startEntities := g.findMatchingEntities(steps[0])

	for _, startEntity := range startEntities {
		// Try to complete the pattern from this entity
		partialMatch := []PatternStep{{Entity: *startEntity}}

		completedMatches := g.expandPatternMatch(partialMatch, steps[1:], q)
		matches = append(matches, completedMatches...)

		if q.Limit > 0 && len(matches) >= q.Limit {
			break
		}
	}

	return matches, nil
}

// findMatchingEntities finds entities matching a pattern step
func (g *KnowledgeGraph) findMatchingEntities(step ParsedPatternStep) []*Entity {
	var results []*Entity

	for _, entity := range g.entities {
		if step.EntityType != "" && entity.Type != step.EntityType {
			continue
		}

		// Check property filters
		if len(step.EntityFilters) > 0 {
			matches := true
			for key, value := range step.EntityFilters {
				entityValue, ok := entity.Properties[key]
				if !ok || entityValue != value {
					// Check direct fields
					switch key {
					case "language":
						if entity.Language != value {
							matches = false
						}
					case "namespace":
						if entity.Namespace != value {
							matches = false
						}
					default:
						matches = false
					}
				}
			}
			if !matches {
				continue
			}
		}

		results = append(results, entity)
	}

	return results
}

// expandPatternMatch recursively expands a partial match
func (g *KnowledgeGraph) expandPatternMatch(partial []PatternStep, remainingSteps []ParsedPatternStep, q EnhancedGraphQuery) []PatternMatch {
	if len(remainingSteps) == 0 {
		// Pattern complete
		return []PatternMatch{{Steps: partial}}
	}

	lastStep := partial[len(partial)-1]
	currentStep := remainingSteps[0]

	var matches []PatternMatch

	// Find outgoing relations from the last entity
	outgoing := g.outgoingRelations[lastStep.Entity.ID]

	for relID := range outgoing {
		rel := g.relations[relID]
		if rel == nil {
			continue
		}

		// Check relation type if specified
		if currentStep.RelationType != "" && rel.Type != currentStep.RelationType {
			continue
		}

		// Get target entity
		targetEntity := g.entities[rel.TargetID]
		if targetEntity == nil {
			continue
		}

		// Check target entity type
		if currentStep.EntityType != "" && targetEntity.Type != currentStep.EntityType {
			continue
		}

		// Check property filters
		if len(currentStep.EntityFilters) > 0 {
			match := true
			for key, value := range currentStep.EntityFilters {
				entityValue, ok := targetEntity.Properties[key]
				if !ok || entityValue != value {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		// Extend the match
		newPartial := make([]PatternStep, len(partial)+1)
		copy(newPartial, partial)
		newPartial[len(partial)-1].Relation = rel
		newPartial[len(partial)] = PatternStep{Entity: *targetEntity}

		// Recurse
		expanded := g.expandPatternMatch(newPartial, remainingSteps[1:], q)
		matches = append(matches, expanded...)

		if q.Limit > 0 && len(matches) >= q.Limit {
			break
		}
	}

	// Handle optional relations
	if currentStep.Optional && len(matches) == 0 {
		// Skip this relation step
		return g.expandPatternMatch(partial, remainingSteps[1:], q)
	}

	return matches
}

// findPaths finds all paths from start entity within depth constraints
func (g *KnowledgeGraph) findPaths(q EnhancedGraphQuery) ([]EnhancedPath, error) {
	startEntity := g.entities[q.StartEntityID]
	if startEntity == nil {
		return nil, fmt.Errorf("start entity not found: %s", q.StartEntityID)
	}

	maxDepth := q.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 5
	}
	minDepth := q.MinDepth

	var paths []EnhancedPath

	// BFS to find all paths
	type queueItem struct {
		path    EnhancedPath
		visited map[string]bool
	}

	initial := queueItem{
		path: EnhancedPath{
			Nodes: []Entity{*startEntity},
		},
		visited: map[string]bool{startEntity.ID: true},
	}

	queue := []queueItem{initial}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		currentDepth := len(current.path.Nodes) - 1

		// If we've reached min depth, add to results
		if currentDepth >= minDepth {
			paths = append(paths, current.path)
			if q.Limit > 0 && len(paths) >= q.Limit {
				break
			}
		}

		// If we've reached max depth, don't expand further
		if currentDepth >= maxDepth {
			continue
		}

		// Get last entity
		lastEntity := current.path.Nodes[len(current.path.Nodes)-1]

		// Expand outgoing relations
		for relID := range g.outgoingRelations[lastEntity.ID] {
			rel := g.relations[relID]
			if rel == nil {
				continue
			}

			targetEntity := g.entities[rel.TargetID]
			if targetEntity == nil {
				continue
			}

			// Skip visited entities (avoid cycles)
			if current.visited[targetEntity.ID] {
				continue
			}

			// Create new path
			newPath := EnhancedPath{
				Nodes:       make([]Entity, len(current.path.Nodes)+1),
				Edges:       make([]Relation, len(current.path.Edges)+1),
				Length:      currentDepth + 1,
				TotalWeight: current.path.TotalWeight + rel.Weight,
			}
			copy(newPath.Nodes, current.path.Nodes)
			newPath.Nodes[len(current.path.Nodes)] = *targetEntity
			copy(newPath.Edges, current.path.Edges)
			newPath.Edges[len(current.path.Edges)] = *rel

			// Create new visited set
			newVisited := make(map[string]bool)
			for k, v := range current.visited {
				newVisited[k] = v
			}
			newVisited[targetEntity.ID] = true

			queue = append(queue, queueItem{
				path:    newPath,
				visited: newVisited,
			})
		}
	}

	return paths, nil
}
