package agentcontext

import (
	"time"
)

// =========================================================================
// Agent Presence Types
// =========================================================================

// PresenceStatus defines agent presence states
type PresenceStatus string

const (
	PresenceStatusActive  PresenceStatus = "active"
	PresenceStatusIdle    PresenceStatus = "idle"
	PresenceStatusOffline PresenceStatus = "offline"
	PresenceStatusExpired PresenceStatus = "expired"
)

// NudgeType defines the kind of nudge sent to an agent.
type NudgeType string

const (
	NudgeTypeContextInject NudgeType = "context_inject"
	NudgeTypeTaskRedirect  NudgeType = "task_redirect"
	NudgeTypePauseRequest  NudgeType = "pause_request"
	NudgeTypeMessage       NudgeType = "message"
)

// Nudge represents a pending message or directive for an agent,
// delivered via heartbeat response.
type Nudge struct {
	ID        string    `json:"id"`
	Type      NudgeType `json:"type"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	FromAgent string    `json:"from_agent,omitempty"` // source agent or "hud"
}

// AgentPresence represents an agent's live presence in the system
type AgentPresence struct {
	ID          string         `json:"id"`
	AgentID     string         `json:"agent_id"`
	SessionID   string         `json:"session_id,omitempty"`
	Status      PresenceStatus `json:"status"`
	Description string         `json:"description,omitempty"`
	CurrentTask string         `json:"current_task,omitempty"`
	ActiveFiles []string       `json:"active_files,omitempty"`
	WorkingDir  string         `json:"working_dir,omitempty"`
	Branch      string         `json:"branch,omitempty"`
	WorktreeID  string         `json:"worktree_id,omitempty"`
	AgentType   string         `json:"agent_type,omitempty"` // "claude-code", "codex", "gemini"
	PRUrl       string         `json:"pr_url,omitempty"`     // URL of the active PR (if any)

	LastHeartbeat time.Time `json:"last_heartbeat"`
	HeartbeatTTL  int       `json:"heartbeat_ttl"` // seconds, default 120
	RegisteredAt  time.Time `json:"registered_at"`

	Metadata map[string]any `json:"metadata,omitempty"`
}

// =========================================================================
// File Claims Types (Advisory Locks)
// =========================================================================

// ClaimType defines the type of file claim
type ClaimType string

const (
	ClaimTypeEdit    ClaimType = "edit"
	ClaimTypeReview  ClaimType = "review"
	ClaimTypeReserve ClaimType = "reserve"
)

// FileClaim represents an advisory lock on a file
type FileClaim struct {
	ID        string     `json:"id"`
	AgentID   string     `json:"agent_id"`
	SessionID string     `json:"session_id"`
	FilePath  string     `json:"file_path"`
	ClaimType ClaimType  `json:"claim_type"`
	Reason    string     `json:"reason,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// =========================================================================
// Git Worktree Assignment Types
// =========================================================================

// WorktreeStatus defines worktree assignment states
type WorktreeStatus string

const (
	WorktreeStatusActive   WorktreeStatus = "active"
	WorktreeStatusReleased WorktreeStatus = "released"
	WorktreeStatusOrphaned WorktreeStatus = "orphaned"
)

// WorktreeAssignment tracks a worktree assigned to an agent
type WorktreeAssignment struct {
	ID           string         `json:"id"`
	AgentID      string         `json:"agent_id"`
	SessionID    string         `json:"session_id"`
	WorktreePath string         `json:"worktree_path"`
	Branch       string         `json:"branch"`
	BaseBranch   string         `json:"base_branch,omitempty"`
	Purpose      string         `json:"purpose,omitempty"`
	Status       WorktreeStatus `json:"status"`
	CreatedAt    time.Time      `json:"created_at"`
	ReleasedAt   *time.Time     `json:"released_at,omitempty"`

	// Lifecycle management
	OrphanedAt     *time.Time `json:"orphaned_at,omitempty"`      // When worktree was orphaned (for grace period)
	TTL            int        `json:"ttl_hours,omitempty"`        // Max lifetime in hours (0 = no limit)
	DiskUsage      int64      `json:"disk_usage_bytes,omitempty"` // Last measured disk usage in bytes
	DiskMeasuredAt *time.Time `json:"disk_measured_at,omitempty"` // When disk was last measured
}
