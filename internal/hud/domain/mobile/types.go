package mobile

import (
	"encoding/json"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// --- Constants ---

// MaxDeviceIDLen is the maximum length for the X-Device-ID header.
const MaxDeviceIDLen = 128

const (
	DefaultLimit = 50
	MaxLimit     = 200
)

const (
	ScopeRead          = "mobile:read"
	ScopeSessionCreate = "mobile:session:create"
	ScopeSessionEnd    = "mobile:session:end"
	ScopePush          = "mobile:push"
	ScopeAgentSpawn    = "mobile:agent:spawn"
)

// --- Envelope types ---

// Envelope is the standard response shape for /api/mobile/v1 endpoints.
type Envelope struct {
	OK    bool    `json:"ok"`
	Data  any     `json:"data,omitempty"`
	Error any     `json:"error,omitempty"`
	Meta  EnvMeta `json:"meta"`
}

// EnvMeta contains per-response metadata.
type EnvMeta struct {
	RequestID string `json:"request_id"`
	Timestamp string `json:"timestamp"`
}

type envError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// --- TimelineEntry ---

// TimelineEntry mirrors the hud.TimelineEntry shape for event log results.
type TimelineEntry struct {
	Timestamp time.Time       `json:"timestamp"`
	EventType string          `json:"event_type"`
	AgentID   string          `json:"agent_id,omitempty"`
	AgentType string          `json:"agent_type,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// SessionTraceError identifies a partial source failure in a session trace.
type SessionTraceError struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

// SessionTraceEntry is a daemon audit trace entry associated with a session.
type SessionTraceEntry struct {
	Timestamp     string `json:"timestamp"`
	AgentID       string `json:"agent_id,omitempty"`
	AgentType     string `json:"agent_type,omitempty"`
	Server        string `json:"server"`
	Tool          string `json:"tool"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	Target        string `json:"target,omitempty"`
	Cached        bool   `json:"cached,omitempty"`
	PipelineStage string `json:"pipeline_stage,omitempty"`
	DurationMs    int64  `json:"duration_ms"`
	RouteMs       int64  `json:"route_ms,omitempty"`
	BuildMs       int64  `json:"build_ms,omitempty"`
	ExecuteMs     int64  `json:"execute_ms,omitempty"`
	SendMs        int64  `json:"send_ms,omitempty"`
	RecvMs        int64  `json:"recv_ms,omitempty"`
}

// SessionTraceResponse is the mobile-friendly session trace payload. It keeps
// partial source errors in-band so companion clients can still render useful
// session context when one upstream source is temporarily unavailable.
type SessionTraceResponse struct {
	Session      *bridge.SessionInfo `json:"session,omitempty"`
	AgentID      string              `json:"agent_id,omitempty"`
	SessionID    string              `json:"session_id"`
	Entries      []map[string]any    `json:"entries"`
	Events       []TimelineEntry     `json:"events"`
	Traces       []SessionTraceEntry `json:"traces"`
	TraceEnabled bool                `json:"trace_enabled"`
	TracePath    string              `json:"trace_path,omitempty"`
	Errors       []SessionTraceError `json:"errors,omitempty"`
	RetrievedAt  string              `json:"retrieved_at"`
}

// --- Topology types ---

// TopologyNode represents an agent in the topology graph.
type TopologyNode struct {
	AgentID     string `json:"agent_id"`
	Status      string `json:"status"`
	AgentType   string `json:"agent_type"`
	CurrentTask string `json:"current_task,omitempty"`
	Branch      string `json:"branch,omitempty"`
	PRUrl       string `json:"pr_url,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
}

// TopologyEdge represents a relationship between two agents.
type TopologyEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	EdgeType string `json:"edge_type"`
	Weight   int    `json:"weight"`
	Label    string `json:"label,omitempty"`
	Status   string `json:"status,omitempty"`
}

// TopologyCluster groups agents by namespace project.
type TopologyCluster struct {
	Project  string   `json:"project"`
	AgentIDs []string `json:"agent_ids"`
}

// TopologyGraph is the complete topology response.
type TopologyGraph struct {
	Nodes    []TopologyNode    `json:"nodes"`
	Edges    []TopologyEdge    `json:"edges"`
	Clusters []TopologyCluster `json:"clusters"`
}

// --- Control plane DTOs ---

type controlPlaneCostTopAgent struct {
	AgentID   string `json:"agent_id"`
	CallCount int64  `json:"call_count"`
	Errors    int64  `json:"errors"`
	Denied    int64  `json:"denied"`
	Cached    int64  `json:"cached"`
}

type controlPlaneCostTopServer struct {
	Server    string `json:"server"`
	CallCount int64  `json:"call_count"`
	Errors    int64  `json:"errors"`
}

type controlPlaneCost struct {
	Enabled         bool                       `json:"enabled"`
	Timestamp       string                     `json:"timestamp,omitempty"`
	TotalCalls      int64                      `json:"total_calls"`
	TotalErrors     int64                      `json:"total_errors"`
	TotalDenied     int64                      `json:"total_denied"`
	TotalCached     int64                      `json:"total_cached"`
	TotalDurationMs int64                      `json:"total_duration_ms"`
	TopAgent        *controlPlaneCostTopAgent  `json:"top_agent,omitempty"`
	TopServer       *controlPlaneCostTopServer `json:"top_server,omitempty"`
}

type controlPlaneRBAC struct {
	Enabled         bool   `json:"enabled"`
	DefaultPolicy   string `json:"default_policy,omitempty"`
	RoleCount       int    `json:"role_count"`
	BindingCount    int    `json:"binding_count"`
	GlobalDenyCount int    `json:"global_deny_count"`
	RateLimitCount  int    `json:"rate_limit_count"`
	DeniedCount     int    `json:"denied_count"`
}

type controlPlaneOTel struct {
	OTLPConfigured  bool   `json:"otlp_configured"`
	OTLPEndpoint    string `json:"otlp_endpoint,omitempty"`
	JSONLogsEnabled bool   `json:"json_logs_enabled"`
	TracedServers   int    `json:"traced_servers"`
	TotalServers    int    `json:"total_servers"`
	TraceCoverage   string `json:"trace_coverage,omitempty"`
}

type controlPlaneHealth struct {
	TotalServers    int `json:"total_servers"`
	HealthyServers  int `json:"healthy_servers"`
	DegradedServers int `json:"degraded_servers"`
	DownServers     int `json:"down_servers"`
	IdleServers     int `json:"idle_servers"`
	HubTargets      int `json:"hub_targets"`
	LocalTargets    int `json:"local_targets"`
	Unavailable     int `json:"unavailable_targets"`
}

// --- Task DTOs ---

type taskCounts struct {
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Blocked    int `json:"blocked"`
	Completed  int `json:"completed"`
}

type taskDTO struct {
	ID             string              `json:"id"`
	SessionID      string              `json:"session_id"`
	AgentID        string              `json:"agent_id"`
	Namespace      string              `json:"namespace"`
	Project        string              `json:"project,omitempty"`
	Title          string              `json:"title"`
	Context        string              `json:"context"`
	Priority       string              `json:"priority"`
	Status         string              `json:"status"`
	TaskKind       string              `json:"task_kind"`
	SourcePlatform string              `json:"source_platform,omitempty"`
	SourceKind     string              `json:"source_kind,omitempty"`
	SourceID       string              `json:"source_id,omitempty"`
	NativeKey      string              `json:"native_key,omitempty"`
	PipelineRef    *bridge.PipelineRef `json:"pipeline_ref,omitempty"`
	WorkflowID     string              `json:"workflow_id,omitempty"`
	IsProjected    bool                `json:"is_projected,omitempty"`
	Tags           []string            `json:"tags"`
	BlockedBy      []string            `json:"blocked_by"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
}

// --- Workflow DTOs ---

type workflowEventDTO struct {
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	Timestamp string `json:"timestamp"`
	StepID    string `json:"step_id,omitempty"`
	StepName  string `json:"step_name,omitempty"`
	Details   string `json:"details,omitempty"`
}

// --- Presence DTOs ---

type presenceSummary struct {
	ActiveAgents  int `json:"active_agents"`
	IdleAgents    int `json:"idle_agents"`
	OfflineAgents int `json:"offline_agents"`
	TotalAgents   int `json:"total_agents"`
	ClaimCount    int `json:"claim_count"`
	WorktreeCount int `json:"worktree_count"`
}

// --- Memory DTOs ---

type memoryItemDTO struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Content      string  `json:"content,omitempty"`
	Tier         string  `json:"tier"`
	Importance   string  `json:"importance"`
	ImportanceSc float64 `json:"importance_score"`
	Tokens       int     `json:"tokens"`
	Status       string  `json:"status,omitempty"`
	Category     string  `json:"category,omitempty"`
	AccessedAt   string  `json:"accessed_at,omitempty"`
	LastAccessed string  `json:"last_accessed,omitempty"`
}

type streamEntryDTO struct {
	ID        string  `json:"id"`
	EntryType string  `json:"entry_type"`
	AgentID   string  `json:"agent_id"`
	Agent     string  `json:"agent"`
	Namespace string  `json:"namespace"`
	Title     string  `json:"title"`
	Content   string  `json:"content,omitempty"`
	Timestamp string  `json:"timestamp"`
	Score     float64 `json:"score,omitempty"`
}

// --- Mobile topology DTOs ---

type topologyNodeDTO struct {
	AgentID     string `json:"agent_id"`
	Status      string `json:"status"`
	AgentType   string `json:"agent_type"`
	CurrentTask string `json:"current_task,omitempty"`
	Branch      string `json:"branch,omitempty"`
	PRURL       string `json:"pr_url,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
}

type topologyEdgeDTO struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	EdgeType string `json:"edge_type"`
	Weight   int    `json:"weight"`
	Label    string `json:"label,omitempty"`
	Status   string `json:"status,omitempty"`
}

type topologyClusterDTO struct {
	Project  string   `json:"project"`
	AgentIDs []string `json:"agent_ids"`
}

// --- Graph DTOs ---

type graphEntityDTO struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	EntityType  string         `json:"entity_type"`
	Description string         `json:"description,omitempty"`
	Namespace   string         `json:"namespace,omitempty"`
	Properties  map[string]any `json:"properties"`
}

// --- Reasoning DTOs ---

type reasoningStepDTO struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Confidence  float64 `json:"confidence"`
	Evidence    string  `json:"evidence,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

type reasoningChainDTO struct {
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	Status      string             `json:"status"`
	StepCount   int                `json:"step_count"`
	Confidence  float64            `json:"confidence,omitempty"`
	CreatedAt   string             `json:"created_at"`
	CompletedAt string             `json:"completed_at,omitempty"`
	Steps       []reasoningStepDTO `json:"steps,omitempty"`
}

// --- Alert policy ---

// AlertPolicyEntry describes a single event type's notification policy.
type AlertPolicyEntry struct {
	EventType         string   `json:"event_type"`
	Severity          string   `json:"severity"`
	InterruptionLevel string   `json:"interruption_level"`
	Title             string   `json:"title"`
	AllowedActions    []string `json:"allowed_actions"`
	Conditional       bool     `json:"conditional"`
}

// --- Namespace summary ---

type namespaceSummary struct {
	Namespace    string `json:"namespace"`
	SessionCount int    `json:"session_count"`
	AgentCount   int    `json:"agent_count"`
	ActiveAgents int    `json:"active_agents"`
}

// --- Unified agents ---

type unifiedAgent struct {
	AgentID          string   `json:"agent_id"`
	AgentType        string   `json:"agent_type"`
	Status           string   `json:"status"`
	Source           string   `json:"source"`
	Description      string   `json:"description"`
	CurrentTask      string   `json:"current_task"`
	Branch           string   `json:"branch"`
	LastHeartbeat    string   `json:"last_heartbeat"`
	SessionID        string   `json:"session_id,omitempty"`
	Namespace        string   `json:"namespace,omitempty"`
	SessionStatus    string   `json:"session_status,omitempty"`
	SessionStarted   string   `json:"session_started_at,omitempty"`
	EntryCount       int      `json:"entry_count"`
	TotalTokens      int      `json:"total_tokens"`
	SpawnID          string   `json:"spawn_id,omitempty"`
	SpawnStatus      string   `json:"spawn_status,omitempty"`
	Project          string   `json:"project,omitempty"`
	ActiveFileCount  int      `json:"active_file_count"`
	NeedsAttention   bool     `json:"needs_attention"`
	AttentionReasons []string `json:"attention_reasons,omitempty"`
	TaskCount        int      `json:"task_count"`
	BlockedTasks     int      `json:"blocked_tasks"`
	ClaimCount       int      `json:"claim_count"`
	PipelineCount    int      `json:"pipeline_count"`
	PipelineStatus   string   `json:"pipeline_status,omitempty"`
	HeartbeatAgeSec  int      `json:"heartbeat_age_seconds,omitempty"`
	SessionAgeSec    int      `json:"session_age_seconds,omitempty"`
	TelemetryStatus  string   `json:"telemetry_status,omitempty"`
	HasPresence      bool     `json:"has_presence"`
	HasSession       bool     `json:"has_session"`
}

type unifiedAgentsSummary struct {
	TotalAgents   int `json:"total_agents"`
	ActiveAgents  int `json:"active_agents"`
	IdleAgents    int `json:"idle_agents"`
	OfflineAgents int `json:"offline_agents"`
	SpawnedAgents int `json:"spawned_agents"`
	WithSessions  int `json:"with_sessions"`
}
