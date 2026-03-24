package monitor

import (
	"context"
	"fmt"
	"log/slog"
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
//
// PipelineMonitor keeps its own adaptive pollLoop (fast when active, slow when
// idle) but delegates snapshot storage, stop lifecycle, and OnRefresh to
// BaseMonitor.
type PipelineMonitor struct {
	BaseMonitor[[]bridge.PipelineInfo]
	agent    *bridge.AgentBridge
	projects []string                      // GitLab project paths to monitor.
	details  map[int]*cachedPipelineDetail // pipeline ID -> cached detail
}

// NewPipelineMonitor creates a PipelineMonitor that watches the given GitLab projects.
func NewPipelineMonitor(agent *bridge.AgentBridge, projects []string, logger *slog.Logger) *PipelineMonitor {
	m := &PipelineMonitor{
		agent:    agent,
		projects: projects,
		details:  make(map[int]*cachedPipelineDetail),
	}
	m.InitBase(logger, nil, "pipeline-monitor")
	return m
}

// Ready reports whether the monitor has been fully initialized.
func (m *PipelineMonitor) Ready() bool {
	return m != nil && m.stopCh != nil && m.Logger != nil
}

// Start begins the background polling goroutine at the given interval.
func (m *PipelineMonitor) Start(interval time.Duration) {
	m.StartManual()
	// Run initial refresh asynchronously so HUD startup is non-blocking.
	go func() {
		if err := m.Refresh(); err != nil {
			m.Logger.Warn("initial pipeline refresh failed", "error", err)
		}
	}()
	go m.pollLoop(interval)
}

// Pipelines returns the current pipeline list.
func (m *PipelineMonitor) Pipelines() []bridge.PipelineInfo {
	m.RLock()
	defer m.RUnlock()

	snap := m.GetSnapshot()
	out := make([]bridge.PipelineInfo, len(snap))
	copy(out, snap)
	return out
}

// Projects returns the configured GitLab projects watched by this monitor.
func (m *PipelineMonitor) Projects() []string {
	m.RLock()
	defer m.RUnlock()

	out := make([]string, len(m.projects))
	copy(out, m.projects)
	return out
}

// Detail returns the full detail for a pipeline. Uses a cached copy if
// available and fresh (within pipelineDetailTTL). Otherwise fetches fresh data.
func (m *PipelineMonitor) Detail(project string, pipelineID int) (*bridge.PipelineDetail, error) {
	// Check cache first under read lock.
	m.RLock()
	if cached, ok := m.details[pipelineID]; ok && time.Since(cached.fetchedAt) < pipelineDetailTTL {
		m.RUnlock()
		return cached.detail, nil
	}
	m.RUnlock()

	// Fetch fresh detail (outside lock).
	detail, err := m.agent.GetPipelineDetail(project, pipelineID)
	if err != nil {
		return nil, fmt.Errorf("fetch pipeline detail %d: %w", pipelineID, err)
	}

	// Cache the result.
	m.Lock()
	m.details[pipelineID] = &cachedPipelineDetail{
		detail:    detail,
		fetchedAt: time.Now(),
	}
	m.Unlock()

	return detail, nil
}

// HasActivePipelines returns true if any pipelines are currently running.
func (m *PipelineMonitor) HasActivePipelines() bool {
	m.RLock()
	defer m.RUnlock()
	return len(m.GetSnapshot()) > 0
}

// Refresh fetches the latest pipeline list from the mcp-gitlab bridge.
func (m *PipelineMonitor) Refresh() error {
	prev := m.Pipelines()

	pipelines, err := m.refresh(context.Background())
	if err != nil {
		return err
	}
	if len(pipelines) == 0 && len(m.projects) > 0 {
		time.Sleep(500 * time.Millisecond)
		if retry, retryErr := m.refresh(context.Background()); retryErr != nil {
			m.Logger.Warn("embedded pipeline refresh retry failed", "error", retryErr)
		} else if len(retry) > 0 {
			pipelines = retry
		}
	}
	if len(pipelines) == 0 && len(prev) > 0 {
		m.Logger.Info("pipeline refresh returned empty; preserving previous snapshot", "pipelines", len(prev))
		pipelines = prev
	}
	m.update(pipelines)
	return nil
}

// refresh fetches the pipeline list from the bridge.
func (m *PipelineMonitor) refresh(_ context.Context) ([]bridge.PipelineInfo, error) {
	if len(m.projects) == 0 {
		return nil, nil
	}

	pipelines, err := m.agent.ListActivePipelines(m.projects)
	if err != nil {
		m.Logger.Warn("pipeline: failed to fetch active pipelines", "error", err)
		return nil, err
	}
	return pipelines, nil
}

// update stores the pipeline list and prunes stale detail cache entries.
func (m *PipelineMonitor) update(pipelines []bridge.PipelineInfo) {
	// Build a set of current pipeline IDs for cache pruning.
	currentIDs := make(map[int]struct{}, len(pipelines))
	for _, p := range pipelines {
		currentIDs[p.ID] = struct{}{}
	}

	m.Lock()
	m.SetSnapshot(pipelines)

	// Prune cached details for pipelines no longer active.
	for id := range m.details {
		if _, exists := currentIDs[id]; !exists {
			delete(m.details, id)
		}
	}
	m.Unlock()

	// Notify listeners (e.g., SSE hub) with the fresh pipeline list (outside lock).
	out := make([]bridge.PipelineInfo, len(pipelines))
	copy(out, pipelines)
	m.FireOnRefresh(out)
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
		case <-m.StopCh():
			m.Logger.Debug("pipeline monitor stopped")
			return
		case <-ticker.C:
			if err := m.Refresh(); err != nil {
				consecutiveErrors++
				if consecutiveErrors <= 3 {
					m.Logger.Warn("pipeline refresh error", "error", err)
				}
				skipTicks := min(consecutiveErrors-1, 4)
				for range skipTicks {
					select {
					case <-m.StopCh():
						return
					case <-ticker.C:
					}
				}
			} else {
				if consecutiveErrors > 0 {
					m.Logger.Info("pipeline refresh recovered", "after_errors", consecutiveErrors)
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
