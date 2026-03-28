package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// NewGodotClient
// ---------------------------------------------------------------------------

func TestNewGodotClient_FieldsSet(t *testing.T) {
	c := NewGodotClient("10.0.0.1", 7777, true, 3000)
	defer c.Close()

	if c.host != "10.0.0.1" {
		t.Errorf("host = %q, want %q", c.host, "10.0.0.1")
	}
	if c.port != 7777 {
		t.Errorf("port = %d, want %d", c.port, 7777)
	}
	if !c.autoConnect {
		t.Error("autoConnect = false, want true")
	}
	if c.reconnectMs != 3000 {
		t.Errorf("reconnectMs = %d, want %d", c.reconnectMs, 3000)
	}
}

func TestNewGodotClient_ChannelsInitialized(t *testing.T) {
	c := NewGodotClient("127.0.0.1", 6550, false, 5000)
	defer c.Close()

	if c.responseChan == nil {
		t.Error("responseChan is nil")
	}
	if c.errorChan == nil {
		t.Error("errorChan is nil")
	}
	if c.doneCh == nil {
		t.Error("doneCh is nil")
	}
}

func TestNewGodotClient_DefaultsNotAutoConnect(t *testing.T) {
	c := NewGodotClient("localhost", 6550, false, 0)
	defer c.Close()

	if c.autoConnect {
		t.Error("autoConnect = true, want false")
	}
	if c.reconnectMs != 0 {
		t.Errorf("reconnectMs = %d, want 0", c.reconnectMs)
	}
}

// ---------------------------------------------------------------------------
// GodotClient.Close
// ---------------------------------------------------------------------------

func TestClose_Idempotent(t *testing.T) {
	c := NewGodotClient("127.0.0.1", 6550, false, 5000)

	// First close should succeed without panic.
	c.Close()

	// Second close should also succeed without panic.
	c.Close()

	// Verify doneCh is closed (reading returns immediately).
	select {
	case <-c.doneCh:
		// expected
	default:
		t.Error("doneCh is not closed after Close()")
	}
}

func TestClose_DoneChClosed(t *testing.T) {
	c := NewGodotClient("127.0.0.1", 6550, false, 5000)
	c.Close()

	select {
	case <-c.doneCh:
		// ok
	case <-time.After(time.Second):
		t.Fatal("doneCh was not closed within 1s")
	}
}

// ---------------------------------------------------------------------------
// NewLogReader — path expansion
// ---------------------------------------------------------------------------

func TestNewLogReader_TildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home directory: %v", err)
	}

	lr := NewLogReader("~/foo/bar")
	want := filepath.Join(home, "foo/bar")
	if lr.basePath != want {
		t.Errorf("basePath = %q, want %q", lr.basePath, want)
	}
}

func TestNewLogReader_AbsolutePathPassthrough(t *testing.T) {
	lr := NewLogReader("/var/log/godot")
	if lr.basePath != "/var/log/godot" {
		t.Errorf("basePath = %q, want %q", lr.basePath, "/var/log/godot")
	}
}

func TestNewLogReader_RelativePathPassthrough(t *testing.T) {
	lr := NewLogReader("relative/path")
	if lr.basePath != "relative/path" {
		t.Errorf("basePath = %q, want %q", lr.basePath, "relative/path")
	}
}

func TestNewLogReader_TildeAloneNotExpanded(t *testing.T) {
	// Only "~/" prefix triggers expansion, not "~" alone.
	lr := NewLogReader("~notuser/path")
	if lr.basePath != "~notuser/path" {
		t.Errorf("basePath = %q, want %q", lr.basePath, "~notuser/path")
	}
}

// ---------------------------------------------------------------------------
// LogReader.ReadRecent
// ---------------------------------------------------------------------------

func writeLogFile(t *testing.T, dir string, lines []string) {
	t.Helper()
	path := filepath.Join(dir, "kk_logs.jsonl")
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}
}

func TestReadRecent_AllLines(t *testing.T) {
	dir := t.TempDir()
	lines := []string{"line1", "line2", "line3", "line4", "line5"}
	writeLogFile(t, dir, lines)

	lr := NewLogReader(dir)
	got := lr.ReadRecent(0, "")
	if len(got) != 5 {
		t.Errorf("got %d lines, want 5", len(got))
	}
	for i, want := range lines {
		if got[i] != want {
			t.Errorf("line[%d] = %q, want %q", i, got[i], want)
		}
	}
}

func TestReadRecent_LastN(t *testing.T) {
	dir := t.TempDir()
	lines := []string{"a", "b", "c", "d", "e"}
	writeLogFile(t, dir, lines)

	lr := NewLogReader(dir)
	got := lr.ReadRecent(3, "")
	if len(got) != 3 {
		t.Errorf("got %d lines, want 3", len(got))
	}
	want := []string{"c", "d", "e"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("line[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestReadRecent_LinesGreaterThanTotal(t *testing.T) {
	dir := t.TempDir()
	lines := []string{"only", "two"}
	writeLogFile(t, dir, lines)

	lr := NewLogReader(dir)
	got := lr.ReadRecent(100, "")
	if len(got) != 2 {
		t.Errorf("got %d lines, want 2", len(got))
	}
}

func TestReadRecent_WithFilter(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"level":"info","msg":"starting"}`,
		`{"level":"error","msg":"crash"}`,
		`{"level":"info","msg":"recovered"}`,
		`{"level":"error","msg":"timeout"}`,
	}
	writeLogFile(t, dir, lines)

	lr := NewLogReader(dir)
	got := lr.ReadRecent(0, "error")
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2", len(got))
	}
	for _, line := range got {
		if !strings.Contains(line, "error") {
			t.Errorf("line %q does not contain 'error'", line)
		}
	}
}

func TestReadRecent_FilterAndLimit(t *testing.T) {
	dir := t.TempDir()
	lines := []string{"err-1", "ok-1", "err-2", "ok-2", "err-3"}
	writeLogFile(t, dir, lines)

	lr := NewLogReader(dir)
	// Limit to last 3 lines ("ok-2", "err-3"), then filter for "err".
	got := lr.ReadRecent(2, "err")
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1", len(got))
	}
	if got[0] != "err-3" {
		t.Errorf("got %q, want %q", got[0], "err-3")
	}
}

func TestReadRecent_NonexistentFile(t *testing.T) {
	dir := t.TempDir()
	// Do NOT write a log file — directory is empty.

	lr := NewLogReader(dir)
	got := lr.ReadRecent(10, "")
	if len(got) != 0 {
		t.Errorf("got %d lines, want 0 for non-existent file", len(got))
	}
}

func TestReadRecent_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kk_logs.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	lr := NewLogReader(dir)
	got := lr.ReadRecent(10, "")
	// An empty file after TrimSpace+Split yields [""], which has 1 element.
	// This is the current behavior; we test what the code actually does.
	if len(got) != 1 {
		t.Errorf("got %d lines, want 1 (empty-string element)", len(got))
	}
}

func TestReadRecent_FilterNoMatch(t *testing.T) {
	dir := t.TempDir()
	lines := []string{"hello world", "foo bar", "baz qux"}
	writeLogFile(t, dir, lines)

	lr := NewLogReader(dir)
	got := lr.ReadRecent(0, "NONEXISTENT")
	if len(got) != 0 {
		t.Errorf("got %d lines, want 0 when no lines match filter", len(got))
	}
}

// ---------------------------------------------------------------------------
// LogReader.TailStream
// ---------------------------------------------------------------------------

func TestTailStream_CollectsNewLines(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "kk_logs.jsonl")

	// Write initial content.
	if err := os.WriteFile(logFile, []byte("initial\n"), 0o644); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	lr := NewLogReader(dir)

	// Run TailStream for 2.5 seconds. The ticker fires every 1s,
	// so we should get at least 1-2 ticks.
	ctx := context.Background()
	// Append a line after a short delay so the second tick picks it up.
	go func() {
		time.Sleep(1200 * time.Millisecond)
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.WriteString("appended\n")
	}()

	got := lr.TailStream(ctx, 2500, "")
	// We expect to see both "initial" (from first tick) and "appended" (from second tick).
	found := false
	for _, line := range got {
		if line == "appended" {
			found = true
		}
	}
	if !found {
		t.Errorf("did not find 'appended' in collected lines: %v", got)
	}
}

func TestTailStream_FilterApplied(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "kk_logs.jsonl")
	if err := os.WriteFile(logFile, []byte("keep-this\nskip-that\n"), 0o644); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	lr := NewLogReader(dir)
	ctx := context.Background()
	got := lr.TailStream(ctx, 1500, "keep")

	for _, line := range got {
		if !strings.Contains(line, "keep") {
			t.Errorf("unexpected unfiltered line: %q", line)
		}
	}
}

func TestTailStream_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "kk_logs.jsonl")
	if err := os.WriteFile(logFile, []byte("data\n"), 0o644); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	lr := NewLogReader(dir)
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after 200ms — well before the 10s duration would expire.
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_ = lr.TailStream(ctx, 10000, "")
	elapsed := time.Since(start)

	// Should finish well before 10s. Allow generous headroom.
	if elapsed > 3*time.Second {
		t.Errorf("TailStream took %v, expected early exit via context cancellation", elapsed)
	}
}

func TestTailStream_NonexistentFile(t *testing.T) {
	dir := t.TempDir()
	// No log file written.

	lr := NewLogReader(dir)
	ctx := context.Background()
	got := lr.TailStream(ctx, 1500, "")
	// Should return empty slice without error.
	if len(got) != 0 {
		t.Errorf("got %d lines, want 0 for non-existent file", len(got))
	}
}
