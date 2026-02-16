package monitor

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/notify"
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

	// Notification dedup: tracks workflow+step combos already notified.
	notifiedApprovals map[string]bool // "workflowID:stepName" -> true

	onRefresh func([]bridge.WorkflowInfo)

	stopCh   chan struct{}
	stopOnce sync.Once
}

// OnRefresh registers a callback that fires after each successful refresh
// with the new workflow list. Used to broadcast data via SSE.
func (m *WorkflowMonitor) OnRefresh(fn func([]bridge.WorkflowInfo)) {
	m.onRefresh = fn
}

// NewWorkflowMonitor creates a WorkflowMonitor backed by the given agent bridge.
func NewWorkflowMonitor(agent *bridge.AgentBridge, logger *slog.Logger) *WorkflowMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &WorkflowMonitor{
		agent:             agent,
		logger:            logger.With("component", "workflow-monitor"),
		details:           make(map[string]*cachedDetail),
		notifiedApprovals: make(map[string]bool),
		stopCh:            make(chan struct{}),
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
	events, err := m.agent.WorkflowEvents(id)
	if err != nil {
		m.logger.Debug("workflow: failed to fetch workflow events", "workflow_id", id, "error", err)
	} else {
		detail.Events = events
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

// CancelWorkflow cancels a workflow and invalidates the cached detail.
func (m *WorkflowMonitor) CancelWorkflow(workflowID string) error {
	if err := m.agent.CancelWorkflow(workflowID); err != nil {
		return fmt.Errorf("cancel workflow %s: %w", workflowID, err)
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
	m.workflows = workflows

	// Prune cached details for workflows that are no longer in the list.
	for id := range m.details {
		if _, exists := currentIDs[id]; !exists {
			delete(m.details, id)
		}
	}

	// Notify for new pending approvals (deduped by workflow+step).
	for _, w := range workflows {
		if w.Status == "waiting_approval" {
			key := w.ID + ":" + w.CurrentStep
			if !m.notifiedApprovals[key] {
				m.notifiedApprovals[key] = true
				go func(name, step string) {
					if err := notify.NotifyWorkflowApproval(name, step); err != nil {
						m.logger.Debug("workflow-approval notification failed", "workflow", name, "error", err)
					}
				}(w.Name, w.CurrentStep)
			}
		}
	}

	// Prune approval dedup entries for workflows no longer waiting.
	for key := range m.notifiedApprovals {
		wID := key[:strings.Index(key, ":")]
		found := false
		for _, w := range workflows {
			if w.ID == wID && w.Status == "waiting_approval" {
				found = true
				break
			}
		}
		if !found {
			delete(m.notifiedApprovals, key)
		}
	}
	m.mu.Unlock()

	// Notify listeners (e.g., SSE hub) with the fresh workflow list (outside lock).
	if m.onRefresh != nil {
		out := make([]bridge.WorkflowInfo, len(workflows))
		copy(out, workflows)
		m.onRefresh(out)
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
// On consecutive errors, it backs off by skipping ticker ticks.
func (m *WorkflowMonitor) pollLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	consecutiveErrors := 0
	for {
		select {
		case <-m.stopCh:
			m.logger.Debug("workflow monitor stopped")
			return
		case <-ticker.C:
			if err := m.Refresh(); err != nil {
				consecutiveErrors++
				if consecutiveErrors <= 3 {
					m.logger.Warn("workflow refresh error", "error", err)
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
					m.logger.Info("workflow refresh recovered", "after_errors", consecutiveErrors)
				}
				consecutiveErrors = 0
			}
		}
	}
}
