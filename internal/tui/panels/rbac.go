package panels

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
)

// MsgRBACData is sent by the app when fresh RBAC data arrives.
type MsgRBACData struct {
	Enabled        bool
	AuditEnabled   bool
	DefaultPolicy  string
	RoleCount      int
	BindingCount   int
	GlobalDenyN    int
	DeniedCount24h int
	RecentDenied   []RBACDeniedRow
}

// RBACDeniedRow is one entry in the recent denials list.
type RBACDeniedRow struct {
	AgentID   string
	Server    string
	Tool      string
	Reason    string
	Timestamp string
}

// RBACPanel renders RBAC posture: policy summary, recent denials.
type RBACPanel struct {
	width, height int
	data          MsgRBACData
}

// NewRBACPanel creates a new RBAC panel.
func NewRBACPanel() RBACPanel { return RBACPanel{} }

// Init satisfies the bubbletea model interface.
func (p RBACPanel) Init() tea.Cmd { return nil }

// Update processes messages.
func (p RBACPanel) Update(msg tea.Msg) (RBACPanel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height
	case MsgRBACData:
		p.data = msg
	}
	return p, nil
}

// View renders the RBAC panel.
func (p RBACPanel) View() string {
	var b strings.Builder

	b.WriteString(theme.Styles.SectionTitle.Render("RBAC POSTURE"))
	b.WriteString("\n")

	if !p.data.Enabled {
		b.WriteString(theme.Styles.MutedText.Render("  RBAC is disabled"))
		b.WriteString("\n")
		return b.String()
	}

	b.WriteString(p.renderSummary())
	b.WriteString("\n\n")
	b.WriteString(p.renderRecentDenials())

	return b.String()
}

func (p RBACPanel) renderSummary() string {
	auditState := "audit off"
	auditStyle := theme.Styles.MutedText
	if p.data.AuditEnabled {
		auditState = "audit on"
		auditStyle = theme.Styles.StatusOK
	}
	parts := []string{
		theme.Styles.StatusOK.Render(fmt.Sprintf("policy: %s", emptyDashRBAC(p.data.DefaultPolicy))),
		auditStyle.Render(auditState),
		theme.Styles.MutedText.Render(fmt.Sprintf("%d roles", p.data.RoleCount)),
		theme.Styles.MutedText.Render(fmt.Sprintf("%d bindings", p.data.BindingCount)),
		theme.Styles.StatusError.Render(fmt.Sprintf("%d denied", p.data.DeniedCount24h)),
	}
	return "  " + strings.Join(parts, "  ")
}

func (p RBACPanel) renderRecentDenials() string {
	var b strings.Builder
	b.WriteString(theme.Styles.SectionTitle.Render("Recent Denials"))
	b.WriteString("\n")

	if len(p.data.RecentDenied) == 0 {
		b.WriteString(theme.Styles.MutedText.Render("  no recent denials"))
		return b.String()
	}

	headerStyle := lipgloss.NewStyle().Foreground(theme.ColorFgSecondary).Bold(true)
	b.WriteString(headerStyle.Render(padRight("AGENT", 20) + spaces(2) +
		padRight("SERVER", 14) + spaces(2) + padRight("TOOL", 22) + spaces(2) +
		padRight("REASON", 24)))
	b.WriteString("\n")

	for i, r := range p.data.RecentDenied {
		rowStyle := lipgloss.NewStyle().Foreground(theme.ColorFgPrimary)
		if i%2 == 1 {
			rowStyle = rowStyle.Background(theme.ColorBgTertiary)
		}
		row := padRight(truncate(r.AgentID, 20), 20) + spaces(2) +
			padRight(truncate(r.Server, 14), 14) + spaces(2) +
			padRight(truncate(r.Tool, 22), 22) + spaces(2) +
			padRight(truncate(r.Reason, 24), 24)
		b.WriteString(rowStyle.Render(truncate(row, p.width)))
		b.WriteString("\n")
	}
	return b.String()
}

func emptyDashRBAC(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
