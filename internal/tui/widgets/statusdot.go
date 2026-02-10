package widgets

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
)

// StatusDot renders a colored status indicator based on the given status string.
//
// Mappings:
//
//	"ok", "healthy"           -> green filled circle
//	"warning", "degraded"     -> yellow filled circle
//	"error", "down"           -> red filled circle
//	"idle"                    -> gray open circle
//	"active"                  -> green double circle (glow)
//	anything else             -> muted filled circle
func StatusDot(status string) string {
	s := strings.ToLower(status)

	switch s {
	case "ok", "healthy":
		return lipgloss.NewStyle().
			Foreground(theme.ColorSuccess).
			Render("●")

	case "warning", "degraded":
		return lipgloss.NewStyle().
			Foreground(theme.ColorWarning).
			Render("●")

	case "error", "down":
		return lipgloss.NewStyle().
			Foreground(theme.ColorError).
			Render("●")

	case "idle":
		return lipgloss.NewStyle().
			Foreground(theme.ColorFgMuted).
			Render("○")

	case "active":
		return lipgloss.NewStyle().
			Foreground(theme.ColorSuccess).
			Bold(true).
			Render("◉")

	default:
		return lipgloss.NewStyle().
			Foreground(theme.ColorFgMuted).
			Render("●")
	}
}
