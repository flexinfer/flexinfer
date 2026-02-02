package agentcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

const SchemaVersion = "v1"

// EntryType defines the type of context entry
type EntryType string

const (
	EntryTypeFileRead    EntryType = "file_read"
	EntryTypeDecision    EntryType = "decision"
	EntryTypeFinding     EntryType = "finding"
	EntryTypeQuestion    EntryType = "question"
	EntryTypeSummary     EntryType = "summary"
	EntryTypeCodeContext EntryType = "code_context"
	EntryTypeNote        EntryType = "note"
	EntryTypeError       EntryType = "error"
	EntryTypeTask        EntryType = "task"
	EntryTypeHandoff     EntryType = "handoff"
	EntryTypeAnnotation  EntryType = "annotation"
)

// TaskStatus defines the status of a task
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusBlocked    TaskStatus = "blocked"
)

// TaskPriority defines task priority levels
type TaskPriority string

const (
	TaskPriorityLow      TaskPriority = "low"
	TaskPriorityMedium   TaskPriority = "medium"
	TaskPriorityHigh     TaskPriority = "high"
	TaskPriorityCritical TaskPriority = "critical"
)

// AnnotationType defines code annotation types
type AnnotationType string

const (
	AnnotationTypeTodo      AnnotationType = "todo"
	AnnotationTypeFixme     AnnotationType = "fixme"
	AnnotationTypeNote      AnnotationType = "note"
	AnnotationTypeQuestion  AnnotationType = "question"
	AnnotationTypeImportant AnnotationType = "important"
	AnnotationTypeBug       AnnotationType = "bug"
	AnnotationTypePerf      AnnotationType = "perf"
)

// HandoffType defines handoff package types
type HandoffType string

const (
	HandoffTypeFull        HandoffType = "full"
	HandoffTypeSelective   HandoffType = "selective"
	HandoffTypeSummaryOnly HandoffType = "summary_only"
)

// HandoffStatus defines handoff states
type HandoffStatus string

const (
	HandoffStatusPending  HandoffStatus = "pending"
	HandoffStatusAccepted HandoffStatus = "accepted"
	HandoffStatusExpired  HandoffStatus = "expired"
)

// Visibility defines who can access a context entry
type Visibility string

const (
	VisibilityPrivate Visibility = "private"
	VisibilityShared  Visibility = "shared"
	VisibilityPublic  Visibility = "public"
)

// SessionStatus defines the status of a session
type SessionStatus string

const (
	SessionStatusActive     SessionStatus = "active"
	SessionStatusEnded      SessionStatus = "ended"
	SessionStatusSummarized SessionStatus = "summarized"
)

// SourceVersion tracks version information for source-grounded entries (btca-inspired)
type SourceVersion struct {
	CommitHash string    `json:"commit_hash,omitempty"` // Git commit when content was indexed
	FileMtime  time.Time `json:"file_mtime,omitempty"`  // File modification time when indexed
	IndexedAt  time.Time `json:"indexed_at"`            // When this entry was created/indexed
	IsStale    bool      `json:"is_stale,omitempty"`    // True if source has changed since indexing
}

// ContextEntry represents a single piece of agent context
type ContextEntry struct {
	ID            string `json:"id"`
	SchemaVersion string `json:"schema_version"`

	// Namespacing
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	Namespace string `json:"namespace,omitempty"`

	// Entry metadata
	EntryType EntryType `json:"entry_type"`
	Timestamp time.Time `json:"timestamp"`

	// Content
	Title       string `json:"title"`
	Content     string `json:"content"`
	ContentHash string `json:"content_hash"`

	// Optional structured metadata (JSON-encoded)
	Metadata map[string]any `json:"metadata,omitempty"`

	// File context (for file_read entries)
	FilePath  string `json:"file_path,omitempty"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`

	// Source versioning (Phase 2.1 - btca-inspired)
	SourceVersion *SourceVersion `json:"source_version,omitempty"`

	// Relationships
	ParentID   string   `json:"parent_id,omitempty"`
	RelatedIDs []string `json:"related_ids,omitempty"`
	Tags       []string `json:"tags,omitempty"`

	// Token tracking
	TokenCount int `json:"token_count"`

	// Cross-agent sharing
	Visibility Visibility `json:"visibility"`
	SharedWith []string   `json:"shared_with,omitempty"`
}

// Session represents an agent session
type Session struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	Namespace string `json:"namespace,omitempty"`

	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Status    string     `json:"status"`

	// Session metadata
	Description string `json:"description,omitempty"`
	WorkingDir  string `json:"working_dir,omitempty"`

	// Statistics
	EntryCount  int `json:"entry_count"`
	TotalTokens int `json:"total_tokens"`

	// For auto-summarization
	LastSummaryAt *time.Time `json:"last_summary_at,omitempty"`
}

// SessionSummary is a compressed representation of session context
type SessionSummary struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`

	CreatedAt time.Time `json:"created_at"`

	// Summary content
	Summary      string   `json:"summary"`
	KeyFindings  []string `json:"key_findings,omitempty"`
	KeyDecisions []string `json:"key_decisions,omitempty"`
	FilesTouched []string `json:"files_touched,omitempty"`

	// Coverage
	EntryIDs  []string  `json:"entry_ids,omitempty"`
	TimeStart time.Time `json:"time_start"`
	TimeEnd   time.Time `json:"time_end"`

	TokenCount int `json:"token_count"`
}

// SearchResult for search results
type SearchResult struct {
	Score float64      `json:"score"`
	Entry ContextEntry `json:"entry"`
}

// RecallOptions for token-efficient retrieval
type RecallOptions struct {
	Query            string
	AgentID          string
	SessionID        string
	Namespace        string
	TokenBudget      int
	IncludeSummaries bool
	IncludeDecisions bool
	FileContext      string
}

// CrossAgentQuery for querying other agents' context
type CrossAgentQuery struct {
	Query           string      `json:"query"`
	RequestingAgent string      `json:"requesting_agent"`
	TargetAgentID   string      `json:"target_agent_id,omitempty"`
	EntryTypes      []EntryType `json:"entry_types,omitempty"`
	TimeStart       *time.Time  `json:"time_start,omitempty"`
	TimeEnd         *time.Time  `json:"time_end,omitempty"`
	Limit           int         `json:"limit"`
}

// ContextStats for statistics
type ContextStats struct {
	AgentID     string         `json:"agent_id,omitempty"`
	SessionID   string         `json:"session_id,omitempty"`
	Namespace   string         `json:"namespace,omitempty"`
	EntryCount  int            `json:"entry_count"`
	TotalTokens int            `json:"total_tokens"`
	ByType      map[string]int `json:"by_type,omitempty"`
}

// GenerateID creates a unique ID for a context entry
func GenerateID(agentID, sessionID, content string, timestamp time.Time) string {
	input := agentID + ":" + sessionID + ":" + content + ":" + timestamp.Format(time.RFC3339Nano)
	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:])[:16]
}

// ContentHashFunc generates a hash of the content for deduplication
func ContentHashFunc(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])[:16]
}

// EstimateTokens provides a rough token count estimate (4 chars per token)
func EstimateTokens(text string) int {
	return (len(text) + 3) / 4
}

// Task represents a task/todo discovered during agent sessions
type Task struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	Namespace string `json:"namespace,omitempty"`

	// Task details
	Title      string       `json:"title"`
	Context    string       `json:"context,omitempty"`
	Priority   TaskPriority `json:"priority"`
	Status     TaskStatus   `json:"status"`
	Resolution string       `json:"resolution,omitempty"`

	// Code context
	FilePath   string `json:"file_path,omitempty"`
	LineNumber int    `json:"line_number,omitempty"`
	Symbol     string `json:"symbol,omitempty"`

	// Relationships
	Tags      []string `json:"tags,omitempty"`
	BlockedBy []string `json:"blocked_by,omitempty"`
	ParentID  string   `json:"parent_id,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	TokenCount int `json:"token_count"`
}

// CodeAnnotation represents an annotation attached to code
type CodeAnnotation struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	Namespace string `json:"namespace,omitempty"`

	// Code location
	FilePath  string `json:"file_path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
	RepoID    string `json:"repo_id,omitempty"`

	// Annotation
	AnnotationType AnnotationType `json:"annotation_type"`
	Content        string         `json:"content"`

	// Timestamps
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	TokenCount int `json:"token_count"`
}

// Handoff represents a context handoff between agents
type Handoff struct {
	ID            string `json:"id"`
	SourceAgentID string `json:"source_agent_id"`
	SourceSession string `json:"source_session"`
	TargetAgentID string `json:"target_agent_id"`

	// Handoff configuration
	HandoffType  HandoffType   `json:"handoff_type"`
	Status       HandoffStatus `json:"status"`
	Instructions string        `json:"instructions,omitempty"`

	// Content
	Summary    string   `json:"summary,omitempty"`
	EntryIDs   []string `json:"entry_ids,omitempty"`
	TokenCount int      `json:"token_count"`

	// Timestamps
	CreatedAt  time.Time  `json:"created_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// SessionTemplate is a reusable template for starting sessions
type SessionTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	CreatedBy   string `json:"created_by"`

	// Template content
	EntryTypesToInclude []EntryType    `json:"entry_types_to_include,omitempty"`
	InitialEntries      []ContextEntry `json:"initial_entries,omitempty"`

	// Timestamps
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EnhancedRecallOptions extends RecallOptions with new capabilities
type EnhancedRecallOptions struct {
	RecallOptions

	// New options
	SymbolContext string  `json:"symbol_context,omitempty"`
	RecencyWeight float64 `json:"recency_weight,omitempty"` // 0.0-1.0, default 0.2
	IncludeTasks  bool    `json:"include_tasks"`
}

// =========================================================================
// Workflow Orchestration Types
// =========================================================================

// WorkflowStatus defines the status of a workflow
type WorkflowStatus string

const (
	WorkflowStatusPending    WorkflowStatus = "pending"
	WorkflowStatusRunning    WorkflowStatus = "running"
	WorkflowStatusPaused     WorkflowStatus = "paused"
	WorkflowStatusWaiting    WorkflowStatus = "waiting_approval"
	WorkflowStatusCompleted  WorkflowStatus = "completed"
	WorkflowStatusFailed     WorkflowStatus = "failed"
	WorkflowStatusCancelled  WorkflowStatus = "cancelled"
	WorkflowStatusRolledBack WorkflowStatus = "rolled_back"
)

// StepStatus defines the status of a workflow step
type StepStatus string

const (
	StepStatusPending   StepStatus = "pending"
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
	StepStatusSkipped   StepStatus = "skipped"
	StepStatusWaiting   StepStatus = "waiting_approval"
)

// ApprovalStatus defines approval states
type ApprovalStatus string

const (
	ApprovalStatusPending  ApprovalStatus = "pending"
	ApprovalStatusApproved ApprovalStatus = "approved"
	ApprovalStatusRejected ApprovalStatus = "rejected"
)

// StepType defines what kind of step this is
type StepType string

const (
	StepTypeTool     StepType = "tool"     // Execute an MCP tool
	StepTypeApproval StepType = "approval" // Wait for human approval
	StepTypeGate     StepType = "gate"     // Conditional gate
	StepTypeParallel StepType = "parallel" // Execute steps in parallel
	StepTypeSubflow  StepType = "subflow"  // Execute a nested workflow
)

// WorkflowStep represents a single step in a workflow
type WorkflowStep struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	StepType    StepType `json:"step_type"`

	// For tool steps
	ToolName   string         `json:"tool_name,omitempty"`
	ToolArgs   map[string]any `json:"tool_args,omitempty"`
	ServerName string         `json:"server_name,omitempty"` // MCP server to use

	// DAG relationships
	DependsOn []string `json:"depends_on,omitempty"` // Step IDs this depends on

	// Approval gate settings
	RequiresApproval bool   `json:"requires_approval,omitempty"`
	ApprovalMessage  string `json:"approval_message,omitempty"`

	// Conditional execution
	Condition string `json:"condition,omitempty"` // JSONPath or simple expression

	// Parallel steps (for StepTypeParallel)
	ParallelSteps []WorkflowStep `json:"parallel_steps,omitempty"`

	// Subflow (for StepTypeSubflow)
	SubflowID string `json:"subflow_id,omitempty"`

	// Retry settings
	MaxRetries int `json:"max_retries,omitempty"`
	RetryDelay int `json:"retry_delay_ms,omitempty"`

	// Timeout in seconds
	Timeout int `json:"timeout_seconds,omitempty"`

	// Rollback step to execute on failure
	RollbackStepID string `json:"rollback_step_id,omitempty"`

	// Runtime state
	Status       StepStatus     `json:"status"`
	StartedAt    *time.Time     `json:"started_at,omitempty"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
	RetryCount   int            `json:"retry_count"`
	Error        string         `json:"error,omitempty"`
	Result       map[string]any `json:"result,omitempty"`
	ApprovalInfo *ApprovalInfo  `json:"approval_info,omitempty"`
}

// ApprovalInfo tracks approval state for a step
type ApprovalInfo struct {
	Status      ApprovalStatus `json:"status"`
	RequestedAt time.Time      `json:"requested_at"`
	DecidedAt   *time.Time     `json:"decided_at,omitempty"`
	DecidedBy   string         `json:"decided_by,omitempty"`
	Comment     string         `json:"comment,omitempty"`
}

// WorkflowDefinition is the template for a workflow
type WorkflowDefinition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	CreatedBy   string `json:"created_by"`

	// Steps in execution order (respecting DAG)
	Steps []WorkflowStep `json:"steps"`

	// Input schema (JSON Schema)
	InputSchema map[string]any `json:"input_schema,omitempty"`

	// Global timeout
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	// Rollback behavior
	RollbackOnFailure bool `json:"rollback_on_failure"`

	// Timestamps
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Workflow is a running instance of a WorkflowDefinition
type Workflow struct {
	ID           string `json:"id"`
	DefinitionID string `json:"definition_id"`
	SessionID    string `json:"session_id"`
	AgentID      string `json:"agent_id"`
	Namespace    string `json:"namespace,omitempty"`

	// Definition snapshot (for immutability)
	Definition WorkflowDefinition `json:"definition"`

	// Execution state
	Status      WorkflowStatus           `json:"status"`
	CurrentStep string                   `json:"current_step,omitempty"` // Current step ID
	StepStates  map[string]*WorkflowStep `json:"step_states"`

	// Input/Output
	Input  map[string]any `json:"input,omitempty"`
	Output map[string]any `json:"output,omitempty"`

	// Execution context passed between steps
	Context map[string]any `json:"context,omitempty"`

	// Error tracking
	Error        string `json:"error,omitempty"`
	FailedStepID string `json:"failed_step_id,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// Metrics
	TotalSteps     int `json:"total_steps"`
	CompletedSteps int `json:"completed_steps"`
	FailedSteps    int `json:"failed_steps"`
}

// clone returns a deep copy of the workflow.
// Used to provide thread-safe snapshots from GetWorkflow.
func (w *Workflow) clone() *Workflow {
	if w == nil {
		return nil
	}
	cp := *w // shallow copy

	// Deep copy time pointers
	if w.StartedAt != nil {
		t := *w.StartedAt
		cp.StartedAt = &t
	}
	if w.CompletedAt != nil {
		t := *w.CompletedAt
		cp.CompletedAt = &t
	}

	// Deep copy maps
	if w.StepStates != nil {
		cp.StepStates = make(map[string]*WorkflowStep, len(w.StepStates))
		for k, v := range w.StepStates {
			if v != nil {
				stepCopy := *v
				// Deep copy nested pointers in WorkflowStep
				if v.StartedAt != nil {
					t := *v.StartedAt
					stepCopy.StartedAt = &t
				}
				if v.CompletedAt != nil {
					t := *v.CompletedAt
					stepCopy.CompletedAt = &t
				}
				if v.ToolArgs != nil {
					stepCopy.ToolArgs = copyMap(v.ToolArgs)
				}
				if v.Result != nil {
					stepCopy.Result = copyMap(v.Result)
				}
				if v.ApprovalInfo != nil {
					ai := *v.ApprovalInfo
					if v.ApprovalInfo.DecidedAt != nil {
						t := *v.ApprovalInfo.DecidedAt
						ai.DecidedAt = &t
					}
					stepCopy.ApprovalInfo = &ai
				}
				cp.StepStates[k] = &stepCopy
			}
		}
	}
	if w.Input != nil {
		cp.Input = copyMap(w.Input)
	}
	if w.Output != nil {
		cp.Output = copyMap(w.Output)
	}
	if w.Context != nil {
		cp.Context = copyMap(w.Context)
	}

	return &cp
}

// copyMap creates a shallow copy of a map[string]any.
func copyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// WorkflowEvent represents an event in workflow execution
type WorkflowEvent struct {
	ID         string         `json:"id"`
	WorkflowID string         `json:"workflow_id"`
	StepID     string         `json:"step_id,omitempty"`
	EventType  string         `json:"event_type"` // started, completed, failed, approval_requested, approved, rejected, rolled_back
	Timestamp  time.Time      `json:"timestamp"`
	Details    map[string]any `json:"details,omitempty"`
}

// WorkflowSummary is a compact view of a workflow
type WorkflowSummary struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Status      WorkflowStatus `json:"status"`
	Progress    float64        `json:"progress"` // 0.0-1.0
	CurrentStep string         `json:"current_step,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Error       string         `json:"error,omitempty"`
}

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

// =========================================================================
// Memory Hierarchy Types
// =========================================================================

// MemoryTier defines the tier of memory storage
type MemoryTier string

const (
	// Working memory: Immediate context, always available, most detailed
	MemoryTierWorking MemoryTier = "working"
	// Short-term memory: Recent sessions, summarized, expires after days
	MemoryTierShortTerm MemoryTier = "short_term"
	// Long-term memory: Important decisions and learnings, highly compressed, persists indefinitely
	MemoryTierLongTerm MemoryTier = "long_term"
)

// MemoryItemStatus defines the status of a memory item
type MemoryItemStatus string

const (
	MemoryItemStatusActive     MemoryItemStatus = "active"
	MemoryItemStatusCompressed MemoryItemStatus = "compressed"
	MemoryItemStatusArchived   MemoryItemStatus = "archived"
	MemoryItemStatusExpired    MemoryItemStatus = "expired"
)

// ImportanceLevel defines how important a memory item is
type ImportanceLevel string

const (
	ImportanceLevelLow      ImportanceLevel = "low"
	ImportanceLevelMedium   ImportanceLevel = "medium"
	ImportanceLevelHigh     ImportanceLevel = "high"
	ImportanceLevelCritical ImportanceLevel = "critical"
)

// MemoryItem represents an item in the memory hierarchy
type MemoryItem struct {
	ID              string           `json:"id"`
	Tier            MemoryTier       `json:"tier"`
	Status          MemoryItemStatus `json:"status"`
	Importance      ImportanceLevel  `json:"importance"`
	ImportanceScore float64          `json:"importance_score"` // 0.0-1.0

	// Content
	Title   string `json:"title"`
	Content string `json:"content"`
	Summary string `json:"summary,omitempty"` // Compressed version

	// Original context entry reference
	SourceEntryID string    `json:"source_entry_id,omitempty"`
	SourceType    EntryType `json:"source_type,omitempty"`

	// Categorization
	Category  string   `json:"category,omitempty"` // decision, finding, pattern, error, etc.
	Tags      []string `json:"tags,omitempty"`
	Namespace string   `json:"namespace,omitempty"`

	// Provenance
	SessionID string `json:"session_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`

	// Lifecycle
	CreatedAt      time.Time  `json:"created_at"`
	LastAccessedAt time.Time  `json:"last_accessed_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	CompressedAt   *time.Time `json:"compressed_at,omitempty"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty"`

	// Access tracking
	AccessCount int `json:"access_count"`

	// Token tracking
	OriginalTokens   int `json:"original_tokens"`
	CompressedTokens int `json:"compressed_tokens,omitempty"`

	// Relationships to other memory items
	RelatedIDs []string `json:"related_ids,omitempty"`
	ParentID   string   `json:"parent_id,omitempty"` // For merged items
	ChildIDs   []string `json:"child_ids,omitempty"` // Items merged into this

	// Custom metadata
	Metadata map[string]any `json:"metadata,omitempty"`

	// Embedding for similarity search
	Embedding []float32 `json:"embedding,omitempty"`
}

// RetentionPolicy defines how memory items are retained and compressed
type RetentionPolicy struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Tier-specific settings
	Tier MemoryTier `json:"tier"`

	// TTL settings (in hours)
	DefaultTTL       int `json:"default_ttl_hours,omitempty"`        // 0 = no expiry
	MinImportanceTTL int `json:"min_importance_ttl_hours,omitempty"` // TTL for low importance items

	// Compression settings
	CompressAfterHours int     `json:"compress_after_hours,omitempty"`
	CompressionRatio   float64 `json:"compression_ratio,omitempty"` // Target ratio (e.g., 0.2 = 20% of original)
	MergeThreshold     float64 `json:"merge_threshold,omitempty"`   // Similarity threshold for merging (0.0-1.0)

	// Promotion/demotion settings
	PromotionThreshold   float64 `json:"promotion_threshold,omitempty"`    // Min importance to promote
	DemotionThreshold    float64 `json:"demotion_threshold,omitempty"`     // Max importance to demote
	AccessCountThreshold int     `json:"access_count_threshold,omitempty"` // Min accesses to prevent demotion

	// Capacity limits
	MaxItems  int `json:"max_items,omitempty"`
	MaxTokens int `json:"max_tokens,omitempty"`

	// Deduplication
	DedupeEnabled    bool    `json:"dedupe_enabled"`
	DedupeSimilarity float64 `json:"dedupe_similarity,omitempty"` // Similarity threshold for dedup

	// Categories to include/exclude
	IncludeCategories []string `json:"include_categories,omitempty"`
	ExcludeCategories []string `json:"exclude_categories,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CompressionJob represents a pending or completed compression operation
type CompressionJob struct {
	ID        string     `json:"id"`
	Tier      MemoryTier `json:"tier"`
	Status    string     `json:"status"` // pending, running, completed, failed
	ItemCount int        `json:"item_count"`

	// Results
	OriginalTokens   int `json:"original_tokens"`
	CompressedTokens int `json:"compressed_tokens"`
	MergedCount      int `json:"merged_count"`
	ArchivedCount    int `json:"archived_count"`
	ExpiredCount     int `json:"expired_count"`

	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// MemoryHierarchyStats contains statistics about memory usage
type MemoryHierarchyStats struct {
	// Per-tier stats
	WorkingMemory   MemoryTierStats `json:"working_memory"`
	ShortTermMemory MemoryTierStats `json:"short_term_memory"`
	LongTermMemory  MemoryTierStats `json:"long_term_memory"`

	// Overall stats
	TotalItems       int     `json:"total_items"`
	TotalTokens      int     `json:"total_tokens"`
	CompressionRatio float64 `json:"compression_ratio"`

	// Recent activity
	ItemsAddedLast24h      int `json:"items_added_last_24h"`
	ItemsCompressedLast24h int `json:"items_compressed_last_24h"`
	ItemsExpiredLast24h    int `json:"items_expired_last_24h"`
}

// MemoryTierStats contains statistics for a single memory tier
type MemoryTierStats struct {
	Tier          MemoryTier     `json:"tier"`
	ItemCount     int            `json:"item_count"`
	TokenCount    int            `json:"token_count"`
	AvgImportance float64        `json:"avg_importance"`
	OldestItem    *time.Time     `json:"oldest_item,omitempty"`
	NewestItem    *time.Time     `json:"newest_item,omitempty"`
	ByCategory    map[string]int `json:"by_category,omitempty"`
	ByImportance  map[string]int `json:"by_importance,omitempty"`
}

// MemoryRecallRequest represents a request to recall from memory hierarchy
type MemoryRecallRequest struct {
	Query         string       `json:"query"`
	Tiers         []MemoryTier `json:"tiers,omitempty"` // Empty = all tiers
	Categories    []string     `json:"categories,omitempty"`
	Tags          []string     `json:"tags,omitempty"`
	Namespace     string       `json:"namespace,omitempty"`
	SessionID     string       `json:"session_id,omitempty"`
	AgentID       string       `json:"agent_id,omitempty"`
	TokenBudget   int          `json:"token_budget,omitempty"`
	MinImportance float64      `json:"min_importance,omitempty"`
	Limit         int          `json:"limit,omitempty"`
}

// MemoryRecallResult contains the result of a memory recall
type MemoryRecallResult struct {
	Items       []MemoryItem   `json:"items"`
	TotalTokens int            `json:"total_tokens"`
	ByTier      map[string]int `json:"by_tier"`
	Truncated   bool           `json:"truncated"` // True if results were limited by token budget
}
