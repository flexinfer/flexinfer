// agent_dto.go defines shared DTO types used across agent bridge domain files
// and consumed by the HUD API handlers and CLI.
//
// These types are deserialized from MCP tool results and serialized as JSON
// in REST API responses. Field names match the MCP agent-context server's
// wire format.
package bridge

// --- Session DTOs ---

// SessionInfo describes an agent session.
type SessionInfo struct {
	ID          string `json:"id"`
	AgentID     string `json:"agent_id"`
	Namespace   string `json:"namespace"`
	StartedAt   string `json:"started_at"`
	EndedAt     string `json:"ended_at,omitempty"`
	Status      string `json:"status"`
	Description string `json:"description"`
	EntryCount  int    `json:"entry_count"`
	TotalTokens int    `json:"total_tokens"`
}

// --- Task DTOs ---

// TaskInfo describes an agent task.
type TaskInfo struct {
	ID        string   `json:"id"`
	SessionID string   `json:"session_id"`
	AgentID   string   `json:"agent_id"`
	Namespace string   `json:"namespace"`
	Title     string   `json:"title"`
	Context   string   `json:"context,omitempty"`
	Priority  string   `json:"priority"`
	Status    string   `json:"status"`
	Tags      []string `json:"tags,omitempty"`
	BlockedBy []string `json:"blocked_by,omitempty"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

// --- Workflow DTOs ---

// WorkflowInfo describes a workflow summary (MCP field names for deserialization).
type WorkflowInfo struct {
	ID          string  `json:"workflow_id"`
	Name        string  `json:"name,omitempty"`
	Status      string  `json:"status"`
	Progress    float64 `json:"progress,omitempty"`
	CurrentStep string  `json:"current_step"`
	CreatedAt   string  `json:"created_at"`
	Error       string  `json:"error,omitempty"`
}

// WorkflowStep describes a single step within a workflow.
type WorkflowStep struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Type   string `json:"type,omitempty"`
	Error  string `json:"error,omitempty"`
}

// WorkflowDetail is the full workflow status including steps (MCP field names).
type WorkflowDetail struct {
	ID             string          `json:"workflow_id"`
	Name           string          `json:"name,omitempty"`
	Status         string          `json:"status"`
	CurrentStep    string          `json:"current_step"`
	Progress       float64         `json:"progress,omitempty"`
	CompletedSteps int             `json:"completed_steps,omitempty"`
	TotalSteps     int             `json:"total_steps,omitempty"`
	Steps          []WorkflowStep  `json:"steps,omitempty"`
	CreatedAt      string          `json:"created_at"`
	StartedAt      string          `json:"started_at,omitempty"`
	CompletedAt    string          `json:"completed_at,omitempty"`
	Error          string          `json:"error,omitempty"`
	Events         []WorkflowEvent `json:"events,omitempty"`
}

// WorkflowEvent is a single workflow execution event.
type WorkflowEvent struct {
	ID        string         `json:"id"`
	EventType string         `json:"event_type"`
	Timestamp string         `json:"timestamp"`
	StepID    string         `json:"step_id,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// WorkflowDefineResult holds the result of defining a workflow.
type WorkflowDefineResult struct {
	OK           bool   `json:"ok"`
	DefinitionID string `json:"definition_id"`
	Name         string `json:"name"`
	StepCount    int    `json:"step_count"`
}

// WorkflowDefinitionInfo describes a registered workflow definition.
type WorkflowDefinitionInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Namespace   string `json:"namespace"`
	StepCount   int    `json:"step_count"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
}

// --- Memory DTOs ---

// MemoryTierStats describes statistics for a single memory tier.
type MemoryTierStats struct {
	Items  int `json:"item_count"`
	Tokens int `json:"token_count"`
}

// MemoryStatsResult holds the full memory hierarchy statistics.
type MemoryStatsResult struct {
	WorkingMemory   MemoryTierStats `json:"working_memory"`
	ShortTermMemory MemoryTierStats `json:"short_term_memory"`
	LongTermMemory  MemoryTierStats `json:"long_term_memory"`
	TotalItems      int             `json:"total_items"`
	TotalTokens     int             `json:"total_tokens"`

	// Compression stats from the memory hierarchy.
	CompressionRatio       float64 `json:"compression_ratio"`
	ItemsAddedLast24h      int     `json:"items_added_last_24h"`
	ItemsCompressedLast24h int     `json:"items_compressed_last_24h"`
}

// MemoryItem describes a single memory item.
type MemoryItem struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	Content         string  `json:"content,omitempty"`
	Tier            string  `json:"tier"`
	Importance      string  `json:"importance"`
	ImportanceScore float64 `json:"importance_score"`
	Tokens          int     `json:"original_tokens"`
	Status          string  `json:"status,omitempty"`
	Category        string  `json:"category,omitempty"`
	AccessedAt      string  `json:"created_at,omitempty"`
	LastAccessed    string  `json:"last_accessed_at,omitempty"`
}

// --- Knowledge Graph DTOs ---

// GraphStatsResult holds knowledge graph statistics.
type GraphStatsResult struct {
	EntityCount   int            `json:"total_entities"`
	RelationCount int            `json:"total_relations"`
	EntityTypes   map[string]int `json:"entities_by_type"`
	RelationTypes map[string]int `json:"relations_by_type"`
}

// EntityInfo describes an entity in the knowledge graph.
type EntityInfo struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Type        string         `json:"type,omitempty"`
	EntityType  string         `json:"entity_type,omitempty"`
	Description string         `json:"description,omitempty"`
	Namespace   string         `json:"namespace,omitempty"`
	Properties  map[string]any `json:"properties,omitempty"`
}

// EntityDetail describes a single entity with its relations.
type EntityDetail struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Type              string         `json:"type,omitempty"`
	EntityType        string         `json:"entity_type,omitempty"`
	Namespace         string         `json:"namespace,omitempty"`
	Properties        map[string]any `json:"properties,omitempty"`
	InboundRelations  []RelationInfo `json:"inbound_relations,omitempty"`
	OutboundRelations []RelationInfo `json:"outbound_relations,omitempty"`
}

// RelationInfo describes a relation in the knowledge graph.
type RelationInfo struct {
	ID           string `json:"id"`
	Source       string `json:"source_id"`
	SourceName   string `json:"source_name,omitempty"`
	Target       string `json:"target_id"`
	TargetName   string `json:"target_name,omitempty"`
	Type         string `json:"type,omitempty"`
	RelationType string `json:"relation_type,omitempty"`
}

// --- Context DTOs ---

// ContextEntryInfo describes a context stream entry.
type ContextEntryInfo struct {
	Score float64      `json:"score"`
	Entry ContextEntry `json:"entry"`
}

// ContextEntry is the inner entry within a context search result.
type ContextEntry struct {
	ID         string `json:"id"`
	EntryType  string `json:"entry_type"`
	AgentID    string `json:"agent_id"`
	Namespace  string `json:"namespace"`
	Title      string `json:"title"`
	Content    string `json:"content,omitempty"`
	FilePath   string `json:"file_path,omitempty"`
	LineStart  int    `json:"line_start,omitempty"`
	LineEnd    int    `json:"line_end,omitempty"`
	TokenCount int    `json:"token_count,omitempty"`
	Timestamp  string `json:"timestamp"`
}

// ContextInspectBucket describes aggregate context weight by entry type.
type ContextInspectBucket struct {
	EntryType       string `json:"entry_type"`
	Count           int    `json:"count"`
	Chars           int    `json:"chars"`
	EstimatedTokens int    `json:"estimated_tokens"`
}

// ContextInspectTopEntry highlights the heaviest entries in a session.
type ContextInspectTopEntry struct {
	ID              string `json:"id"`
	EntryType       string `json:"entry_type"`
	Title           string `json:"title"`
	Timestamp       string `json:"timestamp"`
	Chars           int    `json:"chars"`
	EstimatedTokens int    `json:"estimated_tokens"`
}

// ContextInspectTasks summarizes task state for the inspected session.
type ContextInspectTasks struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
}

// ContextInspectSection describes estimated prompt budget by section.
type ContextInspectSection struct {
	Section         string `json:"section"`
	Chars           int    `json:"chars"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Source          string `json:"source"`
}

// ContextInspectResult is a context budget breakdown for a session.
type ContextInspectResult struct {
	SessionID              string                   `json:"session_id"`
	AgentID                string                   `json:"agent_id,omitempty"`
	Namespace              string                   `json:"namespace,omitempty"`
	SessionStatus          string                   `json:"session_status,omitempty"`
	Limit                  int                      `json:"limit"`
	EntryCount             int                      `json:"entry_count"`
	ContextChars           int                      `json:"context_chars"`
	ContextEstimatedTokens int                      `json:"context_estimated_tokens"`
	EstimatedTokens        int                      `json:"estimated_tokens"`
	Truncated              bool                     `json:"truncated"`
	ByEntryType            []ContextInspectBucket   `json:"by_entry_type"`
	TopEntries             []ContextInspectTopEntry `json:"top_entries,omitempty"`
	Sections               []ContextInspectSection  `json:"sections"`
	Tasks                  ContextInspectTasks      `json:"tasks"`
	Memory                 *MemoryStatsResult       `json:"memory,omitempty"`
	RetrievedAt            string                   `json:"retrieved_at"`
}

// --- Presence / Coordination DTOs ---

// PresenceInfo describes an agent in the presence registry.
type PresenceInfo struct {
	AgentID       string   `json:"agent_id"`
	SessionID     string   `json:"session_id,omitempty"`
	Status        string   `json:"status"`
	AgentType     string   `json:"agent_type"`
	Description   string   `json:"description"`
	CurrentTask   string   `json:"current_task"`
	ActiveFiles   []string `json:"active_files"`
	Branch        string   `json:"branch"`
	PRUrl         string   `json:"pr_url,omitempty"`
	WorktreeID    string   `json:"worktree_id"`
	LastHeartbeat string   `json:"last_heartbeat"`
	RegisteredAt  string   `json:"registered_at"`
}

// PresenceHeartbeatResult is the response from agent_presence_heartbeat.
type PresenceHeartbeatResult struct {
	OK            bool             `json:"ok"`
	AgentID       string           `json:"agent_id"`
	LastHeartbeat string           `json:"last_heartbeat"`
	HasConflicts  bool             `json:"has_conflicts,omitempty"`
	Conflicts     []map[string]any `json:"conflicts,omitempty"`
	Warning       string           `json:"_warning,omitempty"`
}

// FileClaimInfo describes a file claim (advisory lock).
type FileClaimInfo struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	FilePath  string `json:"file_path"`
	ClaimType string `json:"claim_type"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// WorktreeInfo describes a git worktree assignment.
type WorktreeInfo struct {
	AssignmentID string `json:"assignment_id"`
	AgentID      string `json:"agent_id"`
	SessionID    string `json:"session_id"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
	BaseBranch   string `json:"base_branch"`
	Purpose      string `json:"purpose"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
	ReleasedAt   string `json:"released_at,omitempty"`
	GitStatus    string `json:"git_status,omitempty"`
}

type WorktreeAllocateParams struct {
	AgentID    string `json:"agent_id"`
	SessionID  string `json:"session_id"`
	BranchName string `json:"branch_name"`
	BaseBranch string `json:"base_branch,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
	TTLHours   int    `json:"ttl_hours,omitempty"`
}

type WorktreeAllocateResult struct {
	OK           bool   `json:"ok"`
	AssignmentID string `json:"assignment_id"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
	BaseBranch   string `json:"base_branch,omitempty"`
	Status       string `json:"status,omitempty"`
}

// CompactionInfo describes the compaction scheduler status.
type CompactionInfo struct {
	Running        bool   `json:"running"`
	LastRun        string `json:"last_run,omitempty"`
	ItemsCompacted int    `json:"items_compacted"`
	ItemsPromoted  int    `json:"items_promoted"`
	ItemsExpired   int    `json:"items_expired"`
}

// --- Reasoning DTOs ---

// ReasoningChainInfo describes a reasoning chain.
type ReasoningChainInfo struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Status      string  `json:"status"`
	StepCount   int     `json:"step_count"`
	Confidence  float64 `json:"confidence,omitempty"`
	CreatedAt   string  `json:"created_at"`
	CompletedAt string  `json:"completed_at,omitempty"`
}

// ReasoningStepInfo describes a step in a reasoning chain.
type ReasoningStepInfo struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Confidence  float64 `json:"confidence"`
	Evidence    string  `json:"evidence,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

// ReasoningChainDetail is a chain with its steps.
type ReasoningChainDetail struct {
	ReasoningChainInfo
	Steps []ReasoningStepInfo `json:"steps"`
}

// --- Handoff DTOs ---

// HandoffInfo describes a handoff between agents.
type HandoffInfo struct {
	ID            string `json:"id"`
	FromAgent     string `json:"from_agent"`
	ToAgent       string `json:"to_agent,omitempty"`
	TargetAgentID string `json:"target_agent_id,omitempty"`
	Status        string `json:"status"`
	Summary       string `json:"summary"`
	Context       string `json:"context,omitempty"`
	CreatedAt     string `json:"created_at"`
	AcceptedAt    string `json:"accepted_at,omitempty"`
}

type handoffInboxEntry struct {
	HandoffID    string `json:"handoff_id"`
	SourceAgent  string `json:"source_agent"`
	Status       string `json:"status"`
	Instructions string `json:"instructions,omitempty"`
	Summary      string `json:"summary"`
	CreatedAt    string `json:"created_at"`
}

type HandoffCreateParams struct {
	SessionID     string   `json:"session_id"`
	TargetAgentID string   `json:"target_agent_id"`
	Instructions  string   `json:"instructions"`
	HandoffType   string   `json:"handoff_type,omitempty"`
	EntryIDs      []string `json:"entry_ids,omitempty"`
	TokenBudget   int      `json:"token_budget,omitempty"`
}

type HandoffCreateResult struct {
	OK         bool   `json:"ok"`
	HandoffID  string `json:"handoff_id"`
	TokenCount int    `json:"token_count,omitempty"`
	EntryCount int    `json:"entry_count,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

type HandoffAcceptParams struct {
	HandoffID     string `json:"handoff_id"`
	SessionID     string `json:"session_id"`
	ImportEntries bool   `json:"import_entries,omitempty"`
}

type HandoffAcceptResult struct {
	OK            bool   `json:"ok"`
	HandoffID     string `json:"handoff_id"`
	SourceAgent   string `json:"source_agent,omitempty"`
	Instructions  string `json:"instructions,omitempty"`
	Summary       string `json:"summary,omitempty"`
	TokenCount    int    `json:"token_count,omitempty"`
	ImportedCount int    `json:"imported_count,omitempty"`
}

// --- Template DTOs ---

// TemplateInfo describes a session template.
type TemplateInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

// --- Annotation DTOs ---

// AnnotationInfo describes a code annotation.
type AnnotationInfo struct {
	ID       string `json:"id"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line,omitempty"`
	AgentID  string `json:"agent_id"`
	Content  string `json:"content"`
	Category string `json:"category,omitempty"`
}

// --- Infrastructure DTOs ---

// TunnelInfo describes an SSH tunnel status.
type TunnelInfo struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	RemoteHost string `json:"remote_host"`
	Uptime     string `json:"uptime,omitempty"`
	Reconnects int    `json:"reconnects"`
}

// CacheStatsInfo describes the response cache statistics.
type CacheStatsInfo struct {
	Entries int     `json:"entries"`
	Size    string  `json:"size"`
	HitRate float64 `json:"hit_rate"`
}

// --- Knowledge DTOs ---

// KnowledgeResult is the response from cross-agent enhanced recall.
type KnowledgeResult struct {
	OK          bool             `json:"ok"`
	Entries     []KnowledgeEntry `json:"entries"`
	Count       int              `json:"count"`
	TotalTokens int              `json:"total_tokens"`
	TokenBudget int              `json:"token_budget"`
}

// KnowledgeEntry represents a context entry with source attribution.
type KnowledgeEntry struct {
	ID         string         `json:"id"`
	AgentID    string         `json:"agent_id"`
	SessionID  string         `json:"session_id"`
	Namespace  string         `json:"namespace,omitempty"`
	EntryType  string         `json:"entry_type"`
	Title      string         `json:"title"`
	Content    string         `json:"content"`
	FilePath   string         `json:"file_path,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	Timestamp  string         `json:"timestamp"`
	TokenCount int            `json:"token_count"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}
