package panels

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// MsgSessionEntries delivers context entries for an expanded session.
type MsgSessionEntries struct {
	SessionID string
	Entries   []StreamEntryData
}

// ---------------------------------------------------------------------------
// Message
// ---------------------------------------------------------------------------

// MsgFleetData is sent by the app when new fleet data arrives.
type MsgFleetData struct {
	DaemonRunning          bool
	ServerCount            int
	Sessions               []SessionData
	ActiveSessions         int
	Agents                 []AgentData
	AgentCoordination      []FleetAgentCoordination
	NamespaceCoordination  []FleetNamespaceCoordination
	TotalTokens            int
	NamespacesAtRisk       int
	AgentsNeedingAttention int
	SharedBranches         int
	ConflictFiles          int
	OrphanTasks            int
	UpdatedAt              time.Time
}

// SessionData holds session data for the fleet panel.
type SessionData struct {
	ID          string
	AgentID     string
	Namespace   string
	Status      string
	Description string
	StartedAt   string
	TokenCount  int
	EntryCount  int
}

// AgentData holds agent presence data for the fleet panel.
type AgentData struct {
	AgentID       string
	SessionID     string
	Status        string
	AgentType     string
	Description   string
	CurrentTask   string
	Branch        string
	LastHeartbeat string
}

// FleetAgentCoordination carries attention metadata for a session agent.
type FleetAgentCoordination struct {
	AgentID          string
	ClaimCount       int
	ConflictFiles    int
	BlockingOthers   int
	BlockedByOthers  int
	AttentionReasons []string
	NeedsAttention   bool
}

// FleetNamespaceCoordination carries risk metadata for a namespace.
type FleetNamespaceCoordination struct {
	Namespace          string
	BlockedTasks       int
	OrphanTasks        int
	ConflictFiles      int
	SharedBranches     int
	CrossAgentBlockers int
	AttentionScore     int
	AttentionReasons   []string
	NeedsAttention     bool
}

type fleetSortMode string

const (
	fleetSortStatus fleetSortMode = "status"
	fleetSortRecent fleetSortMode = "recent"
	fleetSortTokens fleetSortMode = "tokens"
)

func (m fleetSortMode) Next() fleetSortMode {
	switch m {
	case fleetSortStatus:
		return fleetSortRecent
	case fleetSortRecent:
		return fleetSortTokens
	default:
		return fleetSortStatus
	}
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// FleetPanel renders an overview of the active agent fleet.
type FleetPanel struct {
	width, height          int
	daemonRunning          bool
	serverCount            int
	sessions               []SessionData
	activeSessions         int
	agents                 []AgentData
	agentCoordination      map[string]FleetAgentCoordination
	namespaceCoordination  map[string]FleetNamespaceCoordination
	totalTokens            int
	namespacesAtRisk       int
	agentsNeedingAttention int
	sharedBranches         int
	conflictFiles          int
	orphanTasks            int
	updatedAt              time.Time

	// Interactive state
	selectedIdx     int
	expanded        map[string]bool              // session ID -> expanded
	sessionEntries  map[string][]StreamEntryData // session ID -> context entries
	flatRows        []SessionData                // flattened row order for cursor
	collapsedNS     map[string]bool              // namespace -> collapsed
	showAllSessions bool
	hiddenSessions  int
	sortMode        fleetSortMode
}

// NewFleetPanel creates a new fleet panel.
func NewFleetPanel() FleetPanel {
	return FleetPanel{
		expanded:        make(map[string]bool),
		sessionEntries:  make(map[string][]StreamEntryData),
		collapsedNS:     make(map[string]bool),
		showAllSessions: false,
		sortMode:        fleetSortStatus,
	}
}

// Init satisfies the bubbletea model interface.
func (p FleetPanel) Init() tea.Cmd { return nil }

// SelectedSession returns the currently selected session ID, if any, for
// use by the parent to fetch session entries on expand.
func (p FleetPanel) SelectedSession() string {
	if len(p.flatRows) == 0 || p.selectedIdx >= len(p.flatRows) {
		return ""
	}
	return p.flatRows[p.selectedIdx].ID
}

// Update processes messages.
func (p FleetPanel) Update(msg tea.Msg) (FleetPanel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height
	case MsgFleetData:
		p.daemonRunning = msg.DaemonRunning
		p.serverCount = msg.ServerCount
		p.sessions = msg.Sessions
		p.activeSessions = msg.ActiveSessions
		p.agents = msg.Agents
		p.agentCoordination = make(map[string]FleetAgentCoordination, len(msg.AgentCoordination))
		for _, coord := range msg.AgentCoordination {
			p.agentCoordination[coord.AgentID] = coord
		}
		p.namespaceCoordination = make(map[string]FleetNamespaceCoordination, len(msg.NamespaceCoordination))
		for _, coord := range msg.NamespaceCoordination {
			p.namespaceCoordination[coord.Namespace] = coord
		}
		p.totalTokens = msg.TotalTokens
		p.namespacesAtRisk = msg.NamespacesAtRisk
		p.agentsNeedingAttention = msg.AgentsNeedingAttention
		p.sharedBranches = msg.SharedBranches
		p.conflictFiles = msg.ConflictFiles
		p.orphanTasks = msg.OrphanTasks
		p.updatedAt = msg.UpdatedAt
		p.rebuildFlatRows()
	case MsgSessionEntries:
		p.sessionEntries[msg.SessionID] = msg.Entries
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if p.selectedIdx < len(p.flatRows)-1 {
				p.selectedIdx++
			}
		case "k", "up":
			if p.selectedIdx > 0 {
				p.selectedIdx--
			}
		case "enter":
			if sid := p.SelectedSession(); sid != "" {
				p.expanded[sid] = !p.expanded[sid]
			}
		case "esc":
			// Collapse all
			for k := range p.expanded {
				delete(p.expanded, k)
			}
		case "v":
			p.showAllSessions = !p.showAllSessions
			p.rebuildFlatRows()
		case "s":
			p.sortMode = p.sortMode.Next()
			p.rebuildFlatRows()
		case "c":
			if ns := p.selectedNamespace(); ns != "" {
				p.collapsedNS[ns] = !p.collapsedNS[ns]
				p.rebuildFlatRows()
			}
		case "x":
			p.collapsedNS = make(map[string]bool)
			p.rebuildFlatRows()
		}
	}
	return p, nil
}

// rebuildFlatRows builds the ordered list of sessions for cursor navigation.
func (p *FleetPanel) rebuildFlatRows() {
	selectedID := p.SelectedSession()
	p.flatRows = p.flatRows[:0]
	agentsBySession, agentsByID := p.agentLookups()
	sessions, hidden := p.filteredSessions(agentsBySession, agentsByID)
	namespaces, groups := p.groupedSessions(sessions, agentsBySession, agentsByID)
	for _, ns := range namespaces {
		if p.collapsedNS[ns] {
			continue
		}
		p.flatRows = append(p.flatRows, groups[ns]...)
	}
	p.hiddenSessions = hidden + p.hiddenByCollapsedNamespaces(groups)
	if selectedID != "" {
		for i, row := range p.flatRows {
			if row.ID == selectedID {
				p.selectedIdx = i
				return
			}
		}
	}
	if p.selectedIdx >= len(p.flatRows) {
		p.selectedIdx = max(0, len(p.flatRows)-1)
	}
}

func (p FleetPanel) selectedNamespace() string {
	if sid := p.SelectedSession(); sid != "" {
		for _, s := range p.sessions {
			if s.ID == sid {
				return sessionNamespace(s)
			}
		}
	}
	return ""
}
