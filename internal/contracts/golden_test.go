// Package contracts provides golden-file contract tests for API response shapes,
// SSE event payloads, and CLI output formats. When a field is added, renamed,
// or removed the golden file diff surfaces the change in code review, preventing
// silent divergence with sibling repos (loom VS Code extension, loom-zed).
package contracts

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

var updateGolden = flag.Bool("update-golden", false, "update golden files with actual output")

// testdataDir returns the absolute path to the testdata directory.
func testdataDir(t *testing.T) string {
	t.Helper()
	// testdata is relative to this test file.
	dir, err := filepath.Abs("testdata")
	require.NoError(t, err)
	return dir
}

// assertGolden compares got against the golden file at testdata/<name>.golden.
// If -update-golden is set, it writes got to the golden file instead.
func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()

	goldenPath := filepath.Join(testdataDir(t), name+".golden")

	if *updateGolden {
		err := os.MkdirAll(filepath.Dir(goldenPath), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(goldenPath, got, 0o644)
		require.NoError(t, err)
		t.Logf("updated golden file: %s", goldenPath)
		return
	}

	expected, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "golden file missing; run with -update-golden to create: %s", goldenPath)

	assert.JSONEq(t, string(expected), string(got),
		"golden file mismatch for %s.\nRun with -update-golden to accept changes.", name)
}

// marshalIndent is a helper that marshals v with indentation for human-readable diffs.
// Appends a trailing newline to match end-of-file-fixer expectations.
func marshalIndent(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	return append(data, '\n')
}

// ---------------------------------------------------------------------------
// Mobile Envelope Contract
// ---------------------------------------------------------------------------

// mobileEnvelope mirrors the hud.mobileEnvelope struct (unexported in hud package).
// We replicate it here to validate the contract shape independently.
type mobileEnvelope struct {
	OK    bool    `json:"ok"`
	Data  any     `json:"data,omitempty"`
	Error any     `json:"error,omitempty"`
	Meta  mobMeta `json:"meta"`
}

type mobMeta struct {
	RequestID string `json:"request_id"`
	Timestamp string `json:"timestamp"`
}

type mobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func TestMobileEnvelopeContract(t *testing.T) {
	t.Run("success_envelope", func(t *testing.T) {
		env := mobileEnvelope{
			OK:   true,
			Data: map[string]any{"pong": true},
			Meta: mobMeta{
				RequestID: "req_0000000000000000",
				Timestamp: "2025-01-15T10:30:00Z",
			},
		}
		assertGolden(t, "mobile_envelope", marshalIndent(t, env))
	})

	t.Run("error_envelope", func(t *testing.T) {
		env := mobileEnvelope{
			OK: false,
			Error: mobError{
				Code:    "upstream_error",
				Message: "failed to list sessions",
			},
			Meta: mobMeta{
				RequestID: "req_0000000000000000",
				Timestamp: "2025-01-15T10:30:00Z",
			},
		}
		assertGolden(t, "mobile_envelope_error", marshalIndent(t, env))
	})
}

// ---------------------------------------------------------------------------
// Mobile Dashboard Contract
// ---------------------------------------------------------------------------

func TestMobileDashboardContract(t *testing.T) {
	dashboard := map[string]any{
		"daemon_running":  true,
		"server_count":    12,
		"active_sessions": 3,
		"active_agents":   2,
		"idle_agents":     1,
		"offline_agents":  0,
		"updated_at":      "2025-01-15T10:30:00Z",
		"health": map[string]any{
			"total_servers":    12,
			"healthy_servers":  10,
			"degraded_servers": 1,
			"down_servers":     1,
			"idle_servers":     0,
		},
		"coordination": map[string]any{
			"summary": map[string]any{
				"active_namespaces":        2,
				"namespaces_at_risk":       0,
				"agents_needing_attention": 0,
				"shared_branches":          1,
				"conflict_files":           0,
				"cross_agent_blockers":     0,
				"orphan_tasks":             0,
				"idle_claim_holders":       0,
				"merge_ready_branches":     0,
			},
			"attention_agents": []any{},
			"risky_namespaces": []any{},
			"active_blockers":  []any{},
			"top_relations":    []any{},
			"attention_lanes": []any{
				map[string]any{
					"type":     "namespace",
					"id":       "services/loom-core/mobile",
					"label":    "Work lane",
					"route":    "work",
					"scope":    "3 tasks",
					"summary":  "blocked tasks",
					"severity": "critical",
				},
			},
		},
		"recent_timeline": []any{},
	}
	assertGolden(t, "mobile_dashboard", marshalIndent(t, dashboard))
}

// ---------------------------------------------------------------------------
// Mobile Agents Contract
// ---------------------------------------------------------------------------

// unifiedAgent mirrors the hud.unifiedAgent struct.
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
}

// unifiedAgentsSummary mirrors the hud.unifiedAgentsSummary struct.
type unifiedAgentsSummary struct {
	TotalAgents   int `json:"total_agents"`
	ActiveAgents  int `json:"active_agents"`
	IdleAgents    int `json:"idle_agents"`
	OfflineAgents int `json:"offline_agents"`
	SpawnedAgents int `json:"spawned_agents"`
	WithSessions  int `json:"with_sessions"`
}

func TestMobileAgentsContract(t *testing.T) {
	agents := []unifiedAgent{
		{
			AgentID:         "claude-code-1",
			AgentType:       "claude-code",
			Status:          "active",
			Source:          "presence",
			Description:     "Implementing feature X",
			CurrentTask:     "Write unit tests",
			Branch:          "feat/feature-x",
			LastHeartbeat:   "2025-01-15T10:29:55Z",
			SessionID:       "sess_abc123",
			Namespace:       "project/feature-x",
			SessionStatus:   "active",
			SessionStarted:  "2025-01-15T10:00:00Z",
			EntryCount:      42,
			TotalTokens:     8500,
			ActiveFileCount: 3,
			NeedsAttention:  false,
			TaskCount:       5,
			BlockedTasks:    1,
			ClaimCount:      2,
		},
		{
			AgentID:        "gemini-1",
			AgentType:      "gemini",
			Status:         "idle",
			Source:         "presence",
			Description:    "Code review agent",
			LastHeartbeat:  "2025-01-15T10:25:00Z",
			NeedsAttention: false,
		},
	}
	summary := unifiedAgentsSummary{
		TotalAgents:  2,
		ActiveAgents: 1,
		IdleAgents:   1,
		WithSessions: 1,
	}
	resp := map[string]any{
		"agents":  agents,
		"summary": summary,
	}
	assertGolden(t, "mobile_agents", marshalIndent(t, resp))
}

// ---------------------------------------------------------------------------
// Mobile Sessions Contract
// ---------------------------------------------------------------------------

func TestMobileSessionsContract(t *testing.T) {
	sessions := []bridge.SessionInfo{
		{
			ID:          "sess_abc123",
			AgentID:     "claude-code-1",
			Namespace:   "project/feature-x",
			StartedAt:   "2025-01-15T10:00:00Z",
			Status:      "active",
			Description: "Working on feature X",
			EntryCount:  42,
			TotalTokens: 8500,
		},
		{
			ID:          "sess_def456",
			AgentID:     "gemini-1",
			Namespace:   "project/review",
			StartedAt:   "2025-01-15T09:00:00Z",
			EndedAt:     "2025-01-15T09:45:00Z",
			Status:      "ended",
			Description: "Code review session",
			EntryCount:  15,
			TotalTokens: 3200,
		},
	}
	resp := map[string]any{
		"sessions": sessions,
	}
	assertGolden(t, "mobile_sessions", marshalIndent(t, resp))
}

// ---------------------------------------------------------------------------
// Mobile Tasks Contract
// ---------------------------------------------------------------------------

// mobileTaskDTO mirrors hud.mobileTaskDTO.
type mobileTaskDTO struct {
	ID        string   `json:"id"`
	SessionID string   `json:"session_id"`
	AgentID   string   `json:"agent_id"`
	Namespace string   `json:"namespace"`
	Title     string   `json:"title"`
	Context   string   `json:"context"`
	Priority  string   `json:"priority"`
	Status    string   `json:"status"`
	Tags      []string `json:"tags"`
	BlockedBy []string `json:"blocked_by"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

// mobileTaskCounts mirrors hud.mobileTaskCounts.
type mobileTaskCounts struct {
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Blocked    int `json:"blocked"`
	Completed  int `json:"completed"`
}

func TestMobileTasksContract(t *testing.T) {
	tasks := []mobileTaskDTO{
		{
			ID:        "task_001",
			SessionID: "sess_abc123",
			AgentID:   "claude-code-1",
			Namespace: "project/feature-x",
			Title:     "Implement golden test framework",
			Context:   "Add contract tests for API response shapes",
			Priority:  "high",
			Status:    "in_progress",
			Tags:      []string{"testing", "contracts"},
			BlockedBy: []string{},
			CreatedAt: "2025-01-15T10:05:00Z",
			UpdatedAt: "2025-01-15T10:20:00Z",
		},
		{
			ID:        "task_002",
			SessionID: "sess_abc123",
			AgentID:   "claude-code-1",
			Namespace: "project/feature-x",
			Title:     "Add SSE event golden files",
			Context:   "Capture SSE event payload shapes",
			Priority:  "medium",
			Status:    "pending",
			Tags:      []string{"testing"},
			BlockedBy: []string{"task_001"},
			CreatedAt: "2025-01-15T10:06:00Z",
			UpdatedAt: "2025-01-15T10:06:00Z",
		},
	}
	counts := mobileTaskCounts{
		Pending:    1,
		InProgress: 1,
	}
	resp := map[string]any{
		"tasks":  tasks,
		"counts": counts,
		"coordination": map[string]any{
			"summary": map[string]any{
				"active_namespaces":        1,
				"namespaces_at_risk":       0,
				"agents_needing_attention": 0,
				"shared_branches":          0,
				"conflict_files":           0,
				"cross_agent_blockers":     0,
				"orphan_tasks":             0,
				"idle_claim_holders":       0,
				"merge_ready_branches":     0,
			},
			"blockers":         []any{},
			"risky_namespaces": []any{},
		},
	}
	assertGolden(t, "mobile_tasks", marshalIndent(t, resp))
}

// ---------------------------------------------------------------------------
// SSE Events Contract
// ---------------------------------------------------------------------------

func TestSSEEventsContract(t *testing.T) {
	fixedTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)

	events := map[string]bridge.SSEEvent{
		"fleet": {
			ID:        "evt_fleet_001",
			Type:      "hud.fleet",
			Timestamp: fixedTime,
			Data:      json.RawMessage(`{"daemon_running":true,"server_count":12,"active_sessions":3,"active_agents":2,"idle_agents":1,"offline_agents":0}`),
		},
		"health": {
			ID:        "evt_health_001",
			Type:      "hud.health",
			Timestamp: fixedTime,
			Data:      json.RawMessage(`{"total_servers":12,"healthy_servers":10,"degraded_servers":1,"down_servers":1,"idle_servers":0}`),
		},
		"session_start": {
			ID:        "evt_sess_001",
			Type:      "hud.session.start",
			Timestamp: fixedTime,
			Data:      json.RawMessage(`{"session_id":"sess_abc123","agent_id":"claude-code-1","namespace":"project/feature-x","description":"Working on feature X"}`),
		},
		"session_end": {
			ID:        "evt_sess_002",
			Type:      "hud.session.end",
			Timestamp: fixedTime,
			Data:      json.RawMessage(`{"session_id":"sess_abc123","agent_id":"claude-code-1","namespace":"project/feature-x","summary":"Completed feature X implementation"}`),
		},
		"heartbeat": {
			ID:        "evt_hb_001",
			Type:      "hud.heartbeat",
			Timestamp: fixedTime,
			Data:      json.RawMessage(`{"agent_id":"claude-code-1","status":"active","current_task":"Writing tests","branch":"feat/feature-x"}`),
		},
	}

	assertGolden(t, "sse_events", marshalIndent(t, events))
}

// ---------------------------------------------------------------------------
// Bridge DTO Shape Contracts
// ---------------------------------------------------------------------------

func TestSessionInfoContract(t *testing.T) {
	s := bridge.SessionInfo{
		ID:          "sess_abc123",
		AgentID:     "claude-code-1",
		Namespace:   "project/feature-x",
		StartedAt:   "2025-01-15T10:00:00Z",
		EndedAt:     "2025-01-15T11:00:00Z",
		Status:      "ended",
		Description: "Working on feature X",
		PipelineRef: &bridge.PipelineRef{
			ID:      67890,
			Project: "services/loom-core",
			Ref:     "feat/feature-x",
		},
		EntryCount:  42,
		TotalTokens: 8500,
	}
	assertGolden(t, "dto_session_info", marshalIndent(t, s))
}

func TestTaskInfoContract(t *testing.T) {
	task := bridge.TaskInfo{
		ID:        "task_001",
		SessionID: "sess_abc123",
		AgentID:   "claude-code-1",
		Namespace: "project/feature-x",
		Title:     "Implement golden test framework",
		Context:   "Add contract tests for API response shapes",
		Priority:  "high",
		Status:    "in_progress",
		Tags:      []string{"testing", "contracts"},
		BlockedBy: []string{"task_000"},
		PipelineRef: &bridge.PipelineRef{
			ID:      12345,
			Project: "services/loom-core",
			Ref:     "feat/golden-tests",
			WebURL:  "https://gitlab.example.com/services/loom-core/-/pipelines/12345",
		},
		WorkflowID: "wf_build_and_test_001",
		CreatedAt:  "2025-01-15T10:05:00Z",
		UpdatedAt:  "2025-01-15T10:20:00Z",
	}
	assertGolden(t, "dto_task_info", marshalIndent(t, task))
}

func TestPresenceInfoContract(t *testing.T) {
	p := bridge.PresenceInfo{
		AgentID:       "claude-code-1",
		SessionID:     "sess_abc123",
		Status:        "active",
		AgentType:     "claude-code",
		Description:   "Working on feature X",
		CurrentTask:   "Writing unit tests",
		ActiveFiles:   []string{"internal/contracts/golden_test.go"},
		Branch:        "feat/feature-x",
		WorktreeID:    "wt_001",
		LastHeartbeat: "2025-01-15T10:29:55Z",
		RegisteredAt:  "2025-01-15T10:00:00Z",
	}
	assertGolden(t, "dto_presence_info", marshalIndent(t, p))
}

func TestEntityInfoContract(t *testing.T) {
	e := bridge.EntityInfo{
		ID:          "ent_001",
		Name:        "UserService",
		Type:        "service",
		EntityType:  "service",
		Description: "Handles user authentication and profile management",
		Namespace:   "project/auth",
		Properties:  map[string]any{"language": "go", "lines": 450},
	}
	assertGolden(t, "dto_entity_info", marshalIndent(t, e))
}

func TestEntityDetailContract(t *testing.T) {
	d := bridge.EntityDetail{
		ID:         "ent_001",
		Name:       "UserService",
		Type:       "service",
		EntityType: "service",
		Namespace:  "project/auth",
		Properties: map[string]any{"language": "go"},
		InboundRelations: []bridge.RelationInfo{
			{
				ID:           "rel_001",
				Source:       "ent_002",
				SourceName:   "APIGateway",
				Target:       "ent_001",
				TargetName:   "UserService",
				Type:         "depends_on",
				RelationType: "depends_on",
			},
		},
		OutboundRelations: []bridge.RelationInfo{
			{
				ID:           "rel_002",
				Source:       "ent_001",
				SourceName:   "UserService",
				Target:       "ent_003",
				TargetName:   "PostgresDB",
				Type:         "uses",
				RelationType: "uses",
			},
		},
	}
	assertGolden(t, "dto_entity_detail", marshalIndent(t, d))
}

func TestRelationInfoContract(t *testing.T) {
	r := bridge.RelationInfo{
		ID:           "rel_001",
		Source:       "ent_001",
		SourceName:   "UserService",
		Target:       "ent_002",
		TargetName:   "PostgresDB",
		Type:         "uses",
		RelationType: "uses",
	}
	assertGolden(t, "dto_relation_info", marshalIndent(t, r))
}

func TestContextInspectRequestContract(t *testing.T) {
	r := bridge.ContextInspectRequest{
		AgentID:   "claude-code-1",
		SessionID: "sess_abc123",
		Detail:    true,
		Limit:     200,
	}
	assertGolden(t, "dto_context_inspect_request", marshalIndent(t, r))
}

func TestNudgeQueuePolicyContract(t *testing.T) {
	p := bridge.NudgeQueuePolicy{
		DebounceMs:   500,
		Cap:          100,
		DropPolicy:   "drop_old",
		LanePriority: []string{"critical", "high", "normal", "low"},
	}
	assertGolden(t, "dto_nudge_queue_policy", marshalIndent(t, p))
}

func TestHeartbeatRequestContract(t *testing.T) {
	h := bridge.HeartbeatRequest{
		AgentID:     "claude-code-1",
		SessionID:   "sess_abc123",
		Status:      "active",
		AgentType:   "claude-code",
		Description: "Working on feature X",
		Namespace:   "project/feature-x",
		ActiveFiles: []string{"internal/contracts/golden_test.go"},
		CurrentTask: "Writing tests",
		Branch:      "feat/feature-x",
	}
	assertGolden(t, "dto_heartbeat_request", marshalIndent(t, h))
}
