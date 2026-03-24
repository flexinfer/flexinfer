package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	loomcache "github.com/crb2nu/loom/internal/cache"
	"github.com/crb2nu/loom/internal/hud/bridge"
)

// cachedPipelineDetail wraps a PipelineDetail with an expiration timestamp.
type cachedPipelineDetail struct {
	detail    *bridge.PipelineDetail
	fetchedAt time.Time
}

// pipelineDetailTTL is how long a cached pipeline detail is considered fresh.
const pipelineDetailTTL = 10 * time.Second

const (
	pipelineActiveCacheKey = "pipelines:active"
	pipelineRecentCacheKey = "pipelines:recent"
	pipelineDetailCacheTTL = 60 * time.Second
	pipelineActiveCacheTTL = 30 * time.Second
	pipelineRecentCacheTTL = 2 * time.Minute
)

// PipelineSummary summarizes the current pipeline state for mobile clients.
type PipelineSummary struct {
	Running      int    `json:"running"`
	Passed       int    `json:"passed"`
	Failed       int    `json:"failed"`
	Pending      int    `json:"pending"`
	LastActivity string `json:"last_activity,omitempty"`
}

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
	cache    loomcache.Store
	recent   []bridge.PipelineInfo
}

// NewPipelineMonitor creates a PipelineMonitor that watches the given GitLab projects.
func NewPipelineMonitor(agent *bridge.AgentBridge, projects []string, cache loomcache.Store, logger *slog.Logger) *PipelineMonitor {
	m := &PipelineMonitor{
		agent:    agent,
		projects: projects,
		details:  make(map[int]*cachedPipelineDetail),
		cache:    cache,
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
		if err := m.RefreshRecent(); err != nil {
			m.Logger.Warn("initial pipeline recent refresh failed", "error", err)
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

// RecentPipelines returns the most recently cached pipeline history.
func (m *PipelineMonitor) RecentPipelines() []bridge.PipelineInfo {
	if recent, ok := m.cachedPipelines(pipelineRecentCacheKey); ok {
		return recent
	}

	m.RLock()
	defer m.RUnlock()
	out := make([]bridge.PipelineInfo, len(m.recent))
	copy(out, m.recent)
	return out
}

// Summary returns a summary of pipeline activity across active and recent lists.
func (m *PipelineMonitor) Summary() PipelineSummary {
	active := m.Pipelines()
	recent := m.RecentPipelines()

	combined := make([]bridge.PipelineInfo, 0, len(active)+len(recent))
	seen := make(map[int]struct{}, len(active)+len(recent))
	for _, pipeline := range active {
		if _, ok := seen[pipeline.ID]; ok {
			continue
		}
		seen[pipeline.ID] = struct{}{}
		combined = append(combined, pipeline)
	}
	for _, pipeline := range recent {
		if _, ok := seen[pipeline.ID]; ok {
			continue
		}
		seen[pipeline.ID] = struct{}{}
		combined = append(combined, pipeline)
	}

	summary := PipelineSummary{}
	var newest time.Time
	for _, pipeline := range combined {
		switch normalizePipelineStatus(pipeline.Status) {
		case "running":
			summary.Running++
		case "success":
			summary.Passed++
		case "pending":
			summary.Pending++
		default:
			summary.Failed++
		}

		if ts := parsePipelineTimestamp(pipeline.CreatedAt); ts.After(newest) {
			newest = ts
		}
		if ts := parsePipelineTimestamp(pipeline.UpdatedAt); ts.After(newest) {
			newest = ts
		}
	}
	if !newest.IsZero() {
		summary.LastActivity = relativePipelineTime(newest)
	}
	return summary
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
	if cached, ok := m.cachedDetailFromCache(project, pipelineID); ok {
		return cached, nil
	}

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
	m.cachePipelineDetail(project, pipelineID, detail)

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
	m.update(pipelines)
	return nil
}

// RefreshRecent fetches the latest historical pipeline list and stores it.
func (m *PipelineMonitor) RefreshRecent() error {
	recent, err := m.refreshRecent(context.Background())
	if err != nil {
		return err
	}
	m.updateRecent(recent)
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

func (m *PipelineMonitor) refreshRecent(_ context.Context) ([]bridge.PipelineInfo, error) {
	if len(m.projects) == 0 {
		return nil, nil
	}

	pipelines, err := m.agent.ListRecentPipelines(m.projects, 10)
	if err != nil {
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

	if m.cache != nil {
		m.cache.Set(pipelineActiveCacheKey, pipelines, pipelineActiveCacheTTL)
	}

	// Notify listeners (e.g., SSE hub) with the fresh pipeline list (outside lock).
	out := make([]bridge.PipelineInfo, len(pipelines))
	copy(out, pipelines)
	m.FireOnRefresh(out)
}

func (m *PipelineMonitor) updateRecent(pipelines []bridge.PipelineInfo) {
	m.Lock()
	m.recent = make([]bridge.PipelineInfo, len(pipelines))
	copy(m.recent, pipelines)
	m.Unlock()

	if m.cache != nil {
		m.cache.Set(pipelineRecentCacheKey, pipelines, pipelineRecentCacheTTL)
	}
}

// pollLoop runs Refresh on a ticker until stopCh is closed.
// Uses adaptive polling: 10s when pipelines are active, 60s when idle.
func (m *PipelineMonitor) pollLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	idleInterval := 60 * time.Second
	activeInterval := interval
	consecutiveErrors := 0
	tickCount := 0

	for {
		select {
		case <-m.StopCh():
			m.Logger.Debug("pipeline monitor stopped")
			return
		case <-ticker.C:
			tickCount++
			if err := m.Refresh(); err != nil {
				consecutiveErrors++
				if consecutiveErrors <= 3 {
					m.Logger.Warn("pipeline refresh error", "error", err)
				}
				if tickCount%6 == 0 {
					if recentErr := m.RefreshRecent(); recentErr != nil {
						m.Logger.Warn("pipeline recent refresh error", "error", recentErr)
					}
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

				if tickCount%6 == 0 {
					if err := m.RefreshRecent(); err != nil {
						m.Logger.Warn("pipeline recent refresh error", "error", err)
					}
				}

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

func (m *PipelineMonitor) cachedPipelines(key string) ([]bridge.PipelineInfo, bool) {
	if m.cache == nil {
		return nil, false
	}
	raw, ok := m.cache.Get(key)
	if !ok || raw == nil {
		return nil, false
	}
	var pipelines []bridge.PipelineInfo
	if !decodeCacheValue(raw, &pipelines) {
		return nil, false
	}
	return pipelines, true
}

func (m *PipelineMonitor) cachedDetailFromCache(project string, pipelineID int) (*bridge.PipelineDetail, bool) {
	if m.cache == nil {
		return nil, false
	}
	key := pipelineDetailCacheKey(project, pipelineID)
	raw, ok := m.cache.Get(key)
	if !ok || raw == nil {
		return nil, false
	}
	var detail bridge.PipelineDetail
	if !decodeCacheValue(raw, &detail) {
		return nil, false
	}
	return &detail, true
}

func (m *PipelineMonitor) cachePipelineDetail(project string, pipelineID int, detail *bridge.PipelineDetail) {
	if m.cache == nil || detail == nil {
		return
	}
	m.cache.Set(pipelineDetailCacheKey(project, pipelineID), detail, pipelineDetailCacheTTL)
}

func pipelineDetailCacheKey(project string, pipelineID int) string {
	safeProject := strings.NewReplacer("/", "_", ":", "_").Replace(project)
	return fmt.Sprintf("pipelines:details:%s:%d", safeProject, pipelineID)
}

func decodeCacheValue(raw any, out any) bool {
	data, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, out) == nil
}

func normalizePipelineStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "running"
	case "success", "passed":
		return "success"
	case "pending", "created", "scheduled", "manual":
		return "pending"
	case "failed", "canceled", "cancelled", "skipped":
		return "failed"
	default:
		return "failed"
	}
}

func parsePipelineTimestamp(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts
	}
	return time.Time{}
}

func relativePipelineTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	diff := time.Since(ts)
	if diff < 0 {
		diff = 0
	}
	switch {
	case diff < 5*time.Second:
		return "just now"
	case diff < time.Minute:
		return fmt.Sprintf("%ds ago", int(diff.Seconds()))
	case diff < time.Hour:
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(diff.Hours()/24))
	}
}
