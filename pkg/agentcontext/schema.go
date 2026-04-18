package agentcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/fi-accel/go/fiaccel"
)

const SchemaVersion = "v1"

// EntryType defines the type of context entry
type EntryType string

const (
	EntryTypeFileRead      EntryType = "file_read"
	EntryTypeDecision      EntryType = "decision"
	EntryTypeFinding       EntryType = "finding"
	EntryTypeQuestion      EntryType = "question"
	EntryTypeSummary       EntryType = "summary"
	EntryTypeCodeContext   EntryType = "code_context"
	EntryTypeNote          EntryType = "note"
	EntryTypeError         EntryType = "error"
	EntryTypeTask          EntryType = "task"
	EntryTypeHandoff       EntryType = "handoff"
	EntryTypeAnnotation    EntryType = "annotation"
	EntryTypePipelineEvent EntryType = "pipeline_event"
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
	HandoffStatusViewed   HandoffStatus = "viewed"
	HandoffStatusRejected HandoffStatus = "rejected"
)

// Durability defines how an entry is stored when added via agent_context_add.
type Durability string

const (
	// DurabilitySession stores to context backend (default, session-scoped).
	DurabilitySession Durability = "session"
	// DurabilityPersistent promotes to memory hierarchy (short-term tier by default).
	DurabilityPersistent Durability = "persistent"
	// DurabilityGraph creates an entity in the knowledge graph.
	DurabilityGraph Durability = "graph"
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
	Project   string `json:"project,omitempty"`

	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Status    string     `json:"status"`

	// Session metadata
	Description string `json:"description,omitempty"`
	WorkingDir  string `json:"working_dir,omitempty"`

	// Session hierarchy (subagent grouping)
	ParentSessionID string `json:"parent_session_id,omitempty"`
	RootSessionID   string `json:"root_session_id,omitempty"`

	// Pipeline linking
	PipelineRef *PipelineRef `json:"pipeline_ref,omitempty"`

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

// tokenizer singleton for accurate BPE counting
var (
	tokenizerOnce   sync.Once
	globalTokenizer fiaccel.Tokenizer
)

func getTokenizer() fiaccel.Tokenizer {
	tokenizerOnce.Do(func() {
		tok, err := fiaccel.NewTokenizer("gpt-4")
		if err != nil {
			return
		}
		globalTokenizer = tok
	})
	return globalTokenizer
}

// EstimateTokens returns an accurate BPE token count when the fi-accel
// native library is available, falling back to a ~4 chars/token heuristic.
func EstimateTokens(text string) int {
	if tok := getTokenizer(); tok != nil {
		if n, err := tok.Count(text); err == nil {
			return n
		}
	}
	return (len(text) + 3) / 4
}

// Task represents a task/todo discovered during agent sessions
type Task struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	Namespace string `json:"namespace,omitempty"`
	Project   string `json:"project,omitempty"`

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

	// Pipeline and workflow linking
	PipelineRef *PipelineRef `json:"pipeline_ref,omitempty"`
	WorkflowID  string       `json:"workflow_id,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	TokenCount int `json:"token_count"`

	// F6: fleet task queue + capability-aware dispatch.
	// CapabilityNeeded lists required capability tags (e.g. "go", "k8s").
	// Scope is "session" (default) or "fleet" — fleet-scope tasks are eligible
	// for capability-aware routing to any active agent in the fleet.
	CapabilityNeeded []string `json:"capability_needed,omitempty"`
	Scope            string   `json:"scope,omitempty"`
}

// TaskScope values for Task.Scope. Empty defaults to TaskScopeSession.
const (
	TaskScopeSession = "session"
	TaskScopeFleet   = "fleet"
)

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

	// Viewed/rejected tracking
	ViewedAt       *time.Time `json:"viewed_at,omitempty"`
	RejectedAt     *time.Time `json:"rejected_at,omitempty"`
	RejectedReason string     `json:"rejected_reason,omitempty"`
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

// RecallSource identifies which storage backend produced a recall result.
type RecallSource string

const (
	RecallSourceContext RecallSource = "context" // Qdrant context entries
	RecallSourceMemory  RecallSource = "memory"  // Memory hierarchy (working/short/long-term)
	RecallSourceGraph   RecallSource = "graph"   // Knowledge graph entities
)

// RecallScope restricts which backends are queried during unified recall.
// An empty slice queries all backends.
type RecallScope []RecallSource

// PipelineRef links an entity to a specific CI pipeline.
type PipelineRef struct {
	ID      int    `json:"id"`
	Project string `json:"project"`
	Ref     string `json:"ref,omitempty"`
	WebURL  string `json:"web_url,omitempty"`
}

// EnhancedRecallOptions extends RecallOptions with new capabilities
type EnhancedRecallOptions struct {
	RecallOptions

	// New options
	SymbolContext string  `json:"symbol_context,omitempty"`
	RecencyWeight float64 `json:"recency_weight,omitempty"` // 0.0-1.0, default 0.2
	IncludeTasks  bool    `json:"include_tasks"`

	// CrossAgent searches across all sessions/agents instead of filtering
	// to a single agent_id/session_id. Results include source attribution.
	CrossAgent bool `json:"cross_agent"`

	// Scope restricts which backends to query. Empty = all backends.
	// Valid values: "context", "memory", "graph"
	Scope RecallScope `json:"scope,omitempty"`

	// IncludeMemory enables memory hierarchy recall (default true).
	IncludeMemory bool `json:"include_memory"`
	// IncludeGraph enables knowledge graph entity search (default true).
	IncludeGraph bool `json:"include_graph"`
}
