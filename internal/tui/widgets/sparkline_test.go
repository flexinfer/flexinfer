package widgets

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/crb2nu/loom/internal/tui/theme"
)

func TestSparklineRenderEmpty(t *testing.T) {
	s := Sparkline{Data: nil, Width: 10, Color: theme.ColorSuccess}
	out := s.Render()
	if !strings.Contains(out, "--") {
		t.Errorf("empty sparkline should contain '--', got %q", out)
	}
}

func TestSparklineRenderSingleValue(t *testing.T) {
	s := Sparkline{Data: []float64{42}, Width: 5, Color: theme.ColorSuccess}
	out := s.Render()
	if lipgloss.Width(out) == 0 {
		t.Error("single value sparkline should produce output")
	}
}

func TestSparklineRenderMultipleValues(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	s := Sparkline{Data: data, Width: 8, Color: theme.ColorSuccess}
	out := s.Render()
	// The rendered output should have ANSI codes wrapping block characters.
	stripped := lipgloss.Width(out)
	if stripped != 8 {
		t.Errorf("sparkline visible width = %d, want 8", stripped)
	}
}

func TestSparklineRenderTruncatesData(t *testing.T) {
	data := make([]float64, 20)
	for i := range data {
		data[i] = float64(i)
	}
	s := Sparkline{Data: data, Width: 5, Color: theme.ColorSuccess}
	out := s.Render()
	visWidth := lipgloss.Width(out)
	if visWidth != 5 {
		t.Errorf("sparkline visible width = %d, want 5", visWidth)
	}
}

func TestSparklineRenderConstantValues(t *testing.T) {
	data := []float64{5, 5, 5, 5}
	s := Sparkline{Data: data, Width: 4, Color: theme.ColorSuccess}
	out := s.Render()
	if lipgloss.Width(out) == 0 {
		t.Error("constant value sparkline should produce output")
	}
}

func TestSparklineRenderZeroWidth(t *testing.T) {
	data := []float64{1, 2, 3}
	s := Sparkline{Data: data, Width: 0, Color: theme.ColorSuccess}
	out := s.Render()
	// With Width=0, defaults to len(data).
	visWidth := lipgloss.Width(out)
	if visWidth != 3 {
		t.Errorf("zero-width sparkline visible width = %d, want 3", visWidth)
	}
}
