package goodhart

import (
	"math"
	"strings"
	"testing"
)

func TestWorkloadRegression(t *testing.T) {
	tests := []struct {
		name        string
		baseline    map[string]float64
		candidate   map[string]float64
		tol         float64
		wantTripped bool
		wantClass   string // substring expected in Reason when tripped
	}{
		{
			// The 2026-06-26 kill-test: aggregate up, long-form down. MUST trip.
			name:        "ngram_sd_kill_test",
			baseline:    map[string]float64{"lookup": 67.0, "novel": 72.6},
			candidate:   map[string]float64{"lookup": 138.9, "novel": 38.1},
			tol:         10,
			wantTripped: true,
			wantClass:   "novel",
		},
		{
			// A/B/A revert: candidate ~= baseline. MUST NOT trip (no false veto).
			name:        "neutral_revert",
			baseline:    map[string]float64{"lookup": 67.0, "novel": 72.6},
			candidate:   map[string]float64{"lookup": 66.8, "novel": 72.2},
			tol:         10,
			wantTripped: false,
		},
		{
			// A genuine universal gain. MUST NOT trip.
			name:        "universal_gain",
			baseline:    map[string]float64{"lookup": 67.0, "novel": 72.6},
			candidate:   map[string]float64{"lookup": 80.0, "novel": 78.0},
			tol:         10,
			wantTripped: false,
		},
		{
			// Within tolerance: -8% < 10% tolerance. MUST NOT trip.
			name:        "within_tolerance",
			baseline:    map[string]float64{"novel": 100},
			candidate:   map[string]float64{"novel": 92},
			tol:         10,
			wantTripped: false,
		},
		{
			// Just past tolerance: -12% > 10%. MUST trip.
			name:        "past_tolerance",
			baseline:    map[string]float64{"novel": 100},
			candidate:   map[string]float64{"novel": 88},
			tol:         10,
			wantTripped: true,
			wantClass:   "novel",
		},
		{
			name:        "missing_protected_class",
			baseline:    map[string]float64{"lookup": 67, "novel": 72},
			candidate:   map[string]float64{"lookup": 130},
			tol:         10,
			wantTripped: true,
			wantClass:   "novel",
		},
		{
			name:        "empty_baseline",
			baseline:    map[string]float64{},
			candidate:   map[string]float64{"novel": 10},
			tol:         10,
			wantTripped: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := WorkloadRegression(tc.baseline, tc.candidate, tc.tol)
			if got.Tripped != tc.wantTripped {
				t.Fatalf("Tripped = %v, want %v (reason: %s)", got.Tripped, tc.wantTripped, got.Reason)
			}
			if got.Detector != "workload_regression" {
				t.Errorf("Detector = %q", got.Detector)
			}
			if tc.wantTripped && !strings.Contains(got.Reason, tc.wantClass) {
				t.Errorf("Reason %q does not mention class %q", got.Reason, tc.wantClass)
			}
		})
	}

	// The headline value on the kill-test must be the worst-class regression (~-47.5%).
	f := WorkloadRegression(map[string]float64{"lookup": 67.0, "novel": 72.6},
		map[string]float64{"lookup": 138.9, "novel": 38.1}, 10)
	if f.Value > -40 || f.Value < -55 {
		t.Errorf("kill-test worst-class Value = %.2f, want ~-47.5", f.Value)
	}
}

func TestVarianceCollapse(t *testing.T) {
	t.Run("constant_stream_trips", func(t *testing.T) {
		d := NewVarianceCollapse(5, 0.01)
		for i := 0; i < 8; i++ {
			d.Observe(3.0)
		}
		if f := d.Finding(); !f.Tripped {
			t.Fatalf("constant stream should trip, got %s", f.Reason)
		}
	})
	t.Run("varying_stream_clear", func(t *testing.T) {
		d := NewVarianceCollapse(5, 0.01)
		for _, x := range []float64{1, 9, 2, 8, 3, 7} {
			d.Observe(x)
		}
		if f := d.Finding(); f.Tripped {
			t.Fatalf("varying stream should not trip, got %s", f.Reason)
		}
	})
	t.Run("insufficient_samples", func(t *testing.T) {
		d := NewVarianceCollapse(5, 0.01)
		d.Observe(3.0) // single sample
		if f := d.Finding(); f.Tripped {
			t.Fatalf("single sample should not trip, got %s", f.Reason)
		}
		empty := NewVarianceCollapse(5, 0.01)
		if f := empty.Finding(); f.Tripped {
			t.Fatalf("empty stream should not trip, got %s", f.Reason)
		}
	})
	t.Run("window_eviction_recovers", func(t *testing.T) {
		// Constant then varied: once the constant values age out, variance returns.
		d := NewVarianceCollapse(4, 0.5)
		for i := 0; i < 4; i++ {
			d.Observe(5.0)
		}
		if f := d.Finding(); !f.Tripped {
			t.Fatalf("constant fill should trip, got %s", f.Reason)
		}
		for _, x := range []float64{1, 9, 1, 9} {
			d.Observe(x)
		}
		if f := d.Finding(); f.Tripped {
			t.Fatalf("after eviction variance should recover, got %s", f.Reason)
		}
	})
}

func TestCUSUM(t *testing.T) {
	t.Run("downward_shift_trips", func(t *testing.T) {
		// target 72 (baseline novel tok/s); stream drops to ~38 (SD-on). Should trip down.
		d := NewCUSUM(72, 2, 10)
		for i := 0; i < 6; i++ {
			d.Observe(38)
		}
		f := d.Finding()
		if !f.Tripped {
			t.Fatalf("downward shift should trip, got %s", f.Reason)
		}
		if !strings.Contains(f.Reason, "downward") {
			t.Errorf("expected downward shift, got %s", f.Reason)
		}
	})
	t.Run("stable_noisy_clear", func(t *testing.T) {
		d := NewCUSUM(72, 2, 10)
		for _, x := range []float64{71, 73, 72, 71.5, 72.5, 72, 73, 71} {
			d.Observe(x)
		}
		if f := d.Finding(); f.Tripped {
			t.Fatalf("stable noise should not trip, got %s", f.Reason)
		}
	})
	t.Run("empty_clear", func(t *testing.T) {
		d := NewCUSUM(72, 2, 10)
		if f := d.Finding(); f.Tripped {
			t.Fatalf("empty should not trip, got %s", f.Reason)
		}
	})
}

func TestCeilingSaturation(t *testing.T) {
	t.Run("all_at_ceiling_trips", func(t *testing.T) {
		d := NewCeilingSaturation(1.0, 5, 0.8)
		for i := 0; i < 6; i++ {
			d.Observe(1.0)
		}
		if f := d.Finding(); !f.Tripped {
			t.Fatalf("saturated stream should trip, got %s", f.Reason)
		}
	})
	t.Run("below_maxfrac_clear", func(t *testing.T) {
		d := NewCeilingSaturation(1.0, 5, 0.8)
		for _, x := range []float64{1, 0, 0, 1, 0} { // 40% saturated
			d.Observe(x)
		}
		if f := d.Finding(); f.Tripped {
			t.Fatalf("40%% saturation should not trip at 80%% threshold, got %s", f.Reason)
		}
	})
	t.Run("insufficient_samples", func(t *testing.T) {
		d := NewCeilingSaturation(1.0, 5, 0.8)
		d.Observe(1.0)
		if f := d.Finding(); f.Tripped {
			t.Fatalf("partial window should not trip, got %s", f.Reason)
		}
	})
}

func TestLengthDrift(t *testing.T) {
	t.Run("verbosity_up_trips", func(t *testing.T) {
		d := NewLengthDrift(100, 3, 0.5) // >50% drift trips
		for _, x := range []float64{180, 190, 200} {
			d.Observe(x)
		}
		f := d.Finding()
		if !f.Tripped {
			t.Fatalf("length inflation should trip, got %s", f.Reason)
		}
		if !strings.Contains(f.Reason, "longer") {
			t.Errorf("expected 'longer', got %s", f.Reason)
		}
	})
	t.Run("stable_clear", func(t *testing.T) {
		d := NewLengthDrift(100, 3, 0.5)
		for _, x := range []float64{105, 98, 102} {
			d.Observe(x)
		}
		if f := d.Finding(); f.Tripped {
			t.Fatalf("stable length should not trip, got %s", f.Reason)
		}
	})
	t.Run("no_baseline_clear", func(t *testing.T) {
		d := NewLengthDrift(0, 3, 0.5)
		for _, x := range []float64{10, 20, 30} {
			d.Observe(x)
		}
		if f := d.Finding(); f.Tripped {
			t.Fatalf("no baseline should not trip, got %s", f.Reason)
		}
	})
}

func TestComponentDominance(t *testing.T) {
	tests := []struct {
		name        string
		components  map[string]float64
		maxShare    float64
		wantTripped bool
	}{
		{"one_dominates", map[string]float64{"length": 9, "correctness": 0.5, "format": 0.5}, 0.7, true},
		{"balanced", map[string]float64{"a": 1, "b": 1, "c": 1}, 0.7, false},
		{"empty", map[string]float64{}, 0.7, false},
		{"all_zero", map[string]float64{"a": 0, "b": 0}, 0.7, false},
		{"negative_magnitudes", map[string]float64{"a": -9, "b": 1}, 0.7, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := ComponentDominance(tc.components, tc.maxShare)
			if f.Tripped != tc.wantTripped {
				t.Fatalf("Tripped = %v, want %v (%s)", f.Tripped, tc.wantTripped, f.Reason)
			}
		})
	}
}

func TestDegeneracy(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		wantTripped bool
	}{
		{"repetitive_words", strings.Repeat("the cat sat ", 30), true},
		{"single_char_run", "hello" + strings.Repeat("a", 30), true},
		{"empty", "   ", true},
		{"coherent_prose", "Time felt boundless in youth, then quickened into a quiet, " +
			"steady current that carried each ordinary afternoon swiftly past.", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := Degeneracy(tc.text, 0.6)
			if f.Tripped != tc.wantTripped {
				t.Fatalf("Tripped = %v, want %v (rep=%.2f, %s)", f.Tripped, tc.wantTripped, f.Value, f.Reason)
			}
		})
	}
}

func TestAggregate(t *testing.T) {
	clear := Aggregate(
		WorkloadRegression(map[string]float64{"a": 10}, map[string]float64{"a": 11}, 10),
		ComponentDominance(map[string]float64{"x": 1, "y": 1}, 0.9),
	)
	if clear.Tripped {
		t.Fatalf("all-clear should not trip: %s", clear.Summary)
	}
	if !strings.Contains(clear.Summary, "clear") {
		t.Errorf("summary = %q", clear.Summary)
	}

	mixed := Aggregate(
		WorkloadRegression(map[string]float64{"novel": 72.6}, map[string]float64{"novel": 38.1}, 10),
		ComponentDominance(map[string]float64{"x": 1, "y": 1}, 0.9),
	)
	if !mixed.Tripped {
		t.Fatalf("a tripped finding should trip the verdict: %s", mixed.Summary)
	}
	if !strings.Contains(mixed.Summary, "OVEROPTIMIZATION") {
		t.Errorf("summary = %q", mixed.Summary)
	}
	if len(mixed.Findings) != 2 {
		t.Errorf("expected 2 findings, got %d", len(mixed.Findings))
	}
}

// Sanity: detectors must not panic on zero-value / boundary construction.
func TestBoundaryConstruction(t *testing.T) {
	NewVarianceCollapse(0, 0).Observe(1)     // window clamped to 2
	NewCeilingSaturation(1, 0, 0).Observe(1) // window clamped to 1
	NewLengthDrift(0, 0, 0).Observe(1)       // window clamped to 1
	c := NewCUSUM(0, -1, -1)                 // slack/limit clamped to 0
	c.Observe(1)
	if math.IsNaN(c.Finding().Value) {
		t.Fatal("CUSUM produced NaN")
	}
}
