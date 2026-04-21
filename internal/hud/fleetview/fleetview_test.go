package fleetview

import (
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func mustTime(t *testing.T, s string) string {
	t.Helper()
	if _, err := time.Parse(time.RFC3339, s); err != nil {
		t.Fatalf("bad test time %q: %v", s, err)
	}
	return s
}

func TestJoin_PresenceWithoutSession_HasSessionFalse(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{AgentID: "claude-code-1", Status: "active", AgentType: "claude-code", LastHeartbeat: mustTime(t, "2026-04-21T11:59:30Z")},
	}
	rows := Join(presences, nil, now)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	got := rows[0]
	if got.HasSession {
		t.Fatalf("presence without session must not report HasSession=true; got %+v", got)
	}
	if got.Source != "presence" {
		t.Fatalf("want source=presence, got %q", got.Source)
	}
	if !got.HasPresence {
		t.Fatalf("want HasPresence=true")
	}
}

func TestJoin_StaleHasSessionFlagIsCleared(t *testing.T) {
	// Regression: a previous Join (or an upstream writer) may have populated
	// HasSession=true. The new join must reset it if no active session
	// matches now.
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{
			AgentID:          "claude-code-ghost",
			Status:           "active",
			HasSession:       true, // stale
			SessionID:        "sess-old",
			SessionStatus:    "active",
			SessionStartedAt: mustTime(t, "2026-04-21T10:00:00Z"),
			LastHeartbeat:    mustTime(t, "2026-04-21T11:59:30Z"),
		},
	}
	// No sessions at all — so HasSession must become false.
	rows := Join(presences, nil, now)
	got := rows[0]
	if got.HasSession {
		t.Fatalf("stale HasSession must be reset; got %+v", got)
	}
	if got.SessionStatus != "" {
		t.Fatalf("stale SessionStatus must be cleared; got %q", got.SessionStatus)
	}
	if got.SessionStartedAt != "" {
		t.Fatalf("stale SessionStartedAt must be cleared; got %q", got.SessionStartedAt)
	}
}

func TestJoin_EndedSessionDoesNotSatisfyHasSession(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{AgentID: "claude-code-1", Status: "active", SessionID: "sess-ended", LastHeartbeat: mustTime(t, "2026-04-21T11:59:30Z")},
	}
	sessions := []bridge.SessionInfo{
		{ID: "sess-ended", AgentID: "claude-code-1", Status: "ended", StartedAt: mustTime(t, "2026-04-21T10:00:00Z")},
	}
	rows := Join(presences, sessions, now)
	if rows[0].HasSession {
		t.Fatalf("ended session must not mark HasSession=true; got %+v", rows[0])
	}
}

func TestJoin_ActiveSessionMatchedBySessionID(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{AgentID: "claude-code-1", Status: "active", SessionID: "sess-live", LastHeartbeat: mustTime(t, "2026-04-21T11:59:30Z")},
	}
	sessions := []bridge.SessionInfo{
		{ID: "sess-live", AgentID: "claude-code-1", Namespace: "loom-core/main", Status: "active", StartedAt: mustTime(t, "2026-04-21T11:30:00Z")},
	}
	rows := Join(presences, sessions, now)
	got := rows[0]
	if !got.HasSession {
		t.Fatalf("active session match must set HasSession=true")
	}
	if got.SessionID != "sess-live" || got.SessionStatus != "active" {
		t.Fatalf("session fields not propagated: %+v", got)
	}
	if got.Source != "presence+session" {
		t.Fatalf("want source=presence+session, got %q", got.Source)
	}
}

func TestJoin_ActiveSessionMatchedByAgentIDWhenSessionIDMissing(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{AgentID: "claude-code-1", Status: "active", LastHeartbeat: mustTime(t, "2026-04-21T11:59:30Z")},
	}
	sessions := []bridge.SessionInfo{
		{ID: "sess-live", AgentID: "claude-code-1", Status: "active", StartedAt: mustTime(t, "2026-04-21T11:30:00Z")},
	}
	rows := Join(presences, sessions, now)
	if !rows[0].HasSession || rows[0].SessionID != "sess-live" {
		t.Fatalf("agent_id fallback match failed: %+v", rows[0])
	}
}

func TestJoin_SessionWithoutPresenceYieldsSyntheticRow(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	sessions := []bridge.SessionInfo{
		{ID: "sess-a", AgentID: "claude-code-ghost", Status: "active", StartedAt: mustTime(t, "2026-04-21T11:00:00Z")},
	}
	rows := Join(nil, sessions, now)
	if len(rows) != 1 {
		t.Fatalf("want 1 synthetic row, got %d", len(rows))
	}
	got := rows[0]
	if got.Source != "session" {
		t.Fatalf("want source=session, got %q", got.Source)
	}
	if !got.HasSession || got.HasPresence {
		t.Fatalf("synthetic row: HasSession=%v HasPresence=%v", got.HasSession, got.HasPresence)
	}
	if got.TelemetryStatus != "session_only" {
		t.Fatalf("want telemetry_status=session_only, got %q", got.TelemetryStatus)
	}
}

func TestJoin_MultipleAgents_CounterMatchesBadges(t *testing.T) {
	// End-to-end invariant: the number of rows with HasSession=true must
	// equal the number of active sessions that joined. This is the UI
	// contract — badge count must equal counter count.
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{AgentID: "claude-code-1", Status: "active"},
		{AgentID: "claude-code-2", Status: "active"},
		{AgentID: "claude-code-3", Status: "active"},
		{AgentID: "claude-code-4", Status: "active"},
	}
	sessions := []bridge.SessionInfo{
		{ID: "s1", AgentID: "claude-code-1", Status: "active", StartedAt: mustTime(t, "2026-04-21T11:00:00Z")},
		{ID: "s2", AgentID: "claude-code-2", Status: "ended", StartedAt: mustTime(t, "2026-04-21T10:00:00Z")},
		{ID: "s3", AgentID: "claude-code-3", Status: "active", StartedAt: mustTime(t, "2026-04-21T11:30:00Z")},
	}
	rows := Join(presences, sessions, now)
	withSession := 0
	for _, r := range rows {
		if r.HasSession {
			withSession++
		}
	}
	if withSession != 2 {
		t.Fatalf("want 2 rows with HasSession=true, got %d (rows=%+v)", withSession, rows)
	}
	if len(rows) != 4 {
		t.Fatalf("want 4 rows total, got %d", len(rows))
	}
}

func TestJoin_PrefersMostRecentActiveSessionPerAgent(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{AgentID: "claude-code-1", Status: "active"},
	}
	sessions := []bridge.SessionInfo{
		{ID: "older", AgentID: "claude-code-1", Status: "active", StartedAt: mustTime(t, "2026-04-21T09:00:00Z")},
		{ID: "newer", AgentID: "claude-code-1", Status: "active", StartedAt: mustTime(t, "2026-04-21T11:00:00Z")},
	}
	rows := Join(presences, sessions, now)
	if rows[0].SessionID != "newer" {
		t.Fatalf("want newer session, got %q", rows[0].SessionID)
	}
}

func TestJoin_SkipsEmptyAgentIDs(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{AgentID: "", Status: "active"},
		{AgentID: "claude-code-1", Status: "active"},
	}
	rows := Join(presences, nil, now)
	if len(rows) != 1 {
		t.Fatalf("empty agent_id must be dropped; got %d rows", len(rows))
	}
	if rows[0].AgentID != "claude-code-1" {
		t.Fatalf("kept wrong row: %+v", rows[0])
	}
}

func TestJoin_ComputesHeartbeatAgeAndTelemetryStatus(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	// heartbeat 1200s ago -> stale
	presences := []bridge.PresenceInfo{
		{AgentID: "claude-code-1", Status: "active", LastHeartbeat: mustTime(t, "2026-04-21T11:40:00Z")},
	}
	rows := Join(presences, nil, now)
	got := rows[0]
	if got.HeartbeatAgeSeconds != 1200 {
		t.Fatalf("heartbeat age wrong: %d", got.HeartbeatAgeSeconds)
	}
	if got.TelemetryStatus != "stale" {
		t.Fatalf("want telemetry_status=stale, got %q", got.TelemetryStatus)
	}
}

func TestJoin_DoesNotMutateInput(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{AgentID: "claude-code-1", Status: "active", HasSession: true, SessionID: "old"},
	}
	sessions := []bridge.SessionInfo{
		{ID: "s1", AgentID: "claude-code-1", Status: "active", StartedAt: mustTime(t, "2026-04-21T11:00:00Z")},
	}
	_ = Join(presences, sessions, now)
	// Input should be unchanged — Join returns new copies.
	if presences[0].HasSession != true {
		t.Fatalf("Join mutated input HasSession")
	}
	if presences[0].SessionID != "old" {
		t.Fatalf("Join mutated input SessionID")
	}
}

// --- Orphan detection ---------------------------------------------------

func TestOrphan_YoungPresenceWithoutSessionIsNotOrphan(t *testing.T) {
	// Agent registered 30s ago, no session yet — still within the grace
	// window for session bootstrap. Should NOT be flagged.
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{AgentID: "claude-code-1", Status: "active", RegisteredAt: mustTime(t, "2026-04-21T11:59:30Z"), LastHeartbeat: mustTime(t, "2026-04-21T11:59:55Z")},
	}
	rows := Join(presences, nil, now)
	if rows[0].IsOrphan {
		t.Fatalf("young presence must not be flagged orphan: %+v", rows[0])
	}
	if rows[0].OrphanAgeSeconds != 0 {
		t.Fatalf("orphan age must be zero when not orphan, got %d", rows[0].OrphanAgeSeconds)
	}
}

func TestOrphan_StalePresenceWithoutSessionIsOrphan(t *testing.T) {
	// Agent registered 5min ago, still heartbeating, but never obtained a
	// session. This is the screenshot's 9-orphans case.
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{AgentID: "claude-code-ghost", Status: "active", RegisteredAt: mustTime(t, "2026-04-21T11:55:00Z"), LastHeartbeat: mustTime(t, "2026-04-21T11:59:50Z")},
	}
	rows := Join(presences, nil, now)
	if !rows[0].IsOrphan {
		t.Fatalf("stale presence without session must be orphan: %+v", rows[0])
	}
	if rows[0].OrphanAgeSeconds < 290 || rows[0].OrphanAgeSeconds > 310 {
		t.Fatalf("orphan age should be ~300s, got %d", rows[0].OrphanAgeSeconds)
	}
}

func TestOrphan_PresenceWithActiveSessionIsNotOrphan(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{AgentID: "claude-code-1", Status: "active", SessionID: "s1", RegisteredAt: mustTime(t, "2026-04-21T11:00:00Z")},
	}
	sessions := []bridge.SessionInfo{
		{ID: "s1", AgentID: "claude-code-1", Status: "active", StartedAt: mustTime(t, "2026-04-21T11:30:00Z")},
	}
	rows := Join(presences, sessions, now)
	if rows[0].IsOrphan {
		t.Fatalf("presence with matched session must not be orphan: %+v", rows[0])
	}
}

func TestOrphan_SessionOnlyRowIsNeverOrphan(t *testing.T) {
	// Synthetic session-only row: Source="session", HasPresence=false.
	// By definition no orphan because there's no dangling presence.
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	sessions := []bridge.SessionInfo{
		{ID: "s1", AgentID: "claude-code-1", Status: "active", StartedAt: mustTime(t, "2026-04-21T11:30:00Z")},
	}
	rows := Join(nil, sessions, now)
	if rows[0].IsOrphan {
		t.Fatalf("session-only row must not be orphan: %+v", rows[0])
	}
}

func TestOrphan_EndedSessionProducesOrphan(t *testing.T) {
	// Presence is heartbeating but its session has ended. After the grace
	// window, the row should be flagged — this catches sessions that
	// terminate silently without deregistering presence.
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{AgentID: "claude-code-ghost", Status: "active", SessionID: "s-ended", RegisteredAt: mustTime(t, "2026-04-21T11:00:00Z"), LastHeartbeat: mustTime(t, "2026-04-21T11:59:50Z")},
	}
	sessions := []bridge.SessionInfo{
		{ID: "s-ended", AgentID: "claude-code-ghost", Status: "ended", StartedAt: mustTime(t, "2026-04-21T10:30:00Z")},
	}
	rows := Join(presences, sessions, now)
	if !rows[0].IsOrphan {
		t.Fatalf("presence with only an ended session must be orphan: %+v", rows[0])
	}
}

func TestOrphan_StaleFlagIsReset(t *testing.T) {
	// An incoming presence row that somehow carries IsOrphan=true from
	// upstream must have that reset before the new computation runs,
	// otherwise Join would compound stale state.
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{
			AgentID:          "claude-code-1",
			Status:           "active",
			IsOrphan:         true,
			OrphanAgeSeconds: 9999,
			SessionID:        "s1",
			RegisteredAt:     mustTime(t, "2026-04-21T11:00:00Z"),
		},
	}
	sessions := []bridge.SessionInfo{
		{ID: "s1", AgentID: "claude-code-1", Status: "active", StartedAt: mustTime(t, "2026-04-21T11:30:00Z")},
	}
	rows := Join(presences, sessions, now)
	if rows[0].IsOrphan {
		t.Fatalf("stale IsOrphan must be reset when session joins: %+v", rows[0])
	}
	if rows[0].OrphanAgeSeconds != 0 {
		t.Fatalf("stale OrphanAgeSeconds must be reset, got %d", rows[0].OrphanAgeSeconds)
	}
}

func TestOrphan_FallsBackToLastHeartbeatWhenRegisteredAtMissing(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-04-21T12:00:00Z")
	presences := []bridge.PresenceInfo{
		{AgentID: "claude-code-1", Status: "active", LastHeartbeat: mustTime(t, "2026-04-21T11:55:00Z")},
	}
	rows := Join(presences, nil, now)
	if !rows[0].IsOrphan {
		t.Fatalf("should fall back to LastHeartbeat when RegisteredAt missing: %+v", rows[0])
	}
}

func TestInferAgentType(t *testing.T) {
	cases := []struct{ in, want string }{
		{"claude-code-12345", "claude-code"},
		{"codex-abc", "codex"},
		{"gemini-9", "gemini-cli"},
		{"zed-session", "codex"},
		{"proxy-x", "codex"},
		{"copilot-x", "copilot"},
		{"kilocode-k", "kilocode"},
		{"custom-agent", "custom"},
		{"", "unknown"},
		{"weirdname", "weirdname"},
	}
	for _, c := range cases {
		if got := InferAgentType(c.in); got != c.want {
			t.Errorf("InferAgentType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
