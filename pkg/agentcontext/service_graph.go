package agentcontext

import (
	"context"
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// GetKnowledgeGraph returns the knowledge graph for direct access
func (s *Service) GetKnowledgeGraph() *KnowledgeGraph {
	return s.knowledgeGraph
}

// HandleEntityAdd adds entities to the knowledge graph
func (s *Service) HandleEntityAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.String("session_id", "")
	agentID := v.String("agent_id", s.cfg.DefaultAgentID)
	entitiesRaw := v.RequiredAny("entities")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	entitiesArr, ok := entitiesRaw.([]any)
	if !ok || len(entitiesArr) == 0 {
		return mcp.ErrorResult(fmt.Errorf("entities array is required")), nil
	}

	var addedIDs []string
	for i, entityRaw := range entitiesArr {
		entityMap, ok := entityRaw.(map[string]any)
		if !ok {
			return mcp.ErrorResult(fmt.Errorf("entity %d must be an object", i)), nil
		}

		entity := &Entity{
			ID:          toString(entityMap["id"]),
			Type:        EntityType(toString(entityMap["type"])),
			Name:        toString(entityMap["name"]),
			Description: toString(entityMap["description"]),
			Namespace:   toString(entityMap["namespace"]),
			FilePath:    toString(entityMap["file_path"]),
			LineStart:   toInt(entityMap["line_start"]),
			LineEnd:     toInt(entityMap["line_end"]),
			Language:    toString(entityMap["language"]),
			Signature:   toString(entityMap["signature"]),
			SessionID:   sessionID,
			AgentID:     agentID,
		}

		if entity.Namespace == "" {
			entity.Namespace = s.cfg.DefaultNamespace
		}

		// Parse properties
		if props, ok := entityMap["properties"].(map[string]any); ok {
			entity.Properties = props
		}

		// Parse tags
		if tags, ok := entityMap["tags"].([]any); ok {
			for _, t := range tags {
				if ts := toString(t); ts != "" {
					entity.Tags = append(entity.Tags, ts)
				}
			}
		}

		if err := s.knowledgeGraph.AddEntity(entity); err != nil {
			return mcp.ErrorResult(fmt.Errorf("failed to add entity %d: %w", i, err)), nil
		}

		addedIDs = append(addedIDs, entity.ID)
	}

	s.metrics.GraphEntitiesAdded.Add(int64(len(addedIDs)))

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"count":      len(addedIDs),
		"entity_ids": addedIDs,
	})
}

// HandleEntityGet retrieves entities by ID
func (s *Service) HandleEntityGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	entityIDs := v.RequiredStringSlice("entity_ids")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var entities []map[string]any
	for _, id := range entityIDs {
		entity, err := s.knowledgeGraph.GetEntity(id)
		if err != nil {
			continue // Skip not found
		}
		entities = append(entities, entityToMap(entity))
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(entities),
		"entities": entities,
	})
}

// HandleEntityFind searches for entities
func (s *Service) HandleEntityFind(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	entityTypeStr := v.String("type", "")
	namespace := v.String("namespace", "")
	namePattern := v.String("name_pattern", "")
	limit := v.Int("limit", 50)

	entityType := EntityType(entityTypeStr)

	entities := s.knowledgeGraph.FindEntities(entityType, namespace, namePattern, limit)

	results := make([]map[string]any, len(entities))
	for i, e := range entities {
		results[i] = entityToMap(e)
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(results),
		"entities": results,
	})
}

// HandleEntityDelete removes entities
func (s *Service) HandleEntityDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	entityIDs := v.RequiredStringSlice("entity_ids")
	confirm := v.Bool("confirm", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if !confirm {
		return mcp.ErrorResult(fmt.Errorf("confirm must be true to delete entities")), nil
	}

	var deleted []string
	for _, id := range entityIDs {
		if err := s.knowledgeGraph.DeleteEntity(id); err == nil {
			deleted = append(deleted, id)
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"deleted": deleted,
	})
}

// HandleRelationAdd adds relations to the knowledge graph
func (s *Service) HandleRelationAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.String("session_id", "")
	agentID := v.String("agent_id", s.cfg.DefaultAgentID)
	relationsRaw := v.RequiredAny("relations")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	relationsArr, ok := relationsRaw.([]any)
	if !ok || len(relationsArr) == 0 {
		return mcp.ErrorResult(fmt.Errorf("relations array is required")), nil
	}

	var addedIDs []string
	for i, relRaw := range relationsArr {
		relMap, ok := relRaw.(map[string]any)
		if !ok {
			return mcp.ErrorResult(fmt.Errorf("relation %d must be an object", i)), nil
		}

		rel := &Relation{
			ID:            toString(relMap["id"]),
			Type:          RelationType(toString(relMap["type"])),
			SourceID:      toString(relMap["source_id"]),
			TargetID:      toString(relMap["target_id"]),
			Weight:        toFloat(relMap["weight"]),
			Bidirectional: getBool(relMap["bidirectional"], false),
			Evidence:      toString(relMap["evidence"]),
			Reasoning:     toString(relMap["reasoning"]),
			SessionID:     sessionID,
			AgentID:       agentID,
		}

		// Parse properties
		if props, ok := relMap["properties"].(map[string]any); ok {
			rel.Properties = props
		}

		if err := s.knowledgeGraph.AddRelation(rel); err != nil {
			return mcp.ErrorResult(fmt.Errorf("failed to add relation %d: %w", i, err)), nil
		}

		addedIDs = append(addedIDs, rel.ID)
	}

	s.metrics.GraphRelationsAdded.Add(int64(len(addedIDs)))

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"count":        len(addedIDs),
		"relation_ids": addedIDs,
	})
}

// HandleRelationGet gets relations for an entity
func (s *Service) HandleRelationGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	entityID := v.Required("entity_id")
	outgoing := v.Bool("outgoing", true)
	incoming := v.Bool("incoming", true)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var relTypes []RelationType
	if types, ok := args["relation_types"].([]any); ok {
		for _, t := range types {
			if ts := toString(t); ts != "" {
				relTypes = append(relTypes, RelationType(ts))
			}
		}
	}

	relations := s.knowledgeGraph.GetEntityRelations(entityID, relTypes, outgoing, incoming)

	results := make([]map[string]any, len(relations))
	for i, r := range relations {
		results[i] = relationToMap(r)
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"count":     len(results),
		"relations": results,
	})
}

// HandleRelationDelete removes relations
func (s *Service) HandleRelationDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	relationIDs := v.RequiredStringSlice("relation_ids")
	confirm := v.Bool("confirm", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if !confirm {
		return mcp.ErrorResult(fmt.Errorf("confirm must be true to delete relations")), nil
	}

	var deleted []string
	for _, id := range relationIDs {
		if err := s.knowledgeGraph.DeleteRelation(id); err == nil {
			deleted = append(deleted, id)
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"deleted": deleted,
	})
}

// HandleGraphQuery executes a graph query
func (s *Service) HandleGraphQuery(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	pattern := v.String("pattern", "")
	entityID := v.String("entity_id", "")
	namespace := v.String("namespace", "")
	sessionID := v.String("session_id", "")
	agentID := v.String("agent_id", "")
	maxDepth := v.Int("max_depth", 2)
	bidirectional := v.Bool("bidirectional", false)
	limit := v.Int("limit", 0)
	includeProperties := v.Bool("include_properties", true)

	query := GraphQuery{
		Pattern:           pattern,
		EntityID:          entityID,
		Namespace:         namespace,
		SessionID:         sessionID,
		AgentID:           agentID,
		MaxDepth:          maxDepth,
		Bidirectional:     bidirectional,
		Limit:             limit,
		IncludeProperties: includeProperties,
	}

	// Parse entity types
	if types, ok := args["source_types"].([]any); ok {
		for _, t := range types {
			if ts := toString(t); ts != "" {
				query.SourceTypes = append(query.SourceTypes, EntityType(ts))
			}
		}
	}
	if types, ok := args["target_types"].([]any); ok {
		for _, t := range types {
			if ts := toString(t); ts != "" {
				query.TargetTypes = append(query.TargetTypes, EntityType(ts))
			}
		}
	}

	// Parse relation types
	if types, ok := args["relation_types"].([]any); ok {
		for _, t := range types {
			if ts := toString(t); ts != "" {
				query.RelationTypes = append(query.RelationTypes, RelationType(ts))
			}
		}
	}

	result, err := s.knowledgeGraph.Query(query)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	s.metrics.GraphQueriesExecuted.Add(1)

	entities := make([]map[string]any, len(result.Entities))
	for i, e := range result.Entities {
		entities[i] = entityToMap(&e)
	}

	relations := make([]map[string]any, len(result.Relations))
	for i, r := range result.Relations {
		relations[i] = relationToMap(&r)
	}

	paths := make([]map[string]any, len(result.Paths))
	for i, p := range result.Paths {
		paths[i] = map[string]any{
			"nodes":  p.Nodes,
			"edges":  p.Edges,
			"length": p.Length,
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":             true,
		"entity_count":   len(entities),
		"relation_count": len(relations),
		"entities":       entities,
		"relations":      relations,
		"paths":          paths,
	})
}

// HandleFindPath finds a path between two entities
func (s *Service) HandleFindPath(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sourceID := v.Required("source_id")
	targetID := v.Required("target_id")
	maxDepth := v.Int("max_depth", 5)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var relTypes []RelationType
	if types, ok := args["relation_types"].([]any); ok {
		for _, t := range types {
			if ts := toString(t); ts != "" {
				relTypes = append(relTypes, RelationType(ts))
			}
		}
	}

	path, err := s.knowledgeGraph.FindPath(sourceID, targetID, maxDepth, relTypes)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"path":   path.Nodes,
		"edges":  path.Edges,
		"length": path.Length,
	})
}

// HandleReasoningChainAdd adds a reasoning chain
func (s *Service) HandleReasoningChainAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.String("session_id", "")
	agentID := v.String("agent_id", s.cfg.DefaultAgentID)
	query := v.Required("query")
	conclusion := v.String("conclusion", "")
	confidence := v.Float("confidence", 0)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	chain := &ReasoningChain{
		Query:      query,
		Conclusion: conclusion,
		Confidence: confidence,
		SessionID:  sessionID,
		AgentID:    agentID,
	}

	// Parse steps
	if stepsRaw, ok := args["steps"].([]any); ok {
		for i, stepRaw := range stepsRaw {
			stepMap, ok := stepRaw.(map[string]any)
			if !ok {
				continue
			}
			step := ReasoningStep{
				StepNumber:  i + 1,
				Description: toString(stepMap["description"]),
				Conclusion:  toString(stepMap["conclusion"]),
				Confidence:  toFloat(stepMap["confidence"]),
				EntityIDs:   toStringSlice(stepMap["entity_ids"]),
				RelationIDs: toStringSlice(stepMap["relation_ids"]),
			}
			chain.Steps = append(chain.Steps, step)
		}
	}

	if err := s.knowledgeGraph.AddReasoningChain(chain); err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"chain_id": chain.ID,
	})
}

// HandleReasoningChainGet retrieves a reasoning chain
func (s *Service) HandleReasoningChainGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	chainID := v.Required("chain_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	chain, err := s.knowledgeGraph.GetReasoningChain(chainID)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	steps := make([]map[string]any, len(chain.Steps))
	for i, step := range chain.Steps {
		steps[i] = map[string]any{
			"step_number":  step.StepNumber,
			"description":  step.Description,
			"conclusion":   step.Conclusion,
			"confidence":   step.Confidence,
			"entity_ids":   step.EntityIDs,
			"relation_ids": step.RelationIDs,
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"chain_id":   chain.ID,
		"query":      chain.Query,
		"steps":      steps,
		"conclusion": chain.Conclusion,
		"confidence": chain.Confidence,
		"created_at": chain.CreatedAt.Format(time.RFC3339Nano),
	})
}

// HandleReasoningChainList lists reasoning chains
func (s *Service) HandleReasoningChainList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.String("session_id", "")
	agentID := v.String("agent_id", "")
	limit := v.Int("limit", 50)

	chains := s.knowledgeGraph.ListReasoningChains(sessionID, agentID, limit)

	results := make([]map[string]any, len(chains))
	for i, chain := range chains {
		results[i] = map[string]any{
			"chain_id":   chain.ID,
			"query":      chain.Query,
			"step_count": len(chain.Steps),
			"conclusion": chain.Conclusion,
			"confidence": chain.Confidence,
			"created_at": chain.CreatedAt.Format(time.RFC3339Nano),
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"count":  len(results),
		"chains": results,
	})
}

// HandleGraphStats returns knowledge graph statistics
func (s *Service) HandleGraphStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	stats := s.knowledgeGraph.Stats()

	return mcp.JSONResult(map[string]any{
		"ok":                true,
		"total_entities":    stats.TotalEntities,
		"total_relations":   stats.TotalRelations,
		"entities_by_type":  stats.EntitiesByType,
		"relations_by_type": stats.RelationsByType,
		"namespaces":        stats.Namespaces,
	})
}

// Helper functions for knowledge graph

func entityToMap(e *Entity) map[string]any {
	m := map[string]any{
		"id":          e.ID,
		"type":        string(e.Type),
		"name":        e.Name,
		"description": e.Description,
		"namespace":   e.Namespace,
		"created_at":  e.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":  e.UpdatedAt.Format(time.RFC3339Nano),
	}
	if e.FilePath != "" {
		m["file_path"] = e.FilePath
	}
	if e.LineStart > 0 {
		m["line_start"] = e.LineStart
	}
	if e.LineEnd > 0 {
		m["line_end"] = e.LineEnd
	}
	if e.Language != "" {
		m["language"] = e.Language
	}
	if e.Signature != "" {
		m["signature"] = e.Signature
	}
	if e.Properties != nil {
		m["properties"] = e.Properties
	}
	if len(e.Tags) > 0 {
		m["tags"] = e.Tags
	}
	if e.SessionID != "" {
		m["session_id"] = e.SessionID
	}
	if e.AgentID != "" {
		m["agent_id"] = e.AgentID
	}
	return m
}

func relationToMap(r *Relation) map[string]any {
	m := map[string]any{
		"id":         r.ID,
		"type":       string(r.Type),
		"source_id":  r.SourceID,
		"target_id":  r.TargetID,
		"weight":     r.Weight,
		"created_at": r.CreatedAt.Format(time.RFC3339Nano),
	}
	if r.Bidirectional {
		m["bidirectional"] = true
	}
	if r.Evidence != "" {
		m["evidence"] = r.Evidence
	}
	if r.Reasoning != "" {
		m["reasoning"] = r.Reasoning
	}
	if r.Properties != nil {
		m["properties"] = r.Properties
	}
	if r.SessionID != "" {
		m["session_id"] = r.SessionID
	}
	if r.AgentID != "" {
		m["agent_id"] = r.AgentID
	}
	return m
}
