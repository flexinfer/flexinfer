package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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
	BaseMonitor[[]bridge.WorkflowInfo]
	agent   *bridge.AgentBridge
	details map[string]*cachedDetail // workflow ID -> cached detail

	// Notification dedup: tracks workflow+step combos already notified.
	notifiedApprovals map[string]bool // "workflowID:stepName" -> true
}

// NewWorkflowMonitor creates a WorkflowMonitor backed by the given agent bridge.
func NewWorkflowMonitor(agent *bridge.AgentBridge, logger *slog.Logger) *WorkflowMonitor {
	m := &WorkflowMonitor{
		agent:             agent,
		details:           make(map[string]*cachedDetail),
		notifiedApprovals: make(map[string]bool),
	}
	m.InitBase(logger, nil, "workflow-monitor")
	return m
}

// Start begins the background polling goroutine at the given interval.
func (m *WorkflowMonitor) Start(interval time.Duration) {
	m.BaseMonitor.Start(interval, m.refresh)
}

// Workflows returns the current workflow list.
func (m *WorkflowMonitor) Workflows() []bridge.WorkflowInfo {
	m.RLock()
	defer m.RUnlock()

	snap := m.GetSnapshot()
	out := make([]bridge.WorkflowInfo, len(snap))
	copy(out, snap)
	return out
}

// Detail returns the full detail for a workflow. It uses a cached copy if
// available and fresh (within detailTTL). Otherwise it fetches from the
// agent bridge.
func (m *WorkflowMonitor) Detail(id string) (*bridge.WorkflowDetail, error) {
	// Check cache first under read lock.
	m.RLock()
	if cached, ok := m.details[id]; ok && time.Since(cached.fetchedAt) < detailTTL {
		m.RUnlock()
		return cached.detail, nil
	}
	m.RUnlock()

	// Fetch fresh detail (outside lock to avoid blocking readers).
	detail, err := m.agent.WorkflowStatus(id)
	if err != nil {
		return nil, fmt.Errorf("fetch workflow detail %s: %w", id, err)
	}
	events, err := m.agent.WorkflowEvents(id)
	if err != nil {
		m.Logger.Debug("workflow: failed to fetch workflow events", "workflow_id", id, "error", err)
	} else {
		detail.Events = events
	}

	// Cache the result.
	m.Lock()
	m.details[id] = &cachedDetail{
		detail:    detail,
		fetchedAt: time.Now(),
	}
	m.Unlock()

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
	m.RLock()
	defer m.RUnlock()

	count := 0
	for _, w := range m.GetSnapshot() {
		if w.Status == "waiting_approval" {
			count++
		}
	}
	return count
}

// refresh fetches the latest workflow list from the agent bridge.
func (m *WorkflowMonitor) refresh(_ context.Context) ([]bridge.WorkflowInfo, error) {
	workflows, err := m.agent.WorkflowList()
	if err != nil {
		m.Logger.Warn("workflow: failed to fetch workflow list", "error", err)
		return nil, err
	}
	return workflows, nil
}

// Update overrides BaseMonitor.Update to handle detail cache pruning
// and notification-dedup bookkeeping.
func (m *WorkflowMonitor) Update(workflows []bridge.WorkflowInfo) {
	// Build a set of current workflow IDs for pruning.
	currentIDs := make(map[string]struct{}, len(workflows))
	for _, w := range workflows {
		currentIDs[w.ID] = struct{}{}
	}

	m.Lock()
	m.SetSnapshot(workflows)

	// Prune cached details for workflows that are no longer in the list.
	for id := range m.details {
		if _, exists := currentIDs[id]; !exists {
			delete(m.details, id)
		}
	}

	// Track waiting-approval workflow keys so the HUD UI and future consumers can
	// still diff approval state without emitting noisy desktop notifications.
	for _, w := range workflows {
		if w.Status == "waiting_approval" {
			key := w.ID + ":" + w.CurrentStep
			m.notifiedApprovals[key] = true
		}
	}

	// Prune approval-tracking entries for workflows no longer waiting.
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
	m.Unlock()

	// Notify listeners (e.g., SSE hub) with the fresh workflow list (outside lock).
	out := make([]bridge.WorkflowInfo, len(workflows))
	copy(out, workflows)
	m.FireOnRefresh(out)
}

// Refresh forces an immediate refresh. Exposed for external callers.
func (m *WorkflowMonitor) Refresh() error {
	workflows, err := m.refresh(context.Background())
	if err != nil {
		return err
	}
	m.Update(workflows)
	return nil
}

// invalidateDetail removes the cached detail for the given workflow ID
// so the next Detail() call will fetch fresh data.
func (m *WorkflowMonitor) invalidateDetail(workflowID string) {
	m.Lock()
	delete(m.details, workflowID)
	m.Unlock()
}
