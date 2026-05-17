package hud

import (
	"context"
	"log/slog"
	"testing"

	"github.com/crb2nu/loom/internal/spawn"
)

// TestCompleteSpawn_ClearsStaleError verifies that completeSpawn wipes a
// leftover state.Error before persisting StatusCompleted. This guards
// against the race where Reconcile poisons the in-memory state with
// "pod not found during reconciliation" between the pod's real exit and
// the orchestrator's success handler — without this clear, the persisted
// ConfigMap row reads `status=completed, error=...` and downstream
// dashboards (Mills system health) mis-classify a successful spawn as a
// failure.
func TestCompleteSpawn_ClearsStaleError(t *testing.T) {
	ctx := context.Background()
	ctrl := spawn.NewK8sController(nil, "test", nil, slog.Default())

	// Seed a running spawn that already has the poison Error attached —
	// simulating a transient reconcile tick that ran between the pod's
	// exit and the orchestrator's success path.
	state := &SpawnState{
		SpawnID: "spawn-poisoned",
		AgentID: "spawn-claude-code-poisoned",
		Status:  SpawnStatusRunning,
		Error:   "pod not found during reconciliation",
		Request: SpawnRequest{
			AgentType: "claude-code",
			Project:   "loom-core",
		},
	}
	ctrl.UpdateState(ctx, state)

	o := NewSpawnOrchestratorForTest(ctrl)

	o.completeSpawn(ctx, state)

	got, ok := ctrl.Get("spawn-poisoned")
	if !ok {
		t.Fatalf("spawn missing after completeSpawn")
	}
	if got.Status != SpawnStatusCompleted {
		t.Fatalf("Status = %q, want %q", got.Status, SpawnStatusCompleted)
	}
	if got.Error != "" {
		t.Fatalf("Error = %q, want empty after completeSpawn cleared stale poison", got.Error)
	}
	if got.EndedAt == nil {
		t.Fatalf("EndedAt = nil, want stamped")
	}
}

// TestReapTerminalSpawn_ClearsStaleErrorForCleanTermination verifies that
// when the terminal hook fires for a spawn whose status is Completed (or
// Stopped) but whose persisted state still carries a stale Error from a
// late-arriving Reconcile tick, the reaper clears the Error so the
// ConfigMap row matches the actual outcome.
func TestReapTerminalSpawn_ClearsStaleErrorForCleanTermination(t *testing.T) {
	ctx := context.Background()
	ctrl := spawn.NewK8sController(nil, "test", nil, slog.Default())

	// Seed a completed spawn that picked up a stale Error after
	// completeSpawn (simulating a Reconcile tick that races the hook).
	state := &SpawnState{
		SpawnID: "spawn-completed-poisoned",
		AgentID: "spawn-claude-code-completed-poisoned",
		Status:  SpawnStatusCompleted,
		Error:   "pod not found during reconciliation",
	}
	ctrl.UpdateState(ctx, state)

	o := NewSpawnOrchestratorForTest(ctrl)
	o.reapTerminalSpawn(ctx, *state)

	got, ok := ctrl.Get("spawn-completed-poisoned")
	if !ok {
		t.Fatalf("spawn missing after reap")
	}
	if got.Status != SpawnStatusCompleted {
		t.Fatalf("Status = %q, want %q (reap must not change status)", got.Status, SpawnStatusCompleted)
	}
	if got.Error != "" {
		t.Fatalf("Error = %q, want empty after reap cleared stale poison", got.Error)
	}
}

// TestReapTerminalSpawn_PreservesErrorForFailedSpawn verifies that the
// reap-hygiene step does NOT wipe Error for spawns that legitimately
// failed — those should keep their failure reason in the ConfigMap.
func TestReapTerminalSpawn_PreservesErrorForFailedSpawn(t *testing.T) {
	ctx := context.Background()
	ctrl := spawn.NewK8sController(nil, "test", nil, slog.Default())

	state := &SpawnState{
		SpawnID: "spawn-genuinely-failed",
		AgentID: "spawn-claude-code-genuinely-failed",
		Status:  SpawnStatusFailed,
		Error:   "agent execution failed: exit 1",
	}
	ctrl.UpdateState(ctx, state)

	o := NewSpawnOrchestratorForTest(ctrl)
	o.reapTerminalSpawn(ctx, *state)

	got, ok := ctrl.Get("spawn-genuinely-failed")
	if !ok {
		t.Fatalf("spawn missing after reap")
	}
	if got.Status != SpawnStatusFailed {
		t.Fatalf("Status = %q, want %q", got.Status, SpawnStatusFailed)
	}
	if got.Error != "agent execution failed: exit 1" {
		t.Fatalf("Error = %q, want preserved failure reason", got.Error)
	}
}
