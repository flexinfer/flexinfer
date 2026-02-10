package widgets

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
)

// Unicode block characters ordered by height (index 0 = lowest).
var blockChars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// Sparkline renders a compact sparkline from a slice of float64 values.
type Sparkline struct {
	Data  []float64
	Width int            // maximum number of characters to render
	Color lipgloss.Color // foreground color for the sparkline
}

// Render produces the styled sparkline string.
func (s Sparkline) Render() string {
	data := s.Data
	if len(data) == 0 {
		return lipgloss.NewStyle().
			Foreground(theme.ColorFgMuted).
			Render("--")
	}

	width := s.Width
	if width <= 0 {
		width = len(data)
	}

	// Take the last `width` data points if we have more.
	if len(data) > width {
		data = data[len(data)-width:]
	}

	min, max := data[0], data[0]
	for _, v := range data {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}

	var b strings.Builder
	span := max - min
	for _, v := range data {
		idx := 0
		if span > 0 {
			idx = int(((v - min) / span) * 7)
		}
		if idx < 0 {
			idx = 0
		}
		if idx > 7 {
			idx = 7
		}
		b.WriteRune(blockChars[idx])
	}

	style := lipgloss.NewStyle().Foreground(s.Color)
	return style.Render(b.String())
}
