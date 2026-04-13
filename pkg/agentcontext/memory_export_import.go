package agentcontext

import (
	"context"
	"fmt"
	"time"
)

// MemoryImporter handles importing memories from universal format
type MemoryImporter struct {
	hierarchy *MemoryHierarchy
	graph     *KnowledgeGraph
	workflows *WorkflowEngine
	embedFunc func(ctx context.Context, texts []string) ([][]float32, error)
}

// NewMemoryImporter creates a new memory importer
func NewMemoryImporter(hierarchy *MemoryHierarchy, graph *KnowledgeGraph, workflows *WorkflowEngine) *MemoryImporter {
	return &MemoryImporter{
		hierarchy: hierarchy,
		graph:     graph,
		workflows: workflows,
	}
}

// SetEmbedFunc sets the embedding function for re-embedding on import
func (mi *MemoryImporter) SetEmbedFunc(fn func(ctx context.Context, texts []string) ([][]float32, error)) {
	mi.embedFunc = fn
}

// ImportOptions configures import behavior
type ImportOptions struct {
	// What to import
	ImportMemories  bool `json:"import_memories"`
	ImportGraph     bool `json:"import_graph"`
	ImportWorkflows bool `json:"import_workflows"`

	// Conflict handling
	ConflictStrategy string `json:"conflict_strategy"` // "skip", "overwrite", "merge"

	// ID prefix to avoid collisions
	IDPrefix string `json:"id_prefix,omitempty"`

	// Target tier for imported memories
	TargetTier string `json:"target_tier,omitempty"`

	// Namespace override
	TargetNamespace string `json:"target_namespace,omitempty"`

	// Regenerate embeddings on import
	RegenerateEmbeddings bool `json:"regenerate_embeddings,omitempty"`
}

// DefaultImportOptions returns default import options
func DefaultImportOptions() ImportOptions {
	return ImportOptions{
		ImportMemories:   true,
		ImportGraph:      true,
		ImportWorkflows:  false,
		ConflictStrategy: "skip",
	}
}

// ImportResult contains import statistics
type ImportResult struct {
	MemoriesImported  int      `json:"memories_imported"`
	MemoriesSkipped   int      `json:"memories_skipped"`
	EntitiesImported  int      `json:"entities_imported"`
	EntitiesSkipped   int      `json:"entities_skipped"`
	RelationsImported int      `json:"relations_imported"`
	RelationsSkipped  int      `json:"relations_skipped"`
	WorkflowsImported int      `json:"workflows_imported"`
	WorkflowsSkipped  int      `json:"workflows_skipped"`
	Errors            []string `json:"errors,omitempty"`
}

// Import imports memories from universal format
func (mi *MemoryImporter) Import(data *UniversalMemoryFormat, opts ImportOptions) (*ImportResult, error) {
	result := &ImportResult{
		Errors: []string{},
	}

	// Import memories
	if opts.ImportMemories && mi.hierarchy != nil {
		mi.importMemories(data.Memories, opts, result)
	}

	// Import graph
	if opts.ImportGraph && mi.graph != nil {
		mi.importGraph(data.Entities, data.Relations, opts, result)
	}

	// Import workflows
	if opts.ImportWorkflows && mi.workflows != nil {
		mi.importWorkflows(data.Workflows, opts, result)
	}

	return result, nil
}

// importMemories imports memory items
func (mi *MemoryImporter) importMemories(memories []UniversalMemory, opts ImportOptions, result *ImportResult) {
	for _, mem := range memories {
		id := mem.ID
		if opts.IDPrefix != "" {
			id = opts.IDPrefix + "_" + id
		}

		// Check for existing
		existing, _ := mi.hierarchy.GetItem(id)
		if existing != nil {
			switch opts.ConflictStrategy {
			case "skip":
				result.MemoriesSkipped++
				continue
			case "merge":
				// Same content → merge metadata + bump access count.
				// Different content → keep higher-tier version.
				existingHash := simpleContentHash(existing.Content)
				importHash := simpleContentHash(mem.Content)
				if existingHash == importHash {
					if existing.Metadata == nil {
						existing.Metadata = make(map[string]any)
					}
					for k, v := range mem.Metadata {
						existing.Metadata[k] = v
					}
					existing.AccessCount++
					if err := mi.hierarchy.UpdateItem(existing); err != nil {
						result.Errors = append(result.Errors, fmt.Sprintf("memory merge %s: %v", id, err))
					}
				} else {
					importTier := MemoryTier(mem.Tier)
					if tierRank(existing.Tier) >= tierRank(importTier) {
						// Existing is higher or equal tier — keep it, merge metadata only.
						if existing.Metadata == nil {
							existing.Metadata = make(map[string]any)
						}
						for k, v := range mem.Metadata {
							existing.Metadata[k] = v
						}
						if err := mi.hierarchy.UpdateItem(existing); err != nil {
							result.Errors = append(result.Errors, fmt.Sprintf("memory merge %s: %v", id, err))
						}
					} else {
						// Imported version is higher tier — overwrite.
						if err := mi.hierarchy.DeleteItem(id); err != nil {
							result.Errors = append(result.Errors, fmt.Sprintf("memory merge-overwrite %s: %v", id, err))
						}
						// Fall through to create new item below.
						goto createItem
					}
				}
				result.MemoriesImported++
				continue
			case "overwrite":
				if err := mi.hierarchy.DeleteItem(id); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("memory overwrite-delete %s: %v", id, err))
				}
			}
		}
	createItem:

		// Create new item
		tier := MemoryTier(mem.Tier)
		if opts.TargetTier != "" {
			tier = MemoryTier(opts.TargetTier)
		}
		if tier == "" {
			tier = MemoryTierShortTerm
		}

		item := &MemoryItem{
			ID:              id,
			Content:         mem.Content,
			Title:           mem.ID, // Use ID as title if not provided
			Category:        reverseMapMemoryTypeToCategory(mem.Type),
			Tier:            tier,
			ImportanceScore: mem.Importance,
			Tags:            mem.Tags,
			Metadata:        mem.Metadata,
			Embedding:       float32SliceFromFloat64(mem.Embedding),
			CreatedAt:       mem.CreatedAt,
			LastAccessedAt:  mem.AccessedAt,
			OriginalTokens:  estimateTokenCount(mem.Content),
		}

		if item.Metadata == nil {
			item.Metadata = make(map[string]any)
		}
		item.Metadata["imported_from"] = "universal_format"
		item.Metadata["import_time"] = time.Now().Format(time.RFC3339)

		if err := mi.hierarchy.AddItem(item); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("memory %s: %v", id, err))
		} else {
			result.MemoriesImported++
		}
	}
}

// importGraph imports entities and relations
func (mi *MemoryImporter) importGraph(entities []UniversalEntity, relations []UniversalRelation, opts ImportOptions, result *ImportResult) {
	// Import entities first
	idMapping := make(map[string]string) // old ID -> new ID

	for _, ue := range entities {
		id := ue.ID
		if opts.IDPrefix != "" {
			id = opts.IDPrefix + "_" + id
		}
		idMapping[ue.ID] = id

		// Check for existing
		existing, _ := mi.graph.GetEntity(id)
		if existing != nil {
			switch opts.ConflictStrategy {
			case "skip":
				result.EntitiesSkipped++
				continue
			case "merge":
				// Merge properties
				for k, v := range ue.Properties {
					existing.Properties[k] = v
				}
				result.EntitiesImported++
				continue
			case "overwrite":
				if err := mi.graph.DeleteEntity(id); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("entity overwrite-delete %s: %v", id, err))
				}
			}
		}

		namespace := ue.Properties["namespace"]
		if ns, ok := namespace.(string); ok && ns != "" {
			// Keep original namespace
			_ = ns // suppress unused warning
		} else if opts.TargetNamespace != "" {
			namespace = opts.TargetNamespace
			_ = namespace // suppress unused warning
		}

		entity := Entity{
			ID:         id,
			Name:       ue.Name,
			Type:       EntityType(ue.Type),
			Properties: ue.Properties,
			Embedding:  float32SliceFromFloat64(ue.Embedding),
			CreatedAt:  ue.CreatedAt,
		}

		if entity.Properties == nil {
			entity.Properties = make(map[string]any)
		}
		entity.Properties["imported_from"] = "universal_format"

		if err := mi.graph.AddEntity(&entity); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("entity %s: %v", id, err))
		} else {
			result.EntitiesImported++
		}
	}

	// Import relations
	for _, ur := range relations {
		id := ur.ID
		if opts.IDPrefix != "" {
			id = opts.IDPrefix + "_" + id
		}

		// Map source and target IDs
		sourceID := ur.SourceID
		if mapped, ok := idMapping[sourceID]; ok {
			sourceID = mapped
		} else if opts.IDPrefix != "" {
			sourceID = opts.IDPrefix + "_" + sourceID
		}

		targetID := ur.TargetID
		if mapped, ok := idMapping[targetID]; ok {
			targetID = mapped
		} else if opts.IDPrefix != "" {
			targetID = opts.IDPrefix + "_" + targetID
		}

		// Check if source and target exist
		sourceEntity, _ := mi.graph.GetEntity(sourceID)
		targetEntity, _ := mi.graph.GetEntity(targetID)
		if sourceEntity == nil || targetEntity == nil {
			result.RelationsSkipped++
			continue
		}

		// Check for existing
		existing, _ := mi.graph.GetRelation(id)
		if existing != nil {
			switch opts.ConflictStrategy {
			case "skip":
				result.RelationsSkipped++
				continue
			case "overwrite":
				if err := mi.graph.DeleteRelation(id); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("relation overwrite-delete %s: %v", id, err))
				}
			}
		}

		relation := Relation{
			ID:         id,
			SourceID:   sourceID,
			TargetID:   targetID,
			Type:       RelationType(ur.Type),
			Weight:     ur.Weight,
			Properties: ur.Properties,
			CreatedAt:  ur.CreatedAt,
		}

		if relation.Properties == nil {
			relation.Properties = make(map[string]any)
		}
		relation.Properties["imported_from"] = "universal_format"

		if err := mi.graph.AddRelation(&relation); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("relation %s: %v", id, err))
		} else {
			result.RelationsImported++
		}
	}
}

// importWorkflows imports workflow definitions
func (mi *MemoryImporter) importWorkflows(workflows []UniversalWorkflow, opts ImportOptions, result *ImportResult) {
	for _, uw := range workflows {
		// Only import definitions, not instances
		if uw.Type != "definition" {
			result.WorkflowsSkipped++
			continue
		}

		id := uw.ID
		if opts.IDPrefix != "" {
			id = opts.IDPrefix + "_" + id
		}

		// Check for existing
		if _, exists := mi.workflows.definitions[id]; exists {
			switch opts.ConflictStrategy {
			case "skip":
				result.WorkflowsSkipped++
				continue
			case "overwrite":
				delete(mi.workflows.definitions, id)
			}
		}

		// Convert steps
		steps := make([]WorkflowStep, 0, len(uw.Steps))
		for _, us := range uw.Steps {
			stepID := us.ID
			if opts.IDPrefix != "" {
				stepID = opts.IDPrefix + "_" + stepID
			}

			// Map dependencies
			deps := make([]string, 0, len(us.DependsOn))
			for _, dep := range us.DependsOn {
				if opts.IDPrefix != "" {
					deps = append(deps, opts.IDPrefix+"_"+dep)
				} else {
					deps = append(deps, dep)
				}
			}

			steps = append(steps, WorkflowStep{
				ID:          stepID,
				Name:        us.Name,
				Description: us.Description,
				DependsOn:   deps,
			})
		}

		def := WorkflowDefinition{
			ID:          id,
			Name:        uw.Name,
			Description: uw.Description,
			Steps:       steps,
			CreatedAt:   uw.CreatedAt,
		}

		mi.workflows.RegisterDefinition(&def)
		result.WorkflowsImported++
	}
}

// simpleContentHash returns a fast hash for content comparison during import.
func simpleContentHash(s string) uint64 {
	// FNV-1a 64-bit
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// tierRank returns a numeric rank for tier comparison (higher = more persistent).
func tierRank(t MemoryTier) int {
	switch t {
	case MemoryTierWorking:
		return 0
	case MemoryTierShortTerm:
		return 1
	case MemoryTierLongTerm:
		return 2
	default:
		return -1
	}
}

// reverseMapMemoryTypeToCategory maps universal type to internal category
func reverseMapMemoryTypeToCategory(t string) string {
	switch t {
	case "episodic":
		return "observation"
	case "semantic":
		return "knowledge"
	case "procedural":
		return "procedure"
	default:
		return "observation"
	}
}
