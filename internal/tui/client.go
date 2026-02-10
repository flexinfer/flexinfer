package tui

import (
	"log/slog"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
)

// Client provides data access for the TUI, backed by the same monitors
// used by the web dashboard.
type Client struct {
	daemon *bridge.DaemonClient
	agent  *bridge.AgentBridge
	fleet  *monitor.FleetMonitor
	health *monitor.HealthMonitor
	memory *monitor.MemoryMonitor
	stream *monitor.StreamMonitor
	logger *slog.Logger
}

// NewClient creates a TUI client connected to the daemon socket.
func NewClient(socketPath string, logger *slog.Logger) (*Client, error) {
	d := bridge.NewDaemonClient(socketPath, logger)
	if err := d.Connect(); err != nil {
		return nil, err
	}
	a := bridge.NewAgentBridge(d)
	c := &Client{
		daemon: d,
		agent:  a,
		logger: logger,
	}
	// Initialize monitors with the same cadences as the web dashboard.
	c.fleet = monitor.NewFleetMonitor(d, a, logger)
	c.health = monitor.NewHealthMonitor(d, logger)
	c.memory = monitor.NewMemoryMonitor(a, logger)
	c.stream = monitor.NewStreamMonitor(a, logger)
	return c, nil
}

// Start begins all background monitor polling.
func (c *Client) Start() {
	c.fleet.Start(15 * time.Second)
	c.health.Start(5 * time.Second)
	c.memory.Start(10 * time.Second)
	c.stream.Start(5 * time.Second)
}

// Stop halts all monitors and closes the daemon connection.
func (c *Client) Stop() {
	c.fleet.Stop()
	c.health.Stop()
	c.memory.Stop()
	c.stream.Stop()
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
}
