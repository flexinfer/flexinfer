package backend

import (
	"strings"
	"testing"
)

func TestStreamLineCallbackWriter_SingleLines(t *testing.T) {
	var lines []string
	w := &lineCallbackWriter{
		onLine: func(line []byte) {
			lines = append(lines, string(line))
		},
	}

	w.Write([]byte("hello\n"))
	w.Write([]byte("world\n"))

	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if lines[0] != "hello" {
		t.Errorf("line[0]: got %q, want %q", lines[0], "hello")
	}
	if lines[1] != "world" {
		t.Errorf("line[1]: got %q, want %q", lines[1], "world")
	}
}

func TestStreamLineCallbackWriter_PartialBuffering(t *testing.T) {
	var lines []string
	w := &lineCallbackWriter{
		onLine: func(line []byte) {
			lines = append(lines, string(line))
		},
	}

	// Write partial line -- should NOT produce a callback
	w.Write([]byte("hel"))
	if len(lines) != 0 {
		t.Fatalf("expected no lines after partial write, got %d", len(lines))
	}

	// Complete the line
	w.Write([]byte("lo\n"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line after completing write, got %d", len(lines))
	}
	if lines[0] != "hello" {
		t.Errorf("line[0]: got %q, want %q", lines[0], "hello")
	}
}

func TestStreamLineCallbackWriter_Flush(t *testing.T) {
	var lines []string
	w := &lineCallbackWriter{
		onLine: func(line []byte) {
			lines = append(lines, string(line))
		},
	}

	// Write partial line without newline
	w.Write([]byte("partial"))
	if len(lines) != 0 {
		t.Fatalf("expected no lines before flush, got %d", len(lines))
	}

	// Flush should deliver the buffered content
	w.Flush()
	if len(lines) != 1 {
		t.Fatalf("expected 1 line after flush, got %d", len(lines))
	}
	if lines[0] != "partial" {
		t.Errorf("line[0]: got %q, want %q", lines[0], "partial")
	}

	// Second flush should be a no-op
	w.Flush()
	if len(lines) != 1 {
		t.Fatalf("expected 1 line after second flush, got %d", len(lines))
	}
}

func TestStreamLineCallbackWriter_EmptyLines(t *testing.T) {
	var lines []string
	w := &lineCallbackWriter{
		onLine: func(line []byte) {
			lines = append(lines, string(line))
		},
	}

	w.Write([]byte("\n"))
	w.Write([]byte("data\n"))
	w.Write([]byte("\n"))

	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[0] != "" {
		t.Errorf("line[0]: got %q, want empty", lines[0])
	}
	if lines[1] != "data" {
		t.Errorf("line[1]: got %q, want %q", lines[1], "data")
	}
	if lines[2] != "" {
		t.Errorf("line[2]: got %q, want empty", lines[2])
	}
}

func TestStreamLineCallbackWriter_VeryLongLine(t *testing.T) {
	var lines []string
	w := &lineCallbackWriter{
		onLine: func(line []byte) {
			lines = append(lines, string(line))
		},
	}

	// Create a line > 64KB
	longLine := strings.Repeat("x", 70000)
	w.Write([]byte(longLine + "\n"))

	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if len(lines[0]) != 70000 {
		t.Errorf("line length: got %d, want 70000", len(lines[0]))
	}
}

func TestStreamLineCallbackWriter_MultipleLinesInSingleWrite(t *testing.T) {
	var lines []string
	w := &lineCallbackWriter{
		onLine: func(line []byte) {
			lines = append(lines, string(line))
		},
	}

	w.Write([]byte("alpha\nbeta\ngamma\n"))

	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[0] != "alpha" {
		t.Errorf("line[0]: got %q, want %q", lines[0], "alpha")
	}
	if lines[1] != "beta" {
		t.Errorf("line[1]: got %q, want %q", lines[1], "beta")
	}
	if lines[2] != "gamma" {
		t.Errorf("line[2]: got %q, want %q", lines[2], "gamma")
	}
}

func TestStreamLineCallbackWriter_MultipleLinesWithTrailingPartial(t *testing.T) {
	var lines []string
	w := &lineCallbackWriter{
		onLine: func(line []byte) {
			lines = append(lines, string(line))
		},
	}

	w.Write([]byte("line1\nline2\nparti"))
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}

	w.Write([]byte("al\n"))
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[2] != "partial" {
		t.Errorf("line[2]: got %q, want %q", lines[2], "partial")
	}
}

func TestStreamLineCallbackWriter_NilCallback(t *testing.T) {
	// Should not panic even with nil onLine.
	w := &lineCallbackWriter{onLine: nil}
	n, err := w.Write([]byte("hello\nworld\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 12 {
		t.Errorf("bytes written: got %d, want 12", n)
	}
	// Flush with nil should also not panic
	w.Flush()
}

func TestStreamLineCallbackWriter_WriteReturnsCorrectByteCount(t *testing.T) {
	w := &lineCallbackWriter{
		onLine: func(line []byte) {},
	}

	data := []byte("abc\ndef\ngh")
	n, err := w.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Errorf("bytes written: got %d, want %d", n, len(data))
	}
}

func TestStreamLineCallbackWriter_CallbackGetsCopy(t *testing.T) {
	// Verify that modifying the callback argument does not affect subsequent calls.
	var captured []byte
	w := &lineCallbackWriter{
		onLine: func(line []byte) {
			captured = line
		},
	}

	w.Write([]byte("first\n"))
	// Mutate the captured slice
	for i := range captured {
		captured[i] = 'Z'
	}

	w.Write([]byte("second\n"))
	if string(captured) != "second" {
		t.Errorf("captured: got %q, want %q", string(captured), "second")
	}
}
