package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestNewStore_CreatesDirectoryAndEmptyState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "dir")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
	entries := s.List()
	if len(entries) != 0 {
		t.Fatalf("expected empty state, got %d entries", len(entries))
	}
}

func TestStore_SetAndGet_RoundTrip(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	entry := &Entry{
		ProjectDir:      "/home/user/project",
		ContainerID:     "abc123",
		ImageTag:        "myimg:v1",
		FingerprintHash: "sha256:deadbeef",
		Backend:         "docker",
		Status:          "running",
		LastUsed:        now,
		CreatedAt:       now,
	}

	if err := s.Set("myproject", entry); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got := s.Get("myproject")
	if got == nil {
		t.Fatal("expected entry, got nil")
	}
	if got.ProjectDir != "/home/user/project" {
		t.Errorf("ProjectDir = %q, want %q", got.ProjectDir, "/home/user/project")
	}
	if got.ContainerID != "abc123" {
		t.Errorf("ContainerID = %q, want %q", got.ContainerID, "abc123")
	}
	if got.ImageTag != "myimg:v1" {
		t.Errorf("ImageTag = %q, want %q", got.ImageTag, "myimg:v1")
	}
	if got.Backend != "docker" {
		t.Errorf("Backend = %q, want %q", got.Backend, "docker")
	}
	if got.Status != "running" {
		t.Errorf("Status = %q, want %q", got.Status, "running")
	}
}

func TestStore_Get_NonExistentKey(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	got := s.Get("does-not-exist")
	if got != nil {
		t.Fatalf("expected nil for non-existent key, got %+v", got)
	}
}

func TestStore_List_ReturnsAllEntries(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	now := time.Now()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := s.Set(name, &Entry{
			ProjectDir: "/tmp/" + name,
			Status:     "ready",
			LastUsed:   now,
			CreatedAt:  now,
		}); err != nil {
			t.Fatalf("Set(%q) failed: %v", name, err)
		}
	}

	entries := s.List()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, ok := entries[name]; !ok {
			t.Errorf("expected key %q in list", name)
		}
	}
}

func TestStore_List_ReturnsCopy(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	now := time.Now()
	if err := s.Set("proj", &Entry{Status: "ready", LastUsed: now, CreatedAt: now}); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	list := s.List()
	delete(list, "proj")

	// The internal map should still have the entry.
	if got := s.Get("proj"); got == nil {
		t.Fatal("deleting from List() result should not affect the store")
	}
}

func TestStore_Delete_RemovesEntry(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	now := time.Now()
	if err := s.Set("tobedeleted", &Entry{Status: "stopped", LastUsed: now, CreatedAt: now}); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if err := s.Delete("tobedeleted"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if got := s.Get("tobedeleted"); got != nil {
		t.Fatalf("expected nil after delete, got %+v", got)
	}

	entries := s.List()
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after delete, got %d", len(entries))
	}
}

func TestStore_IdleEntries_ReturnsOldRunningEntries(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	now := time.Now()
	oldTime := now.Add(-2 * time.Hour)
	idleTimeout := 1 * time.Hour

	// Running and old -- should be returned as idle.
	if err := s.Set("idle-running", &Entry{
		Status:   "running",
		LastUsed: oldTime,
	}); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Running and recent -- should NOT be idle.
	if err := s.Set("active-running", &Entry{
		Status:   "running",
		LastUsed: now,
	}); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Stopped and old -- should NOT be idle (not running).
	if err := s.Set("idle-stopped", &Entry{
		Status:   "stopped",
		LastUsed: oldTime,
	}); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	idle := s.IdleEntries(idleTimeout)

	if len(idle) != 1 {
		t.Fatalf("expected 1 idle entry, got %d", len(idle))
	}
	if _, ok := idle["idle-running"]; !ok {
		t.Error("expected 'idle-running' in idle entries")
	}
	if _, ok := idle["active-running"]; ok {
		t.Error("'active-running' should not be idle")
	}
	if _, ok := idle["idle-stopped"]; ok {
		t.Error("'idle-stopped' should not be idle (status is stopped)")
	}
}

func TestStore_IdleEntries_EmptyWhenNoRunning(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	idle := s.IdleEntries(1 * time.Hour)
	if len(idle) != 0 {
		t.Fatalf("expected 0 idle entries for empty store, got %d", len(idle))
	}
}

func TestStore_PruneOlderThan(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	now := time.Now()
	old := now.Add(-10 * 24 * time.Hour)

	// Stopped and old — should be pruned.
	_ = s.Set("old-stopped", &Entry{Status: "stopped", LastUsed: old, CreatedAt: old})
	// Stopped and recent — should NOT be pruned.
	_ = s.Set("recent-stopped", &Entry{Status: "stopped", LastUsed: now, CreatedAt: now})
	// Running and old — should NOT be pruned (not stopped).
	_ = s.Set("old-running", &Entry{Status: "running", LastUsed: old, CreatedAt: old})

	pruned := s.PruneOlderThan(7 * 24 * time.Hour)
	if pruned != 1 {
		t.Errorf("expected 1 pruned entry, got %d", pruned)
	}

	if s.Get("old-stopped") != nil {
		t.Error("old-stopped should have been pruned")
	}
	if s.Get("recent-stopped") == nil {
		t.Error("recent-stopped should NOT have been pruned")
	}
	if s.Get("old-running") == nil {
		t.Error("old-running should NOT have been pruned")
	}
}

func TestStore_Persistence_AcrossInstances(t *testing.T) {
	dir := t.TempDir()

	// Create store and add an entry.
	s1, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore (first) failed: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	entry := &Entry{
		ProjectDir:      "/workspace/app",
		ContainerID:     "container999",
		ImageTag:        "app:latest",
		FingerprintHash: "sha256:aabbccdd",
		Backend:         "docker",
		Status:          "ready",
		LastUsed:        now,
		CreatedAt:       now,
	}
	if err := s1.Set("persistent-project", entry); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Create a second store instance at the same path.
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore (second) failed: %v", err)
	}

	got := s2.Get("persistent-project")
	if got == nil {
		t.Fatal("expected entry from persisted store, got nil")
	}
	if got.ProjectDir != "/workspace/app" {
		t.Errorf("ProjectDir = %q, want %q", got.ProjectDir, "/workspace/app")
	}
	if got.ContainerID != "container999" {
		t.Errorf("ContainerID = %q, want %q", got.ContainerID, "container999")
	}
	if got.ImageTag != "app:latest" {
		t.Errorf("ImageTag = %q, want %q", got.ImageTag, "app:latest")
	}
	if got.Backend != "docker" {
		t.Errorf("Backend = %q, want %q", got.Backend, "docker")
	}
	if got.Status != "ready" {
		t.Errorf("Status = %q, want %q", got.Status, "ready")
	}
}
