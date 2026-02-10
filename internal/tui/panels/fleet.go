package panels

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
	"github.com/crb2nu/loom/internal/tui/widgets"
)

// ---------------------------------------------------------------------------
// Message
// ---------------------------------------------------------------------------

// MsgFleetData is sent by the app when new fleet data arrives.
type MsgFleetData struct {
	DaemonRunning  bool
	ServerCount    int
	Sessions       []SessionData
	ActiveSessions int
	Agents         []AgentData
	TotalTokens    int
	UpdatedAt      time.Time
}

// SessionData holds session data for the fleet panel.
type SessionData struct {
	ID         string
	AgentID    string
	Namespace  string
	Status     string
	TokenCount int
	EntryCount int
}

// AgentData holds agent presence data for the fleet panel.
type AgentData struct {
	AgentID   string
	Status    string
	AgentType string
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// FleetPanel renders an overview of the active agent fleet.
type FleetPanel struct {
	width, height  int
	daemonRunning  bool
	serverCount    int
	sessions       []SessionData
	activeSessions int
	agents         []AgentData
	totalTokens    int
	updatedAt      time.Time
}

// NewFleetPanel creates a new fleet panel.
func NewFleetPanel() FleetPanel {
	return FleetPanel{}
}

// Init satisfies the bubbletea model interface.
func (p FleetPanel) Init() tea.Cmd { return nil }

// Update processes messages.
func (p FleetPanel) Update(msg tea.Msg) (FleetPanel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height
	case MsgFleetData:
		p.daemonRunning = msg.DaemonRunning
		p.serverCount = msg.ServerCount
		p.sessions = msg.Sessions
		p.activeSessions = msg.ActiveSessions
		p.agents = msg.Agents
		p.totalTokens = msg.TotalTokens
		p.updatedAt = msg.UpdatedAt
	}
	return p, nil
}

// View renders the fleet panel.
func (p FleetPanel) View() string {
	var b strings.Builder

	// Section title
	b.WriteString(theme.Styles.SectionTitle.Render("FLEET OVERVIEW"))
	b.WriteString("\n")

	// Summary line
	b.WriteString(p.renderSummary())
	b.WriteString("\n\n")

	// Session table grouped by namespace
	if len(p.sessions) == 0 {
		b.WriteString(theme.Styles.MutedText.Render("  No active sessions"))
		b.WriteString("\n")
		return b.String()
	}

	b.WriteString(p.renderSessionTable())
	return b.String()
}

func (p FleetPanel) renderSummary() string {
	daemonStatus := widgets.StatusDot("healthy")
	daemonLabel := theme.Styles.StatusOK.Render("running")
	if !p.daemonRunning {
		daemonStatus = widgets.StatusDot("down")
		daemonLabel = theme.Styles.StatusError.Render("stopped")
	}

	parts := []string{
		fmt.Sprintf("%s Daemon %s", daemonStatus, daemonLabel),
		theme.Styles.Label.Render("Servers: ") + theme.Styles.Value.Render(fmt.Sprintf("%d", p.serverCount)),
		theme.Styles.Label.Render("Sessions: ") + theme.Styles.Value.Render(fmt.Sprintf("%d", p.activeSessions)),
		theme.Styles.Label.Render("Tokens: ") + theme.Styles.Value.Render(formatNumber(p.totalTokens)),
	}
	return strings.Join(parts, "  ")
}

func (p FleetPanel) renderSessionTable() string {
	// Group sessions by namespace.
	groups := make(map[string][]SessionData)
	var namespaces []string
	for _, s := range p.sessions {
		ns := s.Namespace
		if ns == "" {
			ns = "(default)"
		}
		if _, ok := groups[ns]; !ok {
			namespaces = append(namespaces, ns)
		}
		groups[ns] = append(groups[ns], s)
	}
	sort.Strings(namespaces)

	// Column widths
	colStatus := 3
	colAgent := 16
	colNS := 20
	colTokens := 10
	colEntries := 8

	headerStyle := theme.Styles.TableHeader
	header := headerStyle.Render(padRight("", colStatus)) +
		headerStyle.Render(padRight("Agent", colAgent)) +
		headerStyle.Render(padRight("Namespace", colNS)) +
		headerStyle.Render(padRight("Tokens", colTokens)) +
		headerStyle.Render(padRight("Entries", colEntries))

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")

	rowIdx := 0
	for _, ns := range namespaces {
		// Namespace header
		nsLabel := lipgloss.NewStyle().
			Foreground(theme.ColorFgSecondary).
			Bold(true).
			Render(ns)
		b.WriteString(nsLabel)
		b.WriteString("\n")

		for _, s := range groups[ns] {
			style := theme.Styles.TableRow
			if rowIdx%2 == 1 {
				style = theme.Styles.TableRowAlt
			}

			dot := widgets.StatusDot(normalizeSessionStatus(s.Status))
			row := style.Render(padRight(dot, colStatus)) +
				style.Render(padRight(truncate(s.AgentID, colAgent-1), colAgent)) +
				style.Render(padRight(truncate(s.Namespace, colNS-1), colNS)) +
				style.Render(padRight(formatNumber(s.TokenCount), colTokens)) +
				style.Render(padRight(fmt.Sprintf("%d", s.EntryCount), colEntries))

			b.WriteString(row)
			b.WriteString("\n")
			rowIdx++
		}
	}

	return b.String()
}

// normalizeSessionStatus maps session status strings to widget status values.
func normalizeSessionStatus(status string) string {
	switch strings.ToLower(status) {
	case "active", "running":
		return "healthy"
	case "idle":
		return "idle"
	case "ended", "closed":
		return "down"
	default:
		return "degraded"
	}
}
