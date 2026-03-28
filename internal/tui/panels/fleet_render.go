package panels

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
	"github.com/crb2nu/loom/internal/tui/widgets"
)

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
		theme.Styles.Label.Render("Risk: ") + theme.Styles.Value.Render(fmt.Sprintf("%d ns / %d agents", p.namespacesAtRisk, p.agentsNeedingAttention)),
		theme.Styles.Label.Render("Relations: ") + theme.Styles.Value.Render(fmt.Sprintf("%d branches · %d files · %d orphans", p.sharedBranches, p.conflictFiles, p.orphanTasks)),
		theme.Styles.Label.Render("Sort: ") + theme.Styles.Value.Render(string(p.sortMode)),
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
	namespaces, groups := p.groupedSessions(sessions, agentsBySession, agentsByID)
	hidden += p.hiddenByCollapsedNamespaces(groups)

	// Column widths (fluid). Namespace is already the group header, so the table
	// focuses on per-session fields to avoid redundant + wrap-prone layouts.
	compact := tableWidth < 90
	gap := 2 // spaces between columns
	colCursor := 2
	colStatus := 2
	colSession := 10
	colState := 11
	colTokens := 8
	colLast := 8
	if compact {
		colSession = 8
		colState = 9
		colTokens = 6
		colLast = 7
	}
	// Whatever remains goes to Actor (truncate as needed).
	colActor := tableWidth - (colCursor + colStatus + colSession + colState + colTokens + colLast) - gap*5
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
		padRight("Last", colLast),
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
	selectedNS := p.selectedNamespace()
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

		// Coordination risk badges
		if coord, ok := p.namespaceCoordination[ns]; ok {
			var badges []string
			if coord.BlockedTasks > 0 {
				badges = append(badges, lipgloss.NewStyle().Foreground(theme.ColorError).Render(fmt.Sprintf("%d blocked", coord.BlockedTasks)))
			}
			if coord.ConflictFiles > 0 {
				badges = append(badges, lipgloss.NewStyle().Foreground(theme.ColorWarning).Render(fmt.Sprintf("%d conflicts", coord.ConflictFiles)))
			}
			if coord.CrossAgentBlockers > 0 {
				badges = append(badges, lipgloss.NewStyle().Foreground(theme.ColorError).Bold(true).Render(fmt.Sprintf("%d x-agent", coord.CrossAgentBlockers)))
			}
			if coord.OrphanTasks > 0 {
				badges = append(badges, lipgloss.NewStyle().Foreground(theme.ColorFgMuted).Render(fmt.Sprintf("%d orphans", coord.OrphanTasks)))
			}
			if len(badges) > 0 {
				nsMeta += "  " + strings.Join(badges, " · ")
			}
		}

		indicator := "▾ "
		if p.collapsedNS[ns] {
			indicator = "▸ "
		}
		nsStyle := lipgloss.NewStyle().
			Foreground(theme.ColorFgSecondary).
			Bold(true)
		if selectedNS == ns {
			nsStyle = nsStyle.Foreground(theme.ColorAccent)
		}
		nsLabel := nsStyle.Render(truncate(indicator+nsMeta, tableWidth))
		b.WriteString(nsLabel)
		b.WriteString("\n")
		if p.collapsedNS[ns] {
			continue
		}

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
			last := truncate(lastActivityLabel(sessionLastActivityTime(s, agentInfo)), colLast)
			tokens := truncate(formatNumber(s.TokenCount), colTokens)
			actor := truncate(sessionActorLabel(s, agentInfo), colActor)
			stateLabel := truncate(state, colState)
			row := strings.Join([]string{
				cursor + padRight(dot, colStatus),
				padRight(sessionID, colSession),
				padRight(actor, colActor),
				padRight(stateLabel, colState),
				padRight(tokens, colTokens),
				padRight(last, colLast),
			}, spaces(gap))

			b.WriteString(rowStyle.Render(row))
			b.WriteString("\n")

			// Show session details if selected
			if isSelected {
				b.WriteString(p.renderSessionDetails(s, agentInfo))
			}

			// Show expanded session entries
			if p.expanded[s.ID] {
				b.WriteString(p.renderExpandedEntries(s.ID))
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
	b.WriteString(hintStyle.Render(fmt.Sprintf("  j/k:move  enter:expand  esc:collapse  v:view(%s)  s:sort(%s)  c:collapse-ns  x:expand-all", focusLabel, p.sortMode)))
	b.WriteString("\n")

	return b.String()
}

func (p FleetPanel) renderSessionDetails(s SessionData, agentInfo AgentData) string {
	var b strings.Builder
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
	if started := relativeTime(s.StartedAt); started != "---" {
		statusDetails = append(statusDetails, fmt.Sprintf("started:%s", started))
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
	if coord, ok := p.namespaceCoordination[sessionNamespace(s)]; ok && coord.NeedsAttention {
		nsParts := []string{
			fmt.Sprintf("ns-risk:%d", coord.AttentionScore),
			fmt.Sprintf("blocked:%d", coord.BlockedTasks),
			fmt.Sprintf("x-agent:%d", coord.CrossAgentBlockers),
			fmt.Sprintf("files:%d", coord.ConflictFiles),
		}
		if coord.OrphanTasks > 0 {
			nsParts = append(nsParts, fmt.Sprintf("orphans:%d", coord.OrphanTasks))
		}
		b.WriteString(detailStyle.Render(truncate(strings.Join(nsParts, "  "), detailMax)))
		b.WriteString("\n")
		if len(coord.AttentionReasons) > 0 {
			b.WriteString(detailStyle.Render(truncate("ns-notes: "+strings.Join(coord.AttentionReasons, ", "), detailMax)))
			b.WriteString("\n")
		}
	}
	if agentID != "" {
		if coord, ok := p.agentCoordination[agentID]; ok && coord.NeedsAttention {
			agentParts := []string{
				fmt.Sprintf("claims:%d", coord.ClaimCount),
				fmt.Sprintf("conflicts:%d", coord.ConflictFiles),
				fmt.Sprintf("blocking:%d", coord.BlockingOthers),
				fmt.Sprintf("waiting:%d", coord.BlockedByOthers),
			}
			b.WriteString(detailStyle.Render(truncate(strings.Join(agentParts, "  "), detailMax)))
			b.WriteString("\n")
			if len(coord.AttentionReasons) > 0 {
				b.WriteString(detailStyle.Render(truncate("agent-notes: "+strings.Join(coord.AttentionReasons, ", "), detailMax)))
				b.WriteString("\n")
			}
		}
	}
	if s.Description != "" {
		b.WriteString(detailStyle.Render(truncate("note: "+s.Description, detailMax)))
		b.WriteString("\n")
	}
	return b.String()
}

func (p FleetPanel) renderExpandedEntries(sessionID string) string {
	var b strings.Builder
	entries := p.sessionEntries[sessionID]
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
	return b.String()
}
