package panels

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTasksPanelUpdateWindowSize(t *testing.T) {
	p := NewTasksPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if p.width != 100 || p.height != 30 {
		t.Errorf("size = (%d, %d), want (100, 30)", p.width, p.height)
	}
}

func TestTasksPanelUpdateData(t *testing.T) {
	p := NewTasksPanel()
	msg := MsgTasksData{
		Tasks: []TaskData{
			{ID: "t1", Title: "Task 1", Status: "pending", Priority: "high"},
			{ID: "t2", Title: "Task 2", Status: "in_progress", Priority: "medium"},
			{ID: "t3", Title: "Task 3", Status: "blocked", Priority: "low"},
		},
		PendingCount: 1,
		ActiveCount:  1,
		BlockedCount: 1,
	}
	p, _ = p.Update(msg)

	if p.pendingCount != 1 {
		t.Errorf("pendingCount = %d, want 1", p.pendingCount)
	}
	if p.activeCount != 1 {
		t.Errorf("activeCount = %d, want 1", p.activeCount)
	}
	if len(p.flatTasks) != 3 {
		t.Errorf("flatTasks = %d, want 3", len(p.flatTasks))
	}
}

func TestTasksPanelKeyNavigation(t *testing.T) {
	p := NewTasksPanel()
	p, _ = p.Update(MsgTasksData{
		Tasks: []TaskData{
			{ID: "t1", Status: "pending"},
			{ID: "t2", Status: "pending"},
		},
	})

	if p.selectedIdx != 0 {
		t.Fatalf("initial selectedIdx = %d, want 0", p.selectedIdx)
	}

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if p.selectedIdx != 1 {
		t.Errorf("after j: selectedIdx = %d, want 1", p.selectedIdx)
	}

	// Boundary: can't go past end
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if p.selectedIdx != 1 {
		t.Errorf("at bottom: selectedIdx = %d, want 1", p.selectedIdx)
	}

	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if p.selectedIdx != 0 {
		t.Errorf("after k: selectedIdx = %d, want 0", p.selectedIdx)
	}

	// Boundary: can't go before 0
	p, _ = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if p.selectedIdx != 0 {
		t.Errorf("at top: selectedIdx = %d, want 0", p.selectedIdx)
	}
}

func TestTasksPanelEnterCyclesStatus(t *testing.T) {
	p := NewTasksPanel()
	p, _ = p.Update(MsgTasksData{
		Tasks: []TaskData{
			{ID: "t1", Status: "pending", Title: "Do thing"},
		},
	})

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from enter key")
	}

	msg := cmd()
	cycled, ok := msg.(MsgTaskStatusCycled)
	if !ok {
		t.Fatalf("expected MsgTaskStatusCycled, got %T", msg)
	}
	if cycled.TaskID != "t1" {
		t.Errorf("TaskID = %q, want %q", cycled.TaskID, "t1")
	}
	if cycled.NewStatus != "in_progress" {
		t.Errorf("NewStatus = %q, want %q", cycled.NewStatus, "in_progress")
	}
}

func TestTasksPanelSelectedTask(t *testing.T) {
	p := NewTasksPanel()
	if p.SelectedTask() != nil {
		t.Error("expected nil SelectedTask on empty panel")
	}

	p, _ = p.Update(MsgTasksData{
		Tasks: []TaskData{
			{ID: "t1", Status: "pending"},
		},
	})
	if p.SelectedTask() == nil || p.SelectedTask().ID != "t1" {
		t.Error("expected SelectedTask to be t1")
	}
}

func TestTasksPanelViewNoTasks(t *testing.T) {
	p := NewTasksPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	v := p.View()
	if v == "" {
		t.Error("expected non-empty view")
	}
}

func TestTasksPanelViewWide(t *testing.T) {
	p := NewTasksPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 150, Height: 40})
	p, _ = p.Update(MsgTasksData{
		Tasks: []TaskData{
			{ID: "t1", Title: "Pending task", Status: "pending", Priority: "high"},
			{ID: "t2", Title: "Active task", Status: "in_progress", Priority: "medium"},
			{ID: "t3", Title: "Blocked task", Status: "blocked", Priority: "low"},
		},
		PendingCount: 1,
		ActiveCount:  1,
		BlockedCount: 1,
	})
	v := p.View()
	if v == "" {
		t.Error("expected non-empty wide view")
	}
}

func TestTasksPanelViewNarrow(t *testing.T) {
	p := NewTasksPanel()
	p, _ = p.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	p, _ = p.Update(MsgTasksData{
		Tasks: []TaskData{
			{ID: "t1", Title: "Task", Status: "pending", Priority: "high"},
		},
		PendingCount: 1,
	})
	v := p.View()
	if v == "" {
		t.Error("expected non-empty narrow view")
	}
}

func TestTasksPanelFlatTaskOrder(t *testing.T) {
	p := NewTasksPanel()
	p, _ = p.Update(MsgTasksData{
		Tasks: []TaskData{
			{ID: "t1", Status: "blocked"},
			{ID: "t2", Status: "in_progress"},
			{ID: "t3", Status: "pending"},
		},
	})

	// flatTasks should be ordered: pending, active, blocked.
	if len(p.flatTasks) != 3 {
		t.Fatalf("flatTasks = %d, want 3", len(p.flatTasks))
	}
	if p.flatTasks[0].ID != "t3" {
		t.Errorf("flatTasks[0].ID = %q, want t3 (pending)", p.flatTasks[0].ID)
	}
	if p.flatTasks[1].ID != "t2" {
		t.Errorf("flatTasks[1].ID = %q, want t2 (in_progress)", p.flatTasks[1].ID)
	}
	if p.flatTasks[2].ID != "t1" {
		t.Errorf("flatTasks[2].ID = %q, want t1 (blocked)", p.flatTasks[2].ID)
	}
}
