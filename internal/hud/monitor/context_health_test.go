package monitor

import (
	"testing"
	"time"
)

func TestComputeHealthScore_FullHealth(t *testing.T) {
	cfg := DefaultContextHealthConfig()
	// Fresh entry, low utilization, good token density, decent coverage.
	score := ComputeHealthScore(
		2*time.Minute, // very fresh
		0.3,           // low utilization
		3000,          // 3000 tokens
		50,            // 50 entries => 60 tokens/entry (ideal range)
		cfg,
	)
	if score < 80 {
		t.Errorf("expected high health score, got %d", score)
	}
}

func TestComputeHealthScore_LowHealth(t *testing.T) {
	cfg := DefaultContextHealthConfig()
	// Stale entry, high utilization, poor token density.
	score := ComputeHealthScore(
		90*time.Minute, // very stale
		0.95,           // nearly exhausted
		50000,          // 50000 tokens
		10,             // 10 entries => 5000 tokens/entry (bloated)
		cfg,
	)
	if score > 20 {
		t.Errorf("expected low health score, got %d", score)
	}
}

func TestComputeHealthScore_MidRange(t *testing.T) {
	cfg := DefaultContextHealthConfig()
	score := ComputeHealthScore(
		30*time.Minute, // moderate freshness
		0.6,            // moderate utilization
		5000,           // moderate tokens
		25,             // moderate entries => 200 tokens/entry
		cfg,
	)
	if score < 20 || score > 80 {
		t.Errorf("expected mid-range health score, got %d", score)
	}
}

func TestComputeFreshness(t *testing.T) {
	fullMark := 5 * time.Minute
	maxAge := 60 * time.Minute

	tests := []struct {
		name    string
		age     time.Duration
		wantMin float64
		wantMax float64
	}{
		{"very fresh", 1 * time.Minute, 100, 100},
		{"at full mark", 5 * time.Minute, 100, 100},
		{"half stale", 32*time.Minute + 30*time.Second, 45, 55},
		{"at max age", 60 * time.Minute, 0, 0},
		{"beyond max age", 120 * time.Minute, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := computeFreshness(tt.age, fullMark, maxAge)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("computeFreshness(%v) = %.1f, want [%.1f, %.1f]",
					tt.age, score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestComputeHeadroom(t *testing.T) {
	tests := []struct {
		name        string
		utilization float64
		wantMin     float64
		wantMax     float64
	}{
		{"low utilization", 0.2, 100, 100},
		{"half utilization", 0.5, 100, 100},
		{"high utilization", 0.75, 45, 55},
		{"full utilization", 1.0, 0, 0},
		{"over budget", 1.5, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := computeHeadroom(tt.utilization)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("computeHeadroom(%.2f) = %.1f, want [%.1f, %.1f]",
					tt.utilization, score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestComputeEfficiency(t *testing.T) {
	tests := []struct {
		name    string
		tokens  int
		entries int
		wantMin float64
		wantMax float64
	}{
		{"no data", 0, 0, 50, 50},
		{"ideal range", 3000, 50, 100, 100}, // 60 tokens/entry
		{"too sparse", 100, 10, 30, 40},     // 10 tokens/entry
		{"bloated", 25000, 50, 0, 50},       // 500 tokens/entry
		{"very bloated", 50000, 10, 0, 5},   // 5000 tokens/entry
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := computeEfficiency(tt.tokens, tt.entries)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("computeEfficiency(%d, %d) = %.1f, want [%.1f, %.1f]",
					tt.tokens, tt.entries, score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestComputeCoverage(t *testing.T) {
	tests := []struct {
		name    string
		entries int
		want    float64
	}{
		{"no entries", 0, 0},
		{"few entries", 10, 20},
		{"half coverage", 25, 50},
		{"full coverage", 50, 100},
		{"over coverage", 100, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := computeCoverage(tt.entries)
			if score != tt.want {
				t.Errorf("computeCoverage(%d) = %.1f, want %.1f",
					tt.entries, score, tt.want)
			}
		})
	}
}

func TestComputeSystemHealth_NoAgents(t *testing.T) {
	score := computeSystemHealth(nil)
	if score != 100 {
		t.Errorf("expected 100 for no agents, got %d", score)
	}
}

func TestComputeSystemHealth_Average(t *testing.T) {
	agents := []AgentContextHealth{
		{HealthScore: 80},
		{HealthScore: 60},
		{HealthScore: 40},
	}
	score := computeSystemHealth(agents)
	if score != 60 {
		t.Errorf("expected 60, got %d", score)
	}
}

func TestRecommend(t *testing.T) {
	tests := []struct {
		name         string
		utilization  float64
		health       int
		staleEntries int
		wantEmpty    bool
	}{
		{"critical utilization", 0.95, 20, 0, false},
		{"warning utilization", 0.85, 50, 0, false},
		{"many stale entries", 0.3, 70, 25, false},
		{"low health", 0.3, 40, 5, false},
		{"healthy", 0.3, 80, 5, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := recommend(tt.utilization, tt.health, tt.staleEntries)
			if tt.wantEmpty && rec != "" {
				t.Errorf("expected empty recommendation, got %q", rec)
			}
			if !tt.wantEmpty && rec == "" {
				t.Errorf("expected non-empty recommendation")
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		dur  time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h30m"},
		{2 * time.Hour, "2h0m"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.dur)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.dur, got, tt.want)
		}
	}
}

func TestCompactionThreshold(t *testing.T) {
	// Verify that utilization > 0.8 triggers compaction needed.
	cfg := DefaultContextHealthConfig()

	// Under threshold.
	score := ComputeHealthScore(2*time.Minute, 0.7, 7000, 50, cfg)
	_ = score // just ensure no panic

	// Over threshold: would flag compaction needed.
	utilization := 0.85
	needsCompaction := utilization > cfg.CompactionThresh
	if !needsCompaction {
		t.Error("expected compaction needed at 0.85 utilization")
	}

	// At threshold: should not trigger.
	utilization = 0.8
	needsCompaction = utilization > cfg.CompactionThresh
	if needsCompaction {
		t.Error("should not need compaction at exactly 0.8")
	}
}
