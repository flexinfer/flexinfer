package bridge

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	AgentContextInspectEndpoint = "/api/agent/context-inspect"
	AgentNudgeQueueEndpoint     = "/api/agent/nudge-queue"
	AgentNudgeQueuePolicyPath   = "/api/agent/nudge-queue-policy"

	DefaultContextInspectLimit = 200
)

// ContextInspectRequest defines the shared request contract for context-inspect
// across HUD handlers, CLI callers, and bridge fallback logic.
type ContextInspectRequest struct {
	AgentID   string `json:"agent_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Detail    bool   `json:"detail,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// Normalize trims identifiers and applies default limit.
func (r ContextInspectRequest) Normalize() ContextInspectRequest {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.Limit <= 0 {
		r.Limit = DefaultContextInspectLimit
	}
	return r
}

// Validate ensures the request is semantically valid.
func (r ContextInspectRequest) Validate() error {
	if r.AgentID == "" && r.SessionID == "" {
		return fmt.Errorf("agent_id or session_id query parameter is required")
	}
	if r.Limit <= 0 {
		return fmt.Errorf("limit must be a positive integer")
	}
	return nil
}

// Path builds the HUD path for this request.
func (r ContextInspectRequest) Path() (string, error) {
	req := r.Normalize()
	if err := req.Validate(); err != nil {
		return "", err
	}

	params := url.Values{}
	if req.AgentID != "" {
		params.Set("agent_id", req.AgentID)
	}
	if req.SessionID != "" {
		params.Set("session_id", req.SessionID)
	}
	if req.Detail {
		params.Set("detail", "true")
	}
	params.Set("limit", strconv.Itoa(req.Limit))

	return AgentContextInspectEndpoint + "?" + params.Encode(), nil
}

// ParseContextInspectRequest decodes and validates query-string input.
func ParseContextInspectRequest(query url.Values) (ContextInspectRequest, error) {
	req := ContextInspectRequest{
		AgentID:   strings.TrimSpace(query.Get("agent_id")),
		SessionID: strings.TrimSpace(query.Get("session_id")),
	}

	switch strings.ToLower(strings.TrimSpace(query.Get("detail"))) {
	case "1", "true", "yes", "y":
		req.Detail = true
	}

	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return ContextInspectRequest{}, fmt.Errorf("limit must be a positive integer")
		}
		req.Limit = parsed
	}

	req = req.Normalize()
	if err := req.Validate(); err != nil {
		return ContextInspectRequest{}, err
	}
	return req, nil
}

// NudgeQueuePolicyMutation defines the shared mutation payload contract for
// nudge queue policy updates.
type NudgeQueuePolicyMutation struct {
	DebounceMs   *int     `json:"debounce_ms,omitempty"`
	Cap          *int     `json:"cap,omitempty"`
	DropPolicy   *string  `json:"drop_policy,omitempty"`
	LanePriority []string `json:"lane_priority,omitempty"`
	UpdatedBy    string   `json:"updated_by,omitempty"`
}

// HasMutation reports whether any policy field was requested for mutation.
func (m NudgeQueuePolicyMutation) HasMutation() bool {
	return m.DebounceMs != nil ||
		m.Cap != nil ||
		m.DropPolicy != nil ||
		m.LanePriority != nil
}

// Normalize trims all string fields while preserving explicit lane_priority
// presence (including empty arrays) for validation/error parity.
func (m NudgeQueuePolicyMutation) Normalize() NudgeQueuePolicyMutation {
	if m.DropPolicy != nil {
		raw := strings.TrimSpace(*m.DropPolicy)
		m.DropPolicy = &raw
	}
	if m.LanePriority != nil {
		lanes := make([]string, 0, len(m.LanePriority))
		for _, lane := range m.LanePriority {
			if trimmed := strings.TrimSpace(lane); trimmed != "" {
				lanes = append(lanes, trimmed)
			}
		}
		m.LanePriority = lanes
	}
	m.UpdatedBy = strings.TrimSpace(m.UpdatedBy)
	return m
}

// Validate enforces mutation field constraints and matches HUD/server messages.
func (m NudgeQueuePolicyMutation) Validate() error {
	if m.DebounceMs != nil && *m.DebounceMs < 0 {
		return fmt.Errorf("debounce_ms must be >= 0")
	}
	if m.Cap != nil && *m.Cap <= 0 {
		return fmt.Errorf("cap must be > 0")
	}
	if m.DropPolicy != nil {
		switch strings.ToLower(strings.TrimSpace(*m.DropPolicy)) {
		case "drop_old", "drop_new", "summarize":
		default:
			return fmt.Errorf("drop_policy must be one of: drop_old, drop_new, summarize")
		}
	}
	if m.LanePriority != nil && len(m.LanePriority) == 0 {
		return fmt.Errorf("lane_priority must include at least one non-empty lane")
	}
	return nil
}

// ParseLanePriorityCSV parses comma-delimited lane order from CLI input.
func ParseLanePriorityCSV(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	lanes := make([]string, 0, len(parts))
	for _, part := range parts {
		if lane := strings.TrimSpace(part); lane != "" {
			lanes = append(lanes, lane)
		}
	}
	if len(lanes) == 0 {
		return nil, fmt.Errorf("lane-priority must include at least one non-empty lane")
	}
	return lanes, nil
}

// NudgeQueueStatusPath builds the HUD path for nudge queue status lookup.
func NudgeQueueStatusPath(agentID string) (string, error) {
	id := strings.TrimSpace(agentID)
	if id == "" {
		return "", fmt.Errorf("agent_id query parameter is required")
	}
	params := url.Values{}
	params.Set("agent_id", id)
	return AgentNudgeQueueEndpoint + "?" + params.Encode(), nil
}

// NudgeQueuePolicy is the shared runtime policy contract used by HUD and CLI.
type NudgeQueuePolicy struct {
	DebounceMs   int      `json:"debounce_ms"`
	Cap          int      `json:"cap"`
	DropPolicy   string   `json:"drop_policy"`
	LanePriority []string `json:"lane_priority"`
}

// NudgeQueueStatus is the shared queue status contract used by HUD and CLI.
type NudgeQueueStatus struct {
	Pending      int            `json:"pending"`
	Dropped      int            `json:"dropped"`
	ByLane       map[string]int `json:"by_lane"`
	DebounceMs   int            `json:"debounce_ms"`
	Cap          int            `json:"cap"`
	DropPolicy   string         `json:"drop_policy"`
	LanePriority []string       `json:"lane_priority"`
}

type NudgeQueueStatusResponse struct {
	OK     bool             `json:"ok"`
	Status NudgeQueueStatus `json:"status"`
}

type NudgeQueuePolicyResponse struct {
	OK     bool             `json:"ok"`
	Policy NudgeQueuePolicy `json:"policy"`
}

// HeartbeatRequest is the shared HTTP body for POST /api/agent/heartbeat.
type HeartbeatRequest struct {
	AgentID     string `json:"agent_id"`
	SessionID   string `json:"session_id,omitempty"`
	Status      string `json:"status,omitempty"`
	AgentType   string `json:"agent_type,omitempty"`
	Description string `json:"description,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	// EnsureSession auto-bootstraps a session when heartbeat clients lack
	// dedicated session-start hooks (for example proxy-only integrations).
	EnsureSession       bool     `json:"ensure_session,omitempty"`
	ActiveFiles         []string `json:"active_files,omitempty"`
	CurrentTask         string   `json:"current_task,omitempty"`
	Branch              string   `json:"branch,omitempty"`
	HeartbeatTTLSeconds int      `json:"heartbeat_ttl_seconds,omitempty"`
}

// HeartbeatParams extracts bridge-level heartbeat params from the request.
func (r HeartbeatRequest) HeartbeatParams() PresenceHeartbeatParams {
	return PresenceHeartbeatParams{
		Status:      r.Status,
		ActiveFiles: r.ActiveFiles,
		CurrentTask: r.CurrentTask,
		Branch:      r.Branch,
	}
}
