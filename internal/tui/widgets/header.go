package widgets

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
)

// Header renders the top bar: ◈ LOOM HUD | Connected | 38 servers | 14:23:05
type Header struct {
	DaemonOnline bool
	ServerCount  int
	SessionCount int
	Width        int
}

// Render produces the styled header bar string.
func (h Header) Render() string {
	logo := lipgloss.NewStyle().
		Foreground(theme.ColorAccent).
		Bold(true).
		Render("◈ LOOM HUD")

	sep := lipgloss.NewStyle().
		Foreground(theme.ColorBorder).
		Render(" │ ")

	statusDot := StatusDot(h.daemonStatus())
	statusLabel := h.daemonLabel()
	status := statusDot + " " + statusLabel

	serverInfo := lipgloss.NewStyle().
		Foreground(theme.ColorFgSecondary).
		Render(fmt.Sprintf("%d servers", h.ServerCount))

	sessionInfo := lipgloss.NewStyle().
		Foreground(theme.ColorFgSecondary).
		Render(fmt.Sprintf("%d sessions", h.SessionCount))

	clock := lipgloss.NewStyle().
		Foreground(theme.ColorFgMuted).
		Render(time.Now().Format("15:04:05"))

	left := logo + sep + status + sep + serverInfo + sep + sessionInfo
	right := clock

	// Calculate padding to right-align the clock.
	gap := h.Width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	content := left + strings.Repeat(" ", gap) + right

	return lipgloss.NewStyle().
		Background(theme.ColorBgSecondary).
		Width(h.Width).
		Render(content)
}

// daemonStatus returns the status string for the daemon indicator.
func (h Header) daemonStatus() string {
	if h.DaemonOnline {
		return "ok"
	}
	return "error"
}

// daemonLabel returns the text label for daemon connectivity.
func (h Header) daemonLabel() string {
	style := lipgloss.NewStyle()
	if h.DaemonOnline {
		style = style.Foreground(theme.ColorSuccess)
		return style.Render("Connected")
	}
	style = style.Foreground(theme.ColorError)
	return style.Render("Disconnected")
}
