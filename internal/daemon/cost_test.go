package daemon

import (
	"log/slog"
	"testing"
)

func TestCost_DisabledReturnsNil(t *testing.T) {
	cfg := DefaultCostConfig()
	ct := NewCostTracker(cfg, slog.Default())
	if ct != nil {
		t.Fatal("expected nil cost tracker when disabled")
	}
}

func TestCost_RecordAndSnapshot(t *testing.T) {
	cfg := CostConfig{Enabled: true}
	ct := NewCostTracker(cfg, slog.Default())
	if ct == nil {
		t.Fatal("expected non-nil cost tracker when enabled")
	}

	ct.Record(UsageRecord{
		AgentID:       "claude-code",
		Server:        "git",
		Tool:          "git_status",
		DurationMs:    50,
		RequestBytes:  100,
		ResponseBytes: 500,
		Status:        "success",
	})

	ct.Record(UsageRecord{
		AgentID:       "claude-code",
		Server:        "git",
		Tool:          "git_status",
		DurationMs:    30,
		RequestBytes:  100,
		ResponseBytes: 500,
		Status:        "success",
	})

	ct.Record(UsageRecord{
		AgentID:       "claude-code",
		Server:        "gitlab",
		Tool:          "list_issues",
		DurationMs:    200,
		RequestBytes:  150,
		ResponseBytes: 2000,
		Status:        "success",
	})

	ct.Record(UsageRecord{
		AgentID:       "codex",
		Server:        "k8s_apps_k3s",
		Tool:          "k8s_apply",
		DurationMs:    0,
		RequestBytes:  100,
		ResponseBytes: 0,
		Status:        "denied",
	})

	snap := ct.Snapshot()

	// Totals
	if snap.Totals.CallCount != 4 {
		t.Errorf("total call_count: got %d, want 4", snap.Totals.CallCount)
	}
	if snap.Totals.DeniedCount != 1 {
		t.Errorf("total denied_count: got %d, want 1", snap.Totals.DeniedCount)
	}
	if snap.Totals.TotalDuration != 280 {
		t.Errorf("total duration: got %d, want 280", snap.Totals.TotalDuration)
	}
	if snap.Totals.TotalReqBytes != 450 {
		t.Errorf("total req bytes: got %d, want 450", snap.Totals.TotalReqBytes)
	}
	if snap.Totals.TotalResBytes != 3000 {
		t.Errorf("total res bytes: got %d, want 3000", snap.Totals.TotalResBytes)
	}

	// By agent
	if len(snap.ByAgent) != 2 {
		t.Fatalf("by_agent count: got %d, want 2", len(snap.ByAgent))
	}
	agentMap := make(map[string]AgentUsage)
	for _, a := range snap.ByAgent {
		agentMap[a.AgentID] = a
	}
	claude := agentMap["claude-code"]
	if claude.CallCount != 3 {
		t.Errorf("claude call_count: got %d, want 3", claude.CallCount)
	}
	if claude.TotalReqBytes != 350 {
		t.Errorf("claude req bytes: got %d, want 350", claude.TotalReqBytes)
	}

	codex := agentMap["codex"]
	if codex.DeniedCount != 1 {
		t.Errorf("codex denied_count: got %d, want 1", codex.DeniedCount)
	}

	// By server
	if len(snap.ByServer) != 3 {
		t.Fatalf("by_server count: got %d, want 3", len(snap.ByServer))
	}
	serverMap := make(map[string]ServerUsage)
	for _, s := range snap.ByServer {
		serverMap[s.Server] = s
	}
	git := serverMap["git"]
	if git.CallCount != 2 {
		t.Errorf("git call_count: got %d, want 2", git.CallCount)
	}
	if git.TotalDuration != 80 {
		t.Errorf("git duration: got %d, want 80", git.TotalDuration)
	}
}

func TestCost_CachedAndErrorCounts(t *testing.T) {
	cfg := CostConfig{Enabled: true}
	ct := NewCostTracker(cfg, slog.Default())

	ct.Record(UsageRecord{AgentID: "a", Server: "s", Tool: "t", Status: "cached"})
	ct.Record(UsageRecord{AgentID: "a", Server: "s", Tool: "t", Status: "cached"})
	ct.Record(UsageRecord{AgentID: "a", Server: "s", Tool: "t", Status: "error"})

	snap := ct.Snapshot()
	if snap.Totals.CachedCount != 2 {
		t.Errorf("cached count: got %d, want 2", snap.Totals.CachedCount)
	}
	if snap.Totals.ErrorCount != 1 {
		t.Errorf("error count: got %d, want 1", snap.Totals.ErrorCount)
	}
	if snap.Totals.CallCount != 3 {
		t.Errorf("call count: got %d, want 3", snap.Totals.CallCount)
	}
}

func TestCost_EmptySnapshot(t *testing.T) {
	cfg := CostConfig{Enabled: true}
	ct := NewCostTracker(cfg, slog.Default())

	snap := ct.Snapshot()
	if snap.Totals.CallCount != 0 {
		t.Errorf("expected 0 calls on empty tracker, got %d", snap.Totals.CallCount)
	}
	if len(snap.ByAgent) != 0 {
		t.Errorf("expected 0 agents, got %d", len(snap.ByAgent))
	}
	if snap.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}
