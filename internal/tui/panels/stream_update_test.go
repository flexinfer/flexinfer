package panels

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestStreamPanelUpdateWindowSize(t *testing.T) {
	p := NewStreamPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if p.width != 100 || p.height != 30 {
		t.Errorf("size = (%d, %d), want (100, 30)", p.width, p.height)
	}
}

func TestStreamPanelUpdateData(t *testing.T) {
	p := NewStreamPanel()
	msg := MsgStreamData{
		Entries: []StreamEntryData{
			{ID: "e1", EntryType: "decision", Title: "Use approach A"},
			{ID: "e2", EntryType: "finding", Title: "Found issue in X"},
		},
	}
	p, _ = p.Update(msg)
	if len(p.entries) != 2 {
		t.Errorf("entries = %d, want 2", len(p.entries))
	}
}

func TestStreamPanelScrollNavigation(t *testing.T) {
	p := NewStreamPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 80, Height: 8}) // visibleLines = 5

	entries := make([]StreamEntryData, 20)
	for i := range entries {
		entries[i] = StreamEntryData{ID: "e", EntryType: "note", Title: "entry"}
	}
	p, _ = p.Update(MsgStreamData{Entries: entries})

	if p.scrollOffset != 0 {
		t.Fatalf("initial scrollOffset = %d, want 0", p.scrollOffset)
	}

	// Scroll down
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if p.scrollOffset != 1 {
		t.Errorf("after j: scrollOffset = %d, want 1", p.scrollOffset)
	}

	// Scroll up
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if p.scrollOffset != 0 {
		t.Errorf("after k: scrollOffset = %d, want 0", p.scrollOffset)
	}

	// Go to bottom
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if p.scrollOffset == 0 {
		t.Error("after G: scrollOffset should not be 0")
	}

	// Go to top
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if p.scrollOffset != 0 {
		t.Errorf("after g: scrollOffset = %d, want 0", p.scrollOffset)
	}
}

func TestStreamPanelFilterCycle(t *testing.T) {
	p := NewStreamPanel()
	if p.filterIdx != 0 {
		t.Fatalf("initial filterIdx = %d, want 0", p.filterIdx)
	}

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if p.filterIdx != 1 {
		t.Errorf("after f: filterIdx = %d, want 1", p.filterIdx)
	}

	// Cycle all the way around
	for i := 0; i < 5; i++ {
		p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	}
	if p.filterIdx != 0 {
		t.Errorf("after full cycle: filterIdx = %d, want 0", p.filterIdx)
	}
}

func TestStreamPanelFilteredEntries(t *testing.T) {
	p := NewStreamPanel()
	p, _ = p.Update(MsgStreamData{
		Entries: []StreamEntryData{
			{ID: "e1", EntryType: "decision"},
			{ID: "e2", EntryType: "finding"},
			{ID: "e3", EntryType: "decision"},
		},
	})

	// All filter
	filtered := p.filteredEntries()
	if len(filtered) != 3 {
		t.Errorf("all filter: %d entries, want 3", len(filtered))
	}

	// Switch to decision filter
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	filtered = p.filteredEntries()
	if len(filtered) != 2 {
		t.Errorf("decision filter: %d entries, want 2", len(filtered))
	}
}

func TestStreamPanelVisibleLines(t *testing.T) {
	p := NewStreamPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	if p.visibleLines() != 7 {
		t.Errorf("visibleLines = %d, want 7", p.visibleLines())
	}

	// Height too small
	p, _ = p.Update(tea.WindowSizeMsg{Width: 80, Height: 2})
	if p.visibleLines() != 1 {
		t.Errorf("visibleLines for height=2: %d, want 1", p.visibleLines())
	}
}

func TestStreamPanelViewNoEntries(t *testing.T) {
	p := NewStreamPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	v := p.View()
	if v == "" {
		t.Error("expected non-empty view")
	}
}

func TestStreamPanelViewWithEntries(t *testing.T) {
	p := NewStreamPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	p, _ = p.Update(MsgStreamData{
		Entries: []StreamEntryData{
			{ID: "e1", EntryType: "decision", AgentID: "claude", Title: "Use Go", Timestamp: "2025-01-15T14:30:00Z"},
		},
	})
	v := p.View()
	if v == "" {
		t.Error("expected non-empty view with entries")
	}
}
