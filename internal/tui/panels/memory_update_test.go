package panels

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMemoryPanelUpdateWindowSize(t *testing.T) {
	p := NewMemoryPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if p.width != 100 || p.height != 30 {
		t.Errorf("size = (%d, %d), want (100, 30)", p.width, p.height)
	}
}

func TestMemoryPanelUpdateData(t *testing.T) {
	p := NewMemoryPanel()
	msg := MsgMemoryData{
		WorkingItems:  10,
		WorkingTokens: 500,
		ShortItems:    20,
		ShortTokens:   1000,
		LongItems:     50,
		LongTokens:    5000,
		TotalItems:    80,
		TotalTokens:   6500,
		History:       []float64{100, 200, 300},
	}
	p, _ = p.Update(msg)

	if p.workingItems != 10 {
		t.Errorf("workingItems = %d, want 10", p.workingItems)
	}
	if p.totalTokens != 6500 {
		t.Errorf("totalTokens = %d, want 6500", p.totalTokens)
	}
	if len(p.history) != 3 {
		t.Errorf("history = %d, want 3", len(p.history))
	}
}

func TestMemoryPanelKeyNavigation(t *testing.T) {
	p := NewMemoryPanel()

	if p.selectedTier != 0 {
		t.Fatalf("initial selectedTier = %d, want 0", p.selectedTier)
	}

	// Move down through tiers
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if p.selectedTier != 1 {
		t.Errorf("after j: selectedTier = %d, want 1", p.selectedTier)
	}

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if p.selectedTier != 2 {
		t.Errorf("after j,j: selectedTier = %d, want 2", p.selectedTier)
	}

	// At bottom, can't go further
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if p.selectedTier != 2 {
		t.Errorf("at bottom: selectedTier = %d, want 2", p.selectedTier)
	}

	// Move up
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if p.selectedTier != 1 {
		t.Errorf("after k: selectedTier = %d, want 1", p.selectedTier)
	}
}

func TestMemoryPanelExpandCollapse(t *testing.T) {
	p := NewMemoryPanel()

	// Expand working tier
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !p.expanded["working"] {
		t.Error("expected working tier expanded")
	}

	// Collapse
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.expanded["working"] {
		t.Error("expected working tier collapsed")
	}

	// Expand then esc
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if p.expanded["working"] {
		t.Error("expected working tier collapsed after esc")
	}
}

func TestMemoryPanelSelectedTier(t *testing.T) {
	p := NewMemoryPanel()
	if p.SelectedTier() != "working" {
		t.Errorf("SelectedTier() = %q, want %q", p.SelectedTier(), "working")
	}

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if p.SelectedTier() != "short_term" {
		t.Errorf("SelectedTier() = %q, want %q", p.SelectedTier(), "short_term")
	}
}

func TestMemoryPanelItems(t *testing.T) {
	p := NewMemoryPanel()
	items := []MemoryItemData{
		{ID: "m1", Title: "item 1", Tier: "working", Tokens: 100},
		{ID: "m2", Title: "item 2", Tier: "working", Tokens: 200},
	}
	p, _ = p.Update(MsgMemoryItems{Tier: "working", Items: items})
	if len(p.tierItems["working"]) != 2 {
		t.Errorf("working items = %d, want 2", len(p.tierItems["working"]))
	}
}

func TestMemoryPanelViewNoData(t *testing.T) {
	p := NewMemoryPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	v := p.View()
	if v == "" {
		t.Error("expected non-empty view")
	}
}

func TestMemoryPanelViewWithData(t *testing.T) {
	p := NewMemoryPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	p, _ = p.Update(MsgMemoryData{
		WorkingItems:  5,
		WorkingTokens: 200,
		TotalItems:    5,
		TotalTokens:   200,
		History:       []float64{100, 150, 200},
	})
	v := p.View()
	if v == "" {
		t.Error("expected non-empty view with data")
	}
}
