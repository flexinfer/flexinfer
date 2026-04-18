package agentcontext

import (
	"testing"
	"time"
)

func TestDefaultAutoHandoffConfig(t *testing.T) {
	cfg := DefaultAutoHandoffConfig()
	if cfg.InputTokenHigh != 160_000 {
		t.Errorf("InputTokenHigh = %d, want 160000", cfg.InputTokenHigh)
	}
	if cfg.CostUSDHigh != 1.50 {
		t.Errorf("CostUSDHigh = %f, want 1.50", cfg.CostUSDHigh)
	}
	if cfg.StalledDuration != 8*time.Minute {
		t.Errorf("StalledDuration = %v, want 8m", cfg.StalledDuration)
	}
	if cfg.Debounce != 10*time.Minute {
		t.Errorf("Debounce = %v, want 10m", cfg.Debounce)
	}
	if cfg.Enabled {
		t.Error("Enabled should default to false")
	}
}

func TestTriggerGate_DisabledIsNoop(t *testing.T) {
	gate := NewTriggerGate(DefaultAutoHandoffConfig()) // Enabled=false
	now := time.Now()
	for i := 0; i < 10; i++ {
		if gate.Observe("s1", AutoHandoffReasonInputTokens, now) {
			t.Fatal("disabled gate must never fire")
		}
	}
}

func enabledCfg() AutoHandoffConfig {
	c := DefaultAutoHandoffConfig()
	c.Enabled = true
	return c
}

// TestTriggerGate_Scenarios covers the acceptance cases from the spec:
//
//	(a) first breach → no fire
//	(b) two consecutive breaches → fire
//	(c) breach → non-breach → breach → no fire (counter reset)
//	(d) fire → within-debounce breach → no fire
//	(e) fire → post-debounce breach → no fire on first, fire on second
func TestTriggerGate_Scenarios(t *testing.T) {
	tests := []struct {
		name  string
		steps []struct {
			reason    string
			advanceBy time.Duration
			wantFire  bool
		}
	}{
		{
			name: "a_first_breach_no_fire",
			steps: []struct {
				reason    string
				advanceBy time.Duration
				wantFire  bool
			}{
				{reason: AutoHandoffReasonInputTokens, advanceBy: 0, wantFire: false},
			},
		},
		{
			name: "b_two_consecutive_breaches_fire",
			steps: []struct {
				reason    string
				advanceBy time.Duration
				wantFire  bool
			}{
				{reason: AutoHandoffReasonInputTokens, advanceBy: 0, wantFire: false},
				{reason: AutoHandoffReasonInputTokens, advanceBy: 5 * time.Second, wantFire: true},
			},
		},
		{
			name: "c_breach_reset_breach_no_fire",
			steps: []struct {
				reason    string
				advanceBy time.Duration
				wantFire  bool
			}{
				{reason: AutoHandoffReasonInputTokens, advanceBy: 0, wantFire: false},
				{reason: "", advanceBy: 1 * time.Second, wantFire: false}, // non-breach resets counter
				{reason: AutoHandoffReasonInputTokens, advanceBy: 1 * time.Second, wantFire: false},
			},
		},
		{
			name: "d_fire_then_within_debounce_no_fire",
			steps: []struct {
				reason    string
				advanceBy time.Duration
				wantFire  bool
			}{
				{reason: AutoHandoffReasonInputTokens, advanceBy: 0, wantFire: false},
				{reason: AutoHandoffReasonInputTokens, advanceBy: 5 * time.Second, wantFire: true},
				// within debounce (default 10m)
				{reason: AutoHandoffReasonInputTokens, advanceBy: 1 * time.Minute, wantFire: false},
				{reason: AutoHandoffReasonInputTokens, advanceBy: 1 * time.Minute, wantFire: false},
			},
		},
		{
			name: "e_fire_then_post_debounce_requires_two",
			steps: []struct {
				reason    string
				advanceBy time.Duration
				wantFire  bool
			}{
				{reason: AutoHandoffReasonInputTokens, advanceBy: 0, wantFire: false},
				{reason: AutoHandoffReasonInputTokens, advanceBy: 5 * time.Second, wantFire: true},
				// jump past debounce (10m)
				{reason: AutoHandoffReasonInputTokens, advanceBy: 11 * time.Minute, wantFire: false},
				{reason: AutoHandoffReasonInputTokens, advanceBy: 5 * time.Second, wantFire: true},
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gate := NewTriggerGate(enabledCfg())
			now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
			for i, s := range tc.steps {
				now = now.Add(s.advanceBy)
				got := gate.Observe("session-1", s.reason, now)
				if got != s.wantFire {
					t.Errorf("step %d (reason=%q): Observe fired=%v, want %v",
						i, s.reason, got, s.wantFire)
				}
			}
		})
	}
}

func TestTriggerGate_IndependentReasons(t *testing.T) {
	gate := NewTriggerGate(enabledCfg())
	now := time.Now()

	// input_tokens breach x2 → fire
	if gate.Observe("s1", AutoHandoffReasonInputTokens, now) {
		t.Fatal("first input_tokens breach must not fire")
	}
	if !gate.Observe("s1", AutoHandoffReasonInputTokens, now.Add(time.Second)) {
		t.Fatal("second input_tokens breach must fire")
	}
	// cost reason tracks independently — first is still a no-fire
	if gate.Observe("s1", AutoHandoffReasonCost, now.Add(2*time.Second)) {
		t.Fatal("first cost breach must not fire (independent counter)")
	}
	if !gate.Observe("s1", AutoHandoffReasonCost, now.Add(3*time.Second)) {
		t.Fatal("second cost breach must fire")
	}
}

func TestTriggerGate_IndependentSessions(t *testing.T) {
	gate := NewTriggerGate(enabledCfg())
	now := time.Now()

	// Two breaches in session A → fire
	gate.Observe("sA", AutoHandoffReasonInputTokens, now)
	if !gate.Observe("sA", AutoHandoffReasonInputTokens, now.Add(time.Second)) {
		t.Fatal("session A second breach must fire")
	}
	// Session B first breach: must NOT fire (independent counter)
	if gate.Observe("sB", AutoHandoffReasonInputTokens, now.Add(2*time.Second)) {
		t.Fatal("session B first breach must not fire")
	}
}

func TestTriggerGate_EmptySessionIsNoop(t *testing.T) {
	gate := NewTriggerGate(enabledCfg())
	now := time.Now()
	for i := 0; i < 5; i++ {
		if gate.Observe("", AutoHandoffReasonInputTokens, now) {
			t.Fatal("empty sessionID must never fire")
		}
	}
}

func TestTriggerGate_NonBreachDoesNotResetDebounce(t *testing.T) {
	gate := NewTriggerGate(enabledCfg())
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)

	// Fire once.
	gate.Observe("s", AutoHandoffReasonInputTokens, base)
	if !gate.Observe("s", AutoHandoffReasonInputTokens, base.Add(5*time.Second)) {
		t.Fatal("should fire")
	}
	// Non-breach within the debounce window must NOT reset the debounce
	// timer — it only resets the consecutive-breach counter.
	gate.Observe("s", "", base.Add(1*time.Minute))
	// Now two consecutive breaches still within debounce — must suppress.
	gate.Observe("s", AutoHandoffReasonInputTokens, base.Add(2*time.Minute))
	if gate.Observe("s", AutoHandoffReasonInputTokens, base.Add(3*time.Minute)) {
		t.Fatal("within-debounce breach must be suppressed despite non-breach reset")
	}
}

func TestTriggerGate_Reset(t *testing.T) {
	gate := NewTriggerGate(enabledCfg())
	now := time.Now()
	gate.Observe("s", AutoHandoffReasonInputTokens, now)
	gate.Reset()
	// After Reset, the next breach must behave like a first breach.
	if gate.Observe("s", AutoHandoffReasonInputTokens, now.Add(time.Second)) {
		t.Fatal("after Reset, first breach must not fire")
	}
}

func TestTriggerGate_ConfigRoundTrip(t *testing.T) {
	cfg := DefaultAutoHandoffConfig()
	cfg.Enabled = true
	gate := NewTriggerGate(cfg)
	got := gate.Config()
	if got.Enabled != true || got.InputTokenHigh != 160_000 {
		t.Errorf("Config round-trip mismatch: %+v", got)
	}
}
