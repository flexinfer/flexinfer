package panels

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestFleetPanelUpdateWindowSize(t *testing.T) {
	p := NewFleetPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if p.width != 120 || p.height != 40 {
		t.Errorf("size = (%d, %d), want (120, 40)", p.width, p.height)
	}
}

func TestFleetPanelUpdateData(t *testing.T) {
	p := NewFleetPanel()
	msg := MsgFleetData{
		DaemonRunning:  true,
		ServerCount:    5,
		ActiveSessions: 3,
		TotalTokens:    1500,
		Sessions: []SessionData{
			{ID: "s1", AgentID: "agent-1", Namespace: "ns1", Status: "active"},
			{ID: "s2", AgentID: "agent-2", Namespace: "ns1", Status: "idle"},
		},
	}
	p, _ = p.Update(msg)

	if !p.daemonRunning {
		t.Error("expected daemonRunning=true")
	}
	if p.serverCount != 5 {
		t.Errorf("serverCount = %d, want 5", p.serverCount)
	}
	if len(p.sessions) != 2 {
		t.Errorf("sessions = %d, want 2", len(p.sessions))
	}
	if len(p.flatRows) != 2 {
		t.Errorf("flatRows = %d, want 2", len(p.flatRows))
	}
}

func TestFleetPanelKeyNavigation(t *testing.T) {
	p := NewFleetPanel()
	p, _ = p.Update(MsgFleetData{
		Sessions: []SessionData{
			{ID: "s1", Namespace: "ns", Status: "active"},
			{ID: "s2", Namespace: "ns", Status: "active"},
			{ID: "s3", Namespace: "ns", Status: "active"},
		},
	})

	if p.selectedIdx != 0 {
		t.Fatalf("initial selectedIdx = %d, want 0", p.selectedIdx)
	}

	// Move down
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if p.selectedIdx != 1 {
		t.Errorf("after j: selectedIdx = %d, want 1", p.selectedIdx)
	}

	// Move down again
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if p.selectedIdx != 2 {
		t.Errorf("after j,j: selectedIdx = %d, want 2", p.selectedIdx)
	}

	// Move down at bottom - should not overflow
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if p.selectedIdx != 2 {
		t.Errorf("at bottom after j: selectedIdx = %d, want 2", p.selectedIdx)
	}

	// Move up
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if p.selectedIdx != 1 {
		t.Errorf("after k: selectedIdx = %d, want 1", p.selectedIdx)
	}
}

func TestFleetPanelExpandCollapse(t *testing.T) {
	p := NewFleetPanel()
	p, _ = p.Update(MsgFleetData{
		Sessions: []SessionData{
			{ID: "s1", Namespace: "ns", Status: "active"},
		},
	})

	// Expand
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !p.expanded["s1"] {
		t.Error("expected s1 expanded after enter")
	}

	// Collapse
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.expanded["s1"] {
		t.Error("expected s1 collapsed after second enter")
	}

	// Expand then esc
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if p.expanded["s1"] {
		t.Error("expected s1 collapsed after esc")
	}
}

func TestFleetPanelSelectedSession(t *testing.T) {
	p := NewFleetPanel()
	if p.SelectedSession() != "" {
		t.Error("expected empty SelectedSession on empty panel")
	}

	p, _ = p.Update(MsgFleetData{
		Sessions: []SessionData{
			{ID: "s1", Namespace: "ns", Status: "active"},
		},
	})
	if p.SelectedSession() != "s1" {
		t.Errorf("SelectedSession() = %q, want %q", p.SelectedSession(), "s1")
	}
}

func TestFleetPanelSessionEntries(t *testing.T) {
	p := NewFleetPanel()
	entries := []StreamEntryData{
		{ID: "e1", Title: "test entry"},
	}
	p, _ = p.Update(MsgSessionEntries{SessionID: "s1", Entries: entries})
	if len(p.sessionEntries["s1"]) != 1 {
		t.Errorf("expected 1 entry for s1, got %d", len(p.sessionEntries["s1"]))
	}
}

func TestFleetPanelViewNoSessions(t *testing.T) {
	p := NewFleetPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	v := p.View()
	if v == "" {
		t.Error("expected non-empty view")
	}
}

func TestFleetPanelViewWithSessions(t *testing.T) {
	p := NewFleetPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	p, _ = p.Update(MsgFleetData{
		DaemonRunning:  true,
		ServerCount:    3,
		ActiveSessions: 1,
		Sessions: []SessionData{
			{
				ID:        "s1",
				AgentID:   "claude-code",
				Namespace: "project/main",
				Status:    "active",
				StartedAt: time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339),
			},
		},
	})
	v := p.View()
	if v == "" {
		t.Error("expected non-empty view with sessions")
	}
}
