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

// MsgMemoryItems delivers items for an expanded tier.
type MsgMemoryItems struct {
	Tier  string
	Items []MemoryItemData
}

// MemoryItemData holds a single memory item for display.
type MemoryItemData struct {
	ID         string
	Title      string
	Tier       string
	Importance string
	Tokens     int
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

	// Interactive state
	selectedTier int                         // 0=Working, 1=Short, 2=Long
	expanded     map[string]bool             // tier name -> expanded
	tierItems    map[string][]MemoryItemData // tier name -> items
	itemOffset   int                         // scroll offset within expanded tier
}

var tierNames = []string{"working", "short_term", "long_term"}

// NewMemoryPanel creates a new memory panel.
func NewMemoryPanel() MemoryPanel {
	return MemoryPanel{
		expanded:  make(map[string]bool),
		tierItems: make(map[string][]MemoryItemData),
	}
}

// Init satisfies the bubbletea model interface.
func (p MemoryPanel) Init() tea.Cmd { return nil }

// SelectedTier returns the tier name for the currently selected tier.
func (p MemoryPanel) SelectedTier() string {
	if p.selectedTier < 0 || p.selectedTier >= len(tierNames) {
		return ""
	}
	return tierNames[p.selectedTier]
}

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
	case MsgMemoryItems:
		p.tierItems[msg.Tier] = msg.Items
	case tea.KeyMsg:
		tier := p.SelectedTier()
		isExpanded := p.expanded[tier]

		switch msg.String() {
		case "j", "down":
			if isExpanded {
				items := p.tierItems[tier]
				if p.itemOffset < len(items)-1 {
					p.itemOffset++
				}
			} else if p.selectedTier < 2 {
				p.selectedTier++
				p.itemOffset = 0
			}
		case "k", "up":
			if isExpanded && p.itemOffset > 0 {
				p.itemOffset--
			} else if isExpanded && p.itemOffset == 0 {
				// Collapse on up at top
				p.expanded[tier] = false
			} else if p.selectedTier > 0 {
				p.selectedTier--
				p.itemOffset = 0
			}
		case "enter":
			p.expanded[tier] = !p.expanded[tier]
			p.itemOffset = 0
		case "esc":
			for k := range p.expanded {
				delete(p.expanded, k)
			}
			p.itemOffset = 0
		}
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

	tierData := []struct {
		name   string
		items  int
		tokens int
		max    int
		color  lipgloss.Color
		tier   string
	}{
		{"Working", p.workingItems, p.workingTokens, 100, theme.ColorTierWorking, "working"},
		{"Short-Term", p.shortItems, p.shortTokens, 500, theme.ColorTierShort, "short_term"},
		{"Long-Term", p.longItems, p.longTokens, 2000, theme.ColorTierLong, "long_term"},
	}

	for i, td := range tierData {
		isSelected := i == p.selectedTier
		cursor := "  "
		if isSelected {
			cursor = lipgloss.NewStyle().Foreground(theme.ColorAccent).Bold(true).Render("▸ ")
		}
		b.WriteString(cursor)
		b.WriteString(p.renderTier(td.name, td.items, td.tokens, td.max, td.color, gaugeWidth-2))
		b.WriteString("\n")

		// Expanded tier item list
		if p.expanded[td.tier] {
			items := p.tierItems[td.tier]
			if len(items) == 0 {
				b.WriteString(lipgloss.NewStyle().Foreground(theme.ColorFgMuted).PaddingLeft(4).Render("(loading items...)"))
				b.WriteString("\n")
			} else {
				maxVisible := 8
				start := p.itemOffset
				if i == p.selectedTier && start > len(items)-maxVisible {
					start = max(0, len(items)-maxVisible)
				}
				end := min(start+maxVisible, len(items))
				for j := start; j < end; j++ {
					item := items[j]
					itemCursor := "   "
					if i == p.selectedTier && j == p.itemOffset {
						itemCursor = lipgloss.NewStyle().Foreground(theme.ColorFgSecondary).Render(" › ")
					}
					titleStr := truncate(item.Title, p.width-20)
					tokStr := lipgloss.NewStyle().Foreground(theme.ColorFgMuted).Render(fmt.Sprintf(" (%s tok)", formatNumber(item.Tokens)))
					b.WriteString(fmt.Sprintf("  %s%s%s\n", itemCursor, titleStr, tokStr))
				}
				if len(items) > maxVisible {
					b.WriteString(lipgloss.NewStyle().Foreground(theme.ColorFgMuted).PaddingLeft(4).Render(
						fmt.Sprintf("[%d-%d of %d]", start+1, end, len(items)),
					))
					b.WriteString("\n")
				}
			}
		}
	}

	b.WriteString("\n")

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

	// Navigation hint
	hintStyle := lipgloss.NewStyle().Foreground(theme.ColorFgMuted)
	b.WriteString(hintStyle.Render("  j/k:move  enter:expand  esc:collapse"))
	b.WriteString("\n")

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
