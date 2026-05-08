package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEvictAgentCache(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	// Fresh files (within maxAge): kept.
	fresh := []string{"agent-id-aaa-bbb", "parent-session-claude-code-x"}
	for _, n := range fresh {
		writeFileWithMtime(t, filepath.Join(dir, n), now.Add(-1*time.Hour))
	}
	// Stale evictable files: removed.
	stale := []string{"agent-id-old-1", "agent-id-old-2", "parent-session-old"}
	for _, n := range stale {
		writeFileWithMtime(t, filepath.Join(dir, n), now.Add(-30*24*time.Hour))
	}
	// Non-matching files (whatever prefix): never touched, even if old.
	otherOld := filepath.Join(dir, "devbox.lock")
	writeFileWithMtime(t, otherOld, now.Add(-30*24*time.Hour))
	// Subdirectory: skipped.
	if err := os.Mkdir(filepath.Join(dir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}

	removed, considered, err := EvictAgentCache(dir, 14*24*time.Hour, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != len(stale) {
		t.Errorf("removed = %d, want %d", removed, len(stale))
	}
	if considered != len(stale)+len(fresh) {
		t.Errorf("considered = %d, want %d", considered, len(stale)+len(fresh))
	}

	// Stale files gone.
	for _, n := range stale {
		if _, err := os.Stat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Errorf("stale file %q still present", n)
		}
	}
	// Fresh and unrelated files present.
	for _, n := range fresh {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("fresh file %q removed: %v", n, err)
		}
	}
	if _, err := os.Stat(otherOld); err != nil {
		t.Errorf("non-matching file %q removed: %v", otherOld, err)
	}
}

func TestEvictAgentCache_MissingDirIsNoop(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	removed, considered, err := EvictAgentCache(missing, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if removed != 0 || considered != 0 {
		t.Errorf("missing dir: removed=%d considered=%d, want 0/0", removed, considered)
	}
}

func TestHasEvictablePrefix(t *testing.T) {
	yes := []string{"agent-id-foo", "parent-session-bar", "agent-id-"}
	no := []string{"agent_id_foo", "session-foo", "foo-agent-id-bar", ""}
	for _, n := range yes {
		if !hasEvictablePrefix(n) {
			t.Errorf("%q: want evictable", n)
		}
	}
	for _, n := range no {
		if hasEvictablePrefix(n) {
			t.Errorf("%q: should not be evictable", n)
		}
	}
}

func writeFileWithMtime(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}
