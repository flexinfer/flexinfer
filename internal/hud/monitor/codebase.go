package monitor

import (
	"context"
	"log/slog"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// CodebaseSnapshot captures the current state of the codebase index.
type CodebaseSnapshot struct {
	TotalFiles   int            `json:"total_files"`
	TotalSymbols int            `json:"total_symbols"`
	Languages    map[string]int `json:"languages"`
	LastIndexed  string         `json:"last_indexed"`
	IndexStatus  string         `json:"index_status"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// CodebaseMonitor tracks codebase index statistics by periodically polling
// the codebase-memory MCP server through the agent bridge.
type CodebaseMonitor struct {
	BaseMonitor[CodebaseSnapshot]
	agent *bridge.AgentBridge
}

// NewCodebaseMonitor creates a CodebaseMonitor backed by the given agent bridge.
func NewCodebaseMonitor(agent *bridge.AgentBridge, logger *slog.Logger) *CodebaseMonitor {
	m := &CodebaseMonitor{agent: agent}
	m.InitBase(logger, nil, "codebase-monitor")
	return m
}

// Start begins the background polling goroutine at the given interval.
func (m *CodebaseMonitor) Start(interval time.Duration) {
	m.BaseMonitor.Start(interval, m.refresh)
}

// Status returns the current cached codebase snapshot.
func (m *CodebaseMonitor) Status() CodebaseSnapshot {
	return m.Snapshot()
}

// refresh fetches the latest codebase stats from the agent bridge.
func (m *CodebaseMonitor) refresh(_ context.Context) (CodebaseSnapshot, error) {
	stats, err := m.agent.CodebaseStats()
	if err != nil {
		return CodebaseSnapshot{}, err
	}
	return CodebaseSnapshot{
		TotalFiles:   stats.TotalFiles,
		TotalSymbols: stats.TotalSymbols,
		Languages:    stats.Languages,
		LastIndexed:  stats.LastIndexed,
		IndexStatus:  stats.IndexStatus,
		UpdatedAt:    time.Now(),
	}, nil
}

// Refresh forces an immediate refresh. Exposed for external callers.
func (m *CodebaseMonitor) Refresh() error {
	snap, err := m.refresh(context.Background())
	if err != nil {
		return err
	}
	m.Update(snap)
	return nil
}
