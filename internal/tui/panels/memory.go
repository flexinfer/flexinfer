package panels

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
	"github.com/crb2nu/loom/internal/tui/widgets"
)

// ---------------------------------------------------------------------------
// Message
// ---------------------------------------------------------------------------

// MsgMemoryData is sent by the app when new memory stats arrive.
type MsgMemoryData struct {
	WorkingItems  int
	WorkingTokens int
	ShortItems    int
	ShortTokens   int
	LongItems     int
	LongTokens    int
	TotalItems    int
	TotalTokens   int
	History       []float64
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// MemoryPanel renders memory tier gauges and token trend.
type MemoryPanel struct {
	width, height int
	workingItems  int
	workingTokens int
	shortItems    int
	shortTokens   int
	longItems     int
	longTokens    int
	totalItems    int
	totalTokens   int
	history       []float64
}

// NewMemoryPanel creates a new memory panel.
func NewMemoryPanel() MemoryPanel {
	return MemoryPanel{}
}

// Init satisfies the bubbletea model interface.
func (p MemoryPanel) Init() tea.Cmd { return nil }

// Update processes messages.
func (p MemoryPanel) Update(msg tea.Msg) (MemoryPanel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height
	case MsgMemoryData:
		p.workingItems = msg.WorkingItems
		p.workingTokens = msg.WorkingTokens
		p.shortItems = msg.ShortItems
		p.shortTokens = msg.ShortTokens
		p.longItems = msg.LongItems
		p.longTokens = msg.LongTokens
		p.totalItems = msg.TotalItems
		p.totalTokens = msg.TotalTokens
		p.history = msg.History
	}
	return p, nil
}

// View renders the memory panel.
func (p MemoryPanel) View() string {
	var b strings.Builder

	// Section title
	b.WriteString(theme.Styles.SectionTitle.Render("MEMORY HIERARCHY"))
	b.WriteString("\n")

	// Summary
	summary := theme.Styles.Label.Render("Total: ") +
		theme.Styles.Value.Render(fmt.Sprintf("%d items", p.totalItems)) +
		"  " +
		theme.Styles.Label.Render("Tokens: ") +
		theme.Styles.Value.Render(formatNumber(p.totalTokens))
	b.WriteString(summary)
	b.WriteString("\n\n")

	// Tier gauges
	gaugeWidth := p.width - 4
	if gaugeWidth < 20 {
		gaugeWidth = 20
	}

	b.WriteString(p.renderTier("Working", p.workingItems, p.workingTokens, 100, theme.ColorTierWorking, gaugeWidth))
	b.WriteString("\n")
	b.WriteString(p.renderTier("Short-Term", p.shortItems, p.shortTokens, 500, theme.ColorTierShort, gaugeWidth))
	b.WriteString("\n")
	b.WriteString(p.renderTier("Long-Term", p.longItems, p.longTokens, 2000, theme.ColorTierLong, gaugeWidth))
	b.WriteString("\n\n")

	// Token trend sparkline
	if len(p.history) > 0 {
		trendLabel := theme.Styles.Label.Render("Token Trend: ")
		spark := widgets.Sparkline{
			Data:  p.history,
			Width: gaugeWidth - 14,
			Color: theme.ColorFgSecondary,
		}.Render()
		b.WriteString(trendLabel + spark)
		b.WriteString("\n")
	}

	return b.String()
}

func (p MemoryPanel) renderTier(name string, items, tokens, maxItems int, color lipgloss.Color, width int) string {
	// Tier label with color
	labelStyle := lipgloss.NewStyle().
		Foreground(color).
		Bold(true).
		Width(12)

	detailStyle := lipgloss.NewStyle().
		Foreground(theme.ColorFgMuted)

	label := labelStyle.Render(name)

	var ratio float64
	if maxItems > 0 {
		ratio = float64(items) / float64(maxItems)
	}
	gauge := widgets.Gauge{
		Value: ratio,
		Width: width - 14,
		Color: color,
	}.Render()

	detail := detailStyle.Render(fmt.Sprintf("  %d items  %s tokens", items, formatNumber(tokens)))

	return label + "\n" + gauge + "\n" + detail
}
