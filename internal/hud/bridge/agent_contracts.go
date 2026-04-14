package bridge

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	AgentSessionStartEndpoint   = "/api/agent/session-start"
	AgentSessionEndEndpoint     = "/api/agent/session-end"
	AgentSessionEndpoint        = "/api/agent/session"
	AgentSessionListEndpoint    = "/api/agent/session-list"
	AgentSessionPruneEndpoint   = "/api/agent/session-prune"
	AgentContextInspectEndpoint = "/api/agent/context-inspect"
	AgentTaskUpdateEndpoint     = "/api/agent/task-update"
	AgentNudgeQueueEndpoint     = "/api/agent/nudge-queue"
	AgentNudgeQueuePolicyPath   = "/api/agent/nudge-queue-policy"
	AgentDispatchEndpoint       = "/api/agent/dispatch"

	DefaultContextInspectLimit = 200
	DefaultSessionListLimit    = 20
	DefaultSessionPruneStatus  = "ended,summarized"
)

// Normalize trims string fields and canonicalizes option values.
func (p SessionStartParams) Normalize() SessionStartParams {
	p.Namespace = strings.TrimSpace(p.Namespace)
	p.Project = strings.TrimSpace(p.Project)
	p.AgentID = strings.TrimSpace(p.AgentID)
	p.AgentType = strings.TrimSpace(p.AgentType)
	p.Description = strings.TrimSpace(p.Description)
	p.AutoRecallStrategy = normalizeAutoRecallStrategy(p.AutoRecallStrategy)
	p.AutoRecallQuery = strings.TrimSpace(p.AutoRecallQuery)
	p.PipelineProject = strings.TrimSpace(p.PipelineProject)
	p.ParentSessionID = strings.TrimSpace(p.ParentSessionID)
	return p
}

// Validate ensures the request is semantically valid.
func (p SessionStartParams) Validate() error {
	if strings.TrimSpace(p.AgentID) == "" {
		return fmt.Errorf("agent_id is required")
	}
	return nil
}

// ToParams returns the normalized bridge params for session start.
func (p SessionStartParams) ToParams() (SessionStartParams, error) {
	p = p.Normalize()
	if err := p.Validate(); err != nil {
		return SessionStartParams{}, err
	}
	return p, nil
}

// Normalize trims string fields.
func (p SessionEndParams) Normalize() SessionEndParams {
	p.SessionID = strings.TrimSpace(p.SessionID)
	p.AgentID = strings.TrimSpace(p.AgentID)
	return p
}

// Validate ensures the request is semantically valid.
func (p SessionEndParams) Validate() error {
	if strings.TrimSpace(p.SessionID) == "" && strings.TrimSpace(p.AgentID) == "" {
		return fmt.Errorf("session_id or agent_id is required")
	}
	return nil
}

// ToParams returns the normalized bridge params for session end.
func (p SessionEndParams) ToParams() (SessionEndParams, error) {
	p = p.Normalize()
	if err := p.Validate(); err != nil {
		return SessionEndParams{}, err
	}
	return p, nil
}

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

// SessionRequest defines the shared query contract for GET /api/agent/session.
type SessionRequest struct {
	AgentID string `json:"agent_id,omitempty"`
}

// Normalize trims identifiers.
func (r SessionRequest) Normalize() SessionRequest {
	r.AgentID = strings.TrimSpace(r.AgentID)
	return r
}

// Validate ensures the request is semantically valid.
func (r SessionRequest) Validate() error {
	if strings.TrimSpace(r.AgentID) == "" {
		return fmt.Errorf("agent_id query parameter is required")
	}
	return nil
}

// Path builds the HUD path for this request.
func (r SessionRequest) Path() (string, error) {
	req := r.Normalize()
	if err := req.Validate(); err != nil {
		return "", err
	}

	params := url.Values{}
	params.Set("agent_id", req.AgentID)
	return AgentSessionEndpoint + "?" + params.Encode(), nil
}

// SessionListRequest defines the shared request contract for POST /api/agent/session-list.
type SessionListRequest struct {
	AgentID   string `json:"agent_id,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Status    string `json:"status,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// Normalize trims identifiers and applies the default limit.
func (r SessionListRequest) Normalize() SessionListRequest {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.Namespace = strings.TrimSpace(r.Namespace)
	r.Status = strings.TrimSpace(r.Status)
	if r.Limit <= 0 {
		r.Limit = DefaultSessionListLimit
	}
	return r
}

// Validate ensures the request is semantically valid.
func (r SessionListRequest) Validate() error {
	if r.Limit <= 0 {
		return fmt.Errorf("limit must be a positive integer")
	}
	return nil
}

// Params builds the canonical bridge/HUD parameter map for this request.
func (r SessionListRequest) Params() (map[string]any, error) {
	req := r.Normalize()
	if err := req.Validate(); err != nil {
		return nil, err
	}

	params := map[string]any{
		"limit": req.Limit,
	}
	if req.AgentID != "" {
		params["agent_id"] = req.AgentID
	}
	if req.Namespace != "" {
		params["namespace"] = req.Namespace
	}
	if req.Status != "" {
		params["status"] = req.Status
	}
	return params, nil
}

// SessionPruneRequest defines the shared request contract for POST /api/agent/session-prune.
type SessionPruneRequest struct {
	MaxAgeHours int    `json:"max_age_hours,omitempty"`
	Status      string `json:"status,omitempty"`
	DryRun      bool   `json:"dry_run,omitempty"`
}

// Normalize applies defaults and trims string fields.
func (r SessionPruneRequest) Normalize() SessionPruneRequest {
	if r.MaxAgeHours <= 0 {
		r.MaxAgeHours = 72
	}
	r.Status = strings.TrimSpace(r.Status)
	if r.Status == "" {
		r.Status = DefaultSessionPruneStatus
	}
	return r
}

// Validate ensures the request is semantically valid.
func (r SessionPruneRequest) Validate() error {
	if r.MaxAgeHours <= 0 {
		return fmt.Errorf("max_age_hours must be a positive integer")
	}
	return nil
}

// Params builds the canonical bridge/HUD parameter map for this request.
func (r SessionPruneRequest) Params() (map[string]any, error) {
	req := r.Normalize()
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return map[string]any{
		"max_age_hours": req.MaxAgeHours,
		"status":        req.Status,
		"dry_run":       req.DryRun,
	}, nil
}

// TaskUpdateRequest defines the shared mutation payload for POST /api/agent/task-update.
type TaskUpdateRequest struct {
	TaskID      string       `json:"task_id"`
	AgentID     string       `json:"agent_id,omitempty"`
	SessionID   string       `json:"session_id,omitempty"`
	Status      string       `json:"status,omitempty"`
	Title       string       `json:"title,omitempty"`
	Priority    string       `json:"priority,omitempty"`
	Resolution  string       `json:"resolution,omitempty"`
	Project     string       `json:"project,omitempty"`
	PipelineRef *PipelineRef `json:"pipeline_ref,omitempty"`
	WorkflowID  string       `json:"workflow_id,omitempty"`
}

// Normalize trims string fields.
func (r TaskUpdateRequest) Normalize() TaskUpdateRequest {
	r.TaskID = strings.TrimSpace(r.TaskID)
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Status = strings.TrimSpace(r.Status)
	r.Title = strings.TrimSpace(r.Title)
	r.Priority = strings.TrimSpace(r.Priority)
	r.Resolution = strings.TrimSpace(r.Resolution)
	r.Project = strings.TrimSpace(r.Project)
	r.WorkflowID = strings.TrimSpace(r.WorkflowID)
	return r
}

// Validate ensures the request is semantically valid.
func (r TaskUpdateRequest) Validate() error {
	if strings.TrimSpace(r.TaskID) == "" {
		return fmt.Errorf("task_id is required")
	}
	return nil
}

// ToParams converts the contract payload into bridge params.
func (r TaskUpdateRequest) ToParams() (UpdateTaskParams, error) {
	req := r.Normalize()
	if err := req.Validate(); err != nil {
		return UpdateTaskParams{}, err
	}
	return UpdateTaskParams{
		ID:          req.TaskID,
		AgentID:     req.AgentID,
		SessionID:   req.SessionID,
		Status:      req.Status,
		Title:       req.Title,
		Priority:    req.Priority,
		Resolution:  req.Resolution,
		Project:     req.Project,
		PipelineRef: req.PipelineRef,
		WorkflowID:  req.WorkflowID,
	}, nil
}

// DispatchTaskRequest defines the shared mutation payload for POST /api/agent/dispatch.
type DispatchTaskRequest struct {
	TargetAgentID   string       `json:"target_agent_id"`
	SourceSessionID string       `json:"source_session_id,omitempty"`
	Title           string       `json:"title"`
	Context         string       `json:"context,omitempty"`
	Priority        string       `json:"priority,omitempty"`
	Tags            []string     `json:"tags,omitempty"`
	FilePath        string       `json:"file_path,omitempty"`
	LineNumber      int          `json:"line_number,omitempty"`
	BlockedBy       []string     `json:"blocked_by,omitempty"`
	PipelineRef     *PipelineRef `json:"pipeline_ref,omitempty"`
	WorkflowID      string       `json:"workflow_id,omitempty"`
}

// Normalize trims strings, normalizes list fields, and applies default priority.
func (r DispatchTaskRequest) Normalize() DispatchTaskRequest {
	r.TargetAgentID = strings.TrimSpace(r.TargetAgentID)
	r.SourceSessionID = strings.TrimSpace(r.SourceSessionID)
	r.Title = strings.TrimSpace(r.Title)
	r.Context = strings.TrimSpace(r.Context)
	r.Priority = normalizeTaskPriority(strings.TrimSpace(r.Priority))
	r.Tags = NormalizeStringList(r.Tags)
	r.FilePath = strings.TrimSpace(r.FilePath)
	if r.LineNumber < 0 {
		r.LineNumber = 0
	}
	r.BlockedBy = NormalizeStringList(r.BlockedBy)
	r.WorkflowID = strings.TrimSpace(r.WorkflowID)
	return r
}

// Validate ensures the request is semantically valid.
func (r DispatchTaskRequest) Validate() error {
	if strings.TrimSpace(r.TargetAgentID) == "" {
		return fmt.Errorf("target_agent_id is required")
	}
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("title is required")
	}
	return nil
}

// ToParams converts the contract payload into bridge params.
func (r DispatchTaskRequest) ToParams() (DispatchTaskParams, error) {
	req := r.Normalize()
	if err := req.Validate(); err != nil {
		return DispatchTaskParams{}, err
	}
	return DispatchTaskParams(req), nil
}

// NormalizeStringList trims, drops empty values, and de-duplicates while preserving order.
func NormalizeStringList(values []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		normalized = append(normalized, v)
	}
	return normalized
}

func normalizeTaskPriority(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "low", "medium", "high", "critical":
		return strings.ToLower(strings.TrimSpace(priority))
	default:
		return "medium"
	}
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

// Normalize trims string fields and normalizes list values.
func (r HeartbeatRequest) Normalize() HeartbeatRequest {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Status = strings.ToLower(strings.TrimSpace(r.Status))
	r.AgentType = strings.TrimSpace(r.AgentType)
	r.Description = strings.TrimSpace(r.Description)
	r.Namespace = strings.TrimSpace(r.Namespace)
	r.ActiveFiles = NormalizeStringList(r.ActiveFiles)
	r.CurrentTask = strings.TrimSpace(r.CurrentTask)
	r.Branch = strings.TrimSpace(r.Branch)
	if r.HeartbeatTTLSeconds < 0 {
		r.HeartbeatTTLSeconds = 0
	}
	return r
}

// Validate ensures the request is semantically valid.
func (r HeartbeatRequest) Validate() error {
	if strings.TrimSpace(r.AgentID) == "" {
		return fmt.Errorf("agent_id is required")
	}
	return nil
}

// ToRequest returns the normalized heartbeat request.
func (r HeartbeatRequest) ToRequest() (HeartbeatRequest, error) {
	r = r.Normalize()
	if err := r.Validate(); err != nil {
		return HeartbeatRequest{}, err
	}
	return r, nil
}

// HeartbeatParams extracts bridge-level heartbeat params from the request.
func (r HeartbeatRequest) HeartbeatParams() PresenceHeartbeatParams {
	return PresenceHeartbeatParams{
		Status:      r.Status,
		AgentType:   r.AgentType,
		ActiveFiles: r.ActiveFiles,
		CurrentTask: r.CurrentTask,
		Branch:      r.Branch,
	}
}
