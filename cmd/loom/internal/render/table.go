package render

import (
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// Table is a column-aligned text table.
//
// Headers names the columns; each row in Rows must match the header arity.
// Rows whose length differs from len(Headers) are normalized: missing cells
// are rendered as empty, extra cells are truncated.
type Table struct {
	Headers []string
	Rows    [][]string
}

// Options control rendering behavior for Table.
//
// MaxColumnWidth bounds each column's width when greater than zero. Cells
// that exceed the bound are truncated and suffixed with a single ellipsis
// rune ('…'). When zero, columns expand to fit their widest cell.
//
// NoColor is reserved for future color-aware renderers. The current
// implementation never emits ANSI escapes; callers may set NoColor for
// forward-compatibility. The package additionally treats the NO_COLOR
// environment variable as authoritative — when it is set the renderer
// behaves as if NoColor were true regardless of caller intent.
type Options struct {
	NoColor        bool
	MaxColumnWidth int
}

// Render writes the table to w using the provided options.
//
// Layout: headers on the first line, rows beneath, each column padded with
// two trailing spaces. Cells are truncated to opts.MaxColumnWidth runes
// (when positive) with a trailing '…'. Empty tables — no headers and no
// rows — produce no output.
func (t Table) Render(w io.Writer, opts Options) error {
	if len(t.Headers) == 0 && len(t.Rows) == 0 {
		return nil
	}

	// Honor NO_COLOR (https://no-color.org/) regardless of caller intent.
	// The current renderer never emits color, but this guard documents the
	// contract for future extension.
	_ = opts.NoColor || os.Getenv("NO_COLOR") != ""

	cols := len(t.Headers)
	if cols == 0 {
		// No headers — derive column count from widest row.
		for _, row := range t.Rows {
			if len(row) > cols {
				cols = len(row)
			}
		}
	}

	// Compute column widths.
	widths := make([]int, cols)
	for i, h := range t.Headers {
		if i >= cols {
			break
		}
		widths[i] = runeLen(truncate(h, opts.MaxColumnWidth))
	}
	for _, row := range t.Rows {
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			if rl := runeLen(truncate(cell, opts.MaxColumnWidth)); rl > widths[i] {
				widths[i] = rl
			}
		}
	}

	// Render headers.
	if len(t.Headers) > 0 {
		if err := writeRow(w, t.Headers, widths, cols, opts.MaxColumnWidth); err != nil {
			return err
		}
	}
	// Render rows.
	for _, row := range t.Rows {
		if err := writeRow(w, row, widths, cols, opts.MaxColumnWidth); err != nil {
			return err
		}
	}
	return nil
}

func writeRow(w io.Writer, row []string, widths []int, cols, maxWidth int) error {
	var b strings.Builder
	for i := 0; i < cols; i++ {
		cell := ""
		if i < len(row) {
			cell = row[i]
		}
		cell = truncate(cell, maxWidth)
		b.WriteString(cell)
		if i < cols-1 {
			pad := widths[i] - runeLen(cell)
			if pad < 0 {
				pad = 0
			}
			b.WriteString(strings.Repeat(" ", pad))
			b.WriteString("  ")
		}
	}
	b.WriteByte('\n')
	_, err := io.WriteString(w, b.String())
	return err
}

// truncate shortens s so it occupies at most max runes, replacing the tail
// with '…' when truncation occurs. A non-positive max disables truncation.
func truncate(s string, max int) string {
	if max <= 0 {
		return s
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	// Reserve one rune for the ellipsis.
	keep := max - 1
	count := 0
	var out strings.Builder
	for _, r := range s {
		if count == keep {
			break
		}
		out.WriteRune(r)
		count++
	}
	out.WriteRune('…')
	return out.String()
}

func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}
