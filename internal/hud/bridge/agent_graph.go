package bridge

import "fmt"

// GraphStats returns knowledge graph statistics.
func (a *AgentBridge) GraphStats() (*GraphStatsResult, error) {
	var result GraphStatsResult
	if err := a.callAgentTool("agent_graph_stats", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// EntityFind searches for entities in the knowledge graph.
func (a *AgentBridge) EntityFind(query string, entityType string, limit int) ([]EntityInfo, error) {
	args := map[string]any{}
	if query != "" {
		args["name_pattern"] = query
	}
	if entityType != "" {
		args["type"] = entityType
	}
	if limit > 0 {
		args["limit"] = limit
	}
	var result struct {
		Entities []EntityInfo `json:"entities"`
	}
	if err := a.callAgentTool("agent_entity_find", args, &result); err != nil {
		return nil, err
	}
	return result.Entities, nil
}

// EntityAdd creates a new entity in the knowledge graph.
func (a *AgentBridge) EntityAdd(name, entityType, namespace string, props map[string]any) error {
	entity := map[string]any{
		"name": name,
		"type": entityType,
	}
	if namespace != "" {
		entity["namespace"] = namespace
	}
	if len(props) > 0 {
		entity["properties"] = props
	}
	args := map[string]any{
		"entities": []map[string]any{entity},
	}
	return a.callAgentTool("agent_entity_add", args, nil)
}

// EntityDelete deletes an entity by ID.
func (a *AgentBridge) EntityDelete(id string) error {
	args := map[string]any{
		"entity_ids": []string{id},
		"confirm":    true,
	}
	return a.callAgentTool("agent_entity_delete", args, nil)
}

// EntityGet retrieves a single entity with its relations.
func (a *AgentBridge) EntityGet(id string) (*EntityDetail, error) {
	args := map[string]any{"entity_ids": []string{id}}
	var result struct {
		Entities []EntityDetail `json:"entities"`
	}
	if err := a.callAgentTool("agent_entity_get", args, &result); err != nil {
		return nil, err
	}
	if len(result.Entities) == 0 {
		return nil, fmt.Errorf("entity not found: %s", id)
	}
	entity := result.Entities[0]

	var relResult struct {
		Relations []RelationInfo `json:"relations"`
	}
	if err := a.callAgentTool("agent_relation_get", map[string]any{"entity_id": id}, &relResult); err == nil {
		for i := range relResult.Relations {
			if relResult.Relations[i].Source == id {
				entity.OutboundRelations = append(entity.OutboundRelations, relResult.Relations[i])
			}
			if relResult.Relations[i].Target == id {
				entity.InboundRelations = append(entity.InboundRelations, relResult.Relations[i])
			}
		}
	}

	return &entity, nil
}

// RelationAdd creates a relation between two entities.
func (a *AgentBridge) RelationAdd(sourceID, targetID, relationType string) error {
	args := map[string]any{
		"relations": []map[string]any{
			{
				"source_id": sourceID,
				"target_id": targetID,
				"type":      relationType,
			},
		},
	}
	return a.callAgentTool("agent_relation_add", args, nil)
}

// RelationDelete deletes a relation by ID.
func (a *AgentBridge) RelationDelete(id string) error {
	args := map[string]any{
		"relation_ids": []string{id},
		"confirm":      true,
	}
	return a.callAgentTool("agent_relation_delete", args, nil)
}

// GraphFindPath finds the shortest path between two entities.
func (a *AgentBridge) GraphFindPath(fromID, toID string, maxDepth int) ([]EntityInfo, error) {
	args := map[string]any{
		"source_id": fromID,
		"target_id": toID,
	}
	if maxDepth > 0 {
		args["max_depth"] = maxDepth
	}
	var result struct {
		Path []string `json:"path"`
	}
	if err := a.callAgentTool("agent_graph_find_path", args, &result); err != nil {
		return nil, err
	}
	if len(result.Path) == 0 {
		return nil, nil
	}

	var entitiesResult struct {
		Entities []EntityInfo `json:"entities"`
	}
	if err := a.callAgentTool("agent_entity_get", map[string]any{"entity_ids": result.Path}, &entitiesResult); err != nil {
		fallback := make([]EntityInfo, 0, len(result.Path))
		for _, id := range result.Path {
			fallback = append(fallback, EntityInfo{
				ID:         id,
				Name:       id,
				Type:       "entity",
				EntityType: "entity",
			})
		}
		return fallback, nil
	}

	byID := make(map[string]EntityInfo, len(entitiesResult.Entities))
	for _, e := range entitiesResult.Entities {
		byID[e.ID] = e
	}

	path := make([]EntityInfo, 0, len(result.Path))
	for _, id := range result.Path {
		if e, ok := byID[id]; ok {
			path = append(path, e)
			continue
		}
		path = append(path, EntityInfo{
			ID:         id,
			Name:       id,
			Type:       "entity",
			EntityType: "entity",
		})
	}
	return path, nil
}

// AnnotationGet retrieves code annotations, optionally filtered by file.
func (a *AgentBridge) AnnotationGet(filePath string) ([]AnnotationInfo, error) {
	args := map[string]any{}
	if filePath != "" {
		args["file_path"] = filePath
	}
	var result struct {
		Annotations []AnnotationInfo `json:"annotations"`
	}
	if err := a.callAgentTool("agent_code_annotations_get", args, &result); err != nil {
		return nil, err
	}
	return result.Annotations, nil
}

// AnnotationAdd creates a code annotation.
func (a *AgentBridge) AnnotationAdd(filePath, content, category string, line int) error {
	args := map[string]any{
		"file_path": filePath,
		"content":   content,
	}
	if category != "" {
		args["category"] = category
	}
	if line > 0 {
		args["line"] = line
	}
	return a.callAgentTool("agent_code_annotate", args, nil)
}
