package agentcontext

import (
	"context"
	"fmt"
	"time"
)

// Compatibility helpers retained for tests and transitional callers.
func (s *Service) buildContextEntry(session *Session, m map[string]any, title, content string) ContextEntry {
	visibility := Visibility(toString(m["visibility"]))
	if visibility == "" {
		visibility = s.cfg.DefaultVisibility
	}
	ts := time.Now()
	entry := ContextEntry{
		ID:            GenerateID(session.AgentID, session.ID, title+"\n"+content, ts),
		SchemaVersion: SchemaVersion,
		AgentID:       session.AgentID,
		SessionID:     session.ID,
		Namespace:     session.Namespace,
		EntryType:     EntryType(toString(m["entry_type"])),
		Timestamp:     ts,
		Title:         title,
		Content:       content,
		ContentHash:   ContentHashFunc(content),
		FilePath:      toString(m["file_path"]),
		LineStart:     toInt(m["line_start"]),
		LineEnd:       toInt(m["line_end"]),
		Tags:          toStringSlice(m["tags"]),
		TokenCount:    EstimateTokens(title + " " + content),
		Visibility:    visibility,
		SharedWith:    toStringSlice(m["shared_with"]),
	}
	if meta, ok := m["metadata"].(map[string]any); ok {
		entry.Metadata = meta
	}
	return entry
}

func (s *Service) routeToMemory(ctx context.Context, session *Session, m map[string]any, title, content string) (string, error) {
	if s.persistedMemoryHierarchy == nil {
		return "", fmt.Errorf("memory hierarchy not available")
	}

	category := toString(m["entry_type"])
	if category == "" {
		category = "finding"
	}

	item := &MemoryItem{
		Tier:       MemoryTierLongTerm,
		Importance: ImportanceLevelMedium,
		Title:      title,
		Content:    content,
		Category:   category,
		Namespace:  session.Namespace,
		SessionID:  session.ID,
		AgentID:    session.AgentID,
		Tags:       toStringSlice(m["tags"]),
	}
	if metadata, ok := m["metadata"].(map[string]any); ok {
		item.Metadata = metadata
	}
	item.OriginalTokens = EstimateTokens(title + " " + content)

	if err := s.persistedMemoryHierarchy.AddItemWithPersistence(ctx, item, nil); err != nil {
		return "", err
	}

	s.metrics.LongTermMemoryItems.Add(1)
	s.metrics.LongTermMemoryTokens.Add(int64(item.OriginalTokens))

	return item.ID, nil
}

func (s *Service) routeToGraph(session *Session, m map[string]any, title, content string) (string, error) {
	if s.knowledgeGraph == nil {
		return "", fmt.Errorf("knowledge graph not available")
	}

	entityType := EntityType(toString(m["entry_type"]))
	if entityType == "" {
		entityType = EntityTypeConcept
	}

	entity := &Entity{
		Type:        entityType,
		Name:        title,
		Description: content,
		Namespace:   session.Namespace,
		FilePath:    toString(m["file_path"]),
		LineStart:   toInt(m["line_start"]),
		LineEnd:     toInt(m["line_end"]),
		Language:    toString(m["language"]),
		SessionID:   session.ID,
		AgentID:     session.AgentID,
		Tags:        toStringSlice(m["tags"]),
	}

	if props, ok := m["metadata"].(map[string]any); ok {
		entity.Properties = props
	}

	if err := s.knowledgeGraph.AddEntity(entity); err != nil {
		return "", err
	}

	s.metrics.GraphEntitiesAdded.Add(1)
	return entity.ID, nil
}
