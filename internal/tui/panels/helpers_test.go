package panels

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestSpaces(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, ""},
		{-1, ""},
		{1, " "},
		{5, "     "},
	}
	for _, tt := range tests {
		got := spaces(tt.n)
		if got != tt.want {
			t.Errorf("spaces(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestPadRight(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		width int
		want  int // expected visible width
	}{
		{"empty string", "", 10, 10},
		{"shorter than width", "abc", 10, 10},
		{"exact width", "abcde", 5, 5},
		{"longer than width", "abcdefgh", 5, 8}, // no truncation
		{"zero width", "abc", 0, 0},
		{"negative width", "abc", -1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := padRight(tt.s, tt.width)
			gotWidth := lipgloss.Width(got)
			if gotWidth != tt.want {
				t.Errorf("padRight(%q, %d) width = %d, want %d", tt.s, tt.width, gotWidth, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		check  func(t *testing.T, got string)
	}{
		{
			name:   "zero maxLen",
			s:      "hello",
			maxLen: 0,
			check: func(t *testing.T, got string) {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
			},
		},
		{
			name:   "negative maxLen",
			s:      "hello",
			maxLen: -1,
			check: func(t *testing.T, got string) {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
			},
		},
		{
			name:   "fits within maxLen",
			s:      "hello",
			maxLen: 10,
			check: func(t *testing.T, got string) {
				if got != "hello" {
					t.Errorf("expected %q, got %q", "hello", got)
				}
			},
		},
		{
			name:   "exact fit",
			s:      "hello",
			maxLen: 5,
			check: func(t *testing.T, got string) {
				if got != "hello" {
					t.Errorf("expected %q, got %q", "hello", got)
				}
			},
		},
		{
			name:   "needs truncation",
			s:      "hello world",
			maxLen: 8,
			check: func(t *testing.T, got string) {
				visWidth := lipgloss.Width(got)
				if visWidth > 8 {
					t.Errorf("visible width %d exceeds maxLen 8", visWidth)
				}
				if !strings.HasSuffix(got, "...") {
					t.Errorf("expected ellipsis suffix, got %q", got)
				}
			},
		},
		{
			name:   "ANSI-styled string preserves sequences",
			s:      lipgloss.NewStyle().Bold(true).Render("hello world"),
			maxLen: 8,
			check: func(t *testing.T, got string) {
				visWidth := lipgloss.Width(got)
				if visWidth > 8 {
					t.Errorf("ANSI truncate visible width %d exceeds maxLen 8", visWidth)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.s, tt.maxLen)
			tt.check(t, got)
		})
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{10000, "10.0K"},
		{1000000, "1.0M"},
		{2500000, "2.5M"},
	}
	for _, tt := range tests {
		got := formatNumber(tt.n)
		if got != tt.want {
			t.Errorf("formatNumber(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
