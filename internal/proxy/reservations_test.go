package proxy

import (
	"sync"
	"testing"
	"time"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func init() { RegisterMetrics() }

// newTestLedger builds a ledger with an explicit TTL and a controllable clock.
// The returned func advances the ledger's notion of "now" so expiry is
// deterministic without sleeping.
func newTestLedger(ttl time.Duration) (*reservationLedger, func(d time.Duration)) {
	l := newReservationLedgerWithTTL(ttl)
	now := time.Unix(0, 0)
	var mu sync.Mutex
	l.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}
	return l, advance
}

func TestReservationLedger_ReserveAndPending(t *testing.T) {
	l, _ := newTestLedger(10 * time.Second)

	assert.Equal(t, 0, l.pending("m"), "no reservations yet")
	l.reserve("m")
	l.reserve("m")
	assert.Equal(t, 2, l.pending("m"), "two reservations must both count")
	assert.Equal(t, 0, l.pending("other"), "reservations are keyed per model")
}

func TestReservationLedger_Consume(t *testing.T) {
	l, _ := newTestLedger(10 * time.Second)

	l.reserve("m")
	l.reserve("m")
	assert.Equal(t, 2, l.pending("m"))

	l.consume("m")
	assert.Equal(t, 1, l.pending("m"), "consume drops one reservation")

	l.consume("m")
	assert.Equal(t, 0, l.pending("m"), "consume drops the last reservation")

	// Double-consume past empty is a no-op, not an underflow.
	l.consume("m")
	l.consume("m")
	assert.Equal(t, 0, l.pending("m"), "consuming with no reservation is a no-op")
}

func TestReservationLedger_ConsumeUnknownModelIsNoOp(t *testing.T) {
	l, _ := newTestLedger(10 * time.Second)
	// Mirrors a direct / non-label-group request: incrementConnections consumes
	// for a model that never went through pickLeastLoaded.
	assert.NotPanics(t, func() { l.consume("never-reserved") })
	assert.Equal(t, 0, l.pending("never-reserved"))
}

func TestReservationLedger_Expiry(t *testing.T) {
	l, advance := newTestLedger(5 * time.Second)

	l.reserve("m")
	l.reserve("m")
	assert.Equal(t, 2, l.pending("m"))

	advance(4 * time.Second)
	assert.Equal(t, 2, l.pending("m"), "reservations still live before TTL")

	advance(2 * time.Second) // total 6s > 5s TTL
	assert.Equal(t, 0, l.pending("m"), "expired reservations stop counting toward load")
}

func TestReservationLedger_ExpiredAndLiveMixed(t *testing.T) {
	l, advance := newTestLedger(5 * time.Second)

	l.reserve("m") // deadline t=5s
	advance(3 * time.Second)
	l.reserve("m")           // deadline t=8s
	advance(3 * time.Second) // now t=6s: first expired, second live

	assert.Equal(t, 1, l.pending("m"), "only the unexpired reservation counts")
}

func TestReservationLedger_ExpiryMetric(t *testing.T) {
	const model = "expiry-metric-model"
	before := promtestutil.ToFloat64(leastLoadedReservationsExpiredTotal.WithLabelValues(model))

	l, advance := newTestLedger(1 * time.Second)
	l.reserve(model)
	l.reserve(model)
	advance(2 * time.Second)
	assert.Equal(t, 0, l.pending(model), "both reservations expired")

	after := promtestutil.ToFloat64(leastLoadedReservationsExpiredTotal.WithLabelValues(model))
	assert.Equal(t, float64(2), after-before, "each expired reservation increments the expired counter exactly once")
}

func TestReservationLedger_ReserveMetric(t *testing.T) {
	const model = "reserve-metric-model"
	before := promtestutil.ToFloat64(leastLoadedReservationsTotal.WithLabelValues(model))

	l, _ := newTestLedger(10 * time.Second)
	l.reserve(model)
	l.reserve(model)
	l.reserve(model)

	after := promtestutil.ToFloat64(leastLoadedReservationsTotal.WithLabelValues(model))
	assert.Equal(t, float64(3), after-before, "each reserve increments the reservations counter")
}

func TestReservationLedger_DisabledTTLZero(t *testing.T) {
	l := newReservationLedgerWithTTL(0)
	assert.False(t, l.enabled(), "ttl 0 disables the ledger")

	l.reserve("m")
	l.reserve("m")
	assert.Equal(t, 0, l.pending("m"), "disabled ledger never accrues load")
	assert.NotPanics(t, func() { l.consume("m") })
}

func TestReservationLedger_NilIsDisabled(t *testing.T) {
	var l *reservationLedger
	assert.False(t, l.enabled())
	assert.Equal(t, 0, l.pending("m"))
	assert.NotPanics(t, func() {
		l.reserve("m")
		l.consume("m")
	})
}

// TestReservationLedger_DefaultTTLFromEnv confirms newReservationLedger reads
// PROXY_LEAST_LOADED_RESERVATION_TTL and honors the documented default.
func TestReservationLedger_DefaultTTLFromEnv(t *testing.T) {
	t.Setenv(reservationEnvTTL, "")
	assert.Equal(t, defaultReservationTTL, newReservationLedger().ttl, "empty/unset env uses the 10s default")

	t.Setenv(reservationEnvTTL, "250ms")
	assert.Equal(t, 250*time.Millisecond, newReservationLedger().ttl, "env value overrides the default")

	t.Setenv(reservationEnvTTL, "0")
	assert.Equal(t, time.Duration(0), newReservationLedger().ttl, "0 disables the ledger")
	assert.False(t, newReservationLedger().enabled())
}

// TestReservationLedger_ConcurrentReserveConsume exercises the mutex under the
// race detector: concurrent reserve/consume/pending must not corrupt the map or
// drive pending negative.
func TestReservationLedger_ConcurrentReserveConsume(t *testing.T) {
	l, _ := newTestLedger(10 * time.Second)
	const workers = 8
	const iters = 500

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				l.reserve("m")
				_ = l.pending("m")
				l.consume("m")
			}
		}()
	}
	wg.Wait()

	// Balanced reserve/consume must net to zero live reservations.
	assert.Equal(t, 0, l.pending("m"), "balanced reserve/consume drains to zero")
}
