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
	AgentID       string
	SessionID     string
	Status        string
	AgentType     string
	Description   string
	CurrentTask   string
	Branch        string
	LastHeartbeat string
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
	selectedIdx     int
	expanded        map[string]bool              // session ID -> expanded
	sessionEntries  map[string][]StreamEntryData // session ID -> context entries
	flatRows        []SessionData                // flattened row order for cursor
	showAllSessions bool
	hiddenSessions  int
}

// NewFleetPanel creates a new fleet panel.
func NewFleetPanel() FleetPanel {
	return FleetPanel{
		expanded:        make(map[string]bool),
		sessionEntries:  make(map[string][]StreamEntryData),
		showAllSessions: false,
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
		case "v":
			p.showAllSessions = !p.showAllSessions
			p.rebuildFlatRows()
		}
	}
	return p, nil
}

// rebuildFlatRows builds the ordered list of sessions for cursor navigation.
func (p *FleetPanel) rebuildFlatRows() {
	p.flatRows = p.flatRows[:0]
	agentsBySession, agentsByID := p.agentLookups()
	sessions, hidden := p.filteredSessions(agentsBySession, agentsByID)
	p.hiddenSessions = hidden
	namespaces, groups := p.groupedSessions(sessions)
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
	tableWidth := p.width
	if tableWidth <= 0 {
		tableWidth = 100
	}

	agentsBySession, agentsByID := p.agentLookups()
	sessions, hidden := p.filteredSessions(agentsBySession, agentsByID)
	namespaces, groups := p.groupedSessions(sessions)

	// Column widths (fluid). Namespace is already the group header, so the table
	// focuses on per-session fields to avoid redundant + wrap-prone layouts.
	compact := tableWidth < 90
	gap := 2 // spaces between columns
	colCursor := 2
	colStatus := 2
	colSession := 10
	colState := 11
	colTokens := 8
	colAge := 8
	if compact {
		colSession = 8
		colState = 9
		colTokens = 6
		colAge = 7
	}
	// Whatever remains goes to Actor (truncate as needed).
	colActor := tableWidth - (colCursor + colStatus + colSession + colState + colTokens + colAge) - gap*5
	if colActor < 14 {
		colActor = 14
	}

	headerTextStyle := lipgloss.NewStyle().Foreground(theme.ColorFgSecondary).Bold(true)
	sepStyle := lipgloss.NewStyle().Foreground(theme.ColorBorder)

	header := strings.Join([]string{
		padRight("", colCursor+colStatus),
		padRight("Session", colSession),
		padRight("Actor", colActor),
		padRight("State", colState),
		padRight("Tokens", colTokens),
		padRight("Age", colAge),
	}, spaces(gap))

	var b strings.Builder
	b.WriteString(headerTextStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(sepStyle.Render(strings.Repeat("─", min(tableWidth, lipgloss.Width(header)))))
	b.WriteString("\n")

	if hidden > 0 && !p.showAllSessions {
		hint := fmt.Sprintf("focused view: %d hidden stale sessions (press v to show all)", hidden)
		b.WriteString(lipgloss.NewStyle().Foreground(theme.ColorFgMuted).Render(truncate(hint, tableWidth)))
		b.WriteString("\n")
	}

	flatIdx := 0
	for _, ns := range namespaces {
		// Namespace header
		nsSessions := groups[ns]
		nsActive := 0
		nsTokens := 0
		for _, s := range nsSessions {
			if normalizedStatus(s.Status) == "active" {
				nsActive++
			}
			nsTokens += s.TokenCount
		}
		nsMeta := fmt.Sprintf("%s  (%d sessions, %d active, %s tok)", ns, len(nsSessions), nsActive, formatNumber(nsTokens))
		nsLabel := lipgloss.NewStyle().
			Foreground(theme.ColorFgSecondary).
			Bold(true).
			Render(truncate(nsMeta, tableWidth))
		b.WriteString(nsLabel)
		b.WriteString("\n")

		for _, s := range nsSessions {
			isSelected := flatIdx == p.selectedIdx
			agentInfo := resolveAgentForSession(s, agentsBySession, agentsByID)

			rowStyle := lipgloss.NewStyle().Foreground(theme.ColorFgPrimary)
			if flatIdx%2 == 1 {
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

			state := sessionStateLabel(s.Status, agentInfo.Status)
			sessionStatus := normalizedStatus(s.Status)
			presenceStatus := normalizedStatus(agentInfo.Status)
			dotStatus := normalizeSessionStatus(sessionStatus)
			if presenceStatus != "" && sessionStatus != "" && presenceStatus != sessionStatus {
				dotStatus = "degraded"
			}
			dot := widgets.StatusDot(dotStatus)
			sessionID := truncate(shortSessionID(s.ID), colSession)
			age := truncate(relativeTime(s.StartedAt), colAge)
			tokens := truncate(formatNumber(s.TokenCount), colTokens)
			actor := truncate(sessionActorLabel(s, agentInfo), colActor)
			stateLabel := truncate(state, colState)
			row := strings.Join([]string{
				cursor + padRight(dot, colStatus),
				padRight(sessionID, colSession),
				padRight(actor, colActor),
				padRight(stateLabel, colState),
				padRight(tokens, colTokens),
				padRight(age, colAge),
			}, spaces(gap))

			b.WriteString(rowStyle.Render(row))
			b.WriteString("\n")

			// Show session details if selected
			if isSelected {
				detailStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted).PaddingLeft(5)
				detailMax := p.width - 8
				if detailMax < 24 {
					detailMax = 24
				}

				agentID := strings.TrimSpace(agentInfo.AgentID)
				if agentID == "" {
					agentID = strings.TrimSpace(s.AgentID)
				}

				details := []string{fmt.Sprintf("id:%s", shortSessionID(s.ID))}
				if agentID != "" {
					details = append(details, fmt.Sprintf("agent:%s", agentID))
				}
				if agentType := canonicalAgentType(agentInfo.AgentType, agentID); agentType != "unknown" {
					details = append(details, fmt.Sprintf("type:%s", agentType))
				}
				if s.ID != "" {
					details = append(details, fmt.Sprintf("sid:%s", s.ID))
				}
				b.WriteString(detailStyle.Render(truncate(strings.Join(details, "  "), detailMax)))
				b.WriteString("\n")

				statusDetails := []string{fmt.Sprintf("session:%s", statusDisplay(s.Status))}
				if presence := strings.TrimSpace(agentInfo.Status); presence != "" {
					statusDetails = append(statusDetails, fmt.Sprintf("presence:%s", statusDisplay(presence)))
				}
				if hb := relativeTime(agentInfo.LastHeartbeat); hb != "---" {
					statusDetails = append(statusDetails, fmt.Sprintf("hb:%s", hb))
				}
				b.WriteString(detailStyle.Render(truncate(strings.Join(statusDetails, "  "), detailMax)))
				b.WriteString("\n")

				if agentInfo.CurrentTask != "" {
					b.WriteString(detailStyle.Render(truncate("task: "+agentInfo.CurrentTask, detailMax)))
					b.WriteString("\n")
				}
				if agentInfo.Branch != "" {
					b.WriteString(detailStyle.Render(truncate("branch: "+agentInfo.Branch, detailMax)))
					b.WriteString("\n")
				}
				if s.Description != "" {
					b.WriteString(detailStyle.Render(truncate("note: "+s.Description, detailMax)))
					b.WriteString("\n")
				}
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
	focusLabel := "focus"
	if p.showAllSessions {
		focusLabel = "all"
	}
	b.WriteString(hintStyle.Render(fmt.Sprintf("  j/k:move  enter:expand  esc:collapse  v:view(%s)", focusLabel)))
	b.WriteString("\n")

	return b.String()
}

func (p FleetPanel) groupedSessions(sessions []SessionData) ([]string, map[string][]SessionData) {
	groups := make(map[string][]SessionData)
	var namespaces []string
	for _, s := range sessions {
		ns := sessionNamespace(s)
		if _, ok := groups[ns]; !ok {
			namespaces = append(namespaces, ns)
		}
		groups[ns] = append(groups[ns], s)
	}
	sort.Strings(namespaces)
	for _, ns := range namespaces {
		sort.SliceStable(groups[ns], func(i, j int) bool {
			left := groups[ns][i]
			right := groups[ns][j]
			leftRank := sessionStatusRank(left.Status)
			rightRank := sessionStatusRank(right.Status)
			if leftRank != rightRank {
				return leftRank < rightRank
			}
			leftStarted := parseRFC3339(left.StartedAt)
			rightStarted := parseRFC3339(right.StartedAt)
			if !leftStarted.Equal(rightStarted) {
				return leftStarted.After(rightStarted)
			}
			if left.TokenCount != right.TokenCount {
				return left.TokenCount > right.TokenCount
			}
			return left.ID < right.ID
		})
	}
	return namespaces, groups
}

func (p FleetPanel) filteredSessions(agentsBySession, agentsByID map[string]AgentData) ([]SessionData, int) {
	if p.showAllSessions {
		return p.sessions, 0
	}
	now := time.Now()
	result := make([]SessionData, 0, len(p.sessions))
	hidden := 0
	for _, s := range p.sessions {
		agentInfo := resolveAgentForSession(s, agentsBySession, agentsByID)
		if isSessionStale(s, agentInfo, now) {
			hidden++
			continue
		}
		result = append(result, s)
	}
	return result, hidden
}

func isSessionStale(session SessionData, agent AgentData, now time.Time) bool {
	sessionStatus := normalizedStatus(session.Status)
	presenceStatus := normalizedStatus(agent.Status)

	// Keep active/idle sessions visible regardless of age.
	if sessionStatus == "active" || sessionStatus == "idle" || presenceStatus == "active" {
		return false
	}
	// Keep sessions with tokens visible so recent useful context doesn't disappear.
	if session.TokenCount > 0 {
		return false
	}

	started := parseRFC3339(session.StartedAt)
	if started.IsZero() {
		return false
	}

	// Hide old terminal/offline sessions in focused view.
	if sessionStatus == "ended" || sessionStatus == "offline" || sessionStatus == "error" {
		return now.Sub(started) > 24*time.Hour
	}
	return false
}

func (p FleetPanel) agentLookups() (map[string]AgentData, map[string]AgentData) {
	bySession := make(map[string]AgentData)
	byAgentID := make(map[string]AgentData)
	for _, agent := range p.agents {
		if sid := strings.TrimSpace(agent.SessionID); sid != "" {
			current, ok := bySession[sid]
			if !ok || preferAgent(agent, current) {
				bySession[sid] = agent
			}
		}
		if aid := strings.TrimSpace(agent.AgentID); aid != "" {
			current, ok := byAgentID[aid]
			if !ok || preferAgent(agent, current) {
				byAgentID[aid] = agent
			}
		}
	}
	return bySession, byAgentID
}

func resolveAgentForSession(session SessionData, bySession, byAgentID map[string]AgentData) AgentData {
	if a, ok := bySession[session.ID]; ok {
		return a
	}
	if a, ok := byAgentID[session.AgentID]; ok {
		return a
	}
	return AgentData{
		AgentID: session.AgentID,
		Status:  session.Status,
	}
}

func sessionNamespace(s SessionData) string {
	ns := strings.TrimSpace(s.Namespace)
	if ns == "" {
		return "(default)"
	}
	return ns
}

func sessionStatusRank(status string) int {
	switch normalizedStatus(status) {
	case "active":
		return 0
	case "idle":
		return 1
	case "offline":
		return 2
	case "ended":
		return 3
	default:
		return 4
	}
}

func preferAgent(candidate, current AgentData) bool {
	candidateRank := sessionStatusRank(candidate.Status)
	currentRank := sessionStatusRank(current.Status)
	if candidateRank != currentRank {
		return candidateRank < currentRank
	}
	candidateHeartbeat := parseRFC3339(candidate.LastHeartbeat)
	currentHeartbeat := parseRFC3339(current.LastHeartbeat)
	return candidateHeartbeat.After(currentHeartbeat)
}

func parseRFC3339(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

func shortSessionID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 10 {
		return id
	}
	return id[:4] + ".." + id[len(id)-4:]
}

func sessionActorLabel(session SessionData, agent AgentData) string {
	agentID := strings.TrimSpace(session.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(agent.AgentID)
	}
	if agentID == "" {
		agentID = "unknown"
	}
	agentType := canonicalAgentType(agent.AgentType, agentID)
	if agentType == "unknown" || strings.EqualFold(agentType, agentID) {
		return agentID
	}
	return fmt.Sprintf("%s/%s", agentType, agentID)
}

func canonicalAgentType(agentType, agentID string) string {
	if t := strings.TrimSpace(strings.ToLower(agentType)); t != "" {
		return t
	}
	id := strings.ToLower(agentID)
	switch {
	case strings.Contains(id, "codex"):
		return "codex"
	case strings.Contains(id, "claude"):
		return "claude"
	case strings.Contains(id, "gemini"):
		return "gemini"
	case strings.Contains(id, "cursor"):
		return "cursor"
	case strings.Contains(id, "zed"):
		return "zed"
	default:
		return "unknown"
	}
}

func sessionStateLabel(sessionStatus, presenceStatus string) string {
	sessionState := normalizedStatus(sessionStatus)
	presenceState := normalizedStatus(presenceStatus)
	sessionCode := statusCode(sessionStatus)
	if sessionState == "" {
		sessionState = "unknown"
		sessionCode = "?"
	}
	if presenceState == "" || presenceState == "unknown" || presenceState == sessionState {
		return sessionCode
	}
	return sessionCode + "/" + statusCode(presenceStatus)
}

func normalizedStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "running", "in_progress", "in-progress":
		return "active"
	case "idle", "waiting":
		return "idle"
	case "offline":
		return "offline"
	case "ended", "closed", "summarized", "completed", "done":
		return "ended"
	case "error", "failed":
		return "error"
	default:
		if strings.TrimSpace(status) == "" {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(status))
	}
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
	switch normalizedStatus(status) {
	case "active":
		return "healthy"
	case "idle":
		return "idle"
	case "ended", "closed", "offline":
		return "down"
	case "error":
		return "degraded"
	default:
		return "degraded"
	}
}

func statusCode(raw string) string {
	status := strings.ToLower(strings.TrimSpace(raw))
	switch status {
	case "":
		return "?"
	case "summarized", "summary":
		return "sum"
	}
	switch normalizedStatus(status) {
	case "active":
		return "act"
	case "idle":
		return "idl"
	case "offline":
		return "off"
	case "ended":
		return "end"
	case "error":
		return "err"
	default:
		if len(status) <= 3 {
			return status
		}
		return status[:3]
	}
}

func statusDisplay(raw string) string {
	status := strings.ToLower(strings.TrimSpace(raw))
	switch status {
	case "":
		return "unknown"
	case "summarized", "summary":
		return "summarized"
	}
	switch normalizedStatus(status) {
	case "active":
		return "active"
	case "idle":
		return "idle"
	case "offline":
		return "offline"
	case "ended":
		return "ended"
	case "error":
		return "error"
	default:
		return status
	}
}
