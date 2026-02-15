package panels

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// padRight pads a string to the given width with spaces.
func padRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	// Use lipgloss.Width so ANSI escape sequences don't break column alignment.
	if lipgloss.Width(s) >= width {
		return s
	}
	return s + spaces(width-lipgloss.Width(s))
}

// spaces returns a string of n spaces.
func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

// truncate shortens a string to maxLen visible characters, adding an ellipsis
// if needed.  Uses ansi.Truncate so ANSI escape sequences (colors, bold, etc.)
// are not counted as visible width.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxLen {
		return s
	}
	return ansi.Truncate(s, maxLen, "...")
}

// formatNumber formats an integer with K/M suffixes for readability.
func formatNumber(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
