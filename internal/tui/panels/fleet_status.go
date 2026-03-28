package panels

import (
	"fmt"
	"strings"
	"time"
)

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

func sessionStatusRankWithAgent(session SessionData, agent AgentData) int {
	sessionStatus := normalizedStatus(session.Status)
	presenceStatus := normalizedStatus(agent.Status)
	switch {
	case sessionStatus == "active" || presenceStatus == "active":
		return 0
	case sessionStatus == "idle":
		return 1
	case sessionStatus == "error":
		return 2
	case sessionStatus == "offline":
		return 3
	case sessionStatus == "ended":
		return 4
	default:
		return 5
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

func maxTime(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() {
		return a
	}
	if a.After(b) {
		return a
	}
	return b
}

func sessionLastActivityTime(session SessionData, agent AgentData) time.Time {
	started := parseRFC3339(session.StartedAt)
	heartbeat := parseRFC3339(agent.LastHeartbeat)
	return maxTime(started, heartbeat)
}

func lastActivityLabel(t time.Time) string {
	if t.IsZero() {
		return "---"
	}
	return relativeTime(t.UTC().Format(time.RFC3339))
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
