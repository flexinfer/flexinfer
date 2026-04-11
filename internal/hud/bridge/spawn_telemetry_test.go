package bridge

import (
	"encoding/json"
	"sync"
	"testing"
)

// --- JSON marshal/unmarshal round-trip tests ---

func TestSpawnTelemetry_JSONRoundTrip(t *testing.T) {
	exitCode := 0
	original := SpawnTelemetry{
		ExternalSessionID: "session-abc",
		TurnCount:         5,
		TotalCostUSD:      0.0342,
		TokenUsage: SpawnTokenUsage{
			InputTokens:         1000,
			OutputTokens:        500,
			CacheCreationTokens: 200,
			CacheReadTokens:     300,
		},
		ModelUsage: map[string]ModelUse{
			"claude-opus-4": {CostUSD: 0.03, InputTokens: 800, OutputTokens: 400},
		},
		ToolCalls: []ToolCallEntry{
			{Name: "Bash", ServerName: "mcp-git", DurationMs: 150, ExitCode: &exitCode, Timestamp: "2026-04-06T10:00:00Z"},
		},
		FileChanges: []FileChangeEntry{
			{Path: "main.go", Kind: "modify"},
		},
		Errors: []AgentError{
			{Type: "rate_limit", Message: "429 Too Many Requests", Time: "2026-04-06T10:01:00Z"},
		},
		StopReason:  "end_turn",
		LastMessage: "Done.",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded SpawnTelemetry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify key fields survived round-trip
	if decoded.ExternalSessionID != original.ExternalSessionID {
		t.Errorf("ExternalSessionID: got %q, want %q", decoded.ExternalSessionID, original.ExternalSessionID)
	}
	if decoded.TurnCount != original.TurnCount {
		t.Errorf("TurnCount: got %d, want %d", decoded.TurnCount, original.TurnCount)
	}
	if decoded.TotalCostUSD != original.TotalCostUSD {
		t.Errorf("TotalCostUSD: got %f, want %f", decoded.TotalCostUSD, original.TotalCostUSD)
	}
	if decoded.TokenUsage.InputTokens != 1000 {
		t.Errorf("InputTokens: got %d, want 1000", decoded.TokenUsage.InputTokens)
	}
	if decoded.TokenUsage.OutputTokens != 500 {
		t.Errorf("OutputTokens: got %d, want 500", decoded.TokenUsage.OutputTokens)
	}
	if decoded.TokenUsage.CacheCreationTokens != 200 {
		t.Errorf("CacheCreationTokens: got %d, want 200", decoded.TokenUsage.CacheCreationTokens)
	}
	if decoded.TokenUsage.CacheReadTokens != 300 {
		t.Errorf("CacheReadTokens: got %d, want 300", decoded.TokenUsage.CacheReadTokens)
	}
	if len(decoded.ModelUsage) != 1 {
		t.Fatalf("ModelUsage length: got %d, want 1", len(decoded.ModelUsage))
	}
	mu := decoded.ModelUsage["claude-opus-4"]
	if mu.CostUSD != 0.03 || mu.InputTokens != 800 || mu.OutputTokens != 400 {
		t.Errorf("ModelUsage[claude-opus-4]: got %+v", mu)
	}
	if len(decoded.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length: got %d, want 1", len(decoded.ToolCalls))
	}
	if decoded.ToolCalls[0].Name != "Bash" {
		t.Errorf("ToolCalls[0].Name: got %q, want %q", decoded.ToolCalls[0].Name, "Bash")
	}
	if decoded.ToolCalls[0].ExitCode == nil || *decoded.ToolCalls[0].ExitCode != 0 {
		t.Errorf("ToolCalls[0].ExitCode: got %v, want 0", decoded.ToolCalls[0].ExitCode)
	}
	if len(decoded.FileChanges) != 1 || decoded.FileChanges[0].Path != "main.go" {
		t.Errorf("FileChanges: got %+v", decoded.FileChanges)
	}
	if len(decoded.Errors) != 1 || decoded.Errors[0].Type != "rate_limit" {
		t.Errorf("Errors: got %+v", decoded.Errors)
	}
	if decoded.StopReason != "end_turn" {
		t.Errorf("StopReason: got %q, want %q", decoded.StopReason, "end_turn")
	}
	if decoded.LastMessage != "Done." {
		t.Errorf("LastMessage: got %q, want %q", decoded.LastMessage, "Done.")
	}
}

func TestSpawnTelemetry_JSONOmitsEmpty(t *testing.T) {
	empty := SpawnTelemetry{}
	data, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// omitempty fields should not be present
	for _, key := range []string{"external_session_id", "model_usage", "tool_calls", "file_changes", "errors", "stop_reason", "last_message"} {
		if _, ok := m[key]; ok {
			t.Errorf("expected %q to be omitted for zero value", key)
		}
	}
}

func TestToolCallEntry_NilExitCode(t *testing.T) {
	tc := ToolCallEntry{Name: "Read", Timestamp: "2026-04-06T10:00:00Z"}
	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := m["exit_code"]; ok {
		t.Error("expected exit_code to be omitted when nil")
	}
}

// --- Accumulator concurrent safety test ---

func TestSpawnAccumulator_ConcurrentSafety(t *testing.T) {
	acc := NewSpawnTelemetryAccumulator()
	var wg sync.WaitGroup

	// Hammer the accumulator from multiple goroutines
	for i := 0; i < 100; i++ {
		wg.Add(4)
		go func(n int) {
			defer wg.Done()
			acc.AddTokens(10, 5, 2, 1)
		}(i)
		go func(n int) {
			defer wg.Done()
			acc.AddFileChange("file.go", "modify", 0, 0)
		}(i)
		go func(n int) {
			defer wg.Done()
			acc.AddError("execution", "test error")
		}(i)
		go func(n int) {
			defer wg.Done()
			_ = acc.Snapshot()
		}(i)
	}
	wg.Wait()

	snap := acc.Snapshot()
	// 100 goroutines each adding 10 input tokens
	if snap.TokenUsage.InputTokens != 1000 {
		t.Errorf("InputTokens: got %d, want 1000", snap.TokenUsage.InputTokens)
	}
	if snap.TokenUsage.OutputTokens != 500 {
		t.Errorf("OutputTokens: got %d, want 500", snap.TokenUsage.OutputTokens)
	}
}

// --- Token accumulation ---

func TestSpawnAccumulator_TokenAccumulation(t *testing.T) {
	acc := NewSpawnTelemetryAccumulator()

	acc.AddTokens(100, 50, 10, 5)
	acc.AddTokens(200, 100, 20, 10)
	acc.AddTokens(30, 15, 3, 1)

	snap := acc.Snapshot()
	if snap.TokenUsage.InputTokens != 330 {
		t.Errorf("InputTokens: got %d, want 330", snap.TokenUsage.InputTokens)
	}
	if snap.TokenUsage.OutputTokens != 165 {
		t.Errorf("OutputTokens: got %d, want 165", snap.TokenUsage.OutputTokens)
	}
	if snap.TokenUsage.CacheCreationTokens != 33 {
		t.Errorf("CacheCreationTokens: got %d, want 33", snap.TokenUsage.CacheCreationTokens)
	}
	if snap.TokenUsage.CacheReadTokens != 16 {
		t.Errorf("CacheReadTokens: got %d, want 16", snap.TokenUsage.CacheReadTokens)
	}
}

// --- Tool call duration tracking ---

func TestSpawnAccumulator_ToolCallDuration(t *testing.T) {
	acc := NewSpawnTelemetryAccumulator()

	acc.StartToolCall("tool-1", "Bash", "mcp-git")

	// Complete it -- duration should be computed from start time
	exitCode := 0
	acc.CompleteToolCall("tool-1", 0, &exitCode, "")

	snap := acc.Snapshot()
	if len(snap.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length: got %d, want 1", len(snap.ToolCalls))
	}
	tc := snap.ToolCalls[0]
	if tc.Name != "Bash" {
		t.Errorf("Name: got %q, want %q", tc.Name, "Bash")
	}
	if tc.ServerName != "mcp-git" {
		t.Errorf("ServerName: got %q, want %q", tc.ServerName, "mcp-git")
	}
	if tc.DurationMs <= 0 {
		// Duration should be >= 0 (practically > 0 because time elapsed between start/complete)
		// but in fast tests it could be 0ms, so just check non-negative
		if tc.DurationMs < 0 {
			t.Errorf("DurationMs: got %d, want >= 0", tc.DurationMs)
		}
	}
	if tc.ExitCode == nil || *tc.ExitCode != 0 {
		t.Errorf("ExitCode: got %v, want 0", tc.ExitCode)
	}
}

// TestSpawnAccumulator_EnsureToolCall_NoPriorStart simulates the Codex
// "only item.completed" path: the parser calls EnsureToolCall to backfill
// MCP server attribution, then CompleteToolCall finalizes the entry. The
// resulting telemetry must contain a single ToolCallEntry with both Name and
// ServerName populated, exactly mirroring the slice 9a Claude behavior.
func TestSpawnAccumulator_EnsureToolCall_NoPriorStart(t *testing.T) {
	acc := NewSpawnTelemetryAccumulator()

	acc.EnsureToolCall("id1", "read_file", "filesystem")
	acc.CompleteToolCall("id1", 0, nil, "")

	snap := acc.Snapshot()
	if len(snap.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length: got %d, want 1", len(snap.ToolCalls))
	}
	tc := snap.ToolCalls[0]
	if tc.Name != "read_file" {
		t.Errorf("Name: got %q, want %q", tc.Name, "read_file")
	}
	if tc.ServerName != "filesystem" {
		t.Errorf("ServerName: got %q, want %q", tc.ServerName, "filesystem")
	}
	if tc.Error != "" {
		t.Errorf("Error: got %q, want empty", tc.Error)
	}
}

// TestSpawnAccumulator_EnsureToolCall_AfterStartIsNoop guards idempotency:
// if StartToolCall already created an entry with the same name, EnsureToolCall
// must not duplicate it. The server name should already be present from the
// start, but Ensure must still tolerate the call gracefully.
func TestSpawnAccumulator_EnsureToolCall_AfterStartIsNoop(t *testing.T) {
	acc := NewSpawnTelemetryAccumulator()

	acc.StartToolCall("id1", "read_file", "filesystem")
	acc.EnsureToolCall("id1", "read_file", "filesystem")
	acc.CompleteToolCall("id1", 0, nil, "")

	snap := acc.Snapshot()
	if len(snap.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length: got %d, want 1 (Ensure must not duplicate)", len(snap.ToolCalls))
	}
	if snap.ToolCalls[0].ServerName != "filesystem" {
		t.Errorf("ServerName: got %q, want %q", snap.ToolCalls[0].ServerName, "filesystem")
	}
}

// TestSpawnAccumulator_EnsureToolCall_BackfillsMissingServer covers the case
// where StartToolCall ran without a server name (e.g. Codex item.started for
// a non-MCP path) but a later EnsureToolCall has the server. The open entry
// should be updated in place.
func TestSpawnAccumulator_EnsureToolCall_BackfillsMissingServer(t *testing.T) {
	acc := NewSpawnTelemetryAccumulator()

	acc.StartToolCall("id1", "read_file", "")
	acc.EnsureToolCall("id1", "read_file", "filesystem")
	acc.CompleteToolCall("id1", 0, nil, "")

	snap := acc.Snapshot()
	if len(snap.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length: got %d, want 1", len(snap.ToolCalls))
	}
	if snap.ToolCalls[0].ServerName != "filesystem" {
		t.Errorf("ServerName: got %q, want %q (backfill failed)", snap.ToolCalls[0].ServerName, "filesystem")
	}
}

func TestSpawnAccumulator_ToolCallWithError(t *testing.T) {
	acc := NewSpawnTelemetryAccumulator()

	acc.StartToolCall("tool-err", "Write", "")
	exitCode := 1
	acc.CompleteToolCall("tool-err", 0, &exitCode, "permission denied")

	snap := acc.Snapshot()
	if len(snap.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length: got %d, want 1", len(snap.ToolCalls))
	}
	if snap.ToolCalls[0].Error != "permission denied" {
		t.Errorf("Error: got %q, want %q", snap.ToolCalls[0].Error, "permission denied")
	}
	if snap.ToolCalls[0].ExitCode == nil || *snap.ToolCalls[0].ExitCode != 1 {
		t.Errorf("ExitCode: got %v, want 1", snap.ToolCalls[0].ExitCode)
	}
}

// --- Cap enforcement ---

func TestSpawnAccumulator_ToolCallsCap(t *testing.T) {
	acc := NewSpawnTelemetryAccumulator()

	// Add more than maxToolCalls
	for i := 0; i < maxToolCalls+100; i++ {
		acc.StartToolCall("id", "Tool", "")
	}

	snap := acc.Snapshot()
	if len(snap.ToolCalls) != maxToolCalls {
		t.Errorf("ToolCalls length: got %d, want %d", len(snap.ToolCalls), maxToolCalls)
	}
}

func TestSpawnAccumulator_FileChangesCap(t *testing.T) {
	acc := NewSpawnTelemetryAccumulator()

	// Add more than maxFileChanges
	for i := 0; i < maxFileChanges+50; i++ {
		acc.AddFileChange("file.go", "modify", 0, 0)
	}

	snap := acc.Snapshot()
	if len(snap.FileChanges) != maxFileChanges {
		t.Errorf("FileChanges length: got %d, want %d", len(snap.FileChanges), maxFileChanges)
	}
}

// --- Snapshot returns independent copy ---

func TestSpawnAccumulator_SnapshotIndependence(t *testing.T) {
	acc := NewSpawnTelemetryAccumulator()

	acc.AddTokens(100, 50, 10, 5)
	acc.StartToolCall("t1", "Bash", "")
	acc.AddFileChange("main.go", "create", 0, 0)
	acc.AddError("execution", "test error")
	acc.SetExternalSessionID("session-1")
	acc.SetResult(0.05, 3, "end_turn")
	acc.SetModelUsage(map[string]ModelUse{
		"claude-opus-4": {CostUSD: 0.05, InputTokens: 100, OutputTokens: 50},
	})
	acc.SetLastMessage("First message")

	// Take a snapshot
	snap := acc.Snapshot()

	// Modify the original accumulator after snapshot
	acc.AddTokens(200, 100, 20, 10)
	acc.StartToolCall("t2", "Read", "")
	acc.AddFileChange("util.go", "modify", 0, 0)
	acc.AddError("rate_limit", "429")
	acc.SetExternalSessionID("session-2")
	acc.SetResult(0.10, 6, "max_turns")
	acc.SetLastMessage("Second message")

	// Snapshot should be unchanged
	if snap.TokenUsage.InputTokens != 100 {
		t.Errorf("Snapshot InputTokens changed: got %d, want 100", snap.TokenUsage.InputTokens)
	}
	if len(snap.ToolCalls) != 1 {
		t.Errorf("Snapshot ToolCalls changed: got %d, want 1", len(snap.ToolCalls))
	}
	if len(snap.FileChanges) != 1 {
		t.Errorf("Snapshot FileChanges changed: got %d, want 1", len(snap.FileChanges))
	}
	if len(snap.Errors) != 1 {
		t.Errorf("Snapshot Errors changed: got %d, want 1", len(snap.Errors))
	}
	if snap.ExternalSessionID != "session-1" {
		t.Errorf("Snapshot ExternalSessionID changed: got %q, want %q", snap.ExternalSessionID, "session-1")
	}
	if snap.TotalCostUSD != 0.05 {
		t.Errorf("Snapshot TotalCostUSD changed: got %f, want 0.05", snap.TotalCostUSD)
	}
	if snap.TurnCount != 3 {
		t.Errorf("Snapshot TurnCount changed: got %d, want 3", snap.TurnCount)
	}
	if snap.StopReason != "end_turn" {
		t.Errorf("Snapshot StopReason changed: got %q, want %q", snap.StopReason, "end_turn")
	}
	if snap.LastMessage != "First message" {
		t.Errorf("Snapshot LastMessage changed: got %q, want %q", snap.LastMessage, "First message")
	}
	if _, ok := snap.ModelUsage["claude-opus-4"]; !ok {
		t.Error("Snapshot ModelUsage missing claude-opus-4")
	}

	// Verify the new snapshot has updated data
	snap2 := acc.Snapshot()
	if snap2.TokenUsage.InputTokens != 300 {
		t.Errorf("Second snapshot InputTokens: got %d, want 300", snap2.TokenUsage.InputTokens)
	}
	if len(snap2.ToolCalls) != 2 {
		t.Errorf("Second snapshot ToolCalls: got %d, want 2", len(snap2.ToolCalls))
	}
}

// --- Snapshot deep copies map ---

func TestSpawnAccumulator_SnapshotMapIndependence(t *testing.T) {
	acc := NewSpawnTelemetryAccumulator()
	acc.SetResult(0.01, 1, "done")
	acc.SetModelUsage(map[string]ModelUse{
		"model-a": {CostUSD: 0.01, InputTokens: 10, OutputTokens: 5},
	})

	snap := acc.Snapshot()

	// Modify the snapshot map -- should not affect accumulator
	snap.ModelUsage["model-b"] = ModelUse{CostUSD: 0.99}

	snap2 := acc.Snapshot()
	if _, ok := snap2.ModelUsage["model-b"]; ok {
		t.Error("modifying snapshot map affected accumulator")
	}
}

// --- IncrementTurns ---

func TestSpawnAccumulator_IncrementTurns(t *testing.T) {
	acc := NewSpawnTelemetryAccumulator()

	acc.IncrementTurns()
	acc.IncrementTurns()
	acc.IncrementTurns()

	snap := acc.Snapshot()
	if snap.TurnCount != 3 {
		t.Errorf("TurnCount: got %d, want 3", snap.TurnCount)
	}
}

// --- Initial state ---

func TestSpawnAccumulator_InitialState(t *testing.T) {
	acc := NewSpawnTelemetryAccumulator()
	snap := acc.Snapshot()

	if snap.TurnCount != 0 {
		t.Errorf("initial TurnCount: got %d, want 0", snap.TurnCount)
	}
	if snap.TotalCostUSD != 0 {
		t.Errorf("initial TotalCostUSD: got %f, want 0", snap.TotalCostUSD)
	}
	if snap.TokenUsage.InputTokens != 0 {
		t.Errorf("initial InputTokens: got %d, want 0", snap.TokenUsage.InputTokens)
	}
	if len(snap.ToolCalls) != 0 {
		t.Errorf("initial ToolCalls: got %d, want 0", len(snap.ToolCalls))
	}
	if len(snap.FileChanges) != 0 {
		t.Errorf("initial FileChanges: got %d, want 0", len(snap.FileChanges))
	}
	if len(snap.Errors) != 0 {
		t.Errorf("initial Errors: got %d, want 0", len(snap.Errors))
	}
	if snap.ModelUsage == nil {
		t.Error("initial ModelUsage should not be nil")
	}
}
