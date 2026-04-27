package eval

import (
	"context"
	"log/slog"
	"time"
)

// CrossRunScheduler is the wall-clock side of Loop C. It wakes once per
// hour, checks if the current minute is the configured Sunday slot, and
// fires CrossRunChecker.Run when it is. Cheap polling (3600s ticks)
// avoids the dependency a real cron library would pull in, and the
// hourly cadence is plenty for a once-a-week job.
//
// Tick alignment: the first tick fires immediately (with a "would have
// run?" check) so a pod restart on Sunday morning doesn't skip the
// week. After that the ticker is a plain time.Ticker — drift over a
// 60-minute interval is negligible.
type CrossRunScheduler struct {
	Checker *CrossRunChecker
	Logger  *slog.Logger

	// Weekday + Hour control when Loop C fires. Defaults: Sunday at
	// 06:00 UTC. The check fires once per (weekday, hour) combination
	// — repeated polls inside the same hour de-duplicate via lastFired.
	Weekday time.Weekday
	Hour    int

	// Now is injectable for tests so we can drive the ticker forward
	// without sleeping. Defaults to time.Now.
	Now func() time.Time

	// lastFired records the start of the (weekday, hour) window that
	// most recently triggered a run. Resets across pod restarts —
	// acceptable because the next eligible window is at most 7 days out.
	lastFired time.Time
}

// NewCrossRunScheduler returns a scheduler with Sunday-06:00-UTC defaults.
func NewCrossRunScheduler(checker *CrossRunChecker) *CrossRunScheduler {
	return &CrossRunScheduler{
		Checker: checker,
		Weekday: time.Sunday,
		Hour:    6,
	}
}

// Run drives the hourly check until ctx is cancelled. Returns nil on
// clean shutdown so it composes with errgroup.WithContext alongside the
// reconciler scheduler in the operator.
func (s *CrossRunScheduler) Run(ctx context.Context) error {
	if s == nil || s.Checker == nil {
		// Loop C disabled — just wait for shutdown so errgroup stays balanced.
		<-ctx.Done()
		return nil
	}
	if s.Logger != nil {
		s.Logger.Info("cross-run scheduler starting",
			"weekday", s.Weekday.String(), "hour_utc", s.Hour)
	}
	// Initial check so a Sunday-morning pod restart still fires the loop.
	s.maybeFire(ctx)

	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.maybeFire(ctx)
		}
	}
}

// maybeFire runs the checker iff now matches the configured (weekday,
// hour) and we haven't already fired in this window.
func (s *CrossRunScheduler) maybeFire(ctx context.Context) {
	now := s.now().UTC()
	if now.Weekday() != s.Weekday || now.Hour() != s.Hour {
		return
	}
	// De-dup: the (weekday, hour) start defines a unique 1h window per week.
	windowStart := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, 0, 0, 0, time.UTC)
	if !s.lastFired.Before(windowStart) {
		return
	}
	s.lastFired = windowStart
	if _, err := s.Checker.Run(ctx); err != nil && s.Logger != nil {
		s.Logger.Warn("cross-run check failed", "error", err)
	}
}

func (s *CrossRunScheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
