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
}

// NewTasksPanel creates a new tasks panel.
func NewTasksPanel() TasksPanel {
	return TasksPanel{}
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
	}
	return p, nil
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
	minColWidth := 28
	useColumns := p.width >= minColWidth*3+4

	if useColumns {
		colWidth := (p.width - 4) / 3
		pendingCol := p.renderColumn("PENDING", pending, colWidth)
		activeCol := p.renderColumn("IN PROGRESS", active, colWidth)
		blockedCol := p.renderColumn("BLOCKED", blocked, colWidth)
		return lipgloss.JoinHorizontal(lipgloss.Top, pendingCol, "  ", activeCol, "  ", blockedCol)
	}

	// Sequential layout for narrow terminals
	var b strings.Builder
	b.WriteString(p.renderColumn("PENDING", pending, p.width))
	b.WriteString("\n")
	b.WriteString(p.renderColumn("IN PROGRESS", active, p.width))
	b.WriteString("\n")
	b.WriteString(p.renderColumn("BLOCKED", blocked, p.width))
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
		badge := priorityBadge(t.Priority)
		taskTitle := truncate(t.Title, width-8)
		line := fmt.Sprintf(" %s %s", badge, taskTitle)
		b.WriteString(line)
		b.WriteString("\n")
	}

	return colStyle.Render(b.String())
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
