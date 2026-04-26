package hive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Scheduler is the wall-clock side of the reconciler. It calls Reconciler
// .Tick on an interval (default 60s) and logs each pass. Council triggers
// (cron + roadmap-change + incident) wire in slice 3.7; for slice 2.3 the
// scheduler only drives pipeline pickup.
//
// Design notes:
//   - Tick duration is read from policy on every loop iteration so a
//     hot-reload to policy.scheduler.tick_seconds takes effect on the next
//     tick without restarting the operator.
//   - Idle backoff (slice 6.1) is a future tightening; for now the
//     interval is fixed.
//   - The scheduler is a singleton inside the operator pod. The canonical
//     SQLite store provides the only mutex we need; multi-replica
//     coordination is explicitly out of scope (the deployment is
//     replicas: 1 with strategy: Recreate).
type Scheduler struct {
	Reconciler *Reconciler
	Logger     *slog.Logger

	// Interval is the tick cadence. Zero falls back to defaultInterval.
	Interval time.Duration

	// stopCh is closed by Stop(); Run() returns once observed.
	stopCh chan struct{}
	stopMu sync.Mutex

	// running guards against double-Run.
	running bool
}

const defaultSchedulerInterval = 60 * time.Second

// NewScheduler returns a Scheduler ready to Run.
func NewScheduler(r *Reconciler) *Scheduler {
	return &Scheduler{
		Reconciler: r,
		Logger:     slog.Default(),
		Interval:   defaultSchedulerInterval,
	}
}

// Run drives Tick on a ticker until ctx cancels or Stop is called. It is
// intentionally synchronous so the operator's errgroup can supervise it
// alongside the HTTP listeners. Returns nil on clean shutdown.
func (s *Scheduler) Run(ctx context.Context) error {
	if s == nil || s.Reconciler == nil {
		return errors.New("scheduler: not configured")
	}
	s.stopMu.Lock()
	if s.running {
		s.stopMu.Unlock()
		return errors.New("scheduler: already running")
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.stopMu.Unlock()

	interval := s.Interval
	if interval <= 0 {
		interval = defaultSchedulerInterval
	}
	if s.Logger != nil {
		s.Logger.Info("scheduler starting", "interval", interval)
	}

	// First tick fires immediately so the operator surfaces queued work
	// without waiting a full interval after boot.
	if err := s.tickOnce(ctx); err != nil && s.Logger != nil {
		s.Logger.Warn("scheduler: initial tick failed", "error", err)
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return s.shutdown()
		case <-s.stopCh:
			return s.shutdown()
		case <-t.C:
			if err := s.tickOnce(ctx); err != nil && s.Logger != nil {
				s.Logger.Warn("scheduler: tick failed", "error", err)
			}
		}
	}
}

// tickOnce wraps Reconciler.Tick with a per-tick deadline so a stuck DAO
// call can't block the loop forever.
func (s *Scheduler) tickOnce(ctx context.Context) error {
	tickCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := s.Reconciler.Tick(tickCtx)
	if err != nil {
		return fmt.Errorf("tick: %w", err)
	}
	if s.Logger != nil {
		s.Logger.Debug("scheduler tick",
			"inspected", res.Inspected, "started", res.Started,
			"deferred", res.Deferred, "skipped", res.Skipped, "errored", res.Errored,
			"skip_reason", res.SkipReason,
		)
	}
	return nil
}

// Stop signals Run to exit at the next tick boundary. Safe to call before
// Run starts (no-op) and concurrently from any goroutine.
func (s *Scheduler) Stop() {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	if s.stopCh != nil {
		select {
		case <-s.stopCh:
			// Already closed.
		default:
			close(s.stopCh)
		}
	}
}

func (s *Scheduler) shutdown() error {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	s.running = false
	if s.Logger != nil {
		s.Logger.Info("scheduler stopped")
	}
	return nil
}
