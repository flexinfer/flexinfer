package monitor

import (
	"log/slog"
	"sync"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// SandboxMonitor tracks devbox sandbox status by periodically calling
// devbox_summary through the daemon client. It follows the same pattern
// as MemoryMonitor.
type SandboxMonitor struct {
	client *bridge.DaemonClient
	logger *slog.Logger

	mu       sync.RWMutex
	snapshot map[string]any

	onRefresh func(map[string]any)

	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewSandboxMonitor creates a SandboxMonitor backed by the given daemon client.
func NewSandboxMonitor(client *bridge.DaemonClient, logger *slog.Logger) *SandboxMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &SandboxMonitor{
		client: client,
		logger: logger.With("component", "sandbox-monitor"),
		stopCh: make(chan struct{}),
	}
}

// OnRefresh registers a callback that fires after each successful refresh
// with the new sandbox snapshot. Used to broadcast data via SSE.
func (m *SandboxMonitor) OnRefresh(fn func(map[string]any)) {
	m.onRefresh = fn
}

// Start begins the background polling goroutine at the given interval.
func (m *SandboxMonitor) Start(interval time.Duration) {
	go func() {
		if err := m.Refresh(); err != nil {
			m.logger.Debug("initial sandbox refresh failed", "error", err)
		}
	}()
	go m.pollLoop(interval)
}

// Stop signals the background goroutine to exit.
func (m *SandboxMonitor) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
}

// Snapshot returns the current cached sandbox summary.
func (m *SandboxMonitor) Snapshot() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snapshot == nil {
		return nil
	}
	cp := make(map[string]any, len(m.snapshot))
	for k, v := range m.snapshot {
		cp[k] = v
	}
	return cp
}

// Refresh fetches the latest sandbox summary from devbox_summary via the daemon.
func (m *SandboxMonitor) Refresh() error {
	raw, err := m.client.CallTool("devbox_summary", nil)
	if err != nil {
		return err
	}
	snap, err := bridge.ParseToolResultMap(raw)
	if err != nil {
		return nil // Devbox unavailable or returned an error; skip silently.
	}
	m.applySnapshot(snap)
	return nil
}

func (m *SandboxMonitor) applySnapshot(snap map[string]any) {
	m.mu.Lock()
	m.snapshot = snap
	m.mu.Unlock()

	if m.onRefresh != nil {
		cp := make(map[string]any, len(snap))
		for k, v := range snap {
			cp[k] = v
		}
		m.onRefresh(cp)
	}
}

// pollLoop runs Refresh on a ticker until stopCh is closed.
func (m *SandboxMonitor) pollLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	consecutiveErrors := 0
	for {
		select {
		case <-m.stopCh:
			m.logger.Debug("sandbox monitor stopped")
			return
		case <-ticker.C:
			if err := m.Refresh(); err != nil {
				consecutiveErrors++
				if consecutiveErrors <= 3 {
					m.logger.Debug("sandbox refresh error", "error", err)
				}
				skipTicks := min(consecutiveErrors-1, 4)
				for range skipTicks {
					select {
					case <-m.stopCh:
						return
					case <-ticker.C:
					}
				}
			} else {
				if consecutiveErrors > 0 {
					m.logger.Info("sandbox refresh recovered", "after_errors", consecutiveErrors)
				}
				consecutiveErrors = 0
			}
		}
	}
}
