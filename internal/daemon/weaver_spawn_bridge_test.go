package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/spawn"
	"github.com/crb2nu/loom/pkg/weaver"
)

// fakeSpawnDispatcher records Spawn+Wait calls and returns preset responses.
// Satisfies spawnDispatcher.
type fakeSpawnDispatcher struct {
	mu         sync.Mutex
	spawnCalls []spawn.Request
	waitCalls  []string
	spawnID    string
	spawnErr   error
	waitState  *spawn.State
	waitErr    error
}

func (f *fakeSpawnDispatcher) Spawn(_ context.Context, req spawn.Request) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spawnCalls = append(f.spawnCalls, req)
	return f.spawnID, f.spawnErr
}

func (f *fakeSpawnDispatcher) Wait(_ context.Context, spawnID string) (*spawn.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waitCalls = append(f.waitCalls, spawnID)
	return f.waitState, f.waitErr
}

func (f *fakeSpawnDispatcher) lastSpawn() (spawn.Request, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.spawnCalls) == 0 {
		return spawn.Request{}, false
	}
	return f.spawnCalls[len(f.spawnCalls)-1], true
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDaemonSpawnBridge_Dispatch_HappyPath(t *testing.T) {
	disp := &fakeSpawnDispatcher{
		spawnID: "spawn-xyz-001",
		waitState: &spawn.State{
			SpawnID: "spawn-xyz-001",
			Status:  spawn.StatusCompleted,
			Telemetry: &bridge.SpawnTelemetry{
				LastMessage:  "claude says done",
				ToolCalls:    []bridge.ToolCallEntry{{Name: "Bash"}, {Name: "Edit"}, {Name: "Read"}},
				TotalCostUSD: 0.21,
				StopReason:   "end_turn",
				TokenUsage:   bridge.SpawnTokenUsage{InputTokens: 1200, OutputTokens: 340},
			},
		},
	}
	b := NewDaemonSpawnBridge(disp, quietLogger(), 0)

	agent := weaver.SubAgent{
		Name:          "cluster-ops-claude",
		Backend:       weaver.BackendClaude,
		RequiresSpawn: true,
		SpawnOverrides: &weaver.SpawnOverrides{
			Project:      "loom-core",
			MaxCostUSD:   1.00,
			MaxTurns:     20,
			Timeout:      10 * time.Minute,
			UseSDKDriver: true,
		},
	}

	result, err := b.Dispatch(context.Background(), agent, "what is k8s health?", "sess-aaa", "qid-zzz")
	if err != nil {
		t.Fatalf("Dispatch error: %v", err)
	}

	if result.SpawnID != "spawn-xyz-001" {
		t.Errorf("SpawnID = %q, want spawn-xyz-001", result.SpawnID)
	}
	if result.LastMessage != "claude says done" {
		t.Errorf("LastMessage = %q, want %q", result.LastMessage, "claude says done")
	}
	if result.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", result.ToolCalls)
	}
	if result.TotalCostUSD != 0.21 {
		t.Errorf("TotalCostUSD = %v, want 0.21", result.TotalCostUSD)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", result.StopReason)
	}
	if result.Tokens != 1540 {
		t.Errorf("Tokens = %d, want 1540", result.Tokens)
	}

	spawned, ok := disp.lastSpawn()
	if !ok {
		t.Fatal("expected Spawn to be called")
	}
	if spawned.AgentType != weaver.BackendClaude {
		t.Errorf("AgentType = %q, want %q", spawned.AgentType, weaver.BackendClaude)
	}
	if spawned.TaskDescription != "what is k8s health?" {
		t.Errorf("TaskDescription = %q, want %q", spawned.TaskDescription, "what is k8s health?")
	}
	if spawned.Project != "loom-core" {
		t.Errorf("Project = %q, want loom-core", spawned.Project)
	}
	if spawned.ParentSessionID != "sess-aaa" {
		t.Errorf("ParentSessionID = %q, want sess-aaa", spawned.ParentSessionID)
	}
	if spawned.MaxCostUSD != 1.0 || spawned.MaxTurns != 20 {
		t.Errorf("budget overrides not propagated: MaxCostUSD=%v MaxTurns=%d", spawned.MaxCostUSD, spawned.MaxTurns)
	}
	if !spawned.UseSDKDriver {
		t.Error("UseSDKDriver should propagate from overrides")
	}
	if spawned.TimeoutMinutes != 10 {
		t.Errorf("TimeoutMinutes = %d, want 10", spawned.TimeoutMinutes)
	}
	if spawned.Metadata["weaver_query_id"] != "qid-zzz" {
		t.Errorf("Metadata[weaver_query_id] = %q, want qid-zzz", spawned.Metadata["weaver_query_id"])
	}
	if spawned.Metadata["weaver_domain"] != "cluster-ops-claude" {
		t.Errorf("Metadata[weaver_domain] = %q, want cluster-ops-claude", spawned.Metadata["weaver_domain"])
	}
}

func TestDaemonSpawnBridge_Dispatch_FlexInferBackendRejected(t *testing.T) {
	disp := &fakeSpawnDispatcher{}
	b := NewDaemonSpawnBridge(disp, quietLogger(), 0)

	agent := weaver.SubAgent{
		Name:    "flexinfer-domain",
		Backend: weaver.BackendFlexInfer,
	}
	_, err := b.Dispatch(context.Background(), agent, "q", "sess", "qid")
	if err == nil {
		t.Fatal("expected error dispatching flexinfer backend through spawn bridge")
	}
	if !strings.Contains(err.Error(), "cannot dispatch flexinfer") {
		t.Errorf("unexpected error message: %q", err.Error())
	}
	if len(disp.spawnCalls) != 0 {
		t.Error("fake dispatcher should not have been called")
	}
}

func TestDaemonSpawnBridge_Dispatch_RejectsEmptyProject(t *testing.T) {
	disp := &fakeSpawnDispatcher{}
	b := NewDaemonSpawnBridge(disp, quietLogger(), 0)

	agent := weaver.SubAgent{
		Name:          "no-project",
		Backend:       weaver.BackendCodex,
		RequiresSpawn: true,
		// SpawnOverrides nil — Project empty.
	}
	_, err := b.Dispatch(context.Background(), agent, "q", "sess", "qid")
	if err == nil {
		t.Fatal("expected error for missing project")
	}
	if !strings.Contains(err.Error(), "spawn.project") {
		t.Errorf("unexpected error message: %q", err.Error())
	}
}

func TestDaemonSpawnBridge_Dispatch_SpawnError(t *testing.T) {
	disp := &fakeSpawnDispatcher{
		spawnErr: errors.New("pod quota exhausted"),
	}
	b := NewDaemonSpawnBridge(disp, quietLogger(), 0)
	agent := weaver.SubAgent{
		Name:           "d",
		Backend:        weaver.BackendCodex,
		RequiresSpawn:  true,
		SpawnOverrides: &weaver.SpawnOverrides{Project: "loom-core"},
	}

	_, err := b.Dispatch(context.Background(), agent, "q", "sess", "qid")
	if err == nil {
		t.Fatal("expected spawn error to surface")
	}
	if !strings.Contains(err.Error(), "pod quota exhausted") {
		t.Errorf("expected wrapped spawn error, got %q", err.Error())
	}
}

func TestDaemonSpawnBridge_Dispatch_WaitError_ReturnsSpawnID(t *testing.T) {
	disp := &fakeSpawnDispatcher{
		spawnID: "spawn-partial-123",
		waitErr: errors.New("wait: context deadline exceeded"),
	}
	b := NewDaemonSpawnBridge(disp, quietLogger(), 0)
	agent := weaver.SubAgent{
		Name:           "d",
		Backend:        weaver.BackendClaude,
		RequiresSpawn:  true,
		SpawnOverrides: &weaver.SpawnOverrides{Project: "loom-core"},
	}

	result, err := b.Dispatch(context.Background(), agent, "q", "sess", "qid")
	if err == nil {
		t.Fatal("expected wait error to surface")
	}
	// Even on wait error, caller should know which spawn was started so
	// they can inspect / stop it.
	if result.SpawnID != "spawn-partial-123" {
		t.Errorf("SpawnID = %q, want spawn-partial-123 even after wait error", result.SpawnID)
	}
}

func TestBuildSpawnRequestFromSubAgent_NilOverrides(t *testing.T) {
	agent := weaver.SubAgent{
		Name:          "d",
		Backend:       weaver.BackendGemini,
		RequiresSpawn: true,
		// SpawnOverrides nil
	}
	req := buildSpawnRequestFromSubAgent(agent, "q", "parent", "qid")
	if req.AgentType != weaver.BackendGemini {
		t.Errorf("AgentType = %q, want %q", req.AgentType, weaver.BackendGemini)
	}
	if req.Project != "" {
		t.Errorf("Project should be empty with nil overrides, got %q", req.Project)
	}
	if req.Metadata["weaver_query_id"] != "qid" {
		t.Errorf("metadata not populated correctly: %v", req.Metadata)
	}
}

func TestBridgeResultFromState_NilTelemetry(t *testing.T) {
	state := &spawn.State{
		SpawnID: "s1",
		Status:  spawn.StatusFailed,
		// Telemetry nil
	}
	got := bridgeResultFromState("s1", state)
	if got.SpawnID != "s1" {
		t.Errorf("SpawnID = %q", got.SpawnID)
	}
	if got.StopReason != "failed" {
		t.Errorf("StopReason = %q, want failed (derived from Status)", got.StopReason)
	}
	if got.LastMessage != "" {
		t.Errorf("LastMessage should be empty for nil telemetry, got %q", got.LastMessage)
	}
}
