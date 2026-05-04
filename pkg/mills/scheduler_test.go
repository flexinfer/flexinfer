package mills

import (
	"testing"
	"time"
)

// TestNextInterval_IdleThrottle exercises the cadence-decision logic in
// isolation so we don't have to drive the real ticker. Wall-clock state
// is fed in via the Clock function on Scheduler.
func TestNextInterval_IdleThrottle(t *testing.T) {
	const (
		fast = 60 * time.Second
		slow = 5 * time.Minute
		idle = 5 * time.Minute
	)

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	s := &Scheduler{Clock: func() time.Time { return now }}

	// First tick is a no-op. We're entering the streak — should still
	// use the fast cadence and start the streak clock.
	streakStart := now
	got, idleSince := s.nextInterval(TickResult{}, time.Time{}, fast, slow, idle)
	if got != fast {
		t.Fatalf("first idle tick: want fast=%s, got %s", fast, got)
	}
	if idleSince != streakStart {
		t.Fatalf("first idle tick: want idleSince=%s, got %s", streakStart, idleSince)
	}

	// Advance 4 minutes — still under IdleAfter, stay fast.
	now = streakStart.Add(4 * time.Minute)
	got, idleSince = s.nextInterval(TickResult{}, idleSince, fast, slow, idle)
	if got != fast {
		t.Fatalf("4min into streak: want fast, got %s", got)
	}
	if idleSince != streakStart {
		t.Fatalf("4min into streak: idleSince should not advance: want %s got %s", streakStart, idleSince)
	}

	// Advance to exactly IdleAfter — should switch to slow.
	now = streakStart.Add(idle)
	got, idleSince = s.nextInterval(TickResult{}, idleSince, fast, slow, idle)
	if got != slow {
		t.Fatalf("at IdleAfter: want slow=%s, got %s", slow, got)
	}
	if idleSince != streakStart {
		t.Fatalf("at IdleAfter: idleSince should still equal streakStart, got %s", idleSince)
	}

	// Stay slow if no work shows up.
	now = streakStart.Add(2 * idle)
	got, _ = s.nextInterval(TickResult{}, idleSince, fast, slow, idle)
	if got != slow {
		t.Fatalf("deep idle: want slow, got %s", got)
	}

	// Real work resets the streak and snaps back to fast.
	got, idleSince = s.nextInterval(TickResult{Inspected: 1, Started: 1}, idleSince, fast, slow, idle)
	if got != fast {
		t.Fatalf("work tick: want fast, got %s", got)
	}
	if !idleSince.IsZero() {
		t.Fatalf("work tick: idleSince should reset to zero, got %s", idleSince)
	}

	// Even Inspected > 0 with no started counts as "work" (e.g. an item
	// got deferred for budget). The reconciler is doing meaningful work.
	got, idleSince = s.nextInterval(TickResult{Inspected: 3, Deferred: 3}, time.Time{}, fast, slow, idle)
	if got != fast || !idleSince.IsZero() {
		t.Fatalf("deferred tick: want fast/zero, got %s/%s", got, idleSince)
	}
}

func TestNextInterval_ThrottleDisabled(t *testing.T) {
	const fast = 60 * time.Second
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	s := &Scheduler{Clock: func() time.Time { return now }}

	// idleAfter=0 in the helper signature means "disabled" because the
	// Run() prelude maps a negative IdleAfter onto zero. Verify that
	// passing zero through nextInterval keeps cadence fast forever.
	idleSince := time.Time{}
	for i := 0; i < 10; i++ {
		now = now.Add(time.Hour)
		got, next := s.nextInterval(TickResult{}, idleSince, fast, 5*time.Minute, 0)
		if got != fast {
			t.Fatalf("iter %d: throttle disabled but got %s, want fast", i, got)
		}
		if !next.IsZero() {
			t.Fatalf("iter %d: throttle disabled, idleSince should stay zero, got %s", i, next)
		}
		idleSince = next
	}
}

func TestIsNoOp(t *testing.T) {
	cases := []struct {
		name string
		res  TickResult
		want bool
	}{
		{"empty", TickResult{}, true},
		{"policy disabled", TickResult{SkipReason: "policy disabled"}, true},
		{"deferred", TickResult{Inspected: 2, Deferred: 2}, false},
		{"started", TickResult{Inspected: 1, Started: 1}, false},
		{"errored only", TickResult{Errored: 1}, true},
		// Errored alone (without Inspected) is treated as a no-op. Real
		// errors come with Inspected > 0 (we tried to look at items and
		// the call failed); a pure read failure surfaces via the err
		// return, not the result.
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.res.IsNoOp(); got != tc.want {
				t.Fatalf("IsNoOp(%+v) = %v, want %v", tc.res, got, tc.want)
			}
		})
	}
}
