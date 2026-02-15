package panels

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
)

// ---------------------------------------------------------------------------
// Message
// ---------------------------------------------------------------------------

// MsgTasksData is sent by the app when new task data arrives.
type MsgTasksData struct {
	Tasks        []TaskData
	PendingCount int
	ActiveCount  int
	BlockedCount int
}

// MsgTaskStatusCycled signals that a task status was toggled via the TUI.
type MsgTaskStatusCycled struct {
	TaskID    string
	NewStatus string
}

// TaskData holds task data for the tasks panel.
type TaskData struct {
	ID        string
	Title     string
	Status    string
	Priority  string
	BlockedBy []string
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// TasksPanel renders a task board with status columns.
type TasksPanel struct {
	width, height int
	tasks         []TaskData
	pendingCount  int
	activeCount   int
	blockedCount  int

	// Interactive state
	selectedIdx int
	flatTasks   []TaskData // ordered: pending, active, blocked
}

// NewTasksPanel creates a new tasks panel.
func NewTasksPanel() TasksPanel {
	return TasksPanel{}
}

// SelectedTask returns the currently selected task, if any.
func (p TasksPanel) SelectedTask() *TaskData {
	if len(p.flatTasks) == 0 || p.selectedIdx >= len(p.flatTasks) {
		return nil
	}
	t := p.flatTasks[p.selectedIdx]
	return &t
}

// Init satisfies the bubbletea model interface.
func (p TasksPanel) Init() tea.Cmd { return nil }

// Update processes messages.
func (p TasksPanel) Update(msg tea.Msg) (TasksPanel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height
	case MsgTasksData:
		p.tasks = msg.Tasks
		p.pendingCount = msg.PendingCount
		p.activeCount = msg.ActiveCount
		p.blockedCount = msg.BlockedCount
		p.rebuildFlatTasks()
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if p.selectedIdx < len(p.flatTasks)-1 {
				p.selectedIdx++
			}
		case "k", "up":
			if p.selectedIdx > 0 {
				p.selectedIdx--
			}
		case "enter":
			if t := p.SelectedTask(); t != nil {
				next := cycleTaskStatus(t.Status)
				return p, func() tea.Msg {
					return MsgTaskStatusCycled{TaskID: t.ID, NewStatus: next}
				}
			}
		}
	}
	return p, nil
}

// rebuildFlatTasks orders tasks: pending → in_progress → blocked.
func (p *TasksPanel) rebuildFlatTasks() {
	p.flatTasks = p.flatTasks[:0]
	var pending, active, blocked []TaskData
	for _, t := range p.tasks {
		switch strings.ToLower(t.Status) {
		case "pending":
			pending = append(pending, t)
		case "in_progress", "active":
			active = append(active, t)
		case "blocked":
			blocked = append(blocked, t)
		}
	}
	p.flatTasks = append(p.flatTasks, pending...)
	p.flatTasks = append(p.flatTasks, active...)
	p.flatTasks = append(p.flatTasks, blocked...)
	if p.selectedIdx >= len(p.flatTasks) {
		p.selectedIdx = max(0, len(p.flatTasks)-1)
	}
}

// cycleTaskStatus returns the next status in the cycle.
func cycleTaskStatus(status string) string {
	switch strings.ToLower(status) {
	case "pending":
		return "in_progress"
	case "in_progress", "active":
		return "completed"
	default:
		return "pending"
	}
}

// View renders the tasks panel.
func (p TasksPanel) View() string {
	var b strings.Builder

	// Section title
	b.WriteString(theme.Styles.SectionTitle.Render("TASK BOARD"))
	b.WriteString("\n")

	// Summary counts
	b.WriteString(p.renderSummary())
	b.WriteString("\n\n")

	if len(p.tasks) == 0 {
		b.WriteString(theme.Styles.MutedText.Render("  No tasks"))
		b.WriteString("\n")
		return b.String()
	}

	b.WriteString(p.renderColumns())

	// Navigation hint
	hintStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted)
	b.WriteString(hintStyle.Render("  j/k:move  enter:cycle status"))
	b.WriteString("\n")

	return b.String()
}

func (p TasksPanel) renderSummary() string {
	parts := []string{
		theme.Styles.StatusWarn.Render(fmt.Sprintf("%d pending", p.pendingCount)),
		theme.Styles.StatusOK.Render(fmt.Sprintf("%d active", p.activeCount)),
		theme.Styles.StatusError.Render(fmt.Sprintf("%d blocked", p.blockedCount)),
	}
	return strings.Join(parts, "  ")
}

func (p TasksPanel) renderColumns() string {
	// Partition tasks by status
	var pending, active, blocked []TaskData
	for _, t := range p.tasks {
		switch strings.ToLower(t.Status) {
		case "pending":
			pending = append(pending, t)
		case "in_progress", "active":
			active = append(active, t)
		case "blocked":
			blocked = append(blocked, t)
		}
	}

	// Determine column width based on available space.
	// Use a three-column layout if wide enough, otherwise sequential.
	//
	// renderColumn() sets lipgloss.Width(w) which controls *content* width,
	// but RoundedBorder (+2) and Padding(0,1) (+2) add 4 chars to rendered
	// output.  We subtract that overhead so the total rendered width fits.
	const borderPadding = 4 // 2 border chars + 2 padding chars
	minColWidth := 28
	gapWidth := 4 // 2 spaces between each of the 3 columns (2 gaps)
	useColumns := p.width >= (minColWidth+borderPadding)*3+gapWidth

	if useColumns {
		colContent := (p.width - gapWidth) / 3 // rendered width per column
		colWidth := colContent - borderPadding // content width for lipgloss
		if colWidth < minColWidth {
			colWidth = minColWidth
		}
		pendingCol := p.renderColumn("PENDING", pending, colWidth)
		activeCol := p.renderColumn("IN PROGRESS", active, colWidth)
		blockedCol := p.renderColumn("BLOCKED", blocked, colWidth)
		return lipgloss.JoinHorizontal(lipgloss.Top, pendingCol, "  ", activeCol, "  ", blockedCol)
	}

	// Sequential layout for narrow terminals
	seqWidth := p.width - borderPadding
	if seqWidth < minColWidth {
		seqWidth = minColWidth
	}
	var b strings.Builder
	b.WriteString(p.renderColumn("PENDING", pending, seqWidth))
	b.WriteString("\n")
	b.WriteString(p.renderColumn("IN PROGRESS", active, seqWidth))
	b.WriteString("\n")
	b.WriteString(p.renderColumn("BLOCKED", blocked, seqWidth))
	return b.String()
}

func (p TasksPanel) renderColumn(title string, tasks []TaskData, width int) string {
	colStyle := lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(theme.ColorBorder).
		Padding(0, 1)

	headerStyle := lipgloss.NewStyle().
		Foreground(theme.ColorFgSecondary).
		Bold(true)

	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("%s (%d)", title, len(tasks))))
	b.WriteString("\n")

	if len(tasks) == 0 {
		b.WriteString(theme.Styles.MutedText.Render("  ---"))
		b.WriteString("\n")
		return colStyle.Render(b.String())
	}

	for _, t := range tasks {
		isSelected := p.isTaskSelected(t.ID)
		badge := priorityBadge(t.Priority)
		taskTitle := truncate(t.Title, width-6) // cursor(2) + space(1) + badge(1) + space(1) + buffer(1)

		cursor := " "
		if isSelected {
			cursor = lipgloss.NewStyle().Foreground(theme.ColorAccent).Bold(true).Render("▸")
		}
		line := fmt.Sprintf("%s %s %s", cursor, badge, taskTitle)
		b.WriteString(line)
		b.WriteString("\n")
	}

	return colStyle.Render(b.String())
}

// isTaskSelected returns true if the given task ID matches the currently selected task.
func (p TasksPanel) isTaskSelected(id string) bool {
	if p.selectedIdx >= len(p.flatTasks) {
		return false
	}
	return p.flatTasks[p.selectedIdx].ID == id
}

// priorityBadge returns a colored single-character priority indicator.
func priorityBadge(priority string) string {
	switch strings.ToLower(priority) {
	case "critical":
		return lipgloss.NewStyle().
			Foreground(theme.ColorError).
			Bold(true).
			Render("C")
	case "high":
		return lipgloss.NewStyle().
			Foreground(theme.ColorWarning).
			Bold(true).
			Render("H")
	case "medium":
		return lipgloss.NewStyle().
			Foreground(theme.ColorInfo).
			Render("M")
	case "low":
		return lipgloss.NewStyle().
			Foreground(theme.ColorFgMuted).
			Render("L")
	default:
		return lipgloss.NewStyle().
			Foreground(theme.ColorFgMuted).
			Render("-")
	}
}
