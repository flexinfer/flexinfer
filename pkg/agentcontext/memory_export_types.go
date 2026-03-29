package agentcontext

import "time"

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
