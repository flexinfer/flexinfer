package panels

import "fmt"

// padRight pads a string to the given width with spaces.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + spaces(width-len(s))
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

// truncate shortens a string to maxLen, adding an ellipsis if needed.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
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
