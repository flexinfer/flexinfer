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

// MsgPresenceData is sent by the app when new presence data arrives.
type MsgPresenceData struct {
	Agents       []PresenceAgentData
	Claims       []ClaimData
	Worktrees    []WorktreeData
	ActiveAgents int
	IdleAgents   int
	TotalClaims  int
}

// PresenceAgentData holds agent presence data for the presence panel.
type PresenceAgentData struct {
	AgentID       string
	Status        string
	AgentType     string
	Description   string
	CurrentTask   string
	Branch        string
	LastHeartbeat string
}

// ClaimData holds file claim data for the presence panel.
type ClaimData struct {
	FilePath  string
	AgentID   string
	ClaimType string
	Reason    string
	CreatedAt string
}

// WorktreeData holds worktree data for the presence panel.
type WorktreeData struct {
	Branch    string
	AgentID   string
	Status    string
	Purpose   string
	CreatedAt string
}

// ---------------------------------------------------------------------------
// Sub-tab
// ---------------------------------------------------------------------------

type presenceTab int

const (
	tabAgents presenceTab = iota
	tabClaims
	tabWorktrees
)

var presenceTabNames = []string{"Agents", "Claims", "Worktrees"}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// PresencePanel renders agent presence, file claims, and worktree assignments.
type PresencePanel struct {
	width, height int
	agents        []PresenceAgentData
	claims        []ClaimData
	worktrees     []WorktreeData
	activeAgents  int
	idleAgents    int
	totalClaims   int

	// Interactive state
	activeTab   presenceTab
	selectedIdx int
}

// NewPresencePanel creates a new presence panel.
func NewPresencePanel() PresencePanel {
	return PresencePanel{}
}

// Init satisfies the bubbletea model interface.
func (p PresencePanel) Init() tea.Cmd { return nil }

// Update processes messages.
func (p PresencePanel) Update(msg tea.Msg) (PresencePanel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height
	case MsgPresenceData:
		p.agents = msg.Agents
		p.claims = msg.Claims
		p.worktrees = msg.Worktrees
		p.activeAgents = msg.ActiveAgents
		p.idleAgents = msg.IdleAgents
		p.totalClaims = msg.TotalClaims
		// Clamp cursor after data update.
		p.clampCursor()
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "l":
			p.activeTab = (p.activeTab + 1) % 3
			p.selectedIdx = 0
		case "shift+tab", "h":
			p.activeTab = (p.activeTab + 2) % 3 // wrap backwards
			p.selectedIdx = 0
		case "j", "down":
			if p.selectedIdx < p.rowCount()-1 {
				p.selectedIdx++
			}
		case "k", "up":
			if p.selectedIdx > 0 {
				p.selectedIdx--
			}
		}
	}
	return p, nil
}

// rowCount returns the number of rows in the active tab.
func (p PresencePanel) rowCount() int {
	switch p.activeTab {
	case tabAgents:
		return len(p.agents)
	case tabClaims:
		return len(p.claims)
	case tabWorktrees:
		return len(p.worktrees)
	}
	return 0
}

// clampCursor ensures selectedIdx is within bounds.
func (p *PresencePanel) clampCursor() {
	max := p.rowCount() - 1
	if max < 0 {
		max = 0
	}
	if p.selectedIdx > max {
		p.selectedIdx = max
	}
}

// View renders the presence panel.
func (p PresencePanel) View() string {
	var b strings.Builder

	// Section title
	b.WriteString(theme.Styles.SectionTitle.Render("AGENT PRESENCE"))
	b.WriteString("\n")

	// Summary line
	b.WriteString(p.renderSummary())
	b.WriteString("\n\n")

	// Tab bar
	b.WriteString(p.renderTabBar())
	b.WriteString("\n")

	// Active tab content
	switch p.activeTab {
	case tabAgents:
		b.WriteString(p.renderAgentsTable())
	case tabClaims:
		b.WriteString(p.renderClaimsTable())
	case tabWorktrees:
		b.WriteString(p.renderWorktreesTable())
	}

	// Navigation hint
	hintStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted)
	b.WriteString(hintStyle.Render("  tab/h/l:switch  j/k:move"))
	b.WriteString("\n")

	return b.String()
}

func (p PresencePanel) renderSummary() string {
	offlineCount := len(p.agents) - p.activeAgents - p.idleAgents
	if offlineCount < 0 {
		offlineCount = 0
	}
	parts := []string{
		theme.Styles.StatusOK.Render(fmt.Sprintf("%d active", p.activeAgents)),
		theme.Styles.StatusWarn.Render(fmt.Sprintf("%d idle", p.idleAgents)),
		theme.Styles.StatusMuted.Render(fmt.Sprintf("%d offline", offlineCount)),
		theme.Styles.Label.Render("Claims: ") + theme.Styles.Value.Render(fmt.Sprintf("%d", p.totalClaims)),
		theme.Styles.Label.Render("Worktrees: ") + theme.Styles.Value.Render(fmt.Sprintf("%d", len(p.worktrees))),
	}
	return strings.Join(parts, "  ")
}

func (p PresencePanel) renderTabBar() string {
	var tabs []string
	for i, name := range presenceTabNames {
		style := lipgloss.NewStyle().Foreground(theme.ColorFgMuted).Padding(0, 1)
		if presenceTab(i) == p.activeTab {
			style = lipgloss.NewStyle().
				Foreground(theme.ColorAccent).
				Bold(true).
				Padding(0, 1).
				Border(lipgloss.NormalBorder(), false, false, true, false).
				BorderForeground(theme.ColorAccent)
		}
		count := 0
		switch presenceTab(i) {
		case tabAgents:
			count = len(p.agents)
		case tabClaims:
			count = len(p.claims)
		case tabWorktrees:
			count = len(p.worktrees)
		}
		tabs = append(tabs, style.Render(fmt.Sprintf("%s (%d)", name, count)))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

// ---------------------------------------------------------------------------
// Agents tab
// ---------------------------------------------------------------------------

func (p PresencePanel) renderAgentsTable() string {
	if len(p.agents) == 0 {
		return theme.Styles.MutedText.Render("  No registered agents") + "\n"
	}

	gap := 2
	colCursor := 2
	colStatus := 2
	colType := 10
	colBranch := 14
	colHeart := 8

	// Agent ID gets the remainder.
	colAgent := p.width - (colCursor + colStatus + colType + colBranch + colHeart) - gap*4
	if colAgent < 12 {
		colAgent = 12
	}

	headerTextStyle := lipgloss.NewStyle().Foreground(theme.ColorFgSecondary).Bold(true)
	sepStyle := lipgloss.NewStyle().Foreground(theme.ColorBorder)

	header := strings.Join([]string{
		padRight("", colCursor+colStatus),
		padRight("Agent", colAgent),
		padRight("Type", colType),
		padRight("Branch", colBranch),
		padRight("Heartbeat", colHeart),
	}, spaces(gap))

	var b strings.Builder
	b.WriteString(headerTextStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(sepStyle.Render(strings.Repeat("─", min(p.width, lipgloss.Width(header)))))
	b.WriteString("\n")

	for i, a := range p.agents {
		isSelected := i == p.selectedIdx
		rowStyle := lipgloss.NewStyle().Foreground(theme.ColorFgPrimary)
		if i%2 == 1 {
			rowStyle = rowStyle.Background(theme.ColorBgTertiary)
		}

		cursor := "  "
		if isSelected {
			cursor = lipgloss.NewStyle().
				Foreground(theme.ColorAccent).
				Bold(true).
				Render("▸ ")
			rowStyle = rowStyle.Bold(true)
		}

		dot := widgets.StatusDot(normalizeAgentStatus(a.Status))
		agent := truncate(a.AgentID, colAgent)
		agentType := truncate(a.AgentType, colType)
		branch := truncate(a.Branch, colBranch)
		heartbeat := truncate(relativeTime(a.LastHeartbeat), colHeart)

		row := strings.Join([]string{
			cursor + padRight(dot, colStatus),
			padRight(agent, colAgent),
			padRight(agentType, colType),
			padRight(branch, colBranch),
			padRight(heartbeat, colHeart),
		}, spaces(gap))

		b.WriteString(rowStyle.Render(row))
		b.WriteString("\n")

		// Show current task if selected.
		if isSelected && a.CurrentTask != "" {
			descStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted).PaddingLeft(5)
			b.WriteString(descStyle.Render(truncate(a.CurrentTask, p.width-8)))
			b.WriteString("\n")
		}
	}

	return b.String()
}

// normalizeAgentStatus maps presence status strings to widget status values.
func normalizeAgentStatus(status string) string {
	switch strings.ToLower(status) {
	case "active":
		return "active"
	case "idle":
		return "idle"
	case "offline":
		return "down"
	default:
		return "degraded"
	}
}

// ---------------------------------------------------------------------------
// Claims tab
// ---------------------------------------------------------------------------

func (p PresencePanel) renderClaimsTable() string {
	if len(p.claims) == 0 {
		return theme.Styles.MutedText.Render("  No active file claims") + "\n"
	}

	gap := 2
	colCursor := 2
	colAgent := 14
	colType := 6
	colAge := 8

	// File path gets the remainder.
	colFile := p.width - (colCursor + colAgent + colType + colAge) - gap*3
	if colFile < 16 {
		colFile = 16
	}

	headerTextStyle := lipgloss.NewStyle().Foreground(theme.ColorFgSecondary).Bold(true)
	sepStyle := lipgloss.NewStyle().Foreground(theme.ColorBorder)

	header := strings.Join([]string{
		padRight("", colCursor),
		padRight("File", colFile),
		padRight("Agent", colAgent),
		padRight("Type", colType),
		padRight("Since", colAge),
	}, spaces(gap))

	var b strings.Builder
	b.WriteString(headerTextStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(sepStyle.Render(strings.Repeat("─", min(p.width, lipgloss.Width(header)))))
	b.WriteString("\n")

	for i, c := range p.claims {
		isSelected := i == p.selectedIdx
		rowStyle := lipgloss.NewStyle().Foreground(theme.ColorFgPrimary)
		if i%2 == 1 {
			rowStyle = rowStyle.Background(theme.ColorBgTertiary)
		}

		cursor := "  "
		if isSelected {
			cursor = lipgloss.NewStyle().
				Foreground(theme.ColorAccent).
				Bold(true).
				Render("▸ ")
			rowStyle = rowStyle.Bold(true)
		}

		filePath := truncate(shortenPath(c.FilePath), colFile)
		agent := truncate(c.AgentID, colAgent)
		claimType := truncate(c.ClaimType, colType)
		age := truncate(relativeTime(c.CreatedAt), colAge)

		row := strings.Join([]string{
			cursor,
			padRight(filePath, colFile),
			padRight(agent, colAgent),
			padRight(claimType, colType),
			padRight(age, colAge),
		}, spaces(gap))

		b.WriteString(rowStyle.Render(row))
		b.WriteString("\n")

		// Show reason if selected.
		if isSelected && c.Reason != "" {
			descStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted).PaddingLeft(5)
			b.WriteString(descStyle.Render(truncate(c.Reason, p.width-8)))
			b.WriteString("\n")
		}
	}

	return b.String()
}

// shortenPath shortens an absolute file path by removing common workspace prefixes.
func shortenPath(path string) string {
	// Strip common prefixes for readability.
	prefixes := []string{
		"/Users/",
		"/home/",
	}
	for _, pfx := range prefixes {
		if idx := strings.Index(path, pfx); idx == 0 {
			// Find the third slash (past /Users/name/) and trim.
			parts := strings.SplitN(path[len(pfx):], "/", 2)
			if len(parts) == 2 {
				return "~/" + parts[1]
			}
		}
	}
	return path
}

// ---------------------------------------------------------------------------
// Worktrees tab
// ---------------------------------------------------------------------------

func (p PresencePanel) renderWorktreesTable() string {
	if len(p.worktrees) == 0 {
		return theme.Styles.MutedText.Render("  No active worktrees") + "\n"
	}

	gap := 2
	colCursor := 2
	colStatus := 2
	colAgent := 14
	colAge := 8

	// Branch gets the remainder.
	colBranch := p.width - (colCursor + colStatus + colAgent + colAge) - gap*3
	if colBranch < 16 {
		colBranch = 16
	}

	headerTextStyle := lipgloss.NewStyle().Foreground(theme.ColorFgSecondary).Bold(true)
	sepStyle := lipgloss.NewStyle().Foreground(theme.ColorBorder)

	header := strings.Join([]string{
		padRight("", colCursor+colStatus),
		padRight("Branch", colBranch),
		padRight("Agent", colAgent),
		padRight("Created", colAge),
	}, spaces(gap))

	var b strings.Builder
	b.WriteString(headerTextStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(sepStyle.Render(strings.Repeat("─", min(p.width, lipgloss.Width(header)))))
	b.WriteString("\n")

	for i, w := range p.worktrees {
		isSelected := i == p.selectedIdx
		rowStyle := lipgloss.NewStyle().Foreground(theme.ColorFgPrimary)
		if i%2 == 1 {
			rowStyle = rowStyle.Background(theme.ColorBgTertiary)
		}

		cursor := "  "
		if isSelected {
			cursor = lipgloss.NewStyle().
				Foreground(theme.ColorAccent).
				Bold(true).
				Render("▸ ")
			rowStyle = rowStyle.Bold(true)
		}

		dot := widgets.StatusDot(normalizeWorktreeStatus(w.Status))
		branch := truncate(w.Branch, colBranch)
		agent := truncate(w.AgentID, colAgent)
		age := truncate(relativeTime(w.CreatedAt), colAge)

		row := strings.Join([]string{
			cursor + padRight(dot, colStatus),
			padRight(branch, colBranch),
			padRight(agent, colAgent),
			padRight(age, colAge),
		}, spaces(gap))

		b.WriteString(rowStyle.Render(row))
		b.WriteString("\n")

		// Show purpose if selected.
		if isSelected && w.Purpose != "" {
			descStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted).PaddingLeft(5)
			b.WriteString(descStyle.Render(truncate(w.Purpose, p.width-8)))
			b.WriteString("\n")
		}
	}

	return b.String()
}

// normalizeWorktreeStatus maps worktree status to widget status values.
func normalizeWorktreeStatus(status string) string {
	switch strings.ToLower(status) {
	case "active":
		return "healthy"
	case "released", "cleaned":
		return "idle"
	case "error", "conflict":
		return "error"
	default:
		return "degraded"
	}
}
