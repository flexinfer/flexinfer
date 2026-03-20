package spawn

import (
	"context"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"
)

// ---------- FileStore tests ----------

func TestFileStore_SaveAndLoad(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	state := &State{
		SpawnID:   "spawn-abc123",
		AgentID:   "spawn-claude-code-abc123",
		PodName:   "spawn-abc123",
		Status:    StatusRunning,
		StartedAt: now,
		Request: Request{
			AgentType:       "claude-code",
			Namespace:       "test/spawn",
			Project:         "loom-core",
			TaskDescription: "fix the bug",
			MemoryMB:        4096,
			CPUs:            2.0,
			TimeoutMinutes:  60,
		},
	}

	if err := store.Save(ctx, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(ctx, "spawn-abc123")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil state")
	}
	if loaded.SpawnID != state.SpawnID {
		t.Errorf("SpawnID: got %q, want %q", loaded.SpawnID, state.SpawnID)
	}
	if loaded.Status != StatusRunning {
		t.Errorf("Status: got %q, want %q", loaded.Status, StatusRunning)
	}
}

func TestFileStore_LoadAll(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctx := context.Background()

	for _, id := range []string{"spawn-001", "spawn-002", "spawn-003"} {
		if err := store.Save(ctx, &State{
			SpawnID:   id,
			Status:    StatusRunning,
			StartedAt: time.Now(),
		}); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}

	states, err := store.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(states) != 3 {
		t.Errorf("LoadAll: got %d, want 3", len(states))
	}
}

func TestFileStore_Delete(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctx := context.Background()

	state := &State{SpawnID: "spawn-del", Status: StatusCompleted, StartedAt: time.Now()}
	if err := store.Save(ctx, state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Delete(ctx, "spawn-del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	loaded, err := store.Load(ctx, "spawn-del")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded != nil {
		t.Error("expected nil after delete")
	}
}

func TestFileStore_DeleteNonexistent(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.Delete(context.Background(), "spawn-nope"); err != nil {
		t.Errorf("Delete nonexistent: %v", err)
	}
}

func TestFileStore_SaveNil(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.Save(context.Background(), nil); err == nil {
		t.Error("expected error saving nil state")
	}
}

func TestFileStore_EmptyDir(t *testing.T) {
	_, err := NewFileStore("")
	if err == nil {
		t.Error("expected error for empty dir")
	}
}

func TestFileStore_PruneCompleted(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctx := context.Background()

	now := time.Now()
	old := now.Add(-2 * time.Hour)
	recent := now.Add(-5 * time.Minute)

	_ = store.Save(ctx, &State{SpawnID: "old-done", Status: StatusCompleted, StartedAt: now.Add(-3 * time.Hour), EndedAt: &old})
	_ = store.Save(ctx, &State{SpawnID: "recent-done", Status: StatusCompleted, StartedAt: now.Add(-10 * time.Minute), EndedAt: &recent})
	_ = store.Save(ctx, &State{SpawnID: "running", Status: StatusRunning, StartedAt: now.Add(-3 * time.Hour)})

	if err := store.PruneCompleted(ctx, 1*time.Hour); err != nil {
		t.Fatalf("PruneCompleted: %v", err)
	}

	states, _ := store.LoadAll(ctx)
	if len(states) != 2 {
		t.Fatalf("expected 2 after prune, got %d", len(states))
	}

	ids := make(map[string]bool)
	for _, s := range states {
		ids[s.SpawnID] = true
	}
	if !ids["recent-done"] {
		t.Error("expected recent-done to survive prune")
	}
	if !ids["running"] {
		t.Error("expected running to survive prune")
	}
}

// ---------- K8sConfigMapStore tests ----------

func TestK8sConfigMapStore_SaveAndLoad(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewK8sConfigMapStore(client, "devbox", "test-spawn-state")
	ctx := context.Background()

	state := &State{
		SpawnID:   "spawn-k8s-001",
		AgentID:   "agent-k8s-001",
		Status:    StatusRunning,
		StartedAt: time.Now(),
		Request: Request{
			AgentType:       "claude-code",
			Project:         "proj",
			TaskDescription: "task",
		},
	}

	if err := store.Save(ctx, state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(ctx, "spawn-k8s-001")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil state")
	}
	if loaded.SpawnID != "spawn-k8s-001" {
		t.Errorf("SpawnID: got %q, want %q", loaded.SpawnID, "spawn-k8s-001")
	}
}

func TestK8sConfigMapStore_LoadAll(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewK8sConfigMapStore(client, "devbox", "")
	ctx := context.Background()

	for _, id := range []string{"spawn-a", "spawn-b", "spawn-c"} {
		_ = store.Save(ctx, &State{SpawnID: id, Status: StatusRunning, StartedAt: time.Now()})
	}

	states, err := store.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(states) != 3 {
		t.Errorf("LoadAll: got %d, want 3", len(states))
	}
}

func TestK8sConfigMapStore_Delete(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewK8sConfigMapStore(client, "devbox", "test-cm")
	ctx := context.Background()

	_ = store.Save(ctx, &State{SpawnID: "spawn-del", Status: StatusCompleted, StartedAt: time.Now()})

	if err := store.Delete(ctx, "spawn-del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	loaded, err := store.Load(ctx, "spawn-del")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded != nil {
		t.Error("expected nil after delete")
	}
}

func TestK8sConfigMapStore_LoadNonexistentCM(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewK8sConfigMapStore(client, "devbox", "does-not-exist")
	ctx := context.Background()

	loaded, err := store.Load(ctx, "spawn-nope")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded != nil {
		t.Error("expected nil for nonexistent CM")
	}
}

func TestK8sConfigMapStore_LoadAllNonexistentCM(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewK8sConfigMapStore(client, "devbox", "does-not-exist")
	ctx := context.Background()

	states, err := store.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("expected 0, got %d", len(states))
	}
}

func TestK8sConfigMapStore_SaveNil(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewK8sConfigMapStore(client, "devbox", "test")
	if err := store.Save(context.Background(), nil); err == nil {
		t.Error("expected error saving nil state")
	}
}

// ---------- IsTerminal tests ----------

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		status   Status
		terminal bool
	}{
		{StatusPending, false},
		{StatusBuilding, false},
		{StatusRunning, false},
		{StatusCompleted, true},
		{StatusFailed, true},
		{StatusStopped, true},
		{StatusUnknown, false},
	}
	for _, tt := range tests {
		if got := IsTerminal(tt.status); got != tt.terminal {
			t.Errorf("IsTerminal(%q) = %v, want %v", tt.status, got, tt.terminal)
		}
	}
}
