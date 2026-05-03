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
// (cron + roadmap-change + incident) wire in slice 3.7.
//
// Phase 7 introduces sibling schedulers that the operator's errgroup
// supervises alongside this one — pkg/hive/eval/cross_run_scheduler.go
// (Loop C, Sunday 06:00 UTC) and pkg/hive/adaptive/scheduler.go (Phase 7
// slice 7.1, Sunday 05:00 UTC). Both follow an hourly-poll + de-duplicate
// pattern instead of a real cron library; see those files for the
// production wiring contract.
//
// Design notes:
//   - Tick duration is read from policy on every loop iteration so a
//     hot-reload to policy.scheduler.tick_seconds takes effect on the next
//     tick without restarting the operator.
//   - Idle-aware throttling (slice 6.1): after IdleAfter consecutive
//     no-op ticks the scheduler backs off to IdleInterval (default 5min)
//     so the operator stops thrashing the DB + cluster GPUs when the
//     queue is empty. The first non-no-op tick snaps the cadence back to
//     Interval. Active gauges and policy hot-reload still fire on the
//     slow cadence.
//   - The scheduler is a singleton inside the operator pod. The canonical
//     SQLite store provides the only mutex we need; multi-replica
//     coordination is explicitly out of scope (the deployment is
//     replicas: 1 with strategy: Recreate).
type Scheduler struct {
	Reconciler *Reconciler
	Logger     *slog.Logger

	// Interval is the tick cadence when there's queue activity. Zero
	// falls back to defaultSchedulerInterval (60s).
	Interval time.Duration

	// IdleInterval is the slow cadence used after IdleAfter elapses with
	// only no-op ticks. Zero falls back to defaultIdleInterval (5min).
	IdleInterval time.Duration

	// IdleAfter is how long the scheduler waits — measured from the start
	// of the current no-op streak — before switching to IdleInterval.
	// Zero falls back to defaultIdleAfter (5min). A negative value
	// disables the throttle entirely (cadence is always Interval).
	IdleAfter time.Duration

	// Clock is used by tests to drive cadence transitions deterministically.
	// Defaults to time.Now.
	Clock func() time.Time

	// stopCh is closed by Stop(); Run() returns once observed.
	stopCh chan struct{}
	stopMu sync.Mutex

	// running guards against double-Run.
	running bool
}

const (
	defaultSchedulerInterval = 60 * time.Second
	defaultIdleInterval      = 5 * time.Minute
	defaultIdleAfter         = 5 * time.Minute
)

// NewScheduler returns a Scheduler ready to Run.
func NewScheduler(r *Reconciler) *Scheduler {
	return &Scheduler{
		Reconciler:   r,
		Logger:       slog.Default(),
		Interval:     defaultSchedulerInterval,
		IdleInterval: defaultIdleInterval,
		IdleAfter:    defaultIdleAfter,
		Clock:        time.Now,
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
	idleInterval := s.IdleInterval
	if idleInterval <= 0 {
		idleInterval = defaultIdleInterval
	}
	// IdleAfter convention: negative disables the throttle, zero uses
	// the default, positive uses the given value.
	idleAfter := s.IdleAfter
	switch {
	case idleAfter < 0:
		idleAfter = 0 // sentinel: throttle disabled
	case idleAfter == 0:
		idleAfter = defaultIdleAfter
	}
	if s.Logger != nil {
		s.Logger.Info("scheduler starting",
			"interval", interval,
			"idle_interval", idleInterval,
			"idle_after", idleAfter,
		)
	}

	// First tick fires immediately so the operator surfaces queued work
	// without waiting a full interval after boot. The idle-streak clock
	// only starts after the first tick observes a no-op, so a healthy
	// boot doesn't trip the throttle.
	idleSince := time.Time{}
	if res, err := s.tickOnce(ctx); err != nil && s.Logger != nil {
		s.Logger.Warn("scheduler: initial tick failed", "error", err)
	} else if res.IsNoOp() {
		idleSince = s.now()
	}

	curInterval := interval
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return s.shutdown()
		case <-s.stopCh:
			return s.shutdown()
		case <-t.C:
			res, err := s.tickOnce(ctx)
			if err != nil && s.Logger != nil {
				s.Logger.Warn("scheduler: tick failed", "error", err)
			}
			next, nextIdleSince := s.nextInterval(res, idleSince, interval, idleInterval, idleAfter)
			idleSince = nextIdleSince
			if next != curInterval {
				t.Reset(next)
				if s.Logger != nil {
					s.Logger.Info("scheduler cadence changed",
						"from", curInterval, "to", next,
						"reason", cadenceReason(next, interval),
					)
				}
				curInterval = next
			}
		}
	}
}

// nextInterval picks the cadence for the next tick. The fast Interval
// applies whenever there was activity in the last tick OR the idle
// streak hasn't reached IdleAfter; otherwise the slow IdleInterval kicks
// in. idleSince tracks the wall-clock start of the current no-op streak;
// it resets to zero on any non-no-op tick.
//
// Pulled out of Run so tests can drive transitions deterministically.
func (s *Scheduler) nextInterval(
	res TickResult,
	idleSince time.Time,
	interval, idleInterval, idleAfter time.Duration,
) (time.Duration, time.Time) {
	if !res.IsNoOp() {
		// Real work — snap back to the fast cadence and reset the streak.
		return interval, time.Time{}
	}
	if idleAfter <= 0 {
		// Throttle disabled.
		return interval, time.Time{}
	}
	if idleSince.IsZero() {
		idleSince = s.now()
	}
	if s.now().Sub(idleSince) >= idleAfter {
		return idleInterval, idleSince
	}
	return interval, idleSince
}

func cadenceReason(next, fast time.Duration) string {
	if next == fast {
		return "active"
	}
	return "idle"
}

// tickOnce wraps Reconciler.Tick with a per-tick deadline so a stuck DAO
// call can't block the loop forever. Returns the TickResult so the
// caller (Run) can drive idle-aware throttling.
func (s *Scheduler) tickOnce(ctx context.Context) (TickResult, error) {
	tickCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := s.Reconciler.Tick(tickCtx)
	if err != nil {
		return res, fmt.Errorf("tick: %w", err)
	}
	if s.Logger != nil {
		s.Logger.Debug("scheduler tick",
			"inspected", res.Inspected, "started", res.Started,
			"deferred", res.Deferred, "skipped", res.Skipped, "errored", res.Errored,
			"skip_reason", res.SkipReason,
		)
	}
	return res, nil
}

func (s *Scheduler) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
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
