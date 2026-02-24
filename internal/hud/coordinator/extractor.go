package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// ExtractedEntity represents an entity found in context entries.
type ExtractedEntity struct {
	Name       string         `json:"name"`
	EntityType string         `json:"entity_type"`
	Properties map[string]any `json:"properties,omitempty"`
}

// ExtractedRelation represents a relation found in context entries.
type ExtractedRelation struct {
	Source       string `json:"source"`
	Target       string `json:"target"`
	RelationType string `json:"relation_type"`
}

// ExtractionResult holds the output of an extraction run.
type ExtractionResult struct {
	EntitiesAdded  int `json:"entities_added"`
	RelationsAdded int `json:"relations_added"`
}

// Extractor handles LLM-powered knowledge graph entity/relation extraction.
type Extractor struct {
	client *FlexInferClient
	agent  *bridge.AgentBridge
	config Config
	model  string // Resolved model from selectModel().
	logger *slog.Logger
}

// NewExtractor creates an Extractor.
func NewExtractor(client *FlexInferClient, agent *bridge.AgentBridge, cfg Config, logger *slog.Logger) *Extractor {
	return &Extractor{
		client: client,
		agent:  agent,
		config: cfg,
		logger: logger.With("subsystem", "extractor"),
	}
}

// ExtractFromEntries parses context entries for entities and relations.
func (e *Extractor) ExtractFromEntries(ctx context.Context, entries []bridge.ContextEntryInfo) ([]ExtractedEntity, []ExtractedRelation, error) {
	if len(entries) == 0 {
		return nil, nil, nil
	}

	userMsg := formatEntries(entries)

	model := e.model
	if model == "" {
		model = e.config.DefaultModel
	}
	raw, err := e.client.CompleteSimple(ctx, model, promptEntityExtraction, userMsg, 500)
	if err != nil {
		return nil, nil, fmt.Errorf("entity extraction: %w", err)
	}

	raw = stripCodeFence(raw)
	var result struct {
		Entities  []ExtractedEntity   `json:"entities"`
		Relations []ExtractedRelation `json:"relations"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, nil, fmt.Errorf("parse extraction result: %w", err)
	}

	return result.Entities, result.Relations, nil
}

// StoreExtractions writes extracted entities and relations to the knowledge graph.
func (e *Extractor) StoreExtractions(ctx context.Context, entities []ExtractedEntity, relations []ExtractedRelation, namespace string) *ExtractionResult {
	result := &ExtractionResult{}

	for _, ent := range entities {
		if ent.Name == "" || ent.EntityType == "" {
			continue
		}
		if err := e.agent.EntityAdd(ent.Name, ent.EntityType, namespace, ent.Properties); err != nil {
			e.logger.Debug("store entity failed", "name", ent.Name, "error", err)
			continue
		}
		result.EntitiesAdded++
	}

	// For relations, we need entity IDs — look them up by name.
	// Since EntityAdd may deduplicate, we search by name to find the ID.
	for _, rel := range relations {
		if rel.Source == "" || rel.Target == "" || rel.RelationType == "" {
			continue
		}

		sourceEntities, err := e.agent.EntityFind(rel.Source, "", 1)
		if err != nil || len(sourceEntities) == 0 {
			continue
		}
		targetEntities, err := e.agent.EntityFind(rel.Target, "", 1)
		if err != nil || len(targetEntities) == 0 {
			continue
		}

		if err := e.agent.RelationAdd(sourceEntities[0].ID, targetEntities[0].ID, rel.RelationType); err != nil {
			e.logger.Debug("store relation failed", "source", rel.Source, "target", rel.Target, "error", err)
			continue
		}
		result.RelationsAdded++
	}

	return result
}

// ExtractRecent processes recent context entries for entity extraction.
// It first reads from working memory, then supplements with context stream
// entries when working memory is sparse (common at session start).
func (e *Extractor) ExtractRecent(ctx context.Context) (*ExtractionResult, error) {
	batchSize := e.config.ExtractorBatchSize

	items, err := e.agent.MemoryRecall("working", "", batchSize)
	if err != nil {
		return nil, fmt.Errorf("recall working memory: %w", err)
	}

	entries := memoryToContextEntries(items)

	// Supplement with context stream entries when working memory is sparse.
	if len(entries) < batchSize {
		remaining := batchSize - len(entries)
		streamEntries, err := e.agent.ContextStream(time.Time{}, remaining)
		if err != nil {
			e.logger.Debug("extractor: context stream fallback failed", "error", err)
		} else {
			// Deduplicate by ID: working memory entries take priority.
			seen := make(map[string]bool, len(entries))
			for _, entry := range entries {
				seen[entry.Entry.ID] = true
			}
			for _, se := range streamEntries {
				if !seen[se.Entry.ID] {
					entries = append(entries, se)
				}
			}
		}
	}

	if len(entries) == 0 {
		return nil, nil
	}

	entities, relations, err := e.ExtractFromEntries(ctx, entries)
	if err != nil {
		return nil, err
	}
	if len(entities) == 0 && len(relations) == 0 {
		return nil, nil
	}

	result := e.StoreExtractions(ctx, entities, relations, "")
	return result, nil
}
