package monitor

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
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

func TestFleetMonitor_RefreshForceBypassesDebounce(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	client, agent := newBridges(t, sockPath)

	var statusCalls int
	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		statusCalls++
		return bridge.StatusResult{
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
