package monitor

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/fleetview"
	"github.com/crb2nu/loom/internal/visibility/contracts/presence"
	"github.com/crb2nu/loom/internal/visibility/contracts/status"
)

func TestDetectConflicts_NoConflicts(t *testing.T) {
	claims := []bridge.FileClaimInfo{
		{FilePath: "/src/main.go", AgentID: "claude-1"},
		{FilePath: "/src/util.go", AgentID: "gemini-1"},
	}

	count, details := detectConflicts(claims)
	if count != 0 {
		t.Fatalf("expected 0 conflicts, got %d", count)
	}
	if len(details) != 0 {
		t.Fatalf("expected 0 details, got %d", len(details))
	}
}

func TestDetectConflicts_SingleConflict(t *testing.T) {
	claims := []bridge.FileClaimInfo{
		{FilePath: "/src/main.go", AgentID: "claude-1"},
		{FilePath: "/src/main.go", AgentID: "gemini-1"},
		{FilePath: "/src/util.go", AgentID: "claude-1"},
	}

	count, details := detectConflicts(claims)
	if count != 1 {
		t.Fatalf("expected 1 conflict, got %d", count)
	}
	if len(details) != 1 {
		t.Fatalf("expected 1 detail, got %d", len(details))
	}
	if details[0].Path != "/src/main.go" {
		t.Errorf("expected conflict path '/src/main.go', got %q", details[0].Path)
	}

	sort.Strings(details[0].Agents)
	if len(details[0].Agents) != 2 {
		t.Fatalf("expected 2 agents in conflict, got %d", len(details[0].Agents))
	}
	if details[0].Agents[0] != "claude-1" || details[0].Agents[1] != "gemini-1" {
		t.Errorf("unexpected agents: %v", details[0].Agents)
	}
}

func TestDetectConflicts_MultipleConflicts(t *testing.T) {
	claims := []bridge.FileClaimInfo{
		{FilePath: "/a.go", AgentID: "agent-1"},
		{FilePath: "/a.go", AgentID: "agent-2"},
		{FilePath: "/b.go", AgentID: "agent-1"},
		{FilePath: "/b.go", AgentID: "agent-3"},
		{FilePath: "/c.go", AgentID: "agent-2"},
		{FilePath: "/c.go", AgentID: "agent-3"},
		{FilePath: "/c.go", AgentID: "agent-1"},
	}

	count, details := detectConflicts(claims)
	if count != 3 {
		t.Fatalf("expected 3 conflicts, got %d", count)
	}
	if len(details) != 3 {
		t.Fatalf("expected 3 details, got %d", len(details))
	}
}

func TestDetectConflicts_LimitsDetailsToFive(t *testing.T) {
	var claims []bridge.FileClaimInfo
	for i := 0; i < 10; i++ {
		path := "/file" + string(rune('a'+i)) + ".go"
		claims = append(claims,
			bridge.FileClaimInfo{FilePath: path, AgentID: "agent-1"},
			bridge.FileClaimInfo{FilePath: path, AgentID: "agent-2"},
		)
	}

	count, details := detectConflicts(claims)
	if count != 10 {
		t.Fatalf("expected 10 conflicts, got %d", count)
	}
	if len(details) != 5 {
		t.Fatalf("expected details capped at 5, got %d", len(details))
	}
}

func TestDetectConflicts_EmptyClaims(t *testing.T) {
	count, details := detectConflicts(nil)
	if count != 0 {
		t.Fatalf("expected 0 conflicts, got %d", count)
	}
	if len(details) != 0 {
		t.Fatalf("expected 0 details, got %d", len(details))
	}
}

func TestDetectConflicts_SameAgentMultipleClaims(t *testing.T) {
	// Same agent claiming the same file twice should not count as a conflict.
	claims := []bridge.FileClaimInfo{
		{FilePath: "/src/main.go", AgentID: "claude-1"},
		{FilePath: "/src/main.go", AgentID: "claude-1"},
	}

	count, details := detectConflicts(claims)
	if count != 0 {
		t.Fatalf("expected 0 conflicts (same agent), got %d", count)
	}
	if len(details) != 0 {
		t.Fatalf("expected 0 details, got %d", len(details))
	}
}

func TestKPICounters_Fields(t *testing.T) {
	kpis := KPICounters{
		SessionsToday:       3,
		TokensToday:         1500,
		TasksCompletedToday: 7,
		FileConflicts:       1,
	}

	if kpis.SessionsToday != 3 {
		t.Errorf("expected 3 sessions, got %d", kpis.SessionsToday)
	}
	if kpis.TokensToday != 1500 {
		t.Errorf("expected 1500 tokens, got %d", kpis.TokensToday)
	}
	if kpis.TasksCompletedToday != 7 {
		t.Errorf("expected 7 tasks, got %d", kpis.TasksCompletedToday)
	}
}

func TestConflictDetail_Struct(t *testing.T) {
	cd := ConflictDetail{
		Path:   "/src/handler.go",
		Agents: []string{"claude-code", "gemini-cli"},
	}

	if cd.Path != "/src/handler.go" {
		t.Errorf("unexpected path: %q", cd.Path)
	}
	if len(cd.Agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(cd.Agents))
	}
}

func TestFleetSnapshot_DefaultValues(t *testing.T) {
	snap := FleetSnapshot{}

	if snap.DaemonRunning {
		t.Error("expected daemon_running false by default")
	}
	if snap.ServerCount != 0 {
		t.Errorf("expected 0 servers, got %d", snap.ServerCount)
	}
	if snap.ActiveSessions != 0 {
		t.Errorf("expected 0 active sessions, got %d", snap.ActiveSessions)
	}
}

func TestEnrichFleetAgentsWithSessionsSynthesizesSessionOnlyAgents(t *testing.T) {
	now := time.Date(2026, 4, 16, 9, 30, 0, 0, time.UTC)
	agents := []presence.PresenceInfo{
		{
			AgentID:       "claude-code-live",
			Status:        "active",
			AgentType:     "unknown",
			LastHeartbeat: now.Add(-45 * time.Second).Format(time.RFC3339Nano),
		},
	}
	sessions := []bridge.SessionInfo{
		{
			ID:          "sess-live",
			AgentID:     "claude-code-live",
			Namespace:   "services/loom-core/feat/demo",
			StartedAt:   now.Add(-10 * time.Minute).Format(time.RFC3339Nano),
			Status:      "active",
			Description: "Claude session",
		},
		{
			ID:          "sess-codex",
			AgentID:     "codex-missing-presence",
			Namespace:   "services/loom-core/feat/demo",
			StartedAt:   now.Add(-5 * time.Minute).Format(time.RFC3339Nano),
			Status:      "active",
			Description: "Codex session",
		},
	}

	enriched := fleetview.Join(agents, sessions, now)
	byAgent := make(map[string]presence.PresenceInfo)
	for _, agent := range enriched {
		byAgent[agent.AgentID] = agent
	}

	claude := byAgent["claude-code-live"]
	if claude.Source != "presence+session" || !claude.HasPresence || !claude.HasSession {
		t.Fatalf("expected claude presence+session evidence, got %#v", claude)
	}
	if claude.AgentType != "claude-code" {
		t.Fatalf("expected inferred claude-code type, got %q", claude.AgentType)
	}
	if claude.HeartbeatAgeSeconds != 45 {
		t.Fatalf("expected heartbeat age 45s, got %d", claude.HeartbeatAgeSeconds)
	}
	if claude.SessionAgeSeconds != 600 {
		t.Fatalf("expected session age 600s, got %d", claude.SessionAgeSeconds)
	}
	if claude.TelemetryStatus != "live" {
		t.Fatalf("expected live telemetry, got %q", claude.TelemetryStatus)
	}

	codex := byAgent["codex-missing-presence"]
	if codex.Source != "session" || codex.HasPresence || !codex.HasSession {
		t.Fatalf("expected codex session-only evidence, got %#v", codex)
	}
	if codex.Status != "active" || codex.AgentType != "codex" {
		t.Fatalf("expected active codex synthetic presence, got status=%q type=%q", codex.Status, codex.AgentType)
	}
	if codex.TelemetryStatus != "session_only" {
		t.Fatalf("expected session_only telemetry, got %q", codex.TelemetryStatus)
	}
}

func TestFleetMonitor_RefreshForceBypassesDebounce(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	client, agent := newBridges(t, sockPath)

	var statusCalls int
	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		statusCalls++
		return status.DaemonRPCStatus{
			Running:     true,
			Servers:     1,
			ActiveConns: 1,
			Processes:   []string{"git"},
		}, nil
	})
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		switch req.Name {
		case "agent_context__agent_session_list":
			return toolEnvelope(map[string]any{"sessions": []map[string]any{}}), nil
		case "agent_context__agent_task_list":
			return toolEnvelope(map[string]any{"tasks": []map[string]any{}}), nil
		case "agent_context__agent_memory_stats":
			return toolEnvelope(map[string]any{"total_items": 0, "total_tokens": 0}), nil
		case "agent_context__agent_graph_stats":
			return toolEnvelope(map[string]any{"entity_count": 0, "relation_count": 0}), nil
		case "agent_context__agent_workflow_list":
			return toolEnvelope(map[string]any{"workflows": []map[string]any{}}), nil
		case "agent_context__agent_presence_list":
			return toolEnvelope(map[string]any{"agents": []map[string]any{}}), nil
		case "agent_context__agent_file_claim_list":
			return toolEnvelope(map[string]any{"claims": []map[string]any{}}), nil
		case "agent_context__agent_worktree_list":
			return toolEnvelope(map[string]any{"worktrees": []map[string]any{}}), nil
		case "agent_context__agent_handoff_list":
			return toolEnvelope(map[string]any{"handoffs": []map[string]any{}}), nil
		default:
			return nil, fmt.Errorf("unexpected tool: %s", req.Name)
		}
	})

	monitor := NewFleetMonitor(client, agent, nil)
	if err := monitor.Refresh(); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if statusCalls != 1 {
		t.Fatalf("expected 1 status call after first refresh, got %d", statusCalls)
	}

	if err := monitor.Refresh(); err != nil {
		t.Fatalf("debounced refresh: %v", err)
	}
	if statusCalls != 1 {
		t.Fatalf("expected debounce to skip second refresh, got %d status calls", statusCalls)
	}

	if err := monitor.RefreshForce(); err != nil {
		t.Fatalf("forced refresh: %v", err)
	}
	if statusCalls != 2 {
		t.Fatalf("expected forced refresh to bypass debounce, got %d status calls", statusCalls)
	}
}

func TestFleetMonitor_ConcurrentRefreshesCollapse(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	client, agent := newBridges(t, sockPath)

	var statusCalls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})

	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		if statusCalls.Add(1) == 1 {
			close(started)
			<-release
		}
		return status.DaemonRPCStatus{
			Running:     true,
			Servers:     1,
			ActiveConns: 1,
			Processes:   []string{"git"},
		}, nil
	})
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		switch req.Name {
		case "agent_context__agent_session_list":
			return toolEnvelope(map[string]any{"sessions": []map[string]any{}}), nil
		case "agent_context__agent_task_list":
			return toolEnvelope(map[string]any{"tasks": []map[string]any{}}), nil
		case "agent_context__agent_memory_stats":
			return toolEnvelope(map[string]any{"total_items": 0, "total_tokens": 0}), nil
		case "agent_context__agent_graph_stats":
			return toolEnvelope(map[string]any{"entity_count": 0, "relation_count": 0}), nil
		case "agent_context__agent_workflow_list":
			return toolEnvelope(map[string]any{"workflows": []map[string]any{}}), nil
		case "agent_context__agent_presence_list":
			return toolEnvelope(map[string]any{"agents": []map[string]any{}}), nil
		case "agent_context__agent_file_claim_list":
			return toolEnvelope(map[string]any{"claims": []map[string]any{}}), nil
		case "agent_context__agent_worktree_list":
			return toolEnvelope(map[string]any{"worktrees": []map[string]any{}}), nil
		case "agent_context__agent_handoff_list":
			return toolEnvelope(map[string]any{"handoffs": []map[string]any{}}), nil
		default:
			return nil, fmt.Errorf("unexpected tool: %s", req.Name)
		}
	})

	monitor := NewFleetMonitor(client, agent, nil)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- monitor.Refresh()
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first refresh to start")
	}

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- monitor.Refresh()
	}()

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second refresh failed: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected concurrent refresh to collapse immediately")
	}

	close(release)

	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first refresh failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first refresh to finish")
	}

	if got := statusCalls.Load(); got != 1 {
		t.Fatalf("expected only one in-flight status call, got %d", got)
	}
}

// TestFleetMonitor_StaleSessionReaper exercises the defense-in-depth
// reaper that ends sessions whose backing agent has gone silent.
// Three scenarios in one refresh:
//
//   - "sess-live": fresh heartbeat → stays, counted active.
//   - "sess-stale-with-presence": heartbeat ~15 min old → reaped, dropped,
//     agent_session_end called.
//   - "sess-no-presence-old": no presence row, session_started_at ~20 min
//     ago → reaped on session-age fallback.
//
// Non-active sessions are NOT reaped (already ended on MCP side).
func TestFleetMonitor_StaleSessionReaper(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	client, agent := newBridges(t, sockPath)

	now := time.Now().UTC()
	staleHeartbeat := now.Add(-15 * time.Minute).Format(time.RFC3339Nano)
	freshHeartbeat := now.Add(-30 * time.Second).Format(time.RFC3339Nano)
	oldStart := now.Add(-20 * time.Minute).Format(time.RFC3339Nano)
	freshStart := now.Add(-5 * time.Minute).Format(time.RFC3339Nano)

	var endedSessions []string
	var endedMu sync.Mutex

	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		return status.DaemonRPCStatus{Running: true, Servers: 1}, nil
	})
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		switch req.Name {
		case "agent_context__agent_session_list":
			return toolEnvelope(map[string]any{
				"sessions": []map[string]any{
					{
						"id": "sess-live", "agent_id": "agent-live",
						"status": "active", "started_at": freshStart, "total_tokens": 100,
					},
					{
						"id": "sess-stale-with-presence", "agent_id": "agent-stale",
						"status": "active", "started_at": freshStart, "total_tokens": 50,
					},
					{
						"id": "sess-no-presence-old", "agent_id": "agent-missing",
						"status": "active", "started_at": oldStart, "total_tokens": 25,
					},
					{
						"id": "sess-idle-stale", "agent_id": "agent-idle",
						"status": "idle", "started_at": oldStart, "total_tokens": 10,
					},
				},
			}), nil
		case "agent_context__agent_presence_list":
			return toolEnvelope(map[string]any{
				"agents": []map[string]any{
					{
						"agent_id": "agent-live", "session_id": "sess-live",
						"status": "active", "last_heartbeat": freshHeartbeat,
					},
					{
						"agent_id": "agent-stale", "session_id": "sess-stale-with-presence",
						"status": "active", "last_heartbeat": staleHeartbeat,
					},
				},
			}), nil
		case "agent_context__agent_session_end":
			var args struct {
				SessionID string `json:"session_id"`
			}
			_ = json.Unmarshal(req.Arguments, &args)
			endedMu.Lock()
			endedSessions = append(endedSessions, args.SessionID)
			endedMu.Unlock()
			return toolEnvelope(map[string]any{"ok": true}), nil
		case "agent_context__agent_presence_deregister":
			return toolEnvelope(map[string]any{"ok": true}), nil
		case "agent_context__agent_task_list":
			return toolEnvelope(map[string]any{"tasks": []map[string]any{}}), nil
		case "agent_context__agent_memory_stats":
			return toolEnvelope(map[string]any{"total_items": 0, "total_tokens": 0}), nil
		case "agent_context__agent_graph_stats":
			return toolEnvelope(map[string]any{"entity_count": 0, "relation_count": 0}), nil
		case "agent_context__agent_workflow_list":
			return toolEnvelope(map[string]any{"workflows": []map[string]any{}}), nil
		case "agent_context__agent_file_claim_list":
			return toolEnvelope(map[string]any{"claims": []map[string]any{}}), nil
		case "agent_context__agent_worktree_list":
			return toolEnvelope(map[string]any{"assignments": []map[string]any{}}), nil
		case "agent_context__agent_handoff_inbox":
			return toolEnvelope(map[string]any{"handoffs": []map[string]any{}}), nil
		default:
			return nil, fmt.Errorf("unexpected tool: %s", req.Name)
		}
	})

	monitor := NewFleetMonitor(client, agent, nil)
	if err := monitor.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	snap := monitor.Snapshot()
	if snap.ActiveSessions != 1 {
		t.Fatalf("expected 1 active session after stale reap, got %d (sessions=%+v)",
			snap.ActiveSessions, snap.Sessions)
	}
	if snap.StaleSessions != 2 {
		t.Fatalf("expected 2 stale sessions, got %d", snap.StaleSessions)
	}
	if snap.TotalSessions != 2 {
		// sess-live (active, fresh) + sess-idle-stale (non-active, kept).
		t.Fatalf("expected TotalSessions=2 after reap, got %d", snap.TotalSessions)
	}
	if snap.TotalTokens != 110 {
		t.Fatalf("expected TotalTokens=110 after reap, got %d", snap.TotalTokens)
	}
	for _, s := range snap.Sessions {
		if s.ID == "sess-stale-with-presence" || s.ID == "sess-no-presence-old" {
			t.Fatalf("expected stale session %s to be dropped from snapshot", s.ID)
		}
	}

	// Reaper runs in a goroutine; allow up to 2s for both end calls.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		endedMu.Lock()
		n := len(endedSessions)
		endedMu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	endedMu.Lock()
	defer endedMu.Unlock()
	if len(endedSessions) != 2 {
		t.Fatalf("expected 2 agent_session_end calls, got %d: %v",
			len(endedSessions), endedSessions)
	}
	got := map[string]bool{}
	for _, id := range endedSessions {
		got[id] = true
	}
	if !got["sess-stale-with-presence"] || !got["sess-no-presence-old"] {
		t.Fatalf("expected reaper to end stale sessions, got: %v", endedSessions)
	}
	if got["sess-live"] {
		t.Fatal("reaper unexpectedly ended a fresh session")
	}
	if got["sess-idle-stale"] {
		t.Fatal("reaper unexpectedly ended a non-active session")
	}
}

// TestFleetMonitor_StaleSessionReaper_EmptyHeartbeatFallsBackToSessionAge
// pins the fix for spawn pods that register presence but die before
// flushing any heartbeat. Before the fix the stale-session filter seeded
// the heartbeat map with age=0 for these (fleetview.AgeSeconds clamps
// empty/unparseable values), so the reaper saw every such session as
// "freshly heartbeated" forever — visible in the HUD as spawn-* rows
// stuck in "active" with HEARTBEAT="---" long after the pod died.
//
// Scenario: an "active" session with a matching presence row that
// carries LastHeartbeat="" and started_at >10min ago. The reaper must
// fall through to the session.StartedAt clock and end the session.
func TestFleetMonitor_StaleSessionReaper_EmptyHeartbeatFallsBackToSessionAge(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	client, agent := newBridges(t, sockPath)

	now := time.Now().UTC()
	oldStart := now.Add(-20 * time.Minute).Format(time.RFC3339Nano)

	var endedSessions []string
	var endedMu sync.Mutex

	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		return status.DaemonRPCStatus{Running: true}, nil
	})
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		switch req.Name {
		case "agent_context__agent_session_list":
			return toolEnvelope(map[string]any{
				"sessions": []map[string]any{
					{
						"id": "spawn-sess-no-hb", "agent_id": "spawn-claude-code-deadbeef",
						"status": "active", "started_at": oldStart, "total_tokens": 0,
					},
				},
			}), nil
		case "agent_context__agent_presence_list":
			// Presence row exists but never heartbeated. This is the
			// shape produced by a spawn pod that registered presence
			// and then died before its first heartbeat cycle.
			return toolEnvelope(map[string]any{
				"agents": []map[string]any{
					{
						"agent_id":       "spawn-claude-code-deadbeef",
						"session_id":     "spawn-sess-no-hb",
						"status":         "active",
						"last_heartbeat": "", // <-- key field under test
						"registered_at":  oldStart,
					},
				},
			}), nil
		case "agent_context__agent_session_end":
			var args struct {
				SessionID string `json:"session_id"`
			}
			_ = json.Unmarshal(req.Arguments, &args)
			endedMu.Lock()
			endedSessions = append(endedSessions, args.SessionID)
			endedMu.Unlock()
			return toolEnvelope(map[string]any{"ok": true}), nil
		case "agent_context__agent_presence_deregister":
			return toolEnvelope(map[string]any{"ok": true}), nil
		case "agent_context__agent_task_list":
			return toolEnvelope(map[string]any{"tasks": []map[string]any{}}), nil
		case "agent_context__agent_memory_stats":
			return toolEnvelope(map[string]any{"total_items": 0, "total_tokens": 0}), nil
		case "agent_context__agent_graph_stats":
			return toolEnvelope(map[string]any{"entity_count": 0, "relation_count": 0}), nil
		case "agent_context__agent_workflow_list":
			return toolEnvelope(map[string]any{"workflows": []map[string]any{}}), nil
		case "agent_context__agent_file_claim_list":
			return toolEnvelope(map[string]any{"claims": []map[string]any{}}), nil
		case "agent_context__agent_worktree_list":
			return toolEnvelope(map[string]any{"assignments": []map[string]any{}}), nil
		case "agent_context__agent_handoff_inbox":
			return toolEnvelope(map[string]any{"handoffs": []map[string]any{}}), nil
		default:
			return nil, fmt.Errorf("unexpected tool: %s", req.Name)
		}
	})

	monitor := NewFleetMonitor(client, agent, nil)
	if err := monitor.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	snap := monitor.Snapshot()
	if snap.StaleSessions != 1 {
		t.Fatalf("expected 1 stale session (empty-heartbeat fallback to session age), got %d; sessions=%+v",
			snap.StaleSessions, snap.Sessions)
	}
	if snap.ActiveSessions != 0 {
		t.Fatalf("expected ActiveSessions=0 after reap, got %d", snap.ActiveSessions)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		endedMu.Lock()
		n := len(endedSessions)
		endedMu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	endedMu.Lock()
	defer endedMu.Unlock()
	if len(endedSessions) != 1 || endedSessions[0] != "spawn-sess-no-hb" {
		t.Fatalf("expected reaper to call agent_session_end on spawn-sess-no-hb, got %v", endedSessions)
	}
}

// TestFleetMonitor_StaleSessionReaperCooldown verifies that the
// per-session cooldown prevents a hot loop of agent_session_end calls
// when the MCP-side cleanup hasn't yet propagated back to
// agent_session_list.
func TestFleetMonitor_StaleSessionReaperCooldown(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	client, agent := newBridges(t, sockPath)

	now := time.Now().UTC()
	staleHeartbeat := now.Add(-15 * time.Minute).Format(time.RFC3339Nano)
	freshStart := now.Add(-5 * time.Minute).Format(time.RFC3339Nano)

	var endCalls atomic.Int32

	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		return status.DaemonRPCStatus{Running: true}, nil
	})
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		switch req.Name {
		case "agent_context__agent_session_list":
			return toolEnvelope(map[string]any{
				"sessions": []map[string]any{
					{"id": "sess-zombie", "agent_id": "agent-zombie",
						"status": "active", "started_at": freshStart},
				},
			}), nil
		case "agent_context__agent_presence_list":
			return toolEnvelope(map[string]any{
				"agents": []map[string]any{
					{"agent_id": "agent-zombie", "session_id": "sess-zombie",
						"status": "active", "last_heartbeat": staleHeartbeat},
				},
			}), nil
		case "agent_context__agent_session_end":
			endCalls.Add(1)
			return toolEnvelope(map[string]any{"ok": true}), nil
		case "agent_context__agent_presence_deregister":
			return toolEnvelope(map[string]any{"ok": true}), nil
		case "agent_context__agent_task_list":
			return toolEnvelope(map[string]any{"tasks": []map[string]any{}}), nil
		case "agent_context__agent_memory_stats":
			return toolEnvelope(map[string]any{"total_items": 0, "total_tokens": 0}), nil
		case "agent_context__agent_graph_stats":
			return toolEnvelope(map[string]any{"entity_count": 0, "relation_count": 0}), nil
		case "agent_context__agent_workflow_list":
			return toolEnvelope(map[string]any{"workflows": []map[string]any{}}), nil
		case "agent_context__agent_file_claim_list":
			return toolEnvelope(map[string]any{"claims": []map[string]any{}}), nil
		case "agent_context__agent_worktree_list":
			return toolEnvelope(map[string]any{"assignments": []map[string]any{}}), nil
		case "agent_context__agent_handoff_inbox":
			return toolEnvelope(map[string]any{"handoffs": []map[string]any{}}), nil
		default:
			return nil, fmt.Errorf("unexpected tool: %s", req.Name)
		}
	})

	monitor := NewFleetMonitor(client, agent, nil)
	if err := monitor.Refresh(); err != nil {
		t.Fatalf("refresh #1: %v", err)
	}
	if err := monitor.RefreshForce(); err != nil {
		t.Fatalf("refresh #2: %v", err)
	}
	if err := monitor.RefreshForce(); err != nil {
		t.Fatalf("refresh #3: %v", err)
	}

	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) && endCalls.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)

	// 3 refreshes saw the same stale session; cooldown keeps end calls at 1.
	if got := endCalls.Load(); got != 1 {
		t.Fatalf("expected 1 session_end call across 3 refreshes (cooldown), got %d", got)
	}
}

// TestFleetMonitor_LivePresenceFilterDowngradesStaleAndOrphanRows pins
// the heartbeat-age threshold for the Fleet view's "live agents"
// classification (fleetLivePresenceStaleAfter = 90s) and the orphan
// downgrade. Five rows in a single refresh:
//
//   - heartbeat 15s old + active session → stays "active"
//   - heartbeat 89s old + active session → stays "active" (below 90s)
//   - heartbeat 91s old + active session → flipped to "offline"
//   - heartbeat 25s old, orphan (no session, registered 5min ago) →
//     flipped to "offline" even though the heartbeat itself is fresh
//   - session-only synthetic row (no presence, no heartbeat) → stays
//     "active" because the live filter only fires when HasPresence=true
//
// The corresponding ActiveAgents / OfflineAgents counters must match
// the row statuses after filtering so the snapshot's "live agents"
// number agrees with what the frontend renders.
func TestFleetMonitor_LivePresenceFilterDowngradesStaleAndOrphanRows(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	client, agent := newBridges(t, sockPath)

	now := time.Now().UTC()
	hbFresh := now.Add(-15 * time.Second).Format(time.RFC3339Nano)
	hbJustBelow := now.Add(-89 * time.Second).Format(time.RFC3339Nano)
	hbJustOver := now.Add(-91 * time.Second).Format(time.RFC3339Nano)
	hbOrphanFresh := now.Add(-25 * time.Second).Format(time.RFC3339Nano)
	regOrphanOld := now.Add(-5 * time.Minute).Format(time.RFC3339Nano)
	sessStart := now.Add(-3 * time.Minute).Format(time.RFC3339Nano)

	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		return status.DaemonRPCStatus{Running: true}, nil
	})
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		switch req.Name {
		case "agent_context__agent_session_list":
			return toolEnvelope(map[string]any{
				"sessions": []map[string]any{
					{"id": "sess-fresh", "agent_id": "agent-fresh",
						"status": "active", "started_at": sessStart},
					{"id": "sess-just-below", "agent_id": "agent-just-below",
						"status": "active", "started_at": sessStart},
					{"id": "sess-just-over", "agent_id": "agent-just-over",
						"status": "active", "started_at": sessStart},
					// agent-orphan has no session row.
					{"id": "sess-only", "agent_id": "agent-session-only",
						"status": "active", "started_at": sessStart},
				},
			}), nil
		case "agent_context__agent_presence_list":
			return toolEnvelope(map[string]any{
				"agents": []map[string]any{
					{"agent_id": "agent-fresh", "session_id": "sess-fresh",
						"status": "active", "last_heartbeat": hbFresh,
						"registered_at": sessStart},
					{"agent_id": "agent-just-below", "session_id": "sess-just-below",
						"status": "active", "last_heartbeat": hbJustBelow,
						"registered_at": sessStart},
					{"agent_id": "agent-just-over", "session_id": "sess-just-over",
						"status": "active", "last_heartbeat": hbJustOver,
						"registered_at": sessStart},
					{"agent_id": "agent-orphan",
						"status": "active", "last_heartbeat": hbOrphanFresh,
						"registered_at": regOrphanOld},
				},
			}), nil
		case "agent_context__agent_session_end":
			return toolEnvelope(map[string]any{"ok": true}), nil
		case "agent_context__agent_presence_deregister":
			return toolEnvelope(map[string]any{"ok": true}), nil
		case "agent_context__agent_task_list":
			return toolEnvelope(map[string]any{"tasks": []map[string]any{}}), nil
		case "agent_context__agent_memory_stats":
			return toolEnvelope(map[string]any{"total_items": 0, "total_tokens": 0}), nil
		case "agent_context__agent_graph_stats":
			return toolEnvelope(map[string]any{"entity_count": 0, "relation_count": 0}), nil
		case "agent_context__agent_workflow_list":
			return toolEnvelope(map[string]any{"workflows": []map[string]any{}}), nil
		case "agent_context__agent_file_claim_list":
			return toolEnvelope(map[string]any{"claims": []map[string]any{}}), nil
		case "agent_context__agent_worktree_list":
			return toolEnvelope(map[string]any{"assignments": []map[string]any{}}), nil
		case "agent_context__agent_handoff_inbox":
			return toolEnvelope(map[string]any{"handoffs": []map[string]any{}}), nil
		default:
			return nil, fmt.Errorf("unexpected tool: %s", req.Name)
		}
	})

	monitor := NewFleetMonitor(client, agent, nil)
	if err := monitor.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	snap := monitor.Snapshot()
	byAgent := make(map[string]presence.PresenceInfo, len(snap.Agents))
	for _, a := range snap.Agents {
		byAgent[a.AgentID] = a
	}

	cases := []struct {
		agentID    string
		wantStatus string
		reason     string
	}{
		{"agent-fresh", "active", "fresh heartbeat (15s) + live session"},
		{"agent-just-below", "active", "heartbeat 89s old, below 90s threshold"},
		{"agent-just-over", "offline", "heartbeat 91s old, above 90s threshold"},
		{"agent-orphan", "offline", "orphan rows are not live work"},
		{"agent-session-only", "active", "synthetic session-only row, no heartbeat clock"},
	}
	for _, tc := range cases {
		got, ok := byAgent[tc.agentID]
		if !ok {
			t.Fatalf("missing agent %s in snapshot.Agents", tc.agentID)
		}
		if got.Status != tc.wantStatus {
			t.Errorf("agent %s: want status=%q (%s), got %q (heartbeat_age=%ds, is_orphan=%v, has_presence=%v)",
				tc.agentID, tc.wantStatus, tc.reason,
				got.Status, got.HeartbeatAgeSeconds, got.IsOrphan, got.HasPresence)
		}
	}

	// Counters must agree with the row statuses after filtering. Two
	// rows stay active (fresh + just-below), one synthetic session row
	// stays active (no presence), and two downgrade to offline.
	if snap.ActiveAgents != 3 {
		t.Errorf("expected ActiveAgents=3 after filter, got %d", snap.ActiveAgents)
	}
	if snap.OfflineAgents != 2 {
		t.Errorf("expected OfflineAgents=2 after filter, got %d", snap.OfflineAgents)
	}
	if snap.IdleAgents != 0 {
		t.Errorf("expected IdleAgents=0, got %d", snap.IdleAgents)
	}

	// The visible "live agents" count (active + idle) must match the
	// number of rows the frontend will classify as live, so the footer
	// number cannot diverge from the visible-row count.
	live := 0
	for _, a := range snap.Agents {
		if a.Status == "active" || a.Status == "idle" {
			live++
		}
	}
	wantLive := snap.ActiveAgents + snap.IdleAgents
	if live != wantLive {
		t.Errorf("live row count (%d) != ActiveAgents+IdleAgents (%d)", live, wantLive)
	}
}
