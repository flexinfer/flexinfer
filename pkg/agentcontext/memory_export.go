package agentcontext

import (
	"encoding/json"
	"fmt"
	"time"
)

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
	IncludeMemories   bool       `json:"include_memories"`
	IncludeGraph      bool       `json:"include_graph"`
	IncludeWorkflows  bool       `json:"include_workflows"`
	IncludeEmbeddings bool       `json:"include_embeddings"`
	MemoryTiers       []string   `json:"memory_tiers,omitempty"` // Filter by tier
	SessionID         string     `json:"session_id,omitempty"`   // Filter by session
	Namespace         string     `json:"namespace,omitempty"`    // Filter by namespace
	Format            string     `json:"format"`                 // "mem0", "supermemory", "loom"
	AgentID           string     `json:"agent_id,omitempty"`
	Tags              []string   `json:"tags,omitempty"`       // Filter by tags
	TimeStart         *time.Time `json:"time_start,omitempty"` // Filter by time range
	TimeEnd           *time.Time `json:"time_end,omitempty"`   // Filter by time range
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

		// Filter by tags if specified
		if len(opts.Tags) > 0 {
			tagMatch := false
			for _, filterTag := range opts.Tags {
				for _, itemTag := range item.Tags {
					if filterTag == itemTag {
						tagMatch = true
						break
					}
				}
				if tagMatch {
					break
				}
			}
			if !tagMatch {
				continue
			}
		}

		// Filter by time range if specified
		if opts.TimeStart != nil && item.CreatedAt.Before(*opts.TimeStart) {
			continue
		}
		if opts.TimeEnd != nil && item.CreatedAt.After(*opts.TimeEnd) {
			continue
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

// Mem0Memory represents Mem0-compatible format
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
