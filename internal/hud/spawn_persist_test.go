package hud

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewSpawnStore(t *testing.T) {
	t.Run("creates directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "spawns")
		s, err := NewSpawnStore(dir)
		if err != nil {
			t.Fatalf("NewSpawnStore: %v", err)
		}
		if s == nil {
			t.Fatal("expected non-nil store")
		}
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat dir: %v", err)
		}
		if !info.IsDir() {
			t.Fatal("expected directory")
		}
	})

	t.Run("empty dir rejected", func(t *testing.T) {
		_, err := NewSpawnStore("")
		if err == nil {
			t.Fatal("expected error for empty dir")
		}
	})

	t.Run("idempotent on existing dir", func(t *testing.T) {
		dir := t.TempDir()
		s1, err := NewSpawnStore(dir)
		if err != nil {
			t.Fatalf("first create: %v", err)
		}
		s2, err := NewSpawnStore(dir)
		if err != nil {
			t.Fatalf("second create: %v", err)
		}
		if s1 == nil || s2 == nil {
			t.Fatal("expected non-nil stores")
		}
	})
}

func TestSpawnStoreSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSpawnStore(dir)
	if err != nil {
		t.Fatalf("NewSpawnStore: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	state := &SpawnState{
		SpawnID:   "spawn-abc123",
		AgentID:   "spawn-claude-code-abc123",
		PodName:   "spawn-abc123",
		Status:    SpawnStatusRunning,
		StartedAt: now,
		Request: SpawnRequest{
			AgentType:       "claude-code",
			Namespace:       "test/spawn",
			Project:         "loom-core",
			TaskDescription: "fix the bug",
			MemoryMB:        4096,
			CPUs:            2.0,
			TimeoutMinutes:  60,
		},
	}

	// Save
	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists.
	path := filepath.Join(dir, "spawn-abc123.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file at %s: %v", path, err)
	}

	// Load
	states, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected 1 state, got %d", len(states))
	}
	loaded := states[0]
	if loaded.SpawnID != state.SpawnID {
		t.Errorf("SpawnID: got %q, want %q", loaded.SpawnID, state.SpawnID)
	}
	if loaded.Status != SpawnStatusRunning {
		t.Errorf("Status: got %q, want %q", loaded.Status, SpawnStatusRunning)
	}
	if loaded.AgentID != state.AgentID {
		t.Errorf("AgentID: got %q, want %q", loaded.AgentID, state.AgentID)
	}
	if loaded.Request.Project != "loom-core" {
		t.Errorf("Project: got %q, want %q", loaded.Request.Project, "loom-core")
	}
	if loaded.Request.TaskDescription != "fix the bug" {
		t.Errorf("TaskDescription: got %q, want %q", loaded.Request.TaskDescription, "fix the bug")
	}
}

func TestSpawnStoreSaveNil(t *testing.T) {
	store, err := NewSpawnStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSpawnStore: %v", err)
	}
	if err := store.Save(nil); err == nil {
		t.Fatal("expected error saving nil state")
	}
}

func TestSpawnStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSpawnStore(dir)
	if err != nil {
		t.Fatalf("NewSpawnStore: %v", err)
	}

	state := &SpawnState{
		SpawnID:   "spawn-del001",
		AgentID:   "spawn-codex-del001",
		Status:    SpawnStatusCompleted,
		StartedAt: time.Now(),
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Delete.
	if err := store.Delete("spawn-del001"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify file removed.
	path := filepath.Join(dir, "spawn-del001.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted, got: %v", err)
	}

	// Load returns empty.
	states, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(states) != 0 {
		t.Fatalf("expected 0 states after delete, got %d", len(states))
	}
}

func TestSpawnStoreDeleteNonexistent(t *testing.T) {
	store, err := NewSpawnStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSpawnStore: %v", err)
	}
	// Deleting a non-existent spawn should not return an error.
	if err := store.Delete("spawn-nonexistent"); err != nil {
		t.Fatalf("Delete non-existent: %v", err)
	}
}

func TestSpawnStoreLoadEmpty(t *testing.T) {
	store, err := NewSpawnStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSpawnStore: %v", err)
	}
	states, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(states) != 0 {
		t.Fatalf("expected 0 states, got %d", len(states))
	}
}

func TestSpawnStoreLoadSkipsMalformed(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSpawnStore(dir)
	if err != nil {
		t.Fatalf("NewSpawnStore: %v", err)
	}

	// Write a valid state.
	state := &SpawnState{
		SpawnID:   "spawn-good",
		Status:    SpawnStatusRunning,
		StartedAt: time.Now(),
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Write a malformed JSON file.
	if err := os.WriteFile(filepath.Join(dir, "spawn-bad.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("write malformed: %v", err)
	}

	// Write a non-JSON file (should be skipped).
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}

	states, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected 1 valid state (skipping malformed), got %d", len(states))
	}
	if states[0].SpawnID != "spawn-good" {
		t.Errorf("expected spawn-good, got %s", states[0].SpawnID)
	}
}

func TestSpawnStoreMultiple(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSpawnStore(dir)
	if err != nil {
		t.Fatalf("NewSpawnStore: %v", err)
	}

	for _, id := range []string{"spawn-001", "spawn-002", "spawn-003"} {
		if err := store.Save(&SpawnState{
			SpawnID:   id,
			Status:    SpawnStatusRunning,
			StartedAt: time.Now(),
		}); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}

	states, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(states) != 3 {
		t.Fatalf("expected 3 states, got %d", len(states))
	}
}

func TestSpawnStorePruneCompleted(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSpawnStore(dir)
	if err != nil {
		t.Fatalf("NewSpawnStore: %v", err)
	}

	now := time.Now()
	oldEnded := now.Add(-2 * time.Hour)
	recentEnded := now.Add(-5 * time.Minute)

	// Old completed spawn (should be pruned with 1h threshold).
	if err := store.Save(&SpawnState{
		SpawnID:   "spawn-old-completed",
		Status:    SpawnStatusCompleted,
		StartedAt: now.Add(-3 * time.Hour),
		EndedAt:   &oldEnded,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Old failed spawn (should be pruned).
	if err := store.Save(&SpawnState{
		SpawnID:   "spawn-old-failed",
		Status:    SpawnStatusFailed,
		StartedAt: now.Add(-3 * time.Hour),
		EndedAt:   &oldEnded,
		Error:     "something broke",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Recent completed spawn (should NOT be pruned).
	if err := store.Save(&SpawnState{
		SpawnID:   "spawn-recent",
		Status:    SpawnStatusCompleted,
		StartedAt: now.Add(-10 * time.Minute),
		EndedAt:   &recentEnded,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Running spawn (should NOT be pruned).
	if err := store.Save(&SpawnState{
		SpawnID:   "spawn-running",
		Status:    SpawnStatusRunning,
		StartedAt: now.Add(-3 * time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Prune with 1 hour max age.
	if err := store.PruneCompleted(1 * time.Hour); err != nil {
		t.Fatalf("PruneCompleted: %v", err)
	}

	states, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Should have 2 remaining: recent + running.
	if len(states) != 2 {
		t.Fatalf("expected 2 states after prune, got %d", len(states))
	}

	ids := make(map[string]bool)
	for _, s := range states {
		ids[s.SpawnID] = true
	}
	if !ids["spawn-recent"] {
		t.Error("expected spawn-recent to survive prune")
	}
	if !ids["spawn-running"] {
		t.Error("expected spawn-running to survive prune")
	}
}

func TestSpawnStoreOverwrite(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSpawnStore(dir)
	if err != nil {
		t.Fatalf("NewSpawnStore: %v", err)
	}

	state := &SpawnState{
		SpawnID:   "spawn-overwrite",
		Status:    SpawnStatusCreating,
		StartedAt: time.Now(),
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Update status and save again.
	state.Status = SpawnStatusRunning
	state.PodName = "pod-123"
	if err := store.Save(state); err != nil {
		t.Fatalf("Save update: %v", err)
	}

	states, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected 1 state, got %d", len(states))
	}
	if states[0].Status != SpawnStatusRunning {
		t.Errorf("Status: got %q, want %q", states[0].Status, SpawnStatusRunning)
	}
	if states[0].PodName != "pod-123" {
		t.Errorf("PodName: got %q, want %q", states[0].PodName, "pod-123")
	}
}

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		status   SpawnStatus
		terminal bool
	}{
		{SpawnStatusCreating, false},
		{SpawnStatusBuilding, false},
		{SpawnStatusRunning, false},
		{SpawnStatusCompleted, true},
		{SpawnStatusFailed, true},
		{SpawnStatusStopped, true},
	}
	for _, tt := range tests {
		if got := isTerminal(tt.status); got != tt.terminal {
			t.Errorf("isTerminal(%q) = %v, want %v", tt.status, got, tt.terminal)
		}
	}
}
