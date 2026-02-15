package widgets

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
)

func TestClampFloat(t *testing.T) {
	tests := []struct {
		v, min, max, want float64
	}{
		{0.5, 0, 1, 0.5},
		{-1, 0, 1, 0},
		{2, 0, 1, 1},
		{0, 0, 1, 0},
		{1, 0, 1, 1},
	}
	for _, tt := range tests {
		got := clampFloat(tt.v, tt.min, tt.max)
		if got != tt.want {
			t.Errorf("clampFloat(%v, %v, %v) = %v, want %v", tt.v, tt.min, tt.max, got, tt.want)
		}
	}
}

func TestGaugeRender(t *testing.T) {
	tests := []struct {
		name  string
		gauge Gauge
		check func(t *testing.T, out string)
	}{
		{
			name:  "zero value",
			gauge: Gauge{Label: "CPU", Value: 0.0, Width: 10, Color: theme.ColorSuccess},
			check: func(t *testing.T, out string) {
				if !strings.Contains(out, "CPU") {
					t.Error("missing label")
				}
				if !strings.Contains(out, "0%") {
					t.Error("missing 0% indicator")
				}
			},
		},
		{
			name:  "full value",
			gauge: Gauge{Label: "MEM", Value: 1.0, Width: 10, Color: theme.ColorSuccess},
			check: func(t *testing.T, out string) {
				if !strings.Contains(out, "100%") {
					t.Error("missing 100% indicator")
				}
			},
		},
		{
			name:  "half value",
			gauge: Gauge{Label: "", Value: 0.5, Width: 10, Color: theme.ColorSuccess},
			check: func(t *testing.T, out string) {
				if !strings.Contains(out, "50%") {
					t.Error("missing 50% indicator")
				}
			},
		},
		{
			name:  "clamps above 1",
			gauge: Gauge{Value: 1.5, Width: 10, Color: theme.ColorSuccess},
			check: func(t *testing.T, out string) {
				if !strings.Contains(out, "100%") {
					t.Error("expected clamped to 100%")
				}
			},
		},
		{
			name:  "clamps below 0",
			gauge: Gauge{Value: -0.5, Width: 10, Color: theme.ColorSuccess},
			check: func(t *testing.T, out string) {
				if !strings.Contains(out, "0%") {
					t.Error("expected clamped to 0%")
				}
			},
		},
		{
			name:  "default width when too small",
			gauge: Gauge{Value: 0.5, Width: 1, Color: theme.ColorSuccess},
			check: func(t *testing.T, out string) {
				// Should use default width of 10, rendering should not panic.
				if out == "" {
					t.Error("expected non-empty output")
				}
			},
		},
		{
			name:  "no label",
			gauge: Gauge{Label: "", Value: 0.5, Width: 10, Color: theme.ColorSuccess},
			check: func(t *testing.T, out string) {
				// Verify output contains brackets indicating bar.
				if !strings.Contains(out, "[") || !strings.Contains(out, "]") {
					t.Error("expected bar brackets")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := tt.gauge.Render()
			tt.check(t, out)
		})
	}
}

func TestGaugeRenderVisibleWidth(t *testing.T) {
	g := Gauge{Label: "Test", Value: 0.5, Width: 20, Color: theme.ColorSuccess}
	out := g.Render()
	w := lipgloss.Width(out)
	// Label(4) + space + [ + bar(20) + ] + space + pct(~4) ≈ 31+
	if w < 20 {
		t.Errorf("gauge visible width %d too small", w)
	}
}
