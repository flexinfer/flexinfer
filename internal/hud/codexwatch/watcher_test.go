package codexwatch

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// recorder is an in-memory Publisher used by tailer + watcher tests so
// they don't need a live HUD daemon. Thread-safe; preserves order.
type recorder struct {
	mu     sync.Mutex
	events []recorded
}

type recorded struct {
	Type    string
	Payload map[string]any
}

func (r *recorder) Publish(eventType string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pm, _ := payload.(map[string]any)
	r.events = append(r.events, recorded{Type: eventType, Payload: pm})
}

func (r *recorder) countOf(eventType string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.Type == eventType {
			n++
		}
	}
	return n
}

// quietLogger returns a logger that discards output; tailer + watcher
// emit debug logs we don't want polluting test output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestTailer_AppendOnly verifies the tailer reads existing content (when
// startAtEnd=false), maps each line, and emits a synthetic session.end
// when its context is cancelled.
func TestTailer_AppendOnly(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "rollout-sample.jsonl"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-2026-05-16T00-00-00-xxx.jsonl")
	if err := os.WriteFile(path, fixture, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	rec := &recorder{}
	tl := newTailer(path, tailerConfig{
		pollInterval: 20 * time.Millisecond,
		idleTimeout:  500 * time.Millisecond,
		maxLifetime:  5 * time.Second,
		startAtEnd:   false,
	}, rec, quietLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		tl.Run(ctx)
		close(done)
	}()

	// Wait for the tailer to consume the fixture (session_meta is the
	// first record so session.start should appear quickly).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rec.countOf(EventSessionStart) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	if got := rec.countOf(EventSessionStart); got != 1 {
		t.Errorf("session.start = %d, want 1", got)
	}
	if got := rec.countOf(EventToolCallStart); got < 1 {
		t.Errorf("tool.call.start = %d, want >=1", got)
	}
	if got := rec.countOf(EventToolCallEnd); got < 1 {
		t.Errorf("tool.call.end = %d, want >=1", got)
	}
	if got := rec.countOf(EventSessionEnd); got != 1 {
		t.Errorf("session.end = %d, want 1 (on shutdown)", got)
	}
}

// TestTailer_StartAtEndSkipsExistingContent confirms --from=now
// semantics: pre-existing lines are seek()'d past and only later
// appends produce events.
func TestTailer_StartAtEndSkipsExistingContent(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "rollout-sample.jsonl"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-2026-05-16T00-00-00-yyy.jsonl")
	if err := os.WriteFile(path, fixture, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	rec := &recorder{}
	tl := newTailer(path, tailerConfig{
		pollInterval: 20 * time.Millisecond,
		idleTimeout:  5 * time.Second,
		maxLifetime:  10 * time.Second,
		startAtEnd:   true,
	}, rec, quietLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		tl.Run(ctx)
		close(done)
	}()

	// Give the tailer time to seek past existing content and notice no
	// new lines have arrived.
	time.Sleep(200 * time.Millisecond)

	if got := rec.countOf(EventSessionStart); got != 0 {
		t.Errorf("session.start with startAtEnd=true: got %d, want 0", got)
	}

	cancel()
	<-done

	// session.end should NOT fire either: tailer never saw a
	// session_meta, so state.SessionID is empty.
	if got := rec.countOf(EventSessionEnd); got != 0 {
		t.Errorf("session.end without session: got %d, want 0", got)
	}
}

// TestWatcher_DiscoversNewFile verifies the orchestrator picks up a
// newly-created session file and spawns a tailer for it.
func TestWatcher_DiscoversNewFile(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "rollout-sample.jsonl"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	root := t.TempDir()
	rec := &recorder{}
	w, err := NewWatcher(rec, Options{
		SessionsDir:       root,
		PollInterval:      20 * time.Millisecond,
		DiscoveryInterval: 50 * time.Millisecond,
		IdleTimeout:       5 * time.Second,
		MaxLifetime:       5 * time.Second,
		Logger:            quietLogger(),
		FromAll:           true, // tests place files outside today's date tree
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// Drop a fixture file into a date directory after the watcher is
	// already running, so this exercises the rescan path rather than
	// the synchronous startup discovery.
	time.Sleep(100 * time.Millisecond)
	dayDir := filepath.Join(root, "2026", "05", "16")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dayDir, "rollout-2026-05-16T00-00-00-zzz.jsonl")
	if err := os.WriteFile(path, fixture, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// Watcher uses startAtEnd=false when FromAll=true so we get events
	// from the full file body.

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rec.countOf(EventSessionStart) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	if got := rec.countOf(EventSessionStart); got != 1 {
		t.Errorf("discovered file: session.start = %d, want 1", got)
	}
}

// TestWatcher_DoesNotReAdoptAfterIdleTimeout regresses a real smoke-test
// finding: when a tailer exits on idle timeout the file is still on
// disk, so a naive rescan re-adopts it and emits a second session.start
// with the same session_id. The Watcher must remember terminal paths.
func TestWatcher_DoesNotReAdoptAfterIdleTimeout(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "rollout-sample.jsonl"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	root := t.TempDir()
	dayDir := filepath.Join(root, "2026", "05", "16")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dayDir, "rollout-2026-05-16T00-00-00-once.jsonl")
	if err := os.WriteFile(path, fixture, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	rec := &recorder{}
	w, err := NewWatcher(rec, Options{
		SessionsDir:       root,
		PollInterval:      20 * time.Millisecond,
		DiscoveryInterval: 50 * time.Millisecond,
		IdleTimeout:       300 * time.Millisecond, // force a quick terminal exit
		MaxLifetime:       5 * time.Second,
		Logger:            quietLogger(),
		FromAll:           true,
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// Allow the file to be discovered, the tailer to exit on idle
	// timeout (~300ms), and the discoverer to rescan several times.
	time.Sleep(1500 * time.Millisecond)
	cancel()
	<-done

	if got := rec.countOf(EventSessionStart); got != 1 {
		t.Errorf("expected exactly 1 session.start across idle-timeout re-discovery, got %d", got)
	}
	if got := rec.countOf(EventSessionEnd); got != 1 {
		t.Errorf("expected exactly 1 session.end across idle-timeout re-discovery, got %d", got)
	}
}
