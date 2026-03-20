package monitor

import (
	"context"
	"log/slog"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// SandboxMonitor tracks devbox sandbox status by periodically calling
// devbox_summary through the daemon client. It follows the same pattern
// as other simple monitors using BaseMonitor.
type SandboxMonitor struct {
	BaseMonitor[map[string]any]
	client *bridge.DaemonClient
}

// NewSandboxMonitor creates a SandboxMonitor backed by the given daemon client.
func NewSandboxMonitor(client *bridge.DaemonClient, logger *slog.Logger) *SandboxMonitor {
	m := &SandboxMonitor{client: client}
	m.InitBase(logger, nil, "sandbox-monitor")
	return m
}

// Start begins the background polling goroutine at the given interval.
func (m *SandboxMonitor) Start(interval time.Duration) {
	m.BaseMonitor.Start(interval, m.refresh)
}

// Snapshot returns the current cached sandbox summary.
// Overrides BaseMonitor.Snapshot to return nil (not empty map) when
// no data has been fetched, and to return a shallow copy.
func (m *SandboxMonitor) Snapshot() map[string]any {
	m.RLock()
	defer m.RUnlock()
	snap := m.GetSnapshot()
	if snap == nil {
		return nil
	}
	cp := make(map[string]any, len(snap))
	for k, v := range snap {
		cp[k] = v
	}
	return cp
}

// refresh fetches the latest sandbox summary from devbox_summary via the daemon.
func (m *SandboxMonitor) refresh(_ context.Context) (map[string]any, error) {
	raw, err := m.client.CallTool("devbox_summary", nil)
	if err != nil {
		return nil, err
	}
	snap, err := bridge.ParseToolResultMap(raw)
	if err != nil {
		// Devbox unavailable or returned an error; return current snapshot.
		return m.Snapshot(), nil
	}
	return snap, nil
}

// Refresh forces an immediate refresh. Exposed for external callers.
func (m *SandboxMonitor) Refresh() error {
	snap, err := m.refresh(context.Background())
	if err != nil {
		return err
	}
	m.Update(snap)
	return nil
}
