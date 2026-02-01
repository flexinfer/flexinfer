package agentcontext

import (
	"encoding/json"
	"fmt"
	"time"
)

// =========================================================================
// Cross-Agent Memory Export/Import (Phase 3.3)
// SuperMemory/Mem0 inspired universal memory format
// =========================================================================

// UniversalMemoryFormat is a cross-platform memory exchange format
// Compatible with Mem0, SuperMemory MCP, and other agent memory systems
type UniversalMemoryFormat struct {
	// Format metadata
	Version    string    `json:"version"`
	Format     string    `json:"format"` // "mem0", "supermemory", "loom"
	ExportedAt time.Time `json:"exported_at"`
	ExportedBy string    `json:"exported_by,omitempty"`

	// Agent/session info
	AgentID     string `json:"agent_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Description string `json:"description,omitempty"`

	// Memory content
	Memories []UniversalMemory `json:"memories"`

	// Optional: Knowledge graph
	Entities  []UniversalEntity   `json:"entities,omitempty"`
	Relations []UniversalRelation `json:"relations,omitempty"`

	// Optional: Workflows
	Workflows []UniversalWorkflow `json:"workflows,omitempty"`

	// Statistics
	Stats ExportStats `json:"stats"`
}

// UniversalMemory represents a single memory item in universal format
type UniversalMemory struct {
	// Core fields (required)
	ID      string `json:"id"`
	Content string `json:"content"`
	Type    string `json:"type"` // "episodic", "semantic", "procedural"

	// Timestamps
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
	AccessedAt time.Time `json:"accessed_at,omitempty"`

	// Importance/Priority
	Importance float64 `json:"importance"`     // 0.0 - 1.0
	Tier       string  `json:"tier,omitempty"` // "working", "short_term", "long_term"

	// Source tracking
	Source   string `json:"source,omitempty"`
	FilePath string `json:"file_path,omitempty"`

	// Tags and categories
	Tags       []string       `json:"tags,omitempty"`
	Categories []string       `json:"categories,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`

	// Embedding (optional, for vector search compatibility)
	Embedding []float64 `json:"embedding,omitempty"`

	// Links to related memories
	RelatedIDs []string `json:"related_ids,omitempty"`
}

// UniversalEntity represents a knowledge graph entity
type UniversalEntity struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties,omitempty"`
	Embedding  []float64      `json:"embedding,omitempty"`
	CreatedAt  time.Time      `json:"created_at,omitempty"`
}

// UniversalRelation represents a knowledge graph relation
type UniversalRelation struct {
	ID         string         `json:"id"`
	SourceID   string         `json:"source_id"`
	TargetID   string         `json:"target_id"`
	Type       string         `json:"type"`
	Weight     float64        `json:"weight,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
	CreatedAt  time.Time      `json:"created_at,omitempty"`
}

// UniversalWorkflow represents a workflow definition or instance
type UniversalWorkflow struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Description string                  `json:"description,omitempty"`
	Type        string                  `json:"type"` // "definition", "instance"
	Status      string                  `json:"status,omitempty"`
	Steps       []UniversalWorkflowStep `json:"steps,omitempty"`
	CreatedAt   time.Time               `json:"created_at,omitempty"`
	UpdatedAt   time.Time               `json:"updated_at,omitempty"`
}

// UniversalWorkflowStep represents a single workflow step
type UniversalWorkflowStep struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Status      string         `json:"status,omitempty"`
	Input       map[string]any `json:"input,omitempty"`
	Output      map[string]any `json:"output,omitempty"`
	DependsOn   []string       `json:"depends_on,omitempty"`
}

// ExportStats contains export statistics
type ExportStats struct {
	MemoryCount   int `json:"memory_count"`
	EntityCount   int `json:"entity_count"`
	RelationCount int `json:"relation_count"`
	WorkflowCount int `json:"workflow_count"`
	TotalTokens   int `json:"total_tokens,omitempty"`
}

// MemoryExporter handles exporting memories to universal format
type MemoryExporter struct {
	hierarchy *MemoryHierarchy
	graph     *KnowledgeGraph
	workflows *WorkflowEngine
}

// NewMemoryExporter creates a new memory exporter
func NewMemoryExporter(hierarchy *MemoryHierarchy, graph *KnowledgeGraph, workflows *WorkflowEngine) *MemoryExporter {
	return &MemoryExporter{
		hierarchy: hierarchy,
		graph:     graph,
		workflows: workflows,
	}
}

// ExportOptions configures what to export
type ExportOptions struct {
	IncludeMemories   bool     `json:"include_memories"`
	IncludeGraph      bool     `json:"include_graph"`
	IncludeWorkflows  bool     `json:"include_workflows"`
	IncludeEmbeddings bool     `json:"include_embeddings"`
	MemoryTiers       []string `json:"memory_tiers,omitempty"` // Filter by tier
	SessionID         string   `json:"session_id,omitempty"`   // Filter by session
	Namespace         string   `json:"namespace,omitempty"`    // Filter by namespace
	Format            string   `json:"format"`                 // "mem0", "supermemory", "loom"
	AgentID           string   `json:"agent_id,omitempty"`
}

// DefaultExportOptions returns default export options
func DefaultExportOptions() ExportOptions {
	return ExportOptions{
		IncludeMemories:   true,
		IncludeGraph:      true,
		IncludeWorkflows:  false,
		IncludeEmbeddings: false,
		Format:            "loom",
	}
}

// Export exports memories to universal format
func (me *MemoryExporter) Export(opts ExportOptions) (*UniversalMemoryFormat, error) {
	result := &UniversalMemoryFormat{
		Version:    "1.0",
		Format:     opts.Format,
		ExportedAt: time.Now(),
		ExportedBy: opts.AgentID,
		AgentID:    opts.AgentID,
		SessionID:  opts.SessionID,
		Namespace:  opts.Namespace,
		Memories:   []UniversalMemory{},
		Entities:   []UniversalEntity{},
		Relations:  []UniversalRelation{},
		Workflows:  []UniversalWorkflow{},
	}

	// Export memories
	if opts.IncludeMemories && me.hierarchy != nil {
		memories := me.exportMemories(opts)
		result.Memories = memories
		result.Stats.MemoryCount = len(memories)
	}

	// Export knowledge graph
	if opts.IncludeGraph && me.graph != nil {
		entities, relations := me.exportGraph(opts)
		result.Entities = entities
		result.Relations = relations
		result.Stats.EntityCount = len(entities)
		result.Stats.RelationCount = len(relations)
	}

	// Export workflows
	if opts.IncludeWorkflows && me.workflows != nil {
		workflows := me.exportWorkflows(opts)
		result.Workflows = workflows
		result.Stats.WorkflowCount = len(workflows)
	}

	return result, nil
}

// exportMemories exports memory items
func (me *MemoryExporter) exportMemories(opts ExportOptions) []UniversalMemory {
	var memories []UniversalMemory

	// Get all items from hierarchy using Recall
	recallReq := MemoryRecallRequest{
		Limit: 10000, // Get a large number of items
	}
	recallResult, err := me.hierarchy.Recall(recallReq)
	if err != nil || len(recallResult.Items) == 0 {
		return memories
	}

	for _, item := range recallResult.Items {
		// Filter by tier if specified
		if len(opts.MemoryTiers) > 0 {
			tierMatch := false
			for _, t := range opts.MemoryTiers {
				if string(item.Tier) == t {
					tierMatch = true
					break
				}
			}
			if !tierMatch {
				continue
			}
		}

		// Filter by session if specified
		if opts.SessionID != "" {
			if itemSession, ok := item.Metadata["session_id"].(string); ok {
				if itemSession != opts.SessionID {
					continue
				}
			}
		}

		memory := UniversalMemory{
			ID:         item.ID,
			Content:    item.Content,
			Type:       mapCategoryToMemoryType(item.Category),
			CreatedAt:  item.CreatedAt,
			AccessedAt: item.LastAccessedAt,
			Importance: item.ImportanceScore,
			Tier:       string(item.Tier),
			Tags:       item.Tags,
			Metadata:   item.Metadata,
		}

		// Include embedding if requested
		if opts.IncludeEmbeddings && len(item.Embedding) > 0 {
			memory.Embedding = float64SliceFromFloat32(item.Embedding)
		}

		memories = append(memories, memory)
	}

	return memories
}

// exportGraph exports knowledge graph
func (me *MemoryExporter) exportGraph(opts ExportOptions) ([]UniversalEntity, []UniversalRelation) {
	me.graph.mu.RLock()
	defer me.graph.mu.RUnlock()

	var entities []UniversalEntity
	var relations []UniversalRelation

	// Export entities
	for _, entity := range me.graph.entities {
		// Filter by namespace if specified
		if opts.Namespace != "" && entity.Namespace != opts.Namespace {
			continue
		}

		ue := UniversalEntity{
			ID:         entity.ID,
			Name:       entity.Name,
			Type:       string(entity.Type),
			Properties: entity.Properties,
			CreatedAt:  entity.CreatedAt,
		}

		if opts.IncludeEmbeddings && len(entity.Embedding) > 0 {
			ue.Embedding = float64SliceFromFloat32(entity.Embedding)
		}

		entities = append(entities, ue)
	}

	// Export relations
	for _, rel := range me.graph.relations {
		ur := UniversalRelation{
			ID:         rel.ID,
			SourceID:   rel.SourceID,
			TargetID:   rel.TargetID,
			Type:       string(rel.Type),
			Weight:     rel.Weight,
			Properties: rel.Properties,
			CreatedAt:  rel.CreatedAt,
		}
		relations = append(relations, ur)
	}

	return entities, relations
}

// exportWorkflows exports workflow definitions and instances
func (me *MemoryExporter) exportWorkflows(opts ExportOptions) []UniversalWorkflow {
	var workflows []UniversalWorkflow

	// Export definitions
	for _, def := range me.workflows.definitions {
		uw := UniversalWorkflow{
			ID:          def.ID,
			Name:        def.Name,
			Description: def.Description,
			Type:        "definition",
			Steps:       make([]UniversalWorkflowStep, 0, len(def.Steps)),
		}

		for _, step := range def.Steps {
			uw.Steps = append(uw.Steps, UniversalWorkflowStep{
				ID:          step.ID,
				Name:        step.Name,
				Description: step.Description,
				DependsOn:   step.DependsOn,
			})
		}

		workflows = append(workflows, uw)
	}

	// Export active instances
	for _, instance := range me.workflows.workflows {
		uw := UniversalWorkflow{
			ID:        instance.ID,
			Name:      instance.Definition.Name,
			Type:      "instance",
			Status:    string(instance.Status),
			CreatedAt: instance.CreatedAt,
			Steps:     make([]UniversalWorkflowStep, 0),
		}
		if instance.StartedAt != nil {
			uw.CreatedAt = *instance.StartedAt
		}
		if instance.CompletedAt != nil {
			uw.UpdatedAt = *instance.CompletedAt
		}

		for stepID, stepState := range instance.StepStates {
			uw.Steps = append(uw.Steps, UniversalWorkflowStep{
				ID:     stepID,
				Name:   stepState.Name,
				Status: string(stepState.Status),
				Output: stepState.Result,
			})
		}

		workflows = append(workflows, uw)
	}

	return workflows
}

// mapCategoryToMemoryType maps internal category to universal memory type
func mapCategoryToMemoryType(category string) string {
	switch category {
	case "decision", "insight", "observation", "event":
		return "episodic"
	case "pattern", "architecture", "concept", "knowledge":
		return "semantic"
	case "procedure", "workflow", "process", "command":
		return "procedural"
	default:
		return "episodic"
	}
}

// MemoryImporter handles importing memories from universal format
type MemoryImporter struct {
	hierarchy *MemoryHierarchy
	graph     *KnowledgeGraph
	workflows *WorkflowEngine
}

// NewMemoryImporter creates a new memory importer
func NewMemoryImporter(hierarchy *MemoryHierarchy, graph *KnowledgeGraph, workflows *WorkflowEngine) *MemoryImporter {
	return &MemoryImporter{
		hierarchy: hierarchy,
		graph:     graph,
		workflows: workflows,
	}
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
				// Merge metadata
				if existing.Metadata == nil {
					existing.Metadata = make(map[string]any)
				}
				for k, v := range mem.Metadata {
					existing.Metadata[k] = v
				}
				_ = mi.hierarchy.UpdateItem(existing)
				result.MemoriesImported++
				continue
			case "overwrite":
				_ = mi.hierarchy.DeleteItem(id)
			}
		}

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
				_ = mi.graph.DeleteEntity(id)
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
				_ = mi.graph.DeleteRelation(id)
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

// ExportToJSON exports to JSON bytes
func (me *MemoryExporter) ExportToJSON(opts ExportOptions) ([]byte, error) {
	data, err := me.Export(opts)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(data, "", "  ")
}

// ImportFromJSON imports from JSON bytes
func (mi *MemoryImporter) ImportFromJSON(jsonData []byte, opts ImportOptions) (*ImportResult, error) {
	var data UniversalMemoryFormat
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	return mi.Import(&data, opts)
}

// Mem0Format converts to Mem0-compatible format
type Mem0Memory struct {
	ID         string         `json:"id"`
	Text       string         `json:"text"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at,omitempty"`
	Categories []string       `json:"categories,omitempty"`
}

// ToMem0Format converts universal format to Mem0 format
func (umf *UniversalMemoryFormat) ToMem0Format() []Mem0Memory {
	result := make([]Mem0Memory, 0, len(umf.Memories))

	for _, mem := range umf.Memories {
		m0 := Mem0Memory{
			ID:         mem.ID,
			Text:       mem.Content,
			Metadata:   mem.Metadata,
			CreatedAt:  mem.CreatedAt.Format(time.RFC3339),
			Categories: mem.Categories,
		}
		if !mem.UpdatedAt.IsZero() {
			m0.UpdatedAt = mem.UpdatedAt.Format(time.RFC3339)
		}
		result = append(result, m0)
	}

	return result
}

// FromMem0Format creates universal format from Mem0 format
func FromMem0Format(memories []Mem0Memory, agentID string) *UniversalMemoryFormat {
	result := &UniversalMemoryFormat{
		Version:    "1.0",
		Format:     "mem0",
		ExportedAt: time.Now(),
		AgentID:    agentID,
		Memories:   make([]UniversalMemory, 0, len(memories)),
	}

	for _, m0 := range memories {
		createdAt, _ := time.Parse(time.RFC3339, m0.CreatedAt)
		updatedAt, _ := time.Parse(time.RFC3339, m0.UpdatedAt)

		mem := UniversalMemory{
			ID:         m0.ID,
			Content:    m0.Text,
			Type:       "episodic",
			CreatedAt:  createdAt,
			UpdatedAt:  updatedAt,
			Categories: m0.Categories,
			Metadata:   m0.Metadata,
			Importance: 0.5,
		}
		result.Memories = append(result.Memories, mem)
	}

	result.Stats.MemoryCount = len(result.Memories)
	return result
}

// float64SliceFromFloat32 converts []float32 to []float64
func float64SliceFromFloat32(f32 []float32) []float64 {
	if f32 == nil {
		return nil
	}
	f64 := make([]float64, len(f32))
	for i, v := range f32 {
		f64[i] = float64(v)
	}
	return f64
}

// float32SliceFromFloat64 converts []float64 to []float32
func float32SliceFromFloat64(f64 []float64) []float32 {
	if f64 == nil {
		return nil
	}
	f32 := make([]float32, len(f64))
	for i, v := range f64 {
		f32[i] = float32(v)
	}
	return f32
}
