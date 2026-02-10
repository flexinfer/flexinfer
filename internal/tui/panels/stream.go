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

// MsgStreamData is sent by the app when new stream entries arrive.
type MsgStreamData struct {
	Entries []StreamEntryData
}

// StreamEntryData holds a single context stream entry for the stream panel.
type StreamEntryData struct {
	ID        string
	EntryType string
	AgentID   string
	Namespace string
	Title     string
	Timestamp string
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// StreamPanel renders a scrollable activity log.
type StreamPanel struct {
	width, height int
	entries       []StreamEntryData
	scrollOffset  int
}

// NewStreamPanel creates a new stream panel.
func NewStreamPanel() StreamPanel {
	return StreamPanel{}
}

// Init satisfies the bubbletea model interface.
func (p StreamPanel) Init() tea.Cmd { return nil }

// Update processes messages, including scroll keys.
func (p StreamPanel) Update(msg tea.Msg) (StreamPanel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height
		p.clampScroll()
	case MsgStreamData:
		p.entries = msg.Entries
		p.clampScroll()
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			p.scrollOffset++
			p.clampScroll()
		case "k", "up":
			p.scrollOffset--
			p.clampScroll()
		case "g":
			p.scrollOffset = 0
		case "G":
			p.scrollOffset = len(p.entries)
			p.clampScroll()
		}
	}
	return p, nil
}

// visibleLines returns the number of entry lines that fit in the view.
func (p StreamPanel) visibleLines() int {
	// Reserve 3 lines for section header + summary + blank line.
	v := p.height - 3
	if v < 1 {
		v = 1
	}
	return v
}

// clampScroll ensures the scroll offset stays within bounds.
func (p *StreamPanel) clampScroll() {
	maxOffset := len(p.entries) - p.visibleLines()
	if maxOffset < 0 {
		maxOffset = 0
	}
	if p.scrollOffset > maxOffset {
		p.scrollOffset = maxOffset
	}
	if p.scrollOffset < 0 {
		p.scrollOffset = 0
	}
}

// View renders the stream panel.
func (p StreamPanel) View() string {
	var b strings.Builder

	// Section title
	b.WriteString(theme.Styles.SectionTitle.Render("CONTEXT STREAM"))
	b.WriteString("\n")

	// Summary
	countLabel := theme.Styles.Label.Render("Entries: ") +
		theme.Styles.Value.Render(fmt.Sprintf("%d", len(p.entries)))
	b.WriteString(countLabel)

	// Scroll indicator
	if len(p.entries) > p.visibleLines() {
		scrollInfo := theme.Styles.MutedText.Render(
			fmt.Sprintf("  [%d-%d of %d]",
				p.scrollOffset+1,
				min(p.scrollOffset+p.visibleLines(), len(p.entries)),
				len(p.entries)),
		)
		b.WriteString(scrollInfo)
	}
	b.WriteString("\n\n")

	if len(p.entries) == 0 {
		b.WriteString(theme.Styles.MutedText.Render("  No activity yet"))
		b.WriteString("\n")
		return b.String()
	}

	// Render visible entries
	visible := p.visibleLines()
	start := p.scrollOffset
	end := start + visible
	if end > len(p.entries) {
		end = len(p.entries)
	}

	for i := start; i < end; i++ {
		b.WriteString(p.renderEntry(p.entries[i]))
		b.WriteString("\n")
	}

	// Scroll hints
	if p.scrollOffset > 0 {
		b.WriteString(theme.Styles.MutedText.Render("  ^ more above (k/up)"))
		b.WriteString("\n")
	}
	if end < len(p.entries) {
		b.WriteString(theme.Styles.MutedText.Render("  v more below (j/down)"))
		b.WriteString("\n")
	}

	return b.String()
}

func (p StreamPanel) renderEntry(e StreamEntryData) string {
	// Timestamp (HH:MM:SS)
	ts := shortTimestamp(e.Timestamp)
	timeStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted)
	timeStr := timeStyle.Render(ts)

	// Type badge
	badge := entryTypeBadge(e.EntryType)

	// Agent
	agentStyle := lipgloss.NewStyle().Foreground(theme.ColorFgSecondary)
	agent := agentStyle.Render(padRight(truncate(e.AgentID, 12), 12))

	// Title
	titleStyle := lipgloss.NewStyle().Foreground(theme.ColorFgPrimary)
	maxTitle := p.width - 40
	if maxTitle < 10 {
		maxTitle = 10
	}
	title := titleStyle.Render(truncate(e.Title, maxTitle))

	return fmt.Sprintf(" %s %s %s %s", timeStr, badge, agent, title)
}

// shortTimestamp extracts HH:MM:SS from a timestamp string.
func shortTimestamp(ts string) string {
	if len(ts) == 0 {
		return "--:--:--"
	}
	// Try to find a time component in the string (HH:MM:SS).
	// Timestamps may be ISO 8601 like "2025-01-15T14:30:00Z".
	if idx := strings.Index(ts, "T"); idx >= 0 && len(ts) > idx+9 {
		return ts[idx+1 : idx+9]
	}
	// If the string already looks like a time, return as-is.
	if len(ts) >= 8 && ts[2] == ':' && ts[5] == ':' {
		return ts[:8]
	}
	return truncate(ts, 8)
}

// entryTypeBadge returns a colored badge for the entry type.
func entryTypeBadge(entryType string) string {
	var color lipgloss.Color
	switch strings.ToLower(entryType) {
	case "decision":
		color = theme.ColorAccent
	case "finding":
		color = theme.ColorInfo
	case "observation", "note":
		color = theme.ColorSuccess
	case "action", "task":
		color = theme.ColorWarning
	case "error":
		color = theme.ColorError
	default:
		color = theme.ColorFgMuted
	}

	style := lipgloss.NewStyle().
		Foreground(color).
		Bold(true).
		Width(11)

	label := entryType
	if label == "" {
		label = "note"
	}

	return style.Render(padRight(label, 10))
}
