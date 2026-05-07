package panels

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
)

// MsgCostData is sent by the app when new cost data arrives.
type MsgCostData struct {
	Enabled     bool
	TotalCalls  int64
	TotalErrors int64
	TotalDenied int64
	TotalCached int64
	ByAgent     []CostAgentRow
	ByServer    []CostServerRow
}

// CostAgentRow is a per-agent cost summary row.
type CostAgentRow struct {
	AgentID   string
	CallCount int64
	Errors    int64
	Denied    int64
	Cached    int64
}

// CostServerRow is a per-server cost summary row.
type CostServerRow struct {
	Server    string
	CallCount int64
	Errors    int64
}

// CostPanel renders cost/usage rollups from the daemon's cost-stats RPC.
type CostPanel struct {
	width, height int
	data          MsgCostData
}

// NewCostPanel creates a new cost panel.
func NewCostPanel() CostPanel { return CostPanel{} }

// Init satisfies the bubbletea model interface.
func (p CostPanel) Init() tea.Cmd { return nil }

// Update processes messages.
func (p CostPanel) Update(msg tea.Msg) (CostPanel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height
	case MsgCostData:
		p.data = msg
	}
	return p, nil
}

// View renders the cost panel.
func (p CostPanel) View() string {
	var b strings.Builder

	b.WriteString(theme.Styles.SectionTitle.Render("COST & USAGE"))
	b.WriteString("\n")

	if !p.data.Enabled {
		b.WriteString(theme.Styles.MutedText.Render("  Cost tracking is disabled"))
		b.WriteString("\n")
		return b.String()
	}

	b.WriteString(p.renderTotals())
	b.WriteString("\n\n")
	b.WriteString(p.renderByAgent())
	b.WriteString("\n")
	b.WriteString(p.renderByServer())

	return b.String()
}

func (p CostPanel) renderTotals() string {
	parts := []string{
		theme.Styles.StatusOK.Render(fmt.Sprintf("%d calls", p.data.TotalCalls)),
		theme.Styles.StatusError.Render(fmt.Sprintf("%d errors", p.data.TotalErrors)),
		theme.Styles.StatusWarn.Render(fmt.Sprintf("%d denied", p.data.TotalDenied)),
		theme.Styles.MutedText.Render(fmt.Sprintf("%d cached", p.data.TotalCached)),
	}
	return "  " + strings.Join(parts, "  ")
}

func (p CostPanel) renderByAgent() string {
	var b strings.Builder
	b.WriteString(theme.Styles.SectionTitle.Render("By Agent"))
	b.WriteString("\n")

	if len(p.data.ByAgent) == 0 {
		b.WriteString(theme.Styles.MutedText.Render("  no agent activity"))
		return b.String()
	}

	headerStyle := lipgloss.NewStyle().Foreground(theme.ColorFgSecondary).Bold(true)
	b.WriteString(headerStyle.Render(padRight("AGENT", 24) + spaces(2) +
		padRight("CALLS", 8) + spaces(2) + padRight("ERR", 6) + spaces(2) +
		padRight("DENY", 6) + spaces(2) + padRight("CACHE", 8)))
	b.WriteString("\n")

	for i, r := range p.data.ByAgent {
		rowStyle := lipgloss.NewStyle().Foreground(theme.ColorFgPrimary)
		if i%2 == 1 {
			rowStyle = rowStyle.Background(theme.ColorBgTertiary)
		}
		row := padRight(truncate(r.AgentID, 24), 24) + spaces(2) +
			padRight(fmt.Sprintf("%d", r.CallCount), 8) + spaces(2) +
			padRight(fmt.Sprintf("%d", r.Errors), 6) + spaces(2) +
			padRight(fmt.Sprintf("%d", r.Denied), 6) + spaces(2) +
			padRight(fmt.Sprintf("%d", r.Cached), 8)
		b.WriteString(rowStyle.Render(truncate(row, p.width)))
		b.WriteString("\n")
	}
	return b.String()
}

func (p CostPanel) renderByServer() string {
	var b strings.Builder
	b.WriteString(theme.Styles.SectionTitle.Render("By Server"))
	b.WriteString("\n")

	if len(p.data.ByServer) == 0 {
		b.WriteString(theme.Styles.MutedText.Render("  no server activity"))
		return b.String()
	}

	headerStyle := lipgloss.NewStyle().Foreground(theme.ColorFgSecondary).Bold(true)
	b.WriteString(headerStyle.Render(padRight("SERVER", 24) + spaces(2) +
		padRight("CALLS", 8) + spaces(2) + padRight("ERR", 6)))
	b.WriteString("\n")

	for i, r := range p.data.ByServer {
		rowStyle := lipgloss.NewStyle().Foreground(theme.ColorFgPrimary)
		if i%2 == 1 {
			rowStyle = rowStyle.Background(theme.ColorBgTertiary)
		}
		row := padRight(truncate(r.Server, 24), 24) + spaces(2) +
			padRight(fmt.Sprintf("%d", r.CallCount), 8) + spaces(2) +
			padRight(fmt.Sprintf("%d", r.Errors), 6)
		b.WriteString(rowStyle.Render(truncate(row, p.width)))
		b.WriteString("\n")
	}
	return b.String()
}
