// Package presence holds the agent presence/heartbeat DTOs returned by the
// agent_presence_* tools and surfaced through the HUD fleet view.
//
// These are lifted from internal/hud/bridge/agent_dto.go and continue to be
// re-exported there as type aliases during EPIC 2 (#66).
package presence

// PresenceInfo describes an agent in the presence registry.
//
// IsOrphan / OrphanAgeSeconds are derived by fleetview.Join from the
// presence+session pair; they are never persisted or trusted across
// snapshots. An orphan is a heartbeating presence that has no matching
// active session — the typical signature of a vendor CLI that registered
// presence (or got auto-registered on heartbeat) but never successfully
// called agent_session_start.
type PresenceInfo struct {
	AgentID             string   `json:"agent_id"`
	SessionID           string   `json:"session_id,omitempty"`
	Status              string   `json:"status"`
	AgentType           string   `json:"agent_type"`
	Description         string   `json:"description"`
	CurrentTask         string   `json:"current_task"`
	ActiveFiles         []string `json:"active_files"`
	Branch              string   `json:"branch"`
	PRUrl               string   `json:"pr_url,omitempty"`
	WorktreeID          string   `json:"worktree_id"`
	LastHeartbeat       string   `json:"last_heartbeat"`
	RegisteredAt        string   `json:"registered_at"`
	Source              string   `json:"source,omitempty"`
	HasPresence         bool     `json:"has_presence,omitempty"`
	HasSession          bool     `json:"has_session,omitempty"`
	SessionStatus       string   `json:"session_status,omitempty"`
	SessionStartedAt    string   `json:"session_started_at,omitempty"`
	HeartbeatAgeSeconds int      `json:"heartbeat_age_seconds,omitempty"`
	SessionAgeSeconds   int      `json:"session_age_seconds,omitempty"`
	TelemetryStatus     string   `json:"telemetry_status,omitempty"`
	IsOrphan            bool     `json:"is_orphan,omitempty"`
	OrphanAgeSeconds    int      `json:"orphan_age_seconds,omitempty"`
}
