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
