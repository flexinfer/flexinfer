package proxy

import (
	"sync"
	"time"

	"github.com/flexinfer/flexinfer/pkg/envutil"
)

// defaultReservationTTL bounds how long a least-loaded pick "counts" against a
// model before the reservation expires. A reservation is a placeholder for a
// request that pickLeastLoaded has just steered to a member but whose real
// connection gauge (incrementConnections) has not moved yet. The TTL is a
// safety valve: if a picked request never turns into a served connection (the
// caller drops, the queue rejects, a cold start times out), its reservation
// self-heals instead of pinning phantom load on the member forever.
const defaultReservationTTL = 10 * time.Second

// reservationEnvTTL is the env var operators set to tune (or disable) the
// least-loaded reservation ledger. See newReservationLedger.
const reservationEnvTTL = "PROXY_LEAST_LOADED_RESERVATION_TTL"

// reservationLedger is a concurrency-safe, TTL'd count of pending least-loaded
// picks keyed by model name.
//
// Motivation: pickLeastLoaded selects the Ready label-group member with the
// fewest active proxy connections, but the connection gauge only moves once
// trackAndServe actually starts serving upstream. A burst of N concurrent
// requests for the same service label therefore all observe load 0 and pile
// onto the same member before any gauge increments. The ledger closes that
// window: each pick records a short-lived reservation, and pickLeastLoaded adds
// unexpired reservations to the observed connection count so the burst spreads
// across members instead of stampeding one.
//
// incrementConnections consumes one reservation when a real connection starts —
// the reservation has done its job and is replaced by a tracked connection.
// Reservations that are never consumed (dropped callers, rejected queue slots)
// expire lazily on the next access.
//
// A nil or ttl<=0 ledger is disabled: every operation is a cheap no-op and
// pending() reports 0, so pickLeastLoaded behaves exactly as it did before
// reservations existed.
type reservationLedger struct {
	ttl time.Duration

	mu sync.Mutex
	// deadlines maps model name -> reservation expiry deadlines. Deadlines are
	// appended in creation order, so the slice is naturally ascending and the
	// oldest reservation sits at the front. Expiry prunes in place to stay
	// allocation-light.
	deadlines map[string][]time.Time

	// now is injectable so tests can drive expiry deterministically. Production
	// always uses time.Now.
	now func() time.Time
}

// newReservationLedger constructs a ledger, reading the TTL from
// PROXY_LEAST_LOADED_RESERVATION_TTL (default 10s). A value of "0" (or "0s")
// disables the ledger entirely, restoring pre-reservation least-loaded
// behavior. Invalid values fall back to the default.
//
// The env read lives here (not in ProxyConfig) so the reservation subsystem
// owns its own configuration surface.
func newReservationLedger() *reservationLedger {
	return newReservationLedgerWithTTL(envutil.DurationOrDefault(reservationEnvTTL, defaultReservationTTL))
}

// newReservationLedgerWithTTL builds a ledger with an explicit TTL. Used by
// tests to exercise expiry and the disabled path without touching the
// environment.
func newReservationLedgerWithTTL(ttl time.Duration) *reservationLedger {
	return &reservationLedger{
		ttl:       ttl,
		deadlines: make(map[string][]time.Time),
		now:       time.Now,
	}
}

// enabled reports whether the ledger actively tracks reservations. ttl is
// immutable after construction, so this is safe to read without the mutex.
func (l *reservationLedger) enabled() bool {
	return l != nil && l.ttl > 0
}

// reserve records one pending reservation for model, expiring after the TTL.
// No-op when the ledger is disabled.
func (l *reservationLedger) reserve(model string) {
	if !l.enabled() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	live := l.pruneLocked(model)
	l.deadlines[model] = append(live, l.now().Add(l.ttl))
	leastLoadedReservationsTotal.WithLabelValues(model).Inc()
}

// pending returns the number of unexpired reservations for model. Returns 0
// when the ledger is disabled. Expired entries are pruned as a side effect.
func (l *reservationLedger) pending(model string) int {
	if !l.enabled() {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	live := l.pruneLocked(model)
	l.storeLocked(model, live)
	return len(live)
}

// consume removes one unexpired reservation for model — a real connection is
// replacing its placeholder. It is a no-op when the ledger is disabled or when
// the model has no live reservation (direct / non-label-group requests, or a
// member whose burst has already drained). The oldest reservation is removed
// first so long-lived phantom picks cannot accumulate ahead of real traffic.
func (l *reservationLedger) consume(model string) {
	if !l.enabled() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	live := l.pruneLocked(model)
	if len(live) > 0 {
		live = live[1:]
	}
	l.storeLocked(model, live)
}

// pruneLocked drops expired deadlines for model and returns the surviving
// slice, filtering in place to reuse the backing array. Each pruned reservation
// increments the expired counter exactly once because callers always persist
// the returned slice via storeLocked (or an append assignment). Caller holds mu.
func (l *reservationLedger) pruneLocked(model string) []time.Time {
	existing := l.deadlines[model]
	if len(existing) == 0 {
		return existing
	}
	now := l.now()
	live := existing[:0]
	for _, deadline := range existing {
		if deadline.After(now) {
			live = append(live, deadline)
		} else {
			leastLoadedReservationsExpiredTotal.WithLabelValues(model).Inc()
		}
	}
	return live
}

// storeLocked writes back the live reservation slice for model, deleting the
// map entry entirely when empty so idle models do not retain keys. Caller holds
// mu.
func (l *reservationLedger) storeLocked(model string, live []time.Time) {
	if len(live) == 0 {
		delete(l.deadlines, model)
		return
	}
	l.deadlines[model] = live
}

// reservations returns the Proxy's reservation ledger, constructing it (and
// reading PROXY_LEAST_LOADED_RESERVATION_TTL) on first access. Lazy
// construction is required because tests build Proxy struct literals directly
// rather than through New, so there is no single constructor to hook; a test
// may pre-set reservationLedger before first use to pin an explicit TTL.
func (p *Proxy) reservations() *reservationLedger {
	p.reservationsOnce.Do(func() {
		if p.reservationLedger == nil {
			p.reservationLedger = newReservationLedger()
		}
	})
	return p.reservationLedger
}
