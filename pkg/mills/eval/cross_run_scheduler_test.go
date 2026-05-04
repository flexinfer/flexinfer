package eval

import (
	"context"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// TestCrossRunScheduler_FiresOnConfiguredWindow seeds a stale plan,
// drives the clock to a Sunday 06:00 UTC, and verifies a single eval
// score row is written. Calling maybeFire again inside the same window
// must NOT produce a duplicate row.
func TestCrossRunScheduler_FiresOnConfiguredWindow(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 26, 6, 0, 0, 0, time.UTC) // Sunday 06:00
	if got := now.Weekday(); got != time.Sunday {
		t.Fatalf("fixture date not Sunday: %s", got)
	}
	// Seed a stale backlog item so the stale-plans check has something
	// to find — guarantees ≥1 finding row alongside the always-written
	// rubric records.
	if err := st.Backlog.Put(ctx, &store.BacklogItem{
		ID: "stale", Title: "x", State: store.BacklogQueued, Priority: store.P3,
		CreatedAt: now.Add(-10 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	checker := &CrossRunChecker{Store: st, Now: func() time.Time { return now }}
	sched := NewCrossRunScheduler(checker)
	sched.Now = func() time.Time { return now }

	sched.maybeFire(ctx)
	first, err := st.Eval.ListSince(ctx, now.Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("list 1: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("expected 3 rubric rows, got %d", len(first))
	}

	// De-dup: same window → no new rows.
	sched.maybeFire(ctx)
	second, err := st.Eval.ListSince(ctx, now.Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("list 2: %v", err)
	}
	if len(second) != len(first) {
		t.Errorf("second fire wrote %d rows; want 0 (dedup)", len(second)-len(first))
	}
}

// TestCrossRunScheduler_SkipsWrongWeekday verifies the scheduler is
// inert on Monday/Tuesday/etc. — the canonical no-op case. We make sure
// no eval rows are written when the clock is mid-week.
func TestCrossRunScheduler_SkipsWrongWeekday(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 27, 6, 0, 0, 0, time.UTC) // Monday
	if got := now.Weekday(); got == time.Sunday {
		t.Fatalf("fixture date is Sunday — wanted Monday")
	}
	checker := &CrossRunChecker{Store: st, Now: func() time.Time { return now }}
	sched := NewCrossRunScheduler(checker)
	sched.Now = func() time.Time { return now }

	sched.maybeFire(ctx)
	scores, err := st.Eval.ListSince(ctx, now.Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(scores) != 0 {
		t.Errorf("Monday fire wrote %d eval rows; want 0", len(scores))
	}
}

// TestCrossRunScheduler_SkipsWrongHour same as above but for the wrong
// hour on the right weekday — covers the hour-precision dedup path.
func TestCrossRunScheduler_SkipsWrongHour(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 26, 7, 0, 0, 0, time.UTC) // Sunday 07:00 (wrong hour)
	checker := &CrossRunChecker{Store: st, Now: func() time.Time { return now }}
	sched := NewCrossRunScheduler(checker)
	sched.Now = func() time.Time { return now }
	sched.maybeFire(ctx)
	scores, err := st.Eval.ListSince(ctx, now.Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(scores) != 0 {
		t.Errorf("wrong-hour fire wrote %d rows; want 0", len(scores))
	}
}

// TestCrossRunScheduler_NilCheckerExitsOnContextCancel verifies the
// "Loop C disabled" path waits for ctx and returns nil — important so
// the operator's errgroup doesn't blow up when the env-driven feature
// flag is off.
func TestCrossRunScheduler_NilCheckerExitsOnContextCancel(t *testing.T) {
	sched := NewCrossRunScheduler(nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned error after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not exit after cancel")
	}
}
