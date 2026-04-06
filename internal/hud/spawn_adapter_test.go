package hud

import (
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
)

func TestPopulateSpawnTelemetry(t *testing.T) {
	info := &monitor.SpawnInfo{
		SpawnID:   "spawn-abc123",
		AgentID:   "agent-1",
		AgentType: "claude-code",
	}

	tel := &bridge.SpawnTelemetry{
		TurnCount:    5,
		TotalCostUSD: 0.42,
		TokenUsage: bridge.SpawnTokenUsage{
			InputTokens:  1000,
			OutputTokens: 500,
		},
		ToolCalls: []bridge.ToolCallEntry{
			{Name: "Bash", Timestamp: "2026-01-01T00:00:00Z"},
			{Name: "Read", Timestamp: "2026-01-01T00:01:00Z"},
		},
		FileChanges: []bridge.FileChangeEntry{
			{Path: "main.go", Kind: "modify"},
			{Path: "new.go", Kind: "create"},
			{Path: "old.go", Kind: "delete"},
		},
		StopReason:  "end_turn",
		LastMessage: "Done refactoring",
	}

	populateSpawnTelemetry(info, tel)

	if info.TurnCount != 5 {
		t.Errorf("TurnCount = %d, want 5", info.TurnCount)
	}
	if info.TotalCostUSD != 0.42 {
		t.Errorf("TotalCostUSD = %f, want 0.42", info.TotalCostUSD)
	}
	if info.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", info.InputTokens)
	}
	if info.OutputTokens != 500 {
		t.Errorf("OutputTokens = %d, want 500", info.OutputTokens)
	}
	if info.ToolCallCount != 2 {
		t.Errorf("ToolCallCount = %d, want 2", info.ToolCallCount)
	}
	if info.FileChangeCount != 3 {
		t.Errorf("FileChangeCount = %d, want 3", info.FileChangeCount)
	}
	if info.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want %q", info.StopReason, "end_turn")
	}
	if info.LastMessage != "Done refactoring" {
		t.Errorf("LastMessage = %q, want %q", info.LastMessage, "Done refactoring")
	}
}

func TestPopulateSpawnTelemetry_NilSlices(t *testing.T) {
	info := &monitor.SpawnInfo{}

	tel := &bridge.SpawnTelemetry{
		TurnCount:    0,
		TotalCostUSD: 0,
		TokenUsage:   bridge.SpawnTokenUsage{},
		// ToolCalls and FileChanges are nil.
	}

	populateSpawnTelemetry(info, tel)

	if info.ToolCallCount != 0 {
		t.Errorf("ToolCallCount = %d, want 0 for nil ToolCalls", info.ToolCallCount)
	}
	if info.FileChangeCount != 0 {
		t.Errorf("FileChangeCount = %d, want 0 for nil FileChanges", info.FileChangeCount)
	}
}
