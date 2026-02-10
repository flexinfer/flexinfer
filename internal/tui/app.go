// Package tui implements a bubbletea-based terminal dashboard for the Agent HUD.
// It connects directly to the loom daemon socket (same as the web dashboard)
// and renders five panels: Fleet, Health, Tasks, Memory, and Stream.
package tui

import (
	"fmt"
	"log/slog"
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
	PanelFleet Panel = iota
	PanelHealth
	PanelTasks
	PanelMemory
	PanelStream
)

var panelNames = []string{"Fleet", "Health", "Tasks", "Memory", "Stream"}

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
	fleet  panels.FleetPanel
	health panels.HealthPanel
	tasks  panels.TasksPanel
	memory panels.MemoryPanel
	stream panels.StreamPanel

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
		client:  client,
		active:  PanelFleet,
		fleet:   panels.NewFleetPanel(),
		health:  panels.NewHealthPanel(),
		tasks:   panels.NewTasksPanel(),
		memory:  panels.NewMemoryPanel(),
		stream:  panels.NewStreamPanel(),
		spinner: s,
		help:    h,
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
		m.fleet, _ = m.fleet.Update(sizeMsg)
		m.health, _ = m.health.Update(sizeMsg)
		m.tasks, _ = m.tasks.Update(sizeMsg)
		m.memory, _ = m.memory.Update(sizeMsg)
		m.stream, _ = m.stream.Update(sizeMsg)

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
	case panels.MsgStreamData:
		m.stream, _ = m.stream.Update(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)
	}

	// Forward key messages to the active panel (for scrolling etc).
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch m.active {
		case PanelStream:
			m.stream, _ = m.stream.Update(keyMsg)
		case PanelTasks:
			m.tasks, _ = m.tasks.Update(keyMsg)
		}
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
	default:
		return ""
	}
}

func (m Model) renderTabs() string {
	var tabs []string
	for i, name := range panelNames {
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

// tickCmd returns a command that sends a tick after the refresh interval.
func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		return msgTick(t)
	})
}

// fetchAll creates a command that fetches data from all monitors and
// dispatches the results as panel data messages.
func (m Model) fetchAll() tea.Cmd {
	return func() tea.Msg {
		// Snapshot all monitors (thread-safe reads).
		snap := m.client.FleetSnapshot()
		servers := m.client.Servers()
		memStats := m.client.MemoryStats()
		entries := m.client.StreamEntries()

		// Build fleet data.
		fleetSessions := make([]panels.SessionData, len(snap.Sessions))
		for i, s := range snap.Sessions {
			fleetSessions[i] = panels.SessionData{
				ID:         s.ID,
				AgentID:    s.AgentID,
				Namespace:  s.Namespace,
				Status:     s.Status,
				TokenCount: s.TotalTokens,
				EntryCount: s.EntryCount,
			}
		}
		fleetAgents := make([]panels.AgentData, len(snap.Agents))
		for i, a := range snap.Agents {
			fleetAgents[i] = panels.AgentData{
				AgentID:   a.AgentID,
				Status:    a.Status,
				AgentType: a.AgentType,
			}
		}

		// Build health data.
		healthServers := make([]panels.ServerData, len(servers))
		for i, s := range servers {
			healthServers[i] = panels.ServerData{
				Name:           s.Name,
				Running:        s.Running,
				Healthy:        s.Healthy,
				Latency:        s.AvgLatencyMs,
				LatencyHistory: s.LatencyHistory,
				ConsecFails:    s.ConsecFails,
				Error:          s.ErrorMessage,
			}
		}

		// Build memory data.
		var memData panels.MsgMemoryData
		if memStats != nil {
			memData = panels.MsgMemoryData{
				WorkingItems:  memStats.WorkingMemory.Items,
				WorkingTokens: memStats.WorkingMemory.Tokens,
				ShortItems:    memStats.ShortTermMemory.Items,
				ShortTokens:   memStats.ShortTermMemory.Tokens,
				LongItems:     memStats.LongTermMemory.Items,
				LongTokens:    memStats.LongTermMemory.Tokens,
				TotalItems:    memStats.TotalItems,
				TotalTokens:   memStats.TotalTokens,
			}
		}

		// Build tasks data.
		tasksList := make([]panels.TaskData, len(snap.Tasks))
		for i, t := range snap.Tasks {
			tasksList[i] = panels.TaskData{
				ID:        t.ID,
				Title:     t.Title,
				Status:    t.Status,
				Priority:  t.Priority,
				BlockedBy: t.BlockedBy,
			}
		}

		// Build stream data.
		streamEntries := make([]panels.StreamEntryData, len(entries))
		for i, e := range entries {
			streamEntries[i] = panels.StreamEntryData{
				ID:        e.ID,
				EntryType: e.EntryType,
				AgentID:   e.AgentID,
				Namespace: e.Namespace,
				Title:     e.Title,
				Timestamp: e.Timestamp,
			}
		}

		// Return a batch message. We use a wrapper to send multiple messages.
		return batchDataMsg{
			fleet: panels.MsgFleetData{
				DaemonRunning:  snap.DaemonRunning,
				ServerCount:    snap.ServerCount,
				Sessions:       fleetSessions,
				ActiveSessions: snap.ActiveSessions,
				Agents:         fleetAgents,
				TotalTokens:    snap.TotalTokens,
				UpdatedAt:      snap.UpdatedAt,
			},
			health: panels.MsgHealthData{Servers: healthServers},
			tasks: panels.MsgTasksData{
				Tasks:        tasksList,
				PendingCount: snap.PendingTasks,
				ActiveCount:  snap.ActiveTasks,
				BlockedCount: snap.BlockedTasks,
			},
			memory: memData,
			stream: panels.MsgStreamData{Entries: streamEntries},
		}
	}
}

// batchDataMsg carries all panel data in a single message.
// The Update loop unpacks it and routes to individual panels.
type batchDataMsg struct {
	fleet  panels.MsgFleetData
	health panels.MsgHealthData
	tasks  panels.MsgTasksData
	memory panels.MsgMemoryData
	stream panels.MsgStreamData
}

// Run starts the TUI dashboard. This is the main entry point called from the CLI.
func Run(socketPath string) error {
	logger := slog.Default().With("component", "tui")

	client, err := NewClient(socketPath, logger)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	client.Start()
	defer client.Stop()

	model := New(client)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}
