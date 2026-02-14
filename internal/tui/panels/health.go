package panels

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
	"github.com/crb2nu/loom/internal/tui/widgets"
)

// ---------------------------------------------------------------------------
// Message
// ---------------------------------------------------------------------------

// MsgHealthData is sent by the app when new server health data arrives.
type MsgHealthData struct {
	Servers []ServerData
}

// ServerData holds server health data for the health panel.
type ServerData struct {
	Name           string
	Running        bool
	Healthy        bool
	Latency        float64
	LatencyHistory []float64
	ConsecFails    int
	Error          string
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// HealthPanel renders a server health table with status dots and latency.
type HealthPanel struct {
	width, height int
	servers       []ServerData
}

// NewHealthPanel creates a new health panel.
func NewHealthPanel() HealthPanel {
	return HealthPanel{}
}

// Init satisfies the bubbletea model interface.
func (p HealthPanel) Init() tea.Cmd { return nil }

// Update processes messages.
func (p HealthPanel) Update(msg tea.Msg) (HealthPanel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height
	case MsgHealthData:
		p.servers = msg.Servers
	}
	return p, nil
}

// View renders the health panel.
func (p HealthPanel) View() string {
	var b strings.Builder

	// Section title
	b.WriteString(theme.Styles.SectionTitle.Render("SERVER HEALTH"))
	b.WriteString("\n")

	// Summary counts
	b.WriteString(p.renderSummary())
	b.WriteString("\n\n")

	if len(p.servers) == 0 {
		b.WriteString(theme.Styles.MutedText.Render("  No servers registered"))
		b.WriteString("\n")
		return b.String()
	}

	// Table
	b.WriteString(p.renderTable())
	return b.String()
}

func (p HealthPanel) renderSummary() string {
	var healthy, degraded, down int
	for _, s := range p.servers {
		switch {
		case s.Running && s.Healthy:
			healthy++
		case s.Running && !s.Healthy:
			degraded++
		default:
			down++
		}
	}

	parts := []string{
		theme.Styles.StatusOK.Render(fmt.Sprintf("%d healthy", healthy)),
		theme.Styles.StatusWarn.Render(fmt.Sprintf("%d degraded", degraded)),
		theme.Styles.StatusError.Render(fmt.Sprintf("%d down", down)),
	}
	return strings.Join(parts, "  ")
}

func (p HealthPanel) renderTable() string {
	compact := p.width < 60
	gap := 2 // spaces between columns

	colStatus := 2
	colLatency := 8
	colFails := 5
	colTrend := 16
	if compact {
		colTrend = 0
	}
	// Name gets the remainder.
	colName := p.width - (colStatus + colLatency + colFails + colTrend) - gap*3
	if !compact {
		colName -= gap // extra separator before Trend
	}
	if colName < 12 {
		colName = 12
	}

	headerTextStyle := lipgloss.NewStyle().Foreground(theme.ColorFgSecondary).Bold(true)
	sepStyle := lipgloss.NewStyle().Foreground(theme.ColorBorder)

	cols := []string{
		padRight("", colStatus),
		padRight("Name", colName),
		padRight("Latency", colLatency),
		padRight("Fails", colFails),
	}
	if !compact {
		cols = append(cols, padRight("Trend", colTrend))
	}
	header := strings.Join(cols, spaces(gap))

	var b strings.Builder
	b.WriteString(headerTextStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(sepStyle.Render(strings.Repeat("─", min(p.width, lipgloss.Width(header)))))
	b.WriteString("\n")

	for i, s := range p.servers {
		rowStyle := lipgloss.NewStyle().Foreground(theme.ColorFgPrimary)
		if i%2 == 1 {
			rowStyle = rowStyle.Background(theme.ColorBgTertiary)
		}

		dot := widgets.StatusDot(serverStatus(s))
		latencyStr := formatLatency(s.Latency)
		latencyStyled := colorLatency(latencyStr, s.Latency)

		cols := []string{
			padRight(dot, colStatus),
			padRight(truncate(s.Name, colName), colName),
			padRight(latencyStyled, colLatency),
			padRight(fmt.Sprintf("%d", s.ConsecFails), colFails),
		}
		if !compact {
			spark := widgets.Sparkline{
				Data:  s.LatencyHistory,
				Width: colTrend,
				Color: sparkColor(s),
			}.Render()
			cols = append(cols, padRight(spark, colTrend))
		}

		row := strings.Join(cols, spaces(gap))
		b.WriteString(rowStyle.Render(truncate(row, p.width)))
		b.WriteString("\n")

		// Show error line if present
		if s.Error != "" {
			errStyle := lipgloss.NewStyle().
				Foreground(theme.ColorError).
				PaddingLeft(colStatus + 2)
			b.WriteString(errStyle.Render(truncate(s.Error, p.width-colStatus-4)))
			b.WriteString("\n")
		}
	}

	return b.String()
}

// sparkColor returns the sparkline color based on server health.
func sparkColor(s ServerData) lipgloss.Color {
	switch {
	case s.Running && s.Healthy:
		return theme.ColorSuccess
	case s.Running && !s.Healthy:
		return theme.ColorWarning
	default:
		return theme.ColorError
	}
}

// serverStatus returns a status string for the widget.
func serverStatus(s ServerData) string {
	switch {
	case s.Running && s.Healthy:
		return "healthy"
	case s.Running && !s.Healthy:
		return "degraded"
	default:
		return "down"
	}
}

// formatLatency formats a latency value in milliseconds.
func formatLatency(ms float64) string {
	if ms <= 0 {
		return "---"
	}
	if ms < 1 {
		return "<1ms"
	}
	return fmt.Sprintf("%.0fms", ms)
}

// colorLatency applies color based on latency thresholds.
func colorLatency(text string, ms float64) string {
	switch {
	case ms <= 0:
		return theme.Styles.MutedText.Render(text)
	case ms < 100:
		return lipgloss.NewStyle().Foreground(theme.ColorSuccess).Render(text)
	case ms < 500:
		return lipgloss.NewStyle().Foreground(theme.ColorWarning).Render(text)
	default:
		return lipgloss.NewStyle().Foreground(theme.ColorError).Render(text)
	}
}
