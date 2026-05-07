package tui

import (
	"log/slog"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
)

// Deps bundles pre-existing monitors and bridge for shared-mode operation.
// When the HUD and TUI co-host, the HUD owns the monitors and passes them
// here so the TUI reads from the same cached snapshots without creating a
// second daemon connection or duplicate polling loops.
type Deps struct {
	Agent  *bridge.AgentBridge
	Fleet  *monitor.FleetMonitor
	Health *monitor.HealthMonitor
	Memory *monitor.MemoryMonitor
	Stream *monitor.StreamMonitor
	Cost   *monitor.CostMonitor
}

// Client provides data access for the TUI, backed by the same monitors
// used by the web dashboard.
type Client struct {
	daemon *bridge.DaemonClient
	agent  *bridge.AgentBridge
	fleet  *monitor.FleetMonitor
	health *monitor.HealthMonitor
	memory *monitor.MemoryMonitor
	stream *monitor.StreamMonitor
	cost   *monitor.CostMonitor
	logger *slog.Logger
	owned  bool // true = we created monitors and must stop them
}

// NewClient creates a TUI client connected to the daemon socket.
// The client owns its monitors and daemon connection (owned=true).
func NewClient(socketPath string, logger *slog.Logger) (*Client, error) {
	d := bridge.NewDaemonClient(socketPath, logger)
	// Don't fail hard if the daemon isn't up yet. The daemon client can
	// reconnect (and the bridge can autostart) on the first poll/call.
	if err := d.Connect(); err != nil {
		logger.Warn("daemon connect failed; continuing in disconnected mode", "error", err)
	}
	a := bridge.NewAgentBridge(d)
	c := &Client{
		daemon: d,
		agent:  a,
		logger: logger,
		owned:  true,
	}
	// Initialize monitors with the same cadences as the web dashboard.
	c.fleet = monitor.NewFleetMonitor(d, a, logger)
	c.health = monitor.NewHealthMonitor(d, logger)
	c.memory = monitor.NewMemoryMonitor(a, logger)
	c.stream = monitor.NewStreamMonitor(a, logger)
	c.cost = monitor.NewCostMonitor(d, logger)
	return c, nil
}

// NewClientFromDeps creates a TUI client backed by externally-owned monitors.
// Start() and Stop() are no-ops because the caller manages the monitor lifecycle.
func NewClientFromDeps(deps Deps, logger *slog.Logger) *Client {
	return &Client{
		agent:  deps.Agent,
		fleet:  deps.Fleet,
		health: deps.Health,
		memory: deps.Memory,
		stream: deps.Stream,
		cost:   deps.Cost,
		logger: logger,
		owned:  false,
	}
}

// Start begins all background monitor polling.
// No-op when the client was created via NewClientFromDeps (monitors are externally managed).
func (c *Client) Start() {
	if !c.owned {
		return
	}
	c.fleet.Start(15 * time.Second)
	c.health.Start(5 * time.Second)
	c.memory.Start(10 * time.Second)
	c.stream.Start(5 * time.Second)
	if c.cost != nil {
		c.cost.Start(30 * time.Second)
	}
}

// Stop halts all monitors and closes the daemon connection.
// No-op when the client was created via NewClientFromDeps (monitors are externally managed).
func (c *Client) Stop() {
	if !c.owned {
		return
	}
	c.fleet.Stop()
	c.health.Stop()
	c.memory.Stop()
	c.stream.Stop()
	if c.cost != nil {
		c.cost.Stop()
	}
	c.daemon.Close()
}

// FleetSnapshot returns the current fleet state.
func (c *Client) FleetSnapshot() monitor.FleetSnapshot {
	return c.fleet.Snapshot()
}

// Servers returns server health entries.
func (c *Client) Servers() []monitor.ServerHealthEntry {
	return c.health.Servers()
}

// MemoryStats returns memory tier statistics.
func (c *Client) MemoryStats() *bridge.MemoryStatsResult {
	return c.memory.Stats()
}

// MemoryTokenHistory returns sparkline history of TotalTokens readings.
func (c *Client) MemoryTokenHistory() []float64 {
	return c.memory.TokenHistory()
}

// StreamEntries returns recent context stream entries.
func (c *Client) StreamEntries() []monitor.StreamEntry {
	return c.stream.Entries()
}

// Refresh triggers immediate refresh of all monitors.
func (c *Client) Refresh() {
	c.fleet.Refresh()
	c.health.Refresh()
	c.memory.Refresh()
	c.stream.Refresh()
	if c.cost != nil {
		c.cost.Refresh()
	}
}

// CostSnapshot returns the current cost snapshot, or a zero-valued one when
// the cost monitor is unavailable (e.g., bridge-mode without Cost dep).
func (c *Client) CostSnapshot() monitor.CostSnapshot {
	if c.cost == nil {
		return monitor.CostSnapshot{}
	}
	return c.cost.Snapshot()
}

// RBACConfig returns the current RBAC config snapshot directly via daemon RPC.
// Unlike CostSnapshot, RBAC has no dedicated monitor; the TUI fetches on each
// refresh tick. Returns a nil result when the daemon is unreachable.
func (c *Client) RBACConfig() *bridge.RBACConfigResult {
	if c.daemon == nil {
		return nil
	}
	res, err := c.daemon.RBACConfig()
	if err != nil {
		c.logger.Debug("rbac config fetch failed", "error", err)
		return nil
	}
	return res
}

// UpdateTaskStatus updates a task's status via the agent bridge.
func (c *Client) UpdateTaskStatus(taskID, status string) error {
	return c.agent.UpdateTask(bridge.UpdateTaskParams{
		ID:     taskID,
		Status: status,
	})
}

// SessionEntries returns context entries for a specific session.
func (c *Client) SessionEntries(sessionID string) []monitor.StreamEntry {
	entries, err := c.agent.SessionEntries(sessionID, 10)
	if err != nil {
		c.logger.Debug("failed to fetch session entries", "session", sessionID, "error", err)
		return nil
	}
	result := make([]monitor.StreamEntry, len(entries))
	for i, e := range entries {
		result[i] = monitor.ContextEntryToStreamEntry(e)
	}
	return result
}

// MemoryItems returns items from a specific memory tier.
func (c *Client) MemoryItems(tier string) []bridge.MemoryItem {
	items, err := c.memory.Recall(tier, "", 50)
	if err != nil {
		c.logger.Debug("failed to fetch memory items", "tier", tier, "error", err)
		return nil
	}
	return items
}
