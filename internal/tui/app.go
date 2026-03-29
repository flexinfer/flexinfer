// Package tui implements a bubbletea-based terminal dashboard for the Agent HUD.
// It connects directly to the loom daemon socket (same as the web dashboard)
// and renders five panels: Fleet, Health, Tasks, Memory, and Stream.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/panels"
	"github.com/crb2nu/loom/internal/tui/widgets"
)

// Panel identifies which panel is currently active.
type Panel int

const (
	PanelOverview Panel = iota
	PanelFleet
	PanelHealth
	PanelTasks
	PanelMemory
	PanelStream
	PanelPresence
)

var panelNames = []string{"Overview", "Fleet", "Health", "Tasks", "Memory", "Stream", "Presence"}

// msgTick is sent on each refresh interval to trigger data fetches.
type msgTick time.Time

// msgRefreshDone signals that a manual refresh completed.
type msgRefreshDone struct{}

// Model is the root bubbletea model for the TUI dashboard.
type Model struct {
	client   *Client
	active   Panel
	width    int
	height   int
	layout   Layout
	ready    bool
	quitting bool

	// Sub-models
	overview panels.OverviewPanel
	fleet    panels.FleetPanel
	health   panels.HealthPanel
	tasks    panels.TasksPanel
	memory   panels.MemoryPanel
	stream   panels.StreamPanel
	presence panels.PresencePanel

	// UI components
	spinner spinner.Model
	help    help.Model

	// State
	lastRefresh time.Time
	refreshing  bool
}

// New creates a new TUI dashboard model.
func New(client *Client) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(ColorAccent)

	h := help.New()
	h.ShortSeparator = "  "
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(ColorFgMuted)
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(ColorFgMuted)

	return Model{
		client:   client,
		active:   PanelOverview,
		overview: panels.NewOverviewPanel(),
		fleet:    panels.NewFleetPanel(),
		health:   panels.NewHealthPanel(),
		tasks:    panels.NewTasksPanel(),
		memory:   panels.NewMemoryPanel(),
		stream:   panels.NewStreamPanel(),
		presence: panels.NewPresencePanel(),
		spinner:  s,
		help:     h,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.tickCmd(),
		m.fetchAll(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, Keys.Quit):
			m.quitting = true
			return m, tea.Quit

		case key.Matches(msg, Keys.Overview):
			m.active = PanelOverview
		case key.Matches(msg, Keys.Fleet):
			m.active = PanelFleet
		case key.Matches(msg, Keys.Health):
			m.active = PanelHealth
		case key.Matches(msg, Keys.Tasks):
			m.active = PanelTasks
		case key.Matches(msg, Keys.Memory):
			m.active = PanelMemory
		case key.Matches(msg, Keys.Stream):
			m.active = PanelStream
		case key.Matches(msg, Keys.Presence):
			m.active = PanelPresence

		case key.Matches(msg, Keys.Refresh):
			m.refreshing = true
			cmds = append(cmds, m.fetchAll())

		case key.Matches(msg, Keys.Help):
			m.help.ShowAll = !m.help.ShowAll
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout = NewLayout(msg.Width, msg.Height)
		m.ready = true

		// Propagate to all panels.
		sizeMsg := tea.WindowSizeMsg{
			Width:  m.layout.ContentW,
			Height: m.layout.ContentH,
		}
		m.overview, _ = m.overview.Update(sizeMsg)
		m.fleet, _ = m.fleet.Update(sizeMsg)
		m.health, _ = m.health.Update(sizeMsg)
		m.tasks, _ = m.tasks.Update(sizeMsg)
		m.memory, _ = m.memory.Update(sizeMsg)
		m.stream, _ = m.stream.Update(sizeMsg)
		m.presence, _ = m.presence.Update(sizeMsg)

	case msgTick:
		cmds = append(cmds, m.fetchAll(), m.tickCmd())

	case msgRefreshDone:
		m.refreshing = false
		m.lastRefresh = time.Now()

	// Batch data message — unpack and route to all panels.
	case batchDataMsg:
		m.fleet, _ = m.fleet.Update(msg.fleet)
		m.health, _ = m.health.Update(msg.health)
		m.tasks, _ = m.tasks.Update(msg.tasks)
		m.memory, _ = m.memory.Update(msg.memory)
		m.stream, _ = m.stream.Update(msg.stream)
		m.presence, _ = m.presence.Update(msg.presence)
		m.overview, _ = m.overview.Update(msg.overview)
		m.refreshing = false
		m.lastRefresh = time.Now()

	// Individual data messages — route to the appropriate panel.
	case panels.MsgFleetData:
		m.fleet, _ = m.fleet.Update(msg)
	case panels.MsgHealthData:
		m.health, _ = m.health.Update(msg)
	case panels.MsgTasksData:
		m.tasks, _ = m.tasks.Update(msg)
	case panels.MsgMemoryData:
		m.memory, _ = m.memory.Update(msg)
	case panels.MsgMemoryItems:
		m.memory, _ = m.memory.Update(msg)
	case panels.MsgStreamData:
		m.stream, _ = m.stream.Update(msg)
	case panels.MsgPresenceData:
		m.presence, _ = m.presence.Update(msg)

	case tea.MouseMsg:
		// Handle mouse clicks on the tab bar (row 1, after header).
		if msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonLeft {
			if msg.Y == 1 { // Tab bar row
				panel := m.tabFromX(msg.X)
				if panel >= 0 {
					m.active = panel
				}
			}
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Forward key messages to the active panel (for scrolling etc).
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		var cmd tea.Cmd
		switch m.active {
		case PanelOverview:
			m.overview, _ = m.overview.Update(keyMsg)
		case PanelFleet:
			m.fleet, cmd = m.fleet.Update(keyMsg)
			cmds = append(cmds, cmd)
		case PanelHealth:
			m.health, _ = m.health.Update(keyMsg)
		case PanelTasks:
			m.tasks, cmd = m.tasks.Update(keyMsg)
			cmds = append(cmds, cmd)
		case PanelMemory:
			m.memory, _ = m.memory.Update(keyMsg)
		case PanelStream:
			m.stream, _ = m.stream.Update(keyMsg)
		case PanelPresence:
			m.presence, _ = m.presence.Update(keyMsg)
		}
	}

	// Handle task status cycle from the tasks panel.
	if msg, ok := msg.(panels.MsgTaskStatusCycled); ok {
		cmds = append(cmds, m.updateTaskStatus(msg.TaskID, msg.NewStatus))
	}

	// Handle memory tier expansion — fetch items for the requested tier.
	if msg, ok := msg.(panels.MsgMemoryExpandTier); ok {
		cmds = append(cmds, m.fetchMemoryItems(msg.Tier))
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return fmt.Sprintf("\n  %s Loading HUD...\n", m.spinner.View())
	}

	var b strings.Builder

	// Header bar.
	snap := m.client.FleetSnapshot()
	header := widgets.Header{
		DaemonOnline: snap.DaemonRunning,
		ServerCount:  snap.ServerCount,
		SessionCount: snap.ActiveSessions,
		Refreshing:   m.refreshing,
		SpinnerView:  m.spinner.View(),
		Width:        m.width,
	}
	b.WriteString(header.Render())
	b.WriteByte('\n')

	// Tab bar.
	b.WriteString(m.renderTabs())
	b.WriteByte('\n')

	// Active panel content.
	content := m.activeView()
	// Pad to fill available height (minus header, tabs, help = 3 lines).
	contentH := m.height - 3
	if contentH < 1 {
		contentH = 1
	}
	contentStyle := lipgloss.NewStyle().
		Width(m.width).
		Height(contentH)
	b.WriteString(contentStyle.Render(content))

	// Help bar.
	b.WriteString(m.renderHelp())

	return b.String()
}

func (m Model) activeView() string {
	switch m.active {
	case PanelOverview:
		return m.overview.View()
	case PanelFleet:
		return m.fleet.View()
	case PanelHealth:
		return m.health.View()
	case PanelTasks:
		return m.tasks.View()
	case PanelMemory:
		return m.memory.View()
	case PanelStream:
		return m.stream.View()
	case PanelPresence:
		return m.presence.View()
	default:
		return ""
	}
}

var compactPanelNames = []string{"O", "F", "H", "T", "M", "S", "P"}

func (m Model) renderTabs() string {
	compact := m.width < 60
	var tabs []string
	names := panelNames
	if compact {
		names = compactPanelNames
	}
	for i, name := range names {
		style := Styles.InactiveTab
		if Panel(i) == m.active {
			style = Styles.ActiveTab
		}
		key := fmt.Sprintf("%d", i+1)
		tab := style.Render(fmt.Sprintf(" %s %s ", key, name))
		tabs = append(tabs, tab)
	}

	row := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)

	// Fill remaining width with background.
	fill := ""
	rowWidth := lipgloss.Width(row)
	if rowWidth < m.width {
		fill = lipgloss.NewStyle().
			Background(ColorBgSecondary).
			Width(m.width - rowWidth).
			Render("")
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, row, fill)
}

func (m Model) renderHelp() string {
	helpText := m.help.ShortHelpView(Keys.ShortHelp())
	style := Styles.HelpBar.Width(m.width)

	right := ""
	if m.refreshing {
		right = m.spinner.View() + " refreshing..."
	} else if !m.lastRefresh.IsZero() {
		right = lipgloss.NewStyle().
			Foreground(ColorFgMuted).
			Render(m.lastRefresh.Format("15:04:05"))
	}

	left := helpText
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 0 {
		gap = 0
	}

	return style.Render(left + strings.Repeat(" ", gap) + right)
}

// tabFromX returns the Panel index for a mouse click at the given X coordinate
// on the tab bar, or -1 if the click is outside any tab.
func (m Model) tabFromX(x int) Panel {
	// Use compact names when terminal is narrow.
	names := panelNames
	if m.width < 60 {
		names = compactPanelNames
	}
	// Each tab is " N Name " — estimate widths.
	offset := 0
	for i, name := range names {
		tabWidth := len(name) + 4 // " N Name " padding
		if x >= offset && x < offset+tabWidth {
			return Panel(i)
		}
		offset += tabWidth
	}
	return -1
}
