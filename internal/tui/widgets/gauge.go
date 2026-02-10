package widgets

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
)

// Gauge renders a horizontal bar gauge: Label [████░░░░] 67%
type Gauge struct {
	Label string
	Value float64        // 0.0 to 1.0
	Width int            // total character width of the bar portion
	Color lipgloss.Color // fill color
}

// Render produces the styled gauge string.
func (g Gauge) Render() string {
	value := clampFloat(g.Value, 0, 1)
	width := g.Width
	if width < 2 {
		width = 10
	}

	filled := int(float64(width) * value)
	empty := width - filled

	fillStyle := lipgloss.NewStyle().Foreground(g.Color)
	emptyStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted)
	labelStyle := lipgloss.NewStyle().Foreground(theme.ColorFgSecondary)
	pctStyle := lipgloss.NewStyle().Foreground(theme.ColorFgPrimary)

	bar := fillStyle.Render(strings.Repeat("█", filled)) +
		emptyStyle.Render(strings.Repeat("░", empty))

	pct := pctStyle.Render(fmt.Sprintf("%3.0f%%", value*100))

	parts := []string{}
	if g.Label != "" {
		parts = append(parts, labelStyle.Render(g.Label))
	}
	parts = append(parts, "["+bar+"]", pct)

	return strings.Join(parts, " ")
}

// clampFloat restricts v to [min, max].
func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
