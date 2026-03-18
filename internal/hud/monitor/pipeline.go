package monitor

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// cachedPipelineDetail wraps a PipelineDetail with an expiration timestamp.
type cachedPipelineDetail struct {
	detail    *bridge.PipelineDetail
	fetchedAt time.Time
}

// pipelineDetailTTL is how long a cached pipeline detail is considered fresh.
const pipelineDetailTTL = 10 * time.Second

// PipelineMonitor tracks active GitLab CI pipelines and caches their details.
// It polls the mcp-gitlab server at a configurable interval and lazily fetches
// individual pipeline details on demand.
type PipelineMonitor struct {
	agent    *bridge.AgentBridge
	logger   *slog.Logger
	projects []string // GitLab project paths to monitor.

	mu        sync.RWMutex
	pipelines []bridge.PipelineInfo
	details   map[int]*cachedPipelineDetail // pipeline ID -> cached detail

	onRefresh func([]bridge.PipelineInfo)

	stopCh   chan struct{}
	stopOnce sync.Once
}

// OnRefresh registers a callback that fires after each successful refresh
// with the updated pipeline list. Used to broadcast data via SSE.
func (m *PipelineMonitor) OnRefresh(fn func([]bridge.PipelineInfo)) {
	m.onRefresh = fn
}

// NewPipelineMonitor creates a PipelineMonitor that watches the given GitLab projects.
func NewPipelineMonitor(agent *bridge.AgentBridge, projects []string, logger *slog.Logger) *PipelineMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &PipelineMonitor{
		agent:    agent,
		logger:   logger.With("component", "pipeline-monitor"),
		projects: projects,
		details:  make(map[int]*cachedPipelineDetail),
		stopCh:   make(chan struct{}),
	}
}

// Start begins the background polling goroutine at the given interval.
func (m *PipelineMonitor) Start(interval time.Duration) {
	// Run initial refresh asynchronously so HUD startup is non-blocking.
	go func() {
		if err := m.Refresh(); err != nil {
			m.logger.Warn("initial pipeline refresh failed", "error", err)
		}
	}()

	go m.pollLoop(interval)
}

// Stop signals the background goroutine to exit. Safe to call multiple times.
func (m *PipelineMonitor) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
}

// Pipelines returns the current pipeline list.
func (m *PipelineMonitor) Pipelines() []bridge.PipelineInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]bridge.PipelineInfo, len(m.pipelines))
	copy(out, m.pipelines)
	return out
}

// Detail returns the full detail for a pipeline. Uses a cached copy if
// available and fresh (within pipelineDetailTTL). Otherwise fetches fresh data.
func (m *PipelineMonitor) Detail(project string, pipelineID int) (*bridge.PipelineDetail, error) {
	// Check cache first under read lock.
	m.mu.RLock()
	if cached, ok := m.details[pipelineID]; ok && time.Since(cached.fetchedAt) < pipelineDetailTTL {
		m.mu.RUnlock()
		return cached.detail, nil
	}
	m.mu.RUnlock()

	// Fetch fresh detail (outside lock).
	detail, err := m.agent.GetPipelineDetail(project, pipelineID)
	if err != nil {
		return nil, fmt.Errorf("fetch pipeline detail %d: %w", pipelineID, err)
	}

	// Cache the result.
	m.mu.Lock()
	m.details[pipelineID] = &cachedPipelineDetail{
		detail:    detail,
		fetchedAt: time.Now(),
	}
	m.mu.Unlock()

	return detail, nil
}

// HasActivePipelines returns true if any pipelines are currently running.
func (m *PipelineMonitor) HasActivePipelines() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pipelines) > 0
}

// Refresh fetches the latest pipeline list from the mcp-gitlab bridge.
func (m *PipelineMonitor) Refresh() error {
	if len(m.projects) == 0 {
		return nil
	}

	pipelines, err := m.agent.ListActivePipelines(m.projects)
	if err != nil {
		m.logger.Warn("pipeline: failed to fetch active pipelines", "error", err)
		return err
	}

	// Build a set of current pipeline IDs for cache pruning.
	currentIDs := make(map[int]struct{}, len(pipelines))
	for _, p := range pipelines {
		currentIDs[p.ID] = struct{}{}
	}

	m.mu.Lock()
	m.pipelines = pipelines

	// Prune cached details for pipelines no longer active.
	for id := range m.details {
		if _, exists := currentIDs[id]; !exists {
			delete(m.details, id)
		}
	}
	m.mu.Unlock()

	// Notify listeners (e.g., SSE hub) with the fresh pipeline list (outside lock).
	if m.onRefresh != nil {
		out := make([]bridge.PipelineInfo, len(pipelines))
		copy(out, pipelines)
		m.onRefresh(out)
	}

	return nil
}

// pollLoop runs Refresh on a ticker until stopCh is closed.
// Uses adaptive polling: 10s when pipelines are active, 60s when idle.
func (m *PipelineMonitor) pollLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	idleInterval := 60 * time.Second
	activeInterval := interval
	consecutiveErrors := 0

	for {
		select {
		case <-m.stopCh:
			m.logger.Debug("pipeline monitor stopped")
			return
		case <-ticker.C:
			if err := m.Refresh(); err != nil {
				consecutiveErrors++
				if consecutiveErrors <= 3 {
					m.logger.Warn("pipeline refresh error", "error", err)
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
					m.logger.Info("pipeline refresh recovered", "after_errors", consecutiveErrors)
				}
				consecutiveErrors = 0

				// Adaptive polling: fast when active, slow when idle.
				if m.HasActivePipelines() {
					ticker.Reset(activeInterval)
				} else {
					ticker.Reset(idleInterval)
				}
			}
		}
	}
}
