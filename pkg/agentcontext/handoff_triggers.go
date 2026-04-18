// Package agentcontext — handoff_triggers.go (F5 / Slice C1)
//
// Auto-handoff trigger state machine. When a spawn's telemetry crosses a
// configurable threshold (input-token high, cost high, stall) *twice
// consecutively* within the same session, the gate fires and allows a
// draft handoff to be created. Debounced per-session/per-reason for a
// configurable window (default 10m) so a single sustained breach does not
// spam handoff drafts.
//
// Design notes:
//   - Pure, in-memory, unit-testable. No persistence, no network.
//   - The gate is stateful per (sessionID, reason) pair: it tracks
//     whether the previous observation was a breach, and when the last
//     fire occurred for debounce.
//   - Observe() is the sole mutator. Callers pass `reason != ""` only on
//     actual breach; non-breach calls must still be delivered so the
//     consecutive-breach counter resets.
//
// The wider auto-handoff pipeline (threshold evaluation against live
// telemetry, handoff creation, Prometheus metrics) lives in the
// orchestrator wire-in and in service_handoffs.go.
package agentcontext

import (
	"sync"
	"time"
)

// AutoHandoffConfig governs the thresholds + debounce for auto-handoffs.
// Defaults mirror §5.F5 of the 2026-04-17 agent orchestration spec.
type AutoHandoffConfig struct {
	// InputTokenHigh triggers a handoff when cumulative input tokens for
	// a spawn exceed this value. Default 160_000.
	InputTokenHigh int

	// CostUSDHigh triggers a handoff when cumulative spawn cost exceeds
	// this value. Default 1.50 USD.
	CostUSDHigh float64

	// StalledDuration triggers a handoff when the spawn has produced no
	// assistant output for at least this duration. Default 8m.
	StalledDuration time.Duration

	// Debounce suppresses subsequent fires for the same (session, reason)
	// pair within this window. Default 10m.
	Debounce time.Duration

	// Enabled must be true for the trigger gate to fire. Default false
	// (opt-in) to keep v1 conservative.
	Enabled bool
}

// DefaultAutoHandoffConfig returns the conservative spec defaults:
// 160k input tokens, $1.50, 8m stall, 10m debounce, disabled.
func DefaultAutoHandoffConfig() AutoHandoffConfig {
	return AutoHandoffConfig{
		InputTokenHigh:  160_000,
		CostUSDHigh:     1.50,
		StalledDuration: 8 * time.Minute,
		Debounce:        10 * time.Minute,
		Enabled:         false,
	}
}

// triggerState tracks per-(session, reason) bookkeeping for the
// two-consecutive-breach gate and the debounce window.
type triggerState struct {
	// consecutiveBreaches counts consecutive Observe() calls that were
	// breaches (reason != ""). Resets to zero on a non-breach
	// observation.
	consecutiveBreaches int

	// lastFire is the time the gate last fired for this pair. Zero if
	// never fired. Used for debounce.
	lastFire time.Time
}

// TriggerGate gates auto-handoff creation using a two-consecutive-breach
// rule plus a per-(session, reason) debounce window. It is safe for
// concurrent use.
type TriggerGate struct {
	cfg   AutoHandoffConfig
	mu    sync.Mutex
	state map[string]*triggerState // key: sessionID + "\x00" + reason
}

// NewTriggerGate constructs a TriggerGate with the given config.
func NewTriggerGate(cfg AutoHandoffConfig) *TriggerGate {
	return &TriggerGate{
		cfg:   cfg,
		state: make(map[string]*triggerState),
	}
}

// Config returns the gate's config (read-only copy).
func (g *TriggerGate) Config() AutoHandoffConfig {
	return g.cfg
}

// Observe records a telemetry observation for the given session. If
// reason is empty, the observation is a non-breach (resets the
// consecutive counter). If reason is non-empty, the observation is a
// breach.
//
// Returns true iff this observation causes the gate to fire — i.e. it
// is the SECOND (or later, post-reset) consecutive breach for (session,
// reason) AND the gate is not within the debounce window from the last
// fire. When Observe returns true it records `now` as the new
// debounce anchor.
//
// Observe is a no-op returning false if the gate is not enabled.
func (g *TriggerGate) Observe(sessionID, reason string, now time.Time) bool {
	if !g.cfg.Enabled {
		return false
	}
	if sessionID == "" {
		return false
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Non-breach observation: reset the consecutive counter for every
	// reason tracked under this session. We only reset counters, NOT the
	// debounce lastFire — debounce must outlive transient non-breach
	// observations.
	if reason == "" {
		for k, st := range g.state {
			if sessionKeyPrefixMatches(k, sessionID) {
				st.consecutiveBreaches = 0
			}
		}
		return false
	}

	key := sessionID + "\x00" + reason
	st, ok := g.state[key]
	if !ok {
		st = &triggerState{}
		g.state[key] = st
	}

	st.consecutiveBreaches++

	// Gate requires ≥2 consecutive breaches.
	if st.consecutiveBreaches < 2 {
		return false
	}

	// Debounce: suppress if within the window of the last fire.
	if g.cfg.Debounce > 0 && !st.lastFire.IsZero() {
		if now.Sub(st.lastFire) < g.cfg.Debounce {
			return false
		}
	}

	// Fire. Record the debounce anchor. Reset the consecutive counter
	// so the next fire also requires two fresh breaches post-debounce.
	st.lastFire = now
	st.consecutiveBreaches = 0
	return true
}

// Reset clears all gate state. Primarily for tests.
func (g *TriggerGate) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state = make(map[string]*triggerState)
}

// sessionKeyPrefixMatches returns true if key is of the form
// "<sessionID>\x00<reason>".
func sessionKeyPrefixMatches(key, sessionID string) bool {
	if len(key) <= len(sessionID) {
		return false
	}
	if key[len(sessionID)] != 0x00 {
		return false
	}
	return key[:len(sessionID)] == sessionID
}

// Reason constants for auto-handoff triggers. Used as the `reason` value
// passed to Observe and emitted as the `reason` label on Prometheus
// counters.
const (
	AutoHandoffReasonInputTokens = "input_tokens"
	AutoHandoffReasonCost        = "cost"
	AutoHandoffReasonStalled     = "stalled"
)
