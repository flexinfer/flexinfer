package panels

import (
	"strings"
	"testing"
)

func TestPresencePanelClaimConflicts(t *testing.T) {
	panel := PresencePanel{
		claims: []ClaimData{
			{FilePath: "/tmp/a.go", AgentID: "agent-b"},
			{FilePath: "/tmp/a.go", AgentID: "agent-a"},
			{FilePath: "/tmp/a.go", AgentID: "agent-a"}, // duplicate claim from same agent
			{FilePath: "/tmp/b.go", AgentID: "agent-a"},
		},
	}

	conflicts := panel.claimConflicts()
	if got, want := len(conflicts), 1; got != want {
		t.Fatalf("conflict count = %d, want %d", got, want)
	}

	agents := conflicts["/tmp/a.go"]
	if got, want := strings.Join(agents, ","), "agent-a,agent-b"; got != want {
		t.Fatalf("conflict agents = %q, want %q", got, want)
	}

	if panel.claimConflictCount() != 1 {
		t.Fatalf("claimConflictCount() = %d, want 1", panel.claimConflictCount())
	}
}

func TestPresencePanelRenderSummaryIncludesConflictCount(t *testing.T) {
	panel := PresencePanel{
		agents: []PresenceAgentData{
			{AgentID: "agent-a", Status: "active"},
			{AgentID: "agent-b", Status: "idle"},
		},
		claims: []ClaimData{
			{FilePath: "/tmp/a.go", AgentID: "agent-a"},
			{FilePath: "/tmp/a.go", AgentID: "agent-b"},
		},
		activeAgents: 1,
		idleAgents:   1,
		totalClaims:  2,
	}

	summary := panel.renderSummary()
	if !strings.Contains(summary, "1 conflicts") {
		t.Fatalf("summary missing conflicts segment: %q", summary)
	}
}

func TestPresencePanelRenderClaimsTableShowsConflictHints(t *testing.T) {
	panel := PresencePanel{
		width: 120,
		claims: []ClaimData{
			{FilePath: "/tmp/a.go", AgentID: "agent-a", ClaimType: "edit", CreatedAt: "2026-02-17T10:00:00Z", Reason: "editing"},
			{FilePath: "/tmp/a.go", AgentID: "agent-b", ClaimType: "review", CreatedAt: "2026-02-17T10:00:00Z", Reason: "review"},
		},
		selectedIdx: 0,
	}

	view := panel.renderClaimsTable()
	if !strings.Contains(view, "file conflict(s)") {
		t.Fatalf("claims table missing conflict banner: %q", view)
	}
	if !strings.Contains(view, "shared with: agent-a, agent-b") {
		t.Fatalf("claims table missing selected conflict detail: %q", view)
	}
}
