package panels

import (
	"sort"
	"time"
)

func (p FleetPanel) groupedSessions(sessions []SessionData, bySession, byAgentID map[string]AgentData) ([]string, map[string][]SessionData) {
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
			leftAgent := resolveAgentForSession(left, bySession, byAgentID)
			rightAgent := resolveAgentForSession(right, bySession, byAgentID)
			switch p.sortMode {
			case fleetSortRecent:
				leftActivity := sessionLastActivityTime(left, leftAgent)
				rightActivity := sessionLastActivityTime(right, rightAgent)
				if !leftActivity.Equal(rightActivity) {
					return leftActivity.After(rightActivity)
				}
				leftRank := sessionStatusRankWithAgent(left, leftAgent)
				rightRank := sessionStatusRankWithAgent(right, rightAgent)
				if leftRank != rightRank {
					return leftRank < rightRank
				}
				if left.TokenCount != right.TokenCount {
					return left.TokenCount > right.TokenCount
				}
			case fleetSortTokens:
				if left.TokenCount != right.TokenCount {
					return left.TokenCount > right.TokenCount
				}
				leftActivity := sessionLastActivityTime(left, leftAgent)
				rightActivity := sessionLastActivityTime(right, rightAgent)
				if !leftActivity.Equal(rightActivity) {
					return leftActivity.After(rightActivity)
				}
				leftRank := sessionStatusRankWithAgent(left, leftAgent)
				rightRank := sessionStatusRankWithAgent(right, rightAgent)
				if leftRank != rightRank {
					return leftRank < rightRank
				}
			default:
				leftRank := sessionStatusRankWithAgent(left, leftAgent)
				rightRank := sessionStatusRankWithAgent(right, rightAgent)
				if leftRank != rightRank {
					return leftRank < rightRank
				}
				leftActivity := sessionLastActivityTime(left, leftAgent)
				rightActivity := sessionLastActivityTime(right, rightAgent)
				if !leftActivity.Equal(rightActivity) {
					return leftActivity.After(rightActivity)
				}
				if left.TokenCount != right.TokenCount {
					return left.TokenCount > right.TokenCount
				}
			}
			return left.ID < right.ID
		})
	}
	return namespaces, groups
}

func (p FleetPanel) hiddenByCollapsedNamespaces(groups map[string][]SessionData) int {
	hidden := 0
	for ns := range p.collapsedNS {
		if !p.collapsedNS[ns] {
			continue
		}
		hidden += len(groups[ns])
	}
	return hidden
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

	lastActivity := sessionLastActivityTime(session, agent)
	if lastActivity.IsZero() {
		return false
	}

	// Hide old terminal/offline sessions in focused view.
	if sessionStatus == "ended" || sessionStatus == "offline" || sessionStatus == "error" {
		return now.Sub(lastActivity) > 24*time.Hour
	}
	return false
}
