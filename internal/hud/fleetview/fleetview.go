// Package fleetview is the single source of truth for correlating agent
// presence with agent sessions into the unified rows rendered by the HUD
// Fleet panel, the mobile /agents endpoint, and anywhere else that needs to
// answer "which agents have an active session right now?".
//
// Design rules:
//
//  1. HasSession is a derived flag, never stored. It is true only when a
//     presence row joins to a session whose Status == "active" (by SessionID
//     first, then by AgentID).
//
//  2. The join resets any session-derived fields (HasSession, SessionID,
//     SessionStatus, SessionStartedAt, SessionAgeSeconds) on each call so
//     stale state carried in the incoming slice cannot leak through.
//
//  3. A session without a matching presence produces a synthetic
//     "session-only" row so the UI still surfaces the session. A presence
//     without a matching session produces a "presence-only" row with
//     HasSession=false.
//
//  4. Callers feed in the raw snapshots from the agent bridge; this package
//     never mutates the input slices.
package fleetview

import (
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// Join correlates the given presence and session records into a single
// enriched slice. See the package doc for the rules it enforces.
//
// The input slices are not mutated. The returned slice contains enriched
// copies of each presence row, plus synthetic rows for sessions that have no
// matching presence.
func Join(presences []bridge.PresenceInfo, sessions []bridge.SessionInfo, now time.Time) []bridge.PresenceInfo {
	if now.IsZero() {
		now = time.Now()
	}

	liveByID := make(map[string]bridge.SessionInfo)
	liveByAgent := make(map[string]bridge.SessionInfo)
	for _, s := range sessions {
		if !sessionIsActive(s) {
			continue
		}
		liveByID[s.ID] = s
		if current, ok := liveByAgent[s.AgentID]; !ok || parseTime(s.StartedAt).After(parseTime(current.StartedAt)) {
			liveByAgent[s.AgentID] = s
		}
	}

	result := make([]bridge.PresenceInfo, 0, len(presences)+len(liveByAgent))
	seen := make(map[string]struct{}, len(presences))
	for _, agent := range presences {
		agentID := strings.TrimSpace(agent.AgentID)
		if agentID == "" {
			continue
		}
		// Copy, then reset derived session fields so stale state cannot leak.
		row := agent
		resetSessionFields(&row)
		row.HasPresence = true
		row.Source = "presence"
		if row.AgentType == "" || strings.EqualFold(row.AgentType, "unknown") {
			row.AgentType = InferAgentType(agentID)
		}

		// Join: SessionID first (most precise), then AgentID.
		if s, ok := liveByID[agent.SessionID]; ok {
			applySession(&row, s, now)
		} else if s, ok := liveByAgent[agentID]; ok {
			applySession(&row, s, now)
		}

		row.HeartbeatAgeSeconds = AgeSeconds(row.LastHeartbeat, now)
		markOrphan(&row, now)
		row.TelemetryStatus = TelemetryStatus(row)
		result = append(result, row)
		seen[agentID] = struct{}{}
	}

	// Synthetic rows for sessions with no matching presence.
	for _, s := range liveByAgent {
		agentID := strings.TrimSpace(s.AgentID)
		if agentID == "" {
			continue
		}
		if _, ok := seen[agentID]; ok {
			continue
		}
		row := bridge.PresenceInfo{
			AgentID:       agentID,
			Status:        "active",
			AgentType:     InferAgentType(agentID),
			Description:   s.Description,
			LastHeartbeat: s.StartedAt,
			RegisteredAt:  s.StartedAt,
			Source:        "session",
		}
		applySession(&row, s, now)
		row.TelemetryStatus = TelemetryStatus(row)
		result = append(result, row)
	}

	return result
}

// OrphanStaleAfter is the age past which a heartbeating presence with no
// matching active session is flagged as an orphan. Short enough to catch
// real divergence, long enough to avoid false positives during normal
// session-start bootstrap (vendor CLIs register presence, then call
// session-start; the window between those is typically <1s). 120s leaves
// generous room for a flaky daemon retry.
const OrphanStaleAfter = 120 * time.Second

// markOrphan sets the derived IsOrphan / OrphanAgeSeconds fields on an
// enriched presence row. An orphan is a row with presence evidence but no
// joined active session that has persisted past OrphanStaleAfter. Synthetic
// session-only rows (Source="session") can never be orphans by definition.
func markOrphan(row *bridge.PresenceInfo, now time.Time) {
	if row == nil {
		return
	}
	row.IsOrphan = false
	row.OrphanAgeSeconds = 0
	if !row.HasPresence || row.HasSession {
		return
	}
	// Prefer RegisteredAt as the orphan clock (the agent has been
	// *registered* without a session for this long). Fall back to
	// LastHeartbeat when RegisteredAt is absent, e.g. in older fixtures.
	anchor := parseTime(row.RegisteredAt)
	if anchor.IsZero() {
		anchor = parseTime(row.LastHeartbeat)
	}
	if anchor.IsZero() {
		return
	}
	age := now.Sub(anchor)
	if age < OrphanStaleAfter {
		return
	}
	row.IsOrphan = true
	row.OrphanAgeSeconds = int(age.Seconds())
}

// applySession writes session-derived fields onto an enriched presence row.
// Must be called only when the session is active; callers filter first.
func applySession(row *bridge.PresenceInfo, s bridge.SessionInfo, now time.Time) {
	if row == nil {
		return
	}
	row.SessionID = s.ID
	row.HasSession = true
	row.SessionStatus = s.Status
	row.SessionStartedAt = s.StartedAt
	row.SessionAgeSeconds = AgeSeconds(s.StartedAt, now)
	if row.Description == "" {
		row.Description = s.Description
	}
	if row.Source == "presence" {
		row.Source = "presence+session"
	}
}

// resetSessionFields clears all session-derived state on a presence row so a
// fresh join cannot inherit stale flags from a prior computation.
func resetSessionFields(row *bridge.PresenceInfo) {
	if row == nil {
		return
	}
	row.HasSession = false
	row.SessionStatus = ""
	row.SessionStartedAt = ""
	row.SessionAgeSeconds = 0
	row.IsOrphan = false
	row.OrphanAgeSeconds = 0
	// SessionID stays untouched here: it is an identity hint produced by the
	// presence layer (from registration or heartbeat), and callers further
	// down may use it to match against sessions fetched out-of-band. The
	// HasSession flag is what drives UI correlation, and that is gated on a
	// real active session match.
}

// sessionIsActive returns true when the session status (case-insensitive,
// trimmed) equals "active" and the agent_id is non-empty.
func sessionIsActive(s bridge.SessionInfo) bool {
	if strings.TrimSpace(s.AgentID) == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(s.Status), "active")
}

// InferAgentType maps a well-formed agent ID to an agent type. Used as a
// fallback when the presence/session record doesn't carry one.
func InferAgentType(agentID string) string {
	id := strings.ToLower(strings.TrimSpace(agentID))
	if id == "" {
		return "unknown"
	}
	switch {
	case strings.HasPrefix(id, "claude"):
		return "claude-code"
	case strings.HasPrefix(id, "gemini"):
		return "gemini-cli"
	case strings.HasPrefix(id, "codex"), strings.HasPrefix(id, "zed"), strings.HasPrefix(id, "proxy"):
		return "codex"
	case strings.HasPrefix(id, "copilot"):
		return "copilot"
	case strings.HasPrefix(id, "kilocode"):
		return "kilocode"
	}
	if i := strings.IndexAny(id, "-_"); i > 0 {
		return id[:i]
	}
	return id
}

// AgeSeconds returns the number of seconds between parseable RFC3339 time
// `raw` and `now`, clamping to 0 for zero/unparseable/future values.
func AgeSeconds(raw string, now time.Time) int {
	t := parseTime(raw)
	if t.IsZero() || now.Before(t) {
		return 0
	}
	return int(now.Sub(t).Seconds())
}

// TelemetryStatus derives a rollup label from the enriched presence row. It
// is intentionally derived so two callers computing it over the same row
// always agree.
func TelemetryStatus(row bridge.PresenceInfo) string {
	status := strings.ToLower(strings.TrimSpace(row.Status))
	switch {
	case row.HasSession && !row.HasPresence:
		return "session_only"
	case status == "offline":
		return "offline"
	case row.HeartbeatAgeSeconds > 300:
		return "stale"
	case status == "idle":
		return "idle"
	case status == "active":
		return "live"
	default:
		return "unknown"
	}
}

func parseTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return time.Time{}
}
