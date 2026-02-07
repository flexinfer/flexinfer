package monitor

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// cachedDetail wraps a WorkflowDetail with an expiration timestamp for
// staleness checks.
type cachedDetail struct {
	detail    *bridge.WorkflowDetail
	fetchedAt time.Time
}

// detailTTL is how long a cached workflow detail is considered fresh.
const detailTTL = 10 * time.Second

// WorkflowMonitor tracks active workflows and caches their details.
// It polls the workflow list at a configurable interval and lazily
// fetches individual workflow details on demand.
type WorkflowMonitor struct {
	agent  *bridge.AgentBridge
	logger *slog.Logger

	mu        sync.RWMutex
	workflows []bridge.WorkflowInfo
	details   map[string]*cachedDetail // workflow ID -> cached detail

	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewWorkflowMonitor creates a WorkflowMonitor backed by the given agent bridge.
func NewWorkflowMonitor(agent *bridge.AgentBridge, logger *slog.Logger) *WorkflowMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &WorkflowMonitor{
		agent:   agent,
		logger:  logger.With("component", "workflow-monitor"),
		details: make(map[string]*cachedDetail),
		stopCh:  make(chan struct{}),
	}
}

// Start begins the background polling goroutine at the given interval.
func (m *WorkflowMonitor) Start(interval time.Duration) {
	if err := m.Refresh(); err != nil {
		m.logger.Warn("initial workflow refresh failed", "error", err)
	}

	go m.pollLoop(interval)
}

// Stop signals the background goroutine to exit. It is safe to call multiple times.
func (m *WorkflowMonitor) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
}

// Workflows returns the current workflow list.
func (m *WorkflowMonitor) Workflows() []bridge.WorkflowInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]bridge.WorkflowInfo, len(m.workflows))
	copy(out, m.workflows)
	return out
}

// Detail returns the full detail for a workflow. It uses a cached copy if
// available and fresh (within detailTTL). Otherwise it fetches from the
// agent bridge.
func (m *WorkflowMonitor) Detail(id string) (*bridge.WorkflowDetail, error) {
	// Check cache first under read lock.
	m.mu.RLock()
	if cached, ok := m.details[id]; ok && time.Since(cached.fetchedAt) < detailTTL {
		m.mu.RUnlock()
		return cached.detail, nil
	}
	m.mu.RUnlock()

	// Fetch fresh detail (outside lock to avoid blocking readers).
	detail, err := m.agent.WorkflowStatus(id)
	if err != nil {
		return nil, fmt.Errorf("fetch workflow detail %s: %w", id, err)
	}

	// Cache the result.
	m.mu.Lock()
	m.details[id] = &cachedDetail{
		detail:    detail,
		fetchedAt: time.Now(),
	}
	m.mu.Unlock()

	return detail, nil
}

// ApproveStep approves a pending step in a workflow and invalidates the
// cached detail for that workflow.
func (m *WorkflowMonitor) ApproveStep(workflowID, stepID string) error {
	if err := m.agent.ApproveStep(workflowID, stepID); err != nil {
		return fmt.Errorf("approve step %s/%s: %w", workflowID, stepID, err)
	}
	m.invalidateDetail(workflowID)
	return nil
}

// RejectStep rejects a pending step in a workflow and invalidates the
// cached detail for that workflow.
func (m *WorkflowMonitor) RejectStep(workflowID, stepID string) error {
	if err := m.agent.RejectStep(workflowID, stepID); err != nil {
		return fmt.Errorf("reject step %s/%s: %w", workflowID, stepID, err)
	}
	m.invalidateDetail(workflowID)
	return nil
}

// PendingApprovals returns the count of workflows with status "waiting_approval".
func (m *WorkflowMonitor) PendingApprovals() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, w := range m.workflows {
		if w.Status == "waiting_approval" {
			count++
		}
	}
	return count
}

// Refresh fetches the latest workflow list from the agent bridge.
// It also prunes cached details for workflows that no longer exist.
func (m *WorkflowMonitor) Refresh() error {
	workflows, err := m.agent.WorkflowList()
	if err != nil {
		m.logger.Warn("workflow: failed to fetch workflow list", "error", err)
		return err
	}

	// Build a set of current workflow IDs for pruning.
	currentIDs := make(map[string]struct{}, len(workflows))
	for _, w := range workflows {
		currentIDs[w.ID] = struct{}{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.workflows = workflows

	// Prune cached details for workflows that are no longer in the list.
	for id := range m.details {
		if _, exists := currentIDs[id]; !exists {
			delete(m.details, id)
		}
	}

	return nil
}

// invalidateDetail removes the cached detail for the given workflow ID
// so the next Detail() call will fetch fresh data.
func (m *WorkflowMonitor) invalidateDetail(workflowID string) {
	m.mu.Lock()
	delete(m.details, workflowID)
	m.mu.Unlock()
}

// pollLoop runs Refresh on a ticker until stopCh is closed.
func (m *WorkflowMonitor) pollLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			m.logger.Debug("workflow monitor stopped")
			return
		case <-ticker.C:
			if err := m.Refresh(); err != nil {
				m.logger.Warn("workflow refresh error", "error", err)
			}
		}
	}
}
