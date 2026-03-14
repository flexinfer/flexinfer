package panels

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
	"github.com/crb2nu/loom/internal/tui/widgets"
)

// MsgOverviewData is sent by the app with aggregated data for the overview panel.
type MsgOverviewData struct {
	DaemonRunning  bool
	ServerCount    int
	HealthyServers int
	DownServers    int
	ActiveSessions int
	ActiveAgents   int
	IdleAgents     int
	TotalTokens    int
	PendingTasks   int
	ActiveTasks    int
	BlockedTasks   int
	MemoryItems    int
	MemoryTokens   int
	StreamEntries  int
	Conflicts      int
	Worktrees      int

	// Sparkline data
	HealthHistory []float64
	MemoryHistory []float64
}

// OverviewPanel renders a compact KPI dashboard across all monitors.
type OverviewPanel struct {
	width, height int
	data          MsgOverviewData
}

// NewOverviewPanel creates a new overview panel.
func NewOverviewPanel() OverviewPanel {
	return OverviewPanel{}
}

func (p OverviewPanel) Init() tea.Cmd { return nil }

func (p OverviewPanel) Update(msg tea.Msg) (OverviewPanel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height
	case MsgOverviewData:
		p.data = msg
	}
	return p, nil
}

func (p OverviewPanel) View() string {
	var b strings.Builder

	b.WriteString(theme.Styles.SectionTitle.Render("DASHBOARD OVERVIEW"))
	b.WriteString("\n")

	d := p.data

	// Row 1: KPI strip — key numbers across all subsystems
	kpiStyle := lipgloss.NewStyle().Foreground(theme.ColorFgPrimary).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted)
	accentStyle := lipgloss.NewStyle().Foreground(theme.ColorAccent).Bold(true)

	// Daemon status
	daemonStatus := widgets.StatusDot("ok")
	daemonLabel := "Connected"
	if !d.DaemonRunning {
		daemonStatus = widgets.StatusDot("down")
		daemonLabel = "Offline"
	}

	kpiLine := daemonStatus + " " + labelStyle.Render(daemonLabel) +
		"  " + kpiStyle.Render(fmt.Sprintf("%d", d.ServerCount)) + labelStyle.Render(" servers") +
		"  " + kpiStyle.Render(fmt.Sprintf("%d", d.ActiveSessions)) + labelStyle.Render(" sessions") +
		"  " + kpiStyle.Render(formatNumber(d.TotalTokens)) + labelStyle.Render(" tokens")
	b.WriteString(kpiLine)
	b.WriteString("\n\n")

	// Row 2: Health + Fleet side-by-side
	colWidth := (p.width - 4) / 2
	if colWidth < 30 {
		colWidth = p.width - 2
	}

	// Health section
	var healthSection strings.Builder
	healthSection.WriteString(accentStyle.Render("♥ HEALTH"))
	healthSection.WriteString("\n")
	healthLabel := fmt.Sprintf("  %d/%d healthy", d.HealthyServers, d.ServerCount)
	healthSection.WriteString(kpiStyle.Render(healthLabel))
	if d.DownServers > 0 {
		healthSection.WriteString("  " + lipgloss.NewStyle().Foreground(theme.ColorError).Render(fmt.Sprintf("%d down", d.DownServers)))
	}
	healthSection.WriteString("\n")
	if len(d.HealthHistory) > 1 {
		sparkWidth := colWidth - 4
		if sparkWidth > 40 {
			sparkWidth = 40
		}
		healthSection.WriteString("  " + widgets.Sparkline{
			Data:  d.HealthHistory,
			Width: sparkWidth,
			Color: theme.ColorSuccess,
		}.Render())
		healthSection.WriteString("\n")
	}

	// Fleet section
	var fleetSection strings.Builder
	fleetSection.WriteString(accentStyle.Render("◈ FLEET"))
	fleetSection.WriteString("\n")
	fleetSection.WriteString("  " +
		lipgloss.NewStyle().Foreground(theme.ColorSuccess).Render(fmt.Sprintf("%d active", d.ActiveAgents)) +
		"  " +
		lipgloss.NewStyle().Foreground(theme.ColorFgMuted).Render(fmt.Sprintf("%d idle", d.IdleAgents)))
	fleetSection.WriteString("\n")
	if d.Conflicts > 0 {
		fleetSection.WriteString("  " + lipgloss.NewStyle().Foreground(theme.ColorWarning).Render(fmt.Sprintf("%d conflicts", d.Conflicts)))
		fleetSection.WriteString("\n")
	}
	if d.Worktrees > 0 {
		fleetSection.WriteString("  " + labelStyle.Render(fmt.Sprintf("%d worktrees", d.Worktrees)))
		fleetSection.WriteString("\n")
	}

	if colWidth < p.width-2 {
		// Side-by-side layout
		left := lipgloss.NewStyle().Width(colWidth).Render(healthSection.String())
		right := lipgloss.NewStyle().Width(colWidth).Render(fleetSection.String())
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, right))
	} else {
		b.WriteString(healthSection.String())
		b.WriteString(fleetSection.String())
	}
	b.WriteString("\n")

	// Row 3: Tasks + Memory side-by-side
	var taskSection strings.Builder
	taskSection.WriteString(accentStyle.Render("☑ TASKS"))
	taskSection.WriteString("\n")
	taskSection.WriteString("  " +
		lipgloss.NewStyle().Foreground(theme.ColorWarning).Render(fmt.Sprintf("%d pending", d.PendingTasks)) +
		"  " +
		lipgloss.NewStyle().Foreground(theme.ColorSuccess).Render(fmt.Sprintf("%d active", d.ActiveTasks)))
	if d.BlockedTasks > 0 {
		taskSection.WriteString("  " + lipgloss.NewStyle().Foreground(theme.ColorError).Render(fmt.Sprintf("%d blocked", d.BlockedTasks)))
	}
	taskSection.WriteString("\n")

	var memSection strings.Builder
	memSection.WriteString(accentStyle.Render("⦾ MEMORY"))
	memSection.WriteString("\n")
	memSection.WriteString("  " +
		kpiStyle.Render(fmt.Sprintf("%d", d.MemoryItems)) + labelStyle.Render(" items") +
		"  " +
		kpiStyle.Render(formatNumber(d.MemoryTokens)) + labelStyle.Render(" tokens"))
	memSection.WriteString("\n")
	if len(d.MemoryHistory) > 1 {
		sparkWidth := colWidth - 4
		if sparkWidth > 40 {
			sparkWidth = 40
		}
		memSection.WriteString("  " + widgets.Sparkline{
			Data:  d.MemoryHistory,
			Width: sparkWidth,
			Color: theme.ColorTierShort,
		}.Render())
		memSection.WriteString("\n")
	}

	if colWidth < p.width-2 {
		left := lipgloss.NewStyle().Width(colWidth).Render(taskSection.String())
		right := lipgloss.NewStyle().Width(colWidth).Render(memSection.String())
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, right))
	} else {
		b.WriteString(taskSection.String())
		b.WriteString(memSection.String())
	}
	b.WriteString("\n")

	// Row 4: Stream count
	streamLine := accentStyle.Render("≡ STREAM") + "  " +
		kpiStyle.Render(fmt.Sprintf("%d", d.StreamEntries)) + labelStyle.Render(" entries")
	b.WriteString(streamLine)
	b.WriteString("\n\n")

	// Navigation hint
	hintStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted)
	b.WriteString(hintStyle.Render("  1-7:panel  r:refresh  ?:help  q:quit"))
	b.WriteString("\n")

	return b.String()
}
