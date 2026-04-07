package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWriteFileAtomic_CreatesFileWithContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	data := []byte("---\nname: test\n---\n\n# Test\n")

	if err := writeFileAtomic(path, data, 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("content mismatch: got %q, want %q", got, data)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o644 {
		t.Errorf("mode = %o, want 0644", mode)
	}
}

func TestWriteFileAtomic_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	if err := os.WriteFile(path, []byte("old content"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	newData := []byte("---\nname: updated\n---\n")
	if err := writeFileAtomic(path, newData, 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, newData) {
		t.Errorf("content not overwritten: got %q", got)
	}
}

func TestWriteFileAtomic_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "subdir", "SKILL.md")
	data := []byte("---\nname: nested\n---\n")

	if err := writeFileAtomic(path, data, 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file at %s: %v", path, err)
	}
}

// TestWriteFileAtomic_NoPartialReads simulates codex's skill file watcher by
// repeatedly reading the target path while writeFileAtomic is called in a
// loop. A reader should either see the previous complete content or the new
// complete content — never an empty or partial write. This is the regression
// that the atomic write prevents (openai/codex#11495).
func TestWriteFileAtomic_NoPartialReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	contentA := []byte("---\nname: version-a\ndescription: \"first version\"\n---\n\nversion A body\n")
	contentB := []byte("---\nname: version-b\ndescription: \"second version\"\n---\n\nversion B body (slightly longer for distinction)\n")

	// Seed with A so readers always have something valid to read.
	if err := writeFileAtomic(path, contentA, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var (
		stop     atomic.Bool
		reads    atomic.Int64
		partials atomic.Int64
		wg       sync.WaitGroup
	)

	// Reader goroutine: a readermust always see either A or B, never empty.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			got, err := os.ReadFile(path)
			if err != nil {
				// During Rename on some platforms a stat-ahead-of-open can
				// miss; that's not a partial-content observation, skip.
				continue
			}
			reads.Add(1)
			if !bytes.Equal(got, contentA) && !bytes.Equal(got, contentB) {
				partials.Add(1)
			}
		}
	}()

	// Writer: alternate A/B for a short burst.
	deadline := time.Now().Add(100 * time.Millisecond)
	toggle := false
	for time.Now().Before(deadline) {
		toggle = !toggle
		data := contentA
		if toggle {
			data = contentB
		}
		if err := writeFileAtomic(path, data, 0o644); err != nil {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("writeFileAtomic: %v", err)
		}
	}
	stop.Store(true)
	wg.Wait()

	if reads.Load() == 0 {
		t.Fatal("reader did not observe any successful reads; test is ineffective")
	}
	if n := partials.Load(); n != 0 {
		t.Errorf("observed %d partial/empty reads out of %d (atomic write is leaking)", n, reads.Load())
	}
}
