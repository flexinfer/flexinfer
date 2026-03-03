package agentcontext

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// TaskReconcilerConfig configures automatic task lifecycle management.
type TaskReconcilerConfig struct {
	Enabled       bool          `json:"enabled"`
	CheckInterval time.Duration `json:"check_interval"`

	// Completed tasks older than this are deleted from Qdrant.
	CompletedRetention time.Duration `json:"completed_retention"`

	// In-progress tasks with no update longer than this are marked blocked/stale.
	StaleTimeout time.Duration `json:"stale_timeout"`

	// Max tasks to process per reconciliation run.
	MaxPerRun int `json:"max_per_run"`
}

// DefaultTaskReconcilerConfig returns sensible defaults.
func DefaultTaskReconcilerConfig() TaskReconcilerConfig {
	return TaskReconcilerConfig{
		Enabled:            true,
		CheckInterval:      5 * time.Minute,
		CompletedRetention: 7 * 24 * time.Hour, // 7 days
		StaleTimeout:       4 * time.Hour,
		MaxPerRun:          500,
	}
}

// ReconcileStats contains statistics from a single reconciliation run.
type ReconcileStats struct {
	StartTime        time.Time     `json:"start_time"`
	Duration         time.Duration `json:"duration"`
	CompletedGCd     int           `json:"completed_gcd"`
	OrphansCleanedUp int           `json:"orphans_cleaned_up"`
	Unblocked        int           `json:"unblocked"`
	MarkedStale      int           `json:"marked_stale"`
	Errors           int           `json:"errors"`
}

// TaskReconciler runs periodic garbage collection and consistency checks on tasks.
type TaskReconciler struct {
	mu sync.RWMutex

	config TaskReconcilerConfig
	ts     *TaskSvc
	logger *slog.Logger

	// Cross-domain callback for orphan detection.
	getSession func(ctx context.Context, sessionID string) (*Session, error)

	running   bool
	stopCh    chan struct{}
	lastRun   time.Time
	runCount  int64
	lastStats ReconcileStats
}

// NewTaskReconciler creates a task reconciler.
func NewTaskReconciler(config TaskReconcilerConfig, ts *TaskSvc, logger *slog.Logger) *TaskReconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &TaskReconciler{
		config: config,
		ts:     ts,
		logger: logger,
		stopCh: make(chan struct{}),
	}
}

// Start begins the reconciliation loop.
func (r *TaskReconciler) Start(ctx context.Context) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.stopCh = make(chan struct{})
	r.mu.Unlock()

	go r.runLoop(ctx)
}

// Stop stops the reconciler.
func (r *TaskReconciler) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return
	}
	close(r.stopCh)
	r.running = false
}

// TriggerReconcile manually triggers a reconciliation run.
func (r *TaskReconciler) TriggerReconcile(ctx context.Context) (*ReconcileStats, error) {
	return r.reconcile(ctx)
}

// LastStats returns stats from the most recent run.
func (r *TaskReconciler) LastStats() ReconcileStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastStats
}

func (r *TaskReconciler) runLoop(ctx context.Context) {
	ticker := time.NewTicker(r.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			stats, err := r.reconcile(ctx)
			if err != nil {
				r.logger.Warn("task reconciliation failed", "error", err)
			} else if stats != nil && (stats.CompletedGCd+stats.OrphansCleanedUp+stats.Unblocked+stats.MarkedStale) > 0 {
				r.logger.Info("task reconciliation completed",
					"gc", stats.CompletedGCd,
					"orphans", stats.OrphansCleanedUp,
					"unblocked", stats.Unblocked,
					"stale", stats.MarkedStale,
					"errors", stats.Errors,
					"duration", stats.Duration,
				)
			}
		}
	}
}

func (r *TaskReconciler) reconcile(ctx context.Context) (*ReconcileStats, error) {
	start := time.Now()
	stats := ReconcileStats{StartTime: start}

	// Scroll all tasks (up to max).
	points, err := r.ts.qdrant.ScrollPoints(ctx, nil, r.config.MaxPerRun, false)
	if err != nil {
		return nil, err
	}

	tasks := make([]Task, 0, len(points))
	for _, p := range points {
		t, err := payloadToTask(p.Payload)
		if err != nil || t == nil {
			continue
		}
		tasks = append(tasks, *t)
	}

	// Build lookup maps for the reconciliation passes.
	taskByID := make(map[string]*Task, len(tasks))
	for i := range tasks {
		taskByID[tasks[i].ID] = &tasks[i]
	}

	var deleteIDs []string

	now := time.Now()

	for i := range tasks {
		t := &tasks[i]

		// ── Pass 1: GC completed tasks past retention ──
		if t.Status == TaskStatusCompleted && t.CompletedAt != nil {
			if now.Sub(*t.CompletedAt) > r.config.CompletedRetention {
				deleteIDs = append(deleteIDs, t.ID)
				stats.CompletedGCd++
				continue
			}
		}

		// ── Pass 2: Orphan detection (session no longer exists) ──
		if t.SessionID != "" && t.Status != TaskStatusCompleted && r.getSession != nil {
			sess, err := r.getSession(ctx, t.SessionID)
			if err != nil || sess == nil {
				// Session gone — cancel orphaned task.
				deleteIDs = append(deleteIDs, t.ID)
				stats.OrphansCleanedUp++
				continue
			}
		}

		// ── Pass 3: Auto-unblock (all blockers completed) ──
		if t.Status == TaskStatusBlocked && len(t.BlockedBy) > 0 {
			allDone := true
			for _, blockerID := range t.BlockedBy {
				blocker, ok := taskByID[blockerID]
				if !ok {
					// Blocker doesn't exist — treat as completed.
					continue
				}
				if blocker.Status != TaskStatusCompleted {
					allDone = false
					break
				}
			}
			if allDone {
				if err := r.unblockTask(ctx, t); err != nil {
					r.logger.Warn("reconcile: failed to unblock task", "task_id", t.ID, "error", err)
					stats.Errors++
				} else {
					stats.Unblocked++
				}
			}
		}

		// ── Pass 4: Stale in_progress detection ──
		if t.Status == TaskStatusInProgress {
			if now.Sub(t.UpdatedAt) > r.config.StaleTimeout {
				if err := r.markStale(ctx, t); err != nil {
					r.logger.Warn("reconcile: failed to mark task stale", "task_id", t.ID, "error", err)
					stats.Errors++
				} else {
					stats.MarkedStale++
				}
			}
		}
	}

	// Batch delete GC'd and orphaned tasks.
	if len(deleteIDs) > 0 {
		if err := r.ts.qdrant.Delete(ctx, deleteIDs); err != nil {
			r.logger.Warn("reconcile: batch delete failed", "count", len(deleteIDs), "error", err)
			stats.Errors++
		}
	}

	stats.Duration = time.Since(start)

	r.mu.Lock()
	r.lastRun = start
	r.runCount++
	r.lastStats = stats
	r.mu.Unlock()

	return &stats, nil
}

// unblockTask sets a blocked task back to pending and clears its blocked_by list.
func (r *TaskReconciler) unblockTask(ctx context.Context, t *Task) error {
	payload := map[string]any{
		"status":     string(TaskStatusPending),
		"blocked_by": []string{},
		"updated_at": time.Now().Format(time.RFC3339Nano),
	}
	return r.ts.qdrant.SetPayload(ctx, []string{t.ID}, payload, true)
}

// markStale sets an in_progress task to blocked with a stale marker.
func (r *TaskReconciler) markStale(ctx context.Context, t *Task) error {
	payload := map[string]any{
		"status":     string(TaskStatusBlocked),
		"resolution": "auto-marked stale: no update for " + r.config.StaleTimeout.String(),
		"updated_at": time.Now().Format(time.RFC3339Nano),
	}
	return r.ts.qdrant.SetPayload(ctx, []string{t.ID}, payload, true)
}
