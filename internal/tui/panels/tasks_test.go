package panels

import (
	"strings"
	"testing"
)

func TestCycleTaskStatus(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"pending", "in_progress"},
		{"Pending", "in_progress"},
		{"in_progress", "completed"},
		{"active", "completed"},
		{"Active", "completed"},
		{"completed", "pending"},
		{"blocked", "pending"},
		{"unknown", "pending"},
		{"", "pending"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := cycleTaskStatus(tt.input)
			if got != tt.want {
				t.Errorf("cycleTaskStatus(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPriorityBadge(t *testing.T) {
	tests := []struct {
		priority string
		wantChar string
	}{
		{"critical", "C"},
		{"Critical", "C"},
		{"high", "H"},
		{"medium", "M"},
		{"low", "L"},
		{"", "-"},
		{"unknown", "-"},
	}
	for _, tt := range tests {
		t.Run(tt.priority, func(t *testing.T) {
			got := priorityBadge(tt.priority)
			if !strings.Contains(got, tt.wantChar) {
				t.Errorf("priorityBadge(%q) = %q, does not contain %q", tt.priority, got, tt.wantChar)
			}
		})
	}
}

func TestPriorityBadgeNonEmpty(t *testing.T) {
	priorities := []string{"critical", "high", "medium", "low", "", "other"}
	for _, p := range priorities {
		got := priorityBadge(p)
		if got == "" {
			t.Errorf("priorityBadge(%q) returned empty string", p)
		}
	}
}
