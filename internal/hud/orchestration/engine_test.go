package orchestration

import (
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestEvaluateDispatch_SelectsIdleAgent(t *testing.T) {
	engine := NewEngine(nil)
	agents := []CapacityInfo{
		{AgentID: "busy-agent", Status: "active", ActiveTasks: 4, Utilization: 0.8},
		{AgentID: "idle-agent", Status: "idle", ActiveTasks: 0, Utilization: 0.0},
		{AgentID: "medium-agent", Status: "active", ActiveTasks: 2, Utilization: 0.4},
	}

	agentID, reason := engine.EvaluateDispatch("task-1", "Fix bug", agents)

	if agentID != "idle-agent" {
		t.Errorf("expected idle-agent, got %q", agentID)
	}
	if reason == "" {
		t.Error("expected a non-empty reason")
	}
}

func TestEvaluateDispatch_RespectsMaxTasks(t *testing.T) {
	engine := NewEngine(nil)
	// All agents at max capacity.
	agents := []CapacityInfo{
		{AgentID: "agent-a", Status: "active", ActiveTasks: 5, Utilization: 1.0},
		{AgentID: "agent-b", Status: "active", ActiveTasks: 5, Utilization: 1.0},
	}

	agentID, reason := engine.EvaluateDispatch("task-1", "Build feature", agents)

	if agentID != "" {
		t.Errorf("expected empty agent, got %q", agentID)
	}
	if reason != "no available agents" {
		t.Errorf("expected 'no available agents', got %q", reason)
	}
}

func TestEvaluateDispatch_SkipsOfflineAgents(t *testing.T) {
	engine := NewEngine(nil)
	agents := []CapacityInfo{
		{AgentID: "offline-agent", Status: "offline", ActiveTasks: 0, Utilization: 0.0},
		{AgentID: "active-agent", Status: "active", ActiveTasks: 1, Utilization: 0.2},
	}

	agentID, _ := engine.EvaluateDispatch("task-1", "Deploy", agents)

	if agentID != "active-agent" {
		t.Errorf("expected active-agent, got %q", agentID)
	}
}

func TestEvaluateDispatch_RespectsTokenBudget(t *testing.T) {
	engine := NewEngine(nil)
	cfg := DefaultPolicyConfig()
	cfg.Load.TokenBudgetCeiling = 50000
	engine.UpdatePolicy(cfg)

	agents := []CapacityInfo{
		{AgentID: "over-budget", Status: "active", ActiveTasks: 0, TokensUsed: 60000, Utilization: 0.0},
		{AgentID: "under-budget", Status: "active", ActiveTasks: 1, TokensUsed: 10000, Utilization: 0.2},
	}

	agentID, _ := engine.EvaluateDispatch("task-1", "Refactor", agents)

	if agentID != "under-budget" {
		t.Errorf("expected under-budget, got %q", agentID)
	}
}

func TestBuildCapacities_ComputesFromBridgeData(t *testing.T) {
	engine := NewEngine(nil)

	sessions := []bridge.SessionInfo{
		{ID: "sess-1", AgentID: "agent-a", Status: "active", TotalTokens: 5000},
		{ID: "sess-2", AgentID: "agent-b", Status: "active", TotalTokens: 12000},
	}
	tasks := []bridge.TaskInfo{
		{ID: "t1", SessionID: "sess-1", AgentID: "agent-a", Status: "in_progress"},
		{ID: "t2", SessionID: "sess-1", AgentID: "agent-a", Status: "pending"},
		{ID: "t3", SessionID: "sess-2", AgentID: "agent-b", Status: "completed"},
	}
	presence := []bridge.PresenceInfo{
		{AgentID: "agent-a", Status: "active"},
		{AgentID: "agent-b", Status: "idle", LastHeartbeat: "2026-03-24T10:00:00Z"},
	}

	caps := engine.BuildCapacities(sessions, tasks, presence)

	if len(caps) != 2 {
		t.Fatalf("expected 2 capacities, got %d", len(caps))
	}

	capA := findCapacity(caps, "agent-a")
	if capA == nil {
		t.Fatal("agent-a capacity not found")
	}
	if capA.ActiveTasks != 2 {
		t.Errorf("agent-a: expected 2 active tasks, got %d", capA.ActiveTasks)
	}
	if capA.TokensUsed != 5000 {
		t.Errorf("agent-a: expected 5000 tokens, got %d", capA.TokensUsed)
	}
	if capA.AvailableSlots != 3 {
		t.Errorf("agent-a: expected 3 available slots, got %d", capA.AvailableSlots)
	}

	capB := findCapacity(caps, "agent-b")
	if capB == nil {
		t.Fatal("agent-b capacity not found")
	}
	if capB.ActiveTasks != 0 {
		t.Errorf("agent-b: expected 0 active tasks (completed does not count), got %d", capB.ActiveTasks)
	}
	if capB.IdleSince == "" {
		t.Error("agent-b: expected idle_since to be set")
	}
}

func TestPreflightCheck_DetectsConflicts(t *testing.T) {
	engine := NewEngine(nil)

	claims := []bridge.FileClaimInfo{
		{AgentID: "agent-a", FilePath: "cmd/main.go"},
		{AgentID: "agent-b", FilePath: "cmd/main.go"},
		{AgentID: "agent-a", FilePath: "internal/app.go"},
	}

	// agent-b checking cmd/main.go should see agent-a's claim.
	warnings := engine.PreflightCheck("agent-b", "cmd/main.go", claims)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].HeldBy != "agent-a" {
		t.Errorf("expected conflict held by agent-a, got %q", warnings[0].HeldBy)
	}
	if warnings[0].ConflictType != "file_claim" {
		t.Errorf("expected conflict type file_claim, got %q", warnings[0].ConflictType)
	}
}

func TestPreflightCheck_NoConflictForSameAgent(t *testing.T) {
	engine := NewEngine(nil)

	claims := []bridge.FileClaimInfo{
		{AgentID: "agent-a", FilePath: "cmd/main.go"},
	}

	warnings := engine.PreflightCheck("agent-a", "cmd/main.go", claims)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for same agent, got %d", len(warnings))
	}
}

func TestPreflightCheck_EmptyInputs(t *testing.T) {
	engine := NewEngine(nil)

	warnings := engine.PreflightCheck("", "cmd/main.go", nil)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for empty agent, got %d", len(warnings))
	}

	warnings = engine.PreflightCheck("agent-a", "", nil)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for empty path, got %d", len(warnings))
	}
}

func TestGetPolicy_ReturnsDefault(t *testing.T) {
	engine := NewEngine(nil)
	policy := engine.GetPolicy()

	if policy.Load.MaxConcurrentTasks != 5 {
		t.Errorf("expected max_tasks=5, got %d", policy.Load.MaxConcurrentTasks)
	}
	if policy.Load.TokenBudgetCeiling != 100000 {
		t.Errorf("expected token_cap=100000, got %d", policy.Load.TokenBudgetCeiling)
	}
	if !policy.Dispatch.Enabled {
		t.Error("expected dispatch.enabled=true")
	}
}

func TestUpdatePolicy_TakesEffect(t *testing.T) {
	engine := NewEngine(nil)
	cfg := PolicyConfig{
		Dispatch: DispatchPolicy{
			Enabled: true,
			Mode:    "balanced",
		},
		Load: LoadPolicy{
			MaxConcurrentTasks: 10,
			TokenBudgetCeiling: 200000,
		},
	}
	engine.UpdatePolicy(cfg)

	got := engine.GetPolicy()
	if got.Load.MaxConcurrentTasks != 10 {
		t.Errorf("expected max_tasks=10, got %d", got.Load.MaxConcurrentTasks)
	}
	if !got.Dispatch.Enabled {
		t.Error("expected dispatch.enabled=true after update")
	}
}

func TestBuildRecommendations_MatchesPendingTasks(t *testing.T) {
	engine := NewEngine(nil)

	tasks := []bridge.TaskInfo{
		{ID: "t1", Title: "Fix bug", Status: "pending"},
		{ID: "t2", Title: "Add feature", Status: "in_progress"},
		{ID: "t3", Title: "Write docs", Status: "pending"},
	}
	capacities := []CapacityInfo{
		{AgentID: "agent-a", Status: "idle", ActiveTasks: 0, Utilization: 0.0},
	}

	recs := engine.BuildRecommendations(tasks, capacities)

	if len(recs) != 2 {
		t.Fatalf("expected 2 recommendations (pending tasks only), got %d", len(recs))
	}
	for _, rec := range recs {
		if rec.RecommendedAgent != "agent-a" {
			t.Errorf("expected agent-a, got %q", rec.RecommendedAgent)
		}
	}
}

func findCapacity(caps []CapacityInfo, agentID string) *CapacityInfo {
	for i := range caps {
		if caps[i].AgentID == agentID {
			return &caps[i]
		}
	}
	return nil
}
