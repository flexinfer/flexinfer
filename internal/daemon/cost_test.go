package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
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

func TestCost_ConcurrentRecord(t *testing.T) {
	cfg := CostConfig{Enabled: true}
	ct := NewCostTracker(cfg, slog.Default())
	if ct == nil {
		t.Fatal("expected non-nil cost tracker")
	}

	const goroutines = 100
	const recordsPerGoroutine = 10
	const totalRecords = goroutines * recordsPerGoroutine

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < recordsPerGoroutine; j++ {
				ct.Record(UsageRecord{
					AgentID:       fmt.Sprintf("agent-%d", n%10),
					Server:        fmt.Sprintf("server-%d", n%5),
					Tool:          fmt.Sprintf("tool-%d", j),
					DurationMs:    1,
					RequestBytes:  10,
					ResponseBytes: 20,
					Status:        "success",
				})
			}
		}(i)
	}
	wg.Wait()

	snap := ct.Snapshot()
	if snap.Totals.CallCount != int64(totalRecords) {
		t.Errorf("call count: got %d, want %d", snap.Totals.CallCount, totalRecords)
	}
}

func TestCost_SnapshotSerializationRoundTrip(t *testing.T) {
	cfg := CostConfig{Enabled: true}
	ct := NewCostTracker(cfg, slog.Default())

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
		AgentID:       "codex",
		Server:        "gitlab",
		Tool:          "list_issues",
		DurationMs:    200,
		RequestBytes:  150,
		ResponseBytes: 2000,
		Status:        "error",
	})
	ct.Record(UsageRecord{
		AgentID:       "claude-code",
		Server:        "gitlab",
		Tool:          "create_issue",
		DurationMs:    100,
		RequestBytes:  300,
		ResponseBytes: 400,
		Status:        "success",
	})

	snap := ct.Snapshot()

	// Marshal to JSON.
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Unmarshal back.
	var decoded CostSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify fields match.
	if decoded.Totals.CallCount != snap.Totals.CallCount {
		t.Errorf("CallCount: got %d, want %d", decoded.Totals.CallCount, snap.Totals.CallCount)
	}
	if decoded.Totals.ErrorCount != snap.Totals.ErrorCount {
		t.Errorf("ErrorCount: got %d, want %d", decoded.Totals.ErrorCount, snap.Totals.ErrorCount)
	}
	if len(decoded.ByAgent) != len(snap.ByAgent) {
		t.Errorf("ByAgent len: got %d, want %d", len(decoded.ByAgent), len(snap.ByAgent))
	}
	if len(decoded.ByServer) != len(snap.ByServer) {
		t.Errorf("ByServer len: got %d, want %d", len(decoded.ByServer), len(snap.ByServer))
	}
	if decoded.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp after round-trip")
	}
}

func TestCost_ZeroValueRecord(t *testing.T) {
	cfg := CostConfig{Enabled: true}
	ct := NewCostTracker(cfg, slog.Default())

	ct.Record(UsageRecord{
		Status: "success",
	})

	snap := ct.Snapshot()
	if snap.Totals.CallCount != 1 {
		t.Errorf("call count: got %d, want 1", snap.Totals.CallCount)
	}
	if snap.Totals.TotalDuration != 0 {
		t.Errorf("duration: got %d, want 0", snap.Totals.TotalDuration)
	}
	if snap.Totals.TotalReqBytes != 0 {
		t.Errorf("req bytes: got %d, want 0", snap.Totals.TotalReqBytes)
	}
	if snap.Totals.TotalResBytes != 0 {
		t.Errorf("res bytes: got %d, want 0", snap.Totals.TotalResBytes)
	}
}
