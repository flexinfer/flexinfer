package mobile

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
)

type taskFeedCaller struct {
	tasks []bridge.TaskInfo
}

func (c *taskFeedCaller) Call(string, any) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected Call")
}

func (c *taskFeedCaller) CallWithTimeout(string, any, time.Duration) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected CallWithTimeout")
}

func (c *taskFeedCaller) CallTool(name string, _ map[string]any) (json.RawMessage, error) {
	if name != "agent_context__agent_task_list" {
		return nil, fmt.Errorf("unexpected tool %s", name)
	}
	return json.Marshal(map[string]any{"tasks": c.tasks})
}

func (c *taskFeedCaller) CallToolWithTimeout(name string, args map[string]any, _ time.Duration) (json.RawMessage, error) {
	return c.CallTool(name, args)
}

func (c *taskFeedCaller) CircuitOpen() bool { return false }
func (c *taskFeedCaller) Close() error      { return nil }

type failingTaskFeedCaller struct{}

func (c *failingTaskFeedCaller) Call(string, any) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected Call")
}

func (c *failingTaskFeedCaller) CallWithTimeout(string, any, time.Duration) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected CallWithTimeout")
}

func (c *failingTaskFeedCaller) CallTool(name string, _ map[string]any) (json.RawMessage, error) {
	if name != "agent_context__agent_task_list" {
		return nil, fmt.Errorf("unexpected tool %s", name)
	}
	return nil, fmt.Errorf("backend unavailable")
}

func (c *failingTaskFeedCaller) CallToolWithTimeout(name string, args map[string]any, _ time.Duration) (json.RawMessage, error) {
	return c.CallTool(name, args)
}

func (c *failingTaskFeedCaller) CircuitOpen() bool { return false }
func (c *failingTaskFeedCaller) Close() error      { return nil }

type unexpectedTaskFeedCaller struct{}

func (c *unexpectedTaskFeedCaller) Call(string, any) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected Call")
}

func (c *unexpectedTaskFeedCaller) CallWithTimeout(string, any, time.Duration) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected CallWithTimeout")
}

func (c *unexpectedTaskFeedCaller) CallTool(name string, _ map[string]any) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected tool call %s", name)
}

func (c *unexpectedTaskFeedCaller) CallToolWithTimeout(name string, args map[string]any, _ time.Duration) (json.RawMessage, error) {
	return c.CallTool(name, args)
}

func (c *unexpectedTaskFeedCaller) CircuitOpen() bool { return false }
func (c *unexpectedTaskFeedCaller) Close() error      { return nil }

func testMobileTaskFeedSnapshot() monitor.FleetSnapshot {
	now := "2026-03-23T20:30:00Z"
	return monitor.FleetSnapshot{
		Sessions: []bridge.SessionInfo{
			{
				ID:          "sess-1",
				AgentID:     "agent-1",
				Namespace:   "ns-1",
				StartedAt:   "2026-03-23T19:00:00Z",
				Status:      "active",
				Description: "Claude session",
			},
			{
				ID:          "sess-2",
				AgentID:     "agent-2",
				Namespace:   "ns-2",
				StartedAt:   "2026-03-23T19:10:00Z",
				Status:      "active",
				Description: "Codex session",
			},
		},
		Agents: []bridge.PresenceInfo{
			{
				AgentID:       "agent-1",
				SessionID:     "sess-1",
				Status:        "active",
				AgentType:     "claude-code",
				Description:   "Claude agent",
				CurrentTask:   "Implement the API route",
				LastHeartbeat: now,
			},
			{
				AgentID:       "agent-2",
				SessionID:     "sess-2",
				Status:        "active",
				AgentType:     "codex",
				Description:   "Codex agent",
				CurrentTask:   "Refine task projection",
				LastHeartbeat: now,
			},
			{
				AgentID:       "agent-3",
				SessionID:     "sess-3",
				Status:        "offline",
				AgentType:     "proxy",
				Description:   "Stale proxy",
				CurrentTask:   "Should not project",
				LastHeartbeat: now,
			},
		},
		Tasks: []bridge.TaskInfo{
			{
				ID:          "task-1",
				SessionID:   "sess-1",
				AgentID:     "agent-1",
				Namespace:   "ns-1",
				Title:       "Implement the API route",
				Context:     "Explicit task",
				Priority:    "high",
				Status:      "pending",
				CreatedAt:   now,
				UpdatedAt:   now,
				WorkflowID:  "wf-1",
				PipelineRef: &bridge.PipelineRef{ID: 101, Project: "services/loom-core", Ref: "main", WebURL: "https://example.invalid/pipelines/101"},
			},
			{
				ID:        "task-2",
				SessionID: "sess-2",
				AgentID:   "agent-2",
				Namespace: "ns-2",
				Title:     "Review pipeline output",
				Context:   "Blocked task",
				Priority:  "medium",
				Status:    "blocked",
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}
}

func TestHandleMobileTasksProjectsCurrentTaskAndPreservesExplicitMetadata(t *testing.T) {
	deps := newTestMockDeps()
	deps.agent = bridge.NewAgentBridge(&taskFeedCaller{tasks: testMobileTaskFeedSnapshot().Tasks})
	deps.monitors = Monitors{Fleet: &monitor.FleetMonitor{}}
	deps.monitors.Fleet.Update(testMobileTaskFeedSnapshot())
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/tasks", d.handleMobileTasks)

	req := newAuthRequest("GET", "/api/mobile/v1/tasks")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}

	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got %T", env.Data)
	}

	tasks, ok := data["tasks"].([]any)
	if !ok {
		t.Fatalf("expected tasks array, got %T", data["tasks"])
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}

	taskByTitle := map[string]map[string]any{}
	for _, raw := range tasks {
		task, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("expected task object, got %T", raw)
		}
		title, _ := task["title"].(string)
		taskByTitle[title] = task
	}

	explicit := taskByTitle["Implement the API route"]
	if explicit == nil {
		t.Fatal("missing explicit task")
	}
	if explicit["task_kind"] != "explicit" {
		t.Fatalf("expected explicit task_kind, got %v", explicit["task_kind"])
	}
	if explicit["source_platform"] != "agent_context" {
		t.Fatalf("expected explicit source_platform=agent_context, got %v", explicit["source_platform"])
	}
	if explicit["workflow_id"] != "wf-1" {
		t.Fatalf("expected workflow_id to be preserved, got %v", explicit["workflow_id"])
	}
	if explicit["project"] != "services/loom-core" {
		t.Fatalf("expected project services/loom-core, got %v", explicit["project"])
	}
	if got, ok := explicit["pipeline_ref"].(map[string]any); !ok || got["id"] != float64(101) {
		t.Fatalf("expected pipeline_ref to be preserved, got %#v", explicit["pipeline_ref"])
	}

	projected := taskByTitle["Refine task projection"]
	if projected == nil {
		t.Fatal("missing projected task")
	}
	if projected["task_kind"] != "projected" {
		t.Fatalf("expected projected task_kind, got %v", projected["task_kind"])
	}
	if projected["source_platform"] != "codex" {
		t.Fatalf("expected projected source_platform=codex, got %v", projected["source_platform"])
	}
	if projected["session_id"] != "sess-2" {
		t.Fatalf("expected projected session_id=sess-2, got %v", projected["session_id"])
	}
	if projected["project"] != "ns-2" {
		t.Fatalf("expected projected project ns-2, got %v", projected["project"])
	}

	counts, ok := data["counts"].(map[string]any)
	if !ok {
		t.Fatalf("expected counts map, got %T", data["counts"])
	}
	if counts["pending"] != float64(1) || counts["blocked"] != float64(1) || counts["in_progress"] != float64(1) || counts["completed"] != float64(0) {
		t.Fatalf("unexpected counts: %#v", counts)
	}
}

func TestHandleMobileTasks_UsesFleetSnapshotWithoutCallingTaskBackend(t *testing.T) {
	snap := testMobileTaskFeedSnapshot()
	deps := newTestMockDeps()
	deps.agent = bridge.NewAgentBridge(&unexpectedTaskFeedCaller{})
	deps.monitors = Monitors{Fleet: &monitor.FleetMonitor{}}
	deps.monitors.Fleet.Update(snap)
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/tasks", d.handleMobileTasks)

	req := newAuthRequest("GET", "/api/mobile/v1/tasks")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleMobileAgentsUsesNormalizedTaskFeed(t *testing.T) {
	snap := testMobileTaskFeedSnapshot()
	deps := newTestMockDeps()
	deps.monitors = Monitors{Fleet: &monitor.FleetMonitor{}}
	deps.monitors.Fleet.Update(snap)
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/agents", d.handleMobileAgents)

	req := newAuthRequest("GET", "/api/mobile/v1/agents")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}

	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got %T", env.Data)
	}

	agents, ok := data["agents"].([]any)
	if !ok {
		t.Fatalf("expected agents array, got %T", data["agents"])
	}
	byID := map[string]map[string]any{}
	for _, raw := range agents {
		agent, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("expected agent object, got %T", raw)
		}
		id, _ := agent["agent_id"].(string)
		byID[id] = agent
	}

	if got := byID["agent-1"]["task_count"]; got != float64(1) {
		t.Fatalf("expected agent-1 task_count=1, got %v", got)
	}
	if got := byID["agent-1"]["blocked_tasks"]; got != float64(0) {
		t.Fatalf("expected agent-1 blocked_tasks=0, got %v", got)
	}
	if got := byID["agent-2"]["task_count"]; got != float64(2) {
		t.Fatalf("expected agent-2 task_count=2, got %v", got)
	}
	if got := byID["agent-2"]["blocked_tasks"]; got != float64(1) {
		t.Fatalf("expected agent-2 blocked_tasks=1, got %v", got)
	}
	if got := byID["agent-1"]["project"]; got != "ns-1" {
		t.Fatalf("expected agent-1 project=ns-1, got %v", got)
	}
	if got := byID["agent-2"]["project"]; got != "ns-2" {
		t.Fatalf("expected agent-2 project=ns-2, got %v", got)
	}
	if _, ok := byID["agent-3"]; !ok {
		t.Fatal("expected offline agent to remain in response")
	}
}

func TestHandleMobileAgents_IgnoresHistoricalSessionOnlyEntries(t *testing.T) {
	snap := monitor.FleetSnapshot{
		Sessions: []bridge.SessionInfo{
			{
				ID:          "sess-ended",
				AgentID:     "agent-1",
				Namespace:   "legacy/ns",
				StartedAt:   "2026-03-20T19:00:00Z",
				Status:      "ended",
				Description: "old session",
			},
			{
				ID:          "sess-live",
				AgentID:     "agent-2",
				Namespace:   "live/ns",
				StartedAt:   "2026-03-25T19:00:00Z",
				Status:      "active",
				Description: "live session",
			},
			{
				ID:          "sess-summarized",
				AgentID:     "agent-3",
				Namespace:   "summary/ns",
				StartedAt:   "2026-03-25T18:00:00Z",
				Status:      "summarized",
				Description: "summary session",
			},
		},
		Agents: []bridge.PresenceInfo{
			{
				AgentID:       "agent-1",
				SessionID:     "sess-ended",
				Status:        "active",
				AgentType:     "claude-code",
				Description:   "live presence",
				LastHeartbeat: "2026-03-25T19:30:00Z",
			},
		},
	}

	deps := newTestMockDeps()
	deps.monitors = Monitors{Fleet: &monitor.FleetMonitor{}}
	deps.monitors.Fleet.Update(snap)
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/agents", d.handleMobileAgents)

	req := newAuthRequest("GET", "/api/mobile/v1/agents")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}

	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got %T", env.Data)
	}

	agents, ok := data["agents"].([]any)
	if !ok {
		t.Fatalf("expected agents array, got %T", data["agents"])
	}
	if len(agents) != 2 {
		t.Fatalf("expected one live presence agent and one live session agent, got %#v", agents)
	}

	byID := map[string]map[string]any{}
	for _, raw := range agents {
		agent, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("expected agent object, got %T", raw)
		}
		id, _ := agent["agent_id"].(string)
		byID[id] = agent
	}

	if _, ok := byID["agent-3"]; ok {
		t.Fatal("did not expect summarized session-only agent in response")
	}
	if got := byID["agent-1"]["session_status"]; got != nil {
		t.Fatalf("expected stale ended session metadata to be omitted for active presence, got %v", got)
	}
	if got := byID["agent-2"]["source"]; got != "session_only" {
		t.Fatalf("expected active session-only agent to remain visible, got %v", got)
	}

	summary, ok := data["summary"].(map[string]any)
	if !ok {
		t.Fatalf("expected summary map, got %T", data["summary"])
	}
	if got := summary["offline_agents"]; got != float64(0) {
		t.Fatalf("expected no offline historical agents in summary, got %v", got)
	}
}

func TestHandleMobileAgents_BackfillsProjectFromWorktreeAndClaims(t *testing.T) {
	snap := monitor.FleetSnapshot{
		Agents: []bridge.PresenceInfo{
			{
				AgentID:       "claude-code-1",
				Status:        "active",
				AgentType:     "claude-code",
				Description:   "working without a live session",
				LastHeartbeat: "2026-03-25T19:30:00Z",
				ActiveFiles: []string{
					"/Users/cblevins/workspace/services/loom-core/internal/hud/domain/mobile/handler_agents.go",
				},
			},
		},
		FileClaims: []bridge.FileClaimInfo{
			{
				ID:        "claim-1",
				AgentID:   "claude-code-1",
				FilePath:  "/Users/cblevins/workspace/services/loom-core/apps/loom-companion-ios/Sources/LoomCompanion/Views/Agents/AgentsListView.swift",
				CreatedAt: "2026-03-25T19:20:00Z",
			},
		},
		Worktrees: []bridge.WorktreeInfo{
			{
				AssignmentID: "wt-1",
				AgentID:      "claude-code-1",
				WorktreePath: "/Users/cblevins/workspace/services/loom-core/.worktrees/info-arch-mobile-hud",
				Branch:       "codex/info-arch-mobile-hud",
				CreatedAt:    "2026-03-25T19:10:00Z",
			},
		},
	}

	deps := newTestMockDeps()
	deps.monitors = Monitors{Fleet: &monitor.FleetMonitor{}}
	deps.monitors.Fleet.Update(snap)
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/agents", d.handleMobileAgents)

	req := newAuthRequest("GET", "/api/mobile/v1/agents")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}

	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got %T", env.Data)
	}

	agents, ok := data["agents"].([]any)
	if !ok || len(agents) != 1 {
		t.Fatalf("expected one agent, got %#v", data["agents"])
	}

	agent, ok := agents[0].(map[string]any)
	if !ok {
		t.Fatalf("expected agent object, got %T", agents[0])
	}
	if got := agent["project"]; got != "services/loom-core" {
		t.Fatalf("expected project services/loom-core, got %v", got)
	}
	if got := agent["branch"]; got != "codex/info-arch-mobile-hud" {
		t.Fatalf("expected worktree branch backfill, got %v", got)
	}
}

func TestHandleMobileTasks_ReturnsUpstreamErrorWhenNoTaskDataIsAvailable(t *testing.T) {
	deps := newTestMockDeps()
	deps.agent = bridge.NewAgentBridge(&failingTaskFeedCaller{})
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/tasks", d.handleMobileTasks)

	req := newAuthRequest("GET", "/api/mobile/v1/tasks")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.OK {
		t.Fatal("expected ok=false")
	}
	errBody, ok := env.Error.(map[string]any)
	if !ok {
		t.Fatalf("expected error map, got %T", env.Error)
	}
	if errBody["code"] != "upstream_unavailable" {
		t.Fatalf("expected upstream_unavailable error, got %#v", errBody)
	}
}
