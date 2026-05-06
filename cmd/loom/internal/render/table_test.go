package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestTable_EmptyProducesNoOutput(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	tbl := Table{}
	if err := tbl.Render(&buf, Options{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected empty output, got %q", buf.String())
	}
}

func TestTable_AlignsColumns(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	tbl := Table{
		Headers: []string{"NAME", "STATUS"},
		Rows: [][]string{
			{"alpha", "ok"},
			{"longer-name", "down"},
		},
	}
	if err := tbl.Render(&buf, Options{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), buf.String())
	}

	// All non-final columns should be padded to the widest value plus 2
	// spaces of column separation. The first column's widest cell is
	// "longer-name" (11 runes), so the header line should start with
	// "NAME" followed by 7 spaces of padding then 2 separator spaces.
	if !strings.HasPrefix(lines[0], "NAME"+strings.Repeat(" ", 7)+"  STATUS") {
		t.Fatalf("header row alignment mismatch: %q", lines[0])
	}
	// Trailing column (rightmost) gets no padding/separator beyond cell text.
	if !strings.HasSuffix(lines[1], "ok") {
		t.Fatalf("first row should end at last cell with no trailing pad: %q", lines[1])
	}
}

func TestTable_TruncatesAtMaxColumnWidth(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	tbl := Table{
		Headers: []string{"col"},
		Rows: [][]string{
			{"abcdefghij"},
		},
	}
	if err := tbl.Render(&buf, Options{MaxColumnWidth: 5}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "abcd…") {
		t.Fatalf("expected truncated value 'abcd…' in output, got %q", out)
	}
	if strings.Contains(out, "abcdefghij") {
		t.Fatalf("expected original long value to be truncated, got %q", out)
	}
}

func TestTable_NoColorEnvNoANSI(t *testing.T) {
	// t.Setenv is incompatible with t.Parallel.
	t.Setenv("NO_COLOR", "1")

	var buf bytes.Buffer
	tbl := Table{
		Headers: []string{"H"},
		Rows:    [][]string{{"v"}},
	}
	if err := tbl.Render(&buf, Options{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if strings.ContainsRune(buf.String(), '\x1b') {
		t.Fatalf("Render emitted ANSI escape under NO_COLOR: %q", buf.String())
	}
}

func TestTable_NoColorOptionNoANSI(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	tbl := Table{
		Headers: []string{"H"},
		Rows:    [][]string{{"v"}},
	}
	if err := tbl.Render(&buf, Options{NoColor: true}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if strings.ContainsRune(buf.String(), '\x1b') {
		t.Fatalf("Render emitted ANSI escape with NoColor=true: %q", buf.String())
	}
}

func TestTable_DefaultRenderEmitsNoANSI(t *testing.T) {
	t.Parallel()

	// Even without NO_COLOR or NoColor, the current renderer is text-only
	// per the slice contract. Asserting this prevents future color
	// additions from leaking into the unstyled path.
	var buf bytes.Buffer
	tbl := Table{
		Headers: []string{"H"},
		Rows:    [][]string{{"v"}},
	}
	if err := tbl.Render(&buf, Options{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if strings.ContainsRune(buf.String(), '\x1b') {
		t.Fatalf("default Render should not emit ANSI: %q", buf.String())
	}
}

func TestTable_RowShorterThanHeader(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	tbl := Table{
		Headers: []string{"A", "B", "C"},
		Rows:    [][]string{{"x"}},
	}
	if err := tbl.Render(&buf, Options{}); err != nil {
		t.Fatalf("Render error: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	// Empty trailing cells render as padding only.
	if !strings.HasPrefix(lines[1], "x") {
		t.Fatalf("row should start with 'x', got %q", lines[1])
	}
}
