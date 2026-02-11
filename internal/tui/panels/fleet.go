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

// MsgSessionEntries delivers context entries for an expanded session.
type MsgSessionEntries struct {
	SessionID string
	Entries   []StreamEntryData
}

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
	ID          string
	AgentID     string
	Namespace   string
	Status      string
	Description string
	StartedAt   string
	TokenCount  int
	EntryCount  int
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

	// Interactive state
	selectedIdx    int
	expanded       map[string]bool              // session ID -> expanded
	sessionEntries map[string][]StreamEntryData // session ID -> context entries
	flatRows       []SessionData                // flattened row order for cursor
}

// NewFleetPanel creates a new fleet panel.
func NewFleetPanel() FleetPanel {
	return FleetPanel{
		expanded:       make(map[string]bool),
		sessionEntries: make(map[string][]StreamEntryData),
	}
}

// Init satisfies the bubbletea model interface.
func (p FleetPanel) Init() tea.Cmd { return nil }

// SelectedSession returns the currently selected session ID, if any, for
// use by the parent to fetch session entries on expand.
func (p FleetPanel) SelectedSession() string {
	if len(p.flatRows) == 0 || p.selectedIdx >= len(p.flatRows) {
		return ""
	}
	return p.flatRows[p.selectedIdx].ID
}

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
		p.rebuildFlatRows()
	case MsgSessionEntries:
		p.sessionEntries[msg.SessionID] = msg.Entries
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if p.selectedIdx < len(p.flatRows)-1 {
				p.selectedIdx++
			}
		case "k", "up":
			if p.selectedIdx > 0 {
				p.selectedIdx--
			}
		case "enter":
			if sid := p.SelectedSession(); sid != "" {
				p.expanded[sid] = !p.expanded[sid]
			}
		case "esc":
			// Collapse all
			for k := range p.expanded {
				delete(p.expanded, k)
			}
		}
	}
	return p, nil
}

// rebuildFlatRows builds the ordered list of sessions for cursor navigation.
func (p *FleetPanel) rebuildFlatRows() {
	p.flatRows = p.flatRows[:0]
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
	for _, ns := range namespaces {
		p.flatRows = append(p.flatRows, groups[ns]...)
	}
	if p.selectedIdx >= len(p.flatRows) {
		p.selectedIdx = max(0, len(p.flatRows)-1)
	}
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

	// Column widths — compact mode drops namespace and shortens columns.
	compact := p.width < 60
	colStatus := 3
	colAgent := 16
	colNS := 20
	colTokens := 10
	colAge := 8
	if compact {
		colAgent = 12
		colNS = 0
		colTokens = 8
		colAge = 7
	}

	headerStyle := theme.Styles.TableHeader
	header := headerStyle.Render(padRight("", colStatus)) +
		headerStyle.Render(padRight("Agent", colAgent))
	if !compact {
		header += headerStyle.Render(padRight("Namespace", colNS))
	}
	header += headerStyle.Render(padRight("Tokens", colTokens)) +
		headerStyle.Render(padRight("Age", colAge))

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")

	flatIdx := 0
	for _, ns := range namespaces {
		// Namespace header
		nsLabel := lipgloss.NewStyle().
			Foreground(theme.ColorFgSecondary).
			Bold(true).
			Render(ns)
		b.WriteString(nsLabel)
		b.WriteString("\n")

		for _, s := range groups[ns] {
			isSelected := flatIdx == p.selectedIdx

			style := theme.Styles.TableRow
			if flatIdx%2 == 1 {
				style = theme.Styles.TableRowAlt
			}

			cursor := "  "
			if isSelected {
				cursor = lipgloss.NewStyle().
					Foreground(theme.ColorAccent).
					Bold(true).
					Render("▸ ")
				style = style.Foreground(theme.ColorFgPrimary).Bold(true)
			}

			dot := widgets.StatusDot(normalizeSessionStatus(s.Status))
			age := relativeTime(s.StartedAt)
			row := cursor +
				style.Render(padRight(dot, colStatus)) +
				style.Render(padRight(truncate(s.AgentID, colAgent-1), colAgent))
			if !compact {
				row += style.Render(padRight(truncate(s.Namespace, colNS-1), colNS))
			}
			row += style.Render(padRight(formatNumber(s.TokenCount), colTokens)) +
				style.Render(padRight(age, colAge))

			b.WriteString(row)
			b.WriteString("\n")

			// Show description if selected
			if isSelected && s.Description != "" {
				descStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted).PaddingLeft(5)
				b.WriteString(descStyle.Render(truncate(s.Description, p.width-8)))
				b.WriteString("\n")
			}

			// Show expanded session entries
			if p.expanded[s.ID] {
				entries := p.sessionEntries[s.ID]
				if len(entries) == 0 {
					detailStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted).PaddingLeft(5)
					b.WriteString(detailStyle.Render("(loading entries...)"))
					b.WriteString("\n")
				} else {
					for ei, e := range entries {
						if ei >= 10 {
							moreStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted).PaddingLeft(5)
							b.WriteString(moreStyle.Render(fmt.Sprintf("... +%d more", len(entries)-10)))
							b.WriteString("\n")
							break
						}
						badge := entryTypeBadge(e.EntryType)
						ts := shortTimestamp(e.Timestamp)
						tsStr := lipgloss.NewStyle().Foreground(theme.ColorFgMuted).Render(ts)
						titleStr := lipgloss.NewStyle().Foreground(theme.ColorFgSecondary).Render(truncate(e.Title, p.width-30))
						b.WriteString(fmt.Sprintf("     %s %s %s\n", tsStr, badge, titleStr))
					}
				}
			}

			flatIdx++
		}
	}

	// Navigation hint
	hintStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted)
	b.WriteString(hintStyle.Render("  j/k:move  enter:expand  esc:collapse"))
	b.WriteString("\n")

	return b.String()
}

// relativeTime converts an ISO timestamp or duration string to a human-readable
// relative time like "5m ago" or "2h ago".
func relativeTime(ts string) string {
	if ts == "" {
		return "---"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "---"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "<1m ago"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
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
