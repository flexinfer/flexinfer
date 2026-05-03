package adaptive

import (
	"context"
	"log/slog"
	"time"
)

// ProposalsScheduler is the wall-clock side of the Sunday adaptive-policy
// job. It mirrors pkg/hive/eval.CrossRunScheduler — wakes once per hour,
// checks the configured (weekday, hour) slot, and fires the builder when
// the clock matches. Default is Sunday 05:00 UTC, intentionally one hour
// before Loop C so the policy markdown is on disk before the council brief
// gets rendered.
//
// Tick alignment: the first tick fires immediately (with a "would have run?"
// check) so a pod restart on Sunday morning doesn't skip the week. After
// that the ticker is a plain time.Ticker — drift over a 60-minute interval
// is negligible.
type ProposalsScheduler struct {
	Builder *ProposalsBuilder
	Logger  *slog.Logger

	// Weekday + Hour control when the job fires. Defaults to Sunday at
	// 05:00 UTC (Phase 7 plan: "Sunday 0500"). The check fires once per
	// (weekday, hour) combination — repeated polls inside the same hour
	// de-duplicate via lastFired.
	Weekday time.Weekday
	Hour    int

	// Now is injectable for tests. Defaults to time.Now.
	Now func() time.Time

	// lastFired records the start of the (weekday, hour) window that most
	// recently triggered a run. Resets across pod restarts — acceptable
	// because the next eligible window is at most 7 days out.
	lastFired time.Time
}

// NewProposalsScheduler returns a scheduler with Sunday-05:00-UTC defaults.
func NewProposalsScheduler(builder *ProposalsBuilder) *ProposalsScheduler {
	return &ProposalsScheduler{
		Builder: builder,
		Weekday: time.Sunday,
		Hour:    5,
	}
}

// Run drives the hourly check until ctx is cancelled. Returns nil on clean
// shutdown so it composes with errgroup.WithContext alongside the other
// schedulers in the operator.
func (s *ProposalsScheduler) Run(ctx context.Context) error {
	if s == nil || s.Builder == nil {
		// Disabled — wait for shutdown so errgroup stays balanced.
		<-ctx.Done()
		return nil
	}
	if s.Logger != nil {
		s.Logger.Info("proposals scheduler starting",
			"weekday", s.Weekday.String(), "hour_utc", s.Hour)
	}
	// Initial check so a Sunday-morning pod restart still fires the loop.
	_, _ = s.Tick(ctx)

	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			_, _ = s.Tick(ctx)
		}
	}
}

// Tick is the unit-test-visible step. Returns (fired, err): fired is true
// only on the first call within a given (weekday, hour) window. Errors from
// the builder propagate so tests can assert on them; the production Run loop
// drops them onto the logger.
func (s *ProposalsScheduler) Tick(ctx context.Context) (bool, error) {
	if s == nil || s.Builder == nil {
		return false, nil
	}
	now := s.now().UTC()
	if now.Weekday() != s.Weekday || now.Hour() != s.Hour {
		return false, nil
	}
	// De-dup: the (weekday, hour) start defines a unique 1h window per week.
	windowStart := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, 0, 0, 0, time.UTC)
	if !s.lastFired.Before(windowStart) {
		return false, nil
	}
	s.lastFired = windowStart
	if _, err := s.Builder.Run(ctx); err != nil {
		if s.Logger != nil {
			s.Logger.Warn("adaptive proposals run failed", "error", err)
		}
		return true, err
	}
	return true, nil
}

func (s *ProposalsScheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
