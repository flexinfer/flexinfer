package agentcontext

import (
	"time"
)

// =========================================================================
// Knowledge Graph Types
// =========================================================================

// EntityType defines the type of entity in the knowledge graph
type EntityType string

const (
	EntityTypeFile     EntityType = "file"
	EntityTypeFunction EntityType = "function"
	EntityTypeClass    EntityType = "class"
	EntityTypeModule   EntityType = "module"
	EntityTypeVariable EntityType = "variable"
	EntityTypeConcept  EntityType = "concept"
	EntityTypeDecision EntityType = "decision"
	EntityTypeIssue    EntityType = "issue"
	EntityTypePR       EntityType = "pr"
	EntityTypeCommit   EntityType = "commit"
	EntityTypeAgent    EntityType = "agent"
	EntityTypeSession  EntityType = "session"
	EntityTypeTask     EntityType = "task"
	EntityTypeError    EntityType = "error"
	EntityTypeService  EntityType = "service"
	EntityTypeAPI      EntityType = "api"
	EntityTypeDatabase EntityType = "database"
	EntityTypeConfig   EntityType = "config"
)

// RelationType defines the type of relationship between entities
type RelationType string

const (
	// Code relationships
	RelationDependsOn  RelationType = "depends_on"
	RelationImplements RelationType = "implements"
	RelationExtends    RelationType = "extends"
	RelationCalls      RelationType = "calls"
	RelationImports    RelationType = "imports"
	RelationDefines    RelationType = "defines"
	RelationContains   RelationType = "contains"
	RelationReferences RelationType = "references"
	RelationOverrides  RelationType = "overrides"

	// Causal relationships
	RelationCaused    RelationType = "caused"
	RelationResolved  RelationType = "resolved"
	RelationBlockedBy RelationType = "blocked_by"
	RelationTriggered RelationType = "triggered"

	// Agent relationships
	RelationCreatedBy    RelationType = "created_by"
	RelationModifiedBy   RelationType = "modified_by"
	RelationDiscoveredBy RelationType = "discovered_by"
	RelationAssignedTo   RelationType = "assigned_to"

	// Semantic relationships
	RelationRelatedTo  RelationType = "related_to"
	RelationSimilarTo  RelationType = "similar_to"
	RelationOppositeOf RelationType = "opposite_of"
	RelationPartOf     RelationType = "part_of"
	RelationVersionOf  RelationType = "version_of"

	// Temporal relationships
	RelationPrecedes     RelationType = "precedes"
	RelationFollows      RelationType = "follows"
	RelationOccurredWith RelationType = "occurred_with"
)

// Entity represents a node in the knowledge graph
type Entity struct {
	ID          string     `json:"id"`
	Type        EntityType `json:"type"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Namespace   string     `json:"namespace,omitempty"`

	// Properties for different entity types
	Properties map[string]any `json:"properties,omitempty"`

	// Code-specific fields
	FilePath  string `json:"file_path,omitempty"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
	Language  string `json:"language,omitempty"`
	Signature string `json:"signature,omitempty"`

	// Provenance
	SessionID string    `json:"session_id,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// For embeddings/similarity
	Embedding []float32 `json:"embedding,omitempty"`

	// Tags for filtering
	Tags []string `json:"tags,omitempty"`
}

// Relation represents an edge in the knowledge graph
type Relation struct {
	ID            string       `json:"id"`
	Type          RelationType `json:"type"`
	SourceID      string       `json:"source_id"`
	TargetID      string       `json:"target_id"`
	Weight        float64      `json:"weight,omitempty"` // Strength/confidence of relationship
	Bidirectional bool         `json:"bidirectional,omitempty"`

	// Properties
	Properties map[string]any `json:"properties,omitempty"`

	// Evidence/reasoning
	Evidence  string `json:"evidence,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`

	// Provenance
	SessionID string    `json:"session_id,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ReasoningStep represents a step in a reasoning chain
type ReasoningStep struct {
	StepNumber  int      `json:"step_number"`
	Description string   `json:"description"`
	EntityIDs   []string `json:"entity_ids,omitempty"`
	RelationIDs []string `json:"relation_ids,omitempty"`
	Conclusion  string   `json:"conclusion,omitempty"`
	Confidence  float64  `json:"confidence,omitempty"`
}

// ReasoningChain represents a chain of reasoning over the knowledge graph
type ReasoningChain struct {
	ID         string          `json:"id"`
	Query      string          `json:"query"`
	Steps      []ReasoningStep `json:"steps"`
	Conclusion string          `json:"conclusion"`
	Confidence float64         `json:"confidence"`
	SessionID  string          `json:"session_id,omitempty"`
	AgentID    string          `json:"agent_id,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// GraphQuery represents a query against the knowledge graph
type GraphQuery struct {
	// Pattern matching (simplified Cypher-like)
	Pattern string `json:"pattern,omitempty"`

	// Structured query
	SourceTypes   []EntityType   `json:"source_types,omitempty"`
	TargetTypes   []EntityType   `json:"target_types,omitempty"`
	RelationTypes []RelationType `json:"relation_types,omitempty"`

	// Filters
	EntityID  string `json:"entity_id,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`

	// Traversal options
	MaxDepth      int  `json:"max_depth,omitempty"`
	Bidirectional bool `json:"bidirectional,omitempty"`

	// Result options
	Limit             int  `json:"limit,omitempty"`
	IncludeProperties bool `json:"include_properties,omitempty"`
}

// GraphQueryResult contains the result of a graph query
type GraphQueryResult struct {
	Entities  []Entity    `json:"entities"`
	Relations []Relation  `json:"relations"`
	Paths     []GraphPath `json:"paths,omitempty"`
}

// GraphPath represents a path through the graph
type GraphPath struct {
	Nodes  []string `json:"nodes"` // Entity IDs
	Edges  []string `json:"edges"` // Relation IDs
	Length int      `json:"length"`
}

// GraphStats contains statistics about the knowledge graph
type GraphStats struct {
	TotalEntities   int            `json:"total_entities"`
	TotalRelations  int            `json:"total_relations"`
	EntitiesByType  map[string]int `json:"entities_by_type"`
	RelationsByType map[string]int `json:"relations_by_type"`
	Namespaces      []string       `json:"namespaces"`
}
