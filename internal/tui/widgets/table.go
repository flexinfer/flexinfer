package widgets

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
)

// Table renders a simple table with headers and rows.
type Table struct {
	Headers []string
	Rows    [][]string
	Width   int // overall width hint (0 = auto)
}

// Render produces the styled table string.
func (t Table) Render() string {
	if len(t.Headers) == 0 {
		return ""
	}

	colWidths := t.computeColWidths()
	var b strings.Builder

	// Header row: uppercase, secondary color
	headerStyle := lipgloss.NewStyle().
		Foreground(theme.ColorFgSecondary).
		Bold(true)

	headerCells := make([]string, len(t.Headers))
	for i, h := range t.Headers {
		headerCells[i] = headerStyle.Render(padRight(strings.ToUpper(h), colWidths[i]))
	}
	b.WriteString(strings.Join(headerCells, " "))
	b.WriteByte('\n')

	// Separator line
	sepStyle := lipgloss.NewStyle().Foreground(theme.ColorBorder)
	totalWidth := sumInts(colWidths) + len(colWidths) - 1
	b.WriteString(sepStyle.Render(strings.Repeat("─", totalWidth)))
	b.WriteByte('\n')

	// Data rows
	rowStyle := lipgloss.NewStyle().Foreground(theme.ColorFgPrimary)
	for _, row := range t.Rows {
		cells := make([]string, len(t.Headers))
		for i := range t.Headers {
			val := ""
			if i < len(row) {
				val = row[i]
			}
			cells[i] = rowStyle.Render(padRight(val, colWidths[i]))
		}
		b.WriteString(strings.Join(cells, " "))
		b.WriteByte('\n')
	}

	return strings.TrimRight(b.String(), "\n")
}

// computeColWidths calculates widths for each column based on content.
func (t Table) computeColWidths() []int {
	widths := make([]int, len(t.Headers))
	for i, h := range t.Headers {
		widths[i] = len(h)
	}
	for _, row := range t.Rows {
		for i := range t.Headers {
			if i < len(row) && len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}

	// If a total width is specified, distribute remaining space.
	if t.Width > 0 {
		used := sumInts(widths) + len(widths) - 1
		if used < t.Width {
			extra := t.Width - used
			perCol := extra / len(widths)
			for i := range widths {
				widths[i] += perCol
			}
		}
	}

	return widths
}

// padRight pads s with spaces to width w.
func padRight(s string, w int) string {
	if len(s) >= w {
		return s[:w]
	}
	return s + strings.Repeat(" ", w-len(s))
}

// sumInts returns the sum of a slice of ints.
func sumInts(vals []int) int {
	total := 0
	for _, v := range vals {
		total += v
	}
	return total
}
