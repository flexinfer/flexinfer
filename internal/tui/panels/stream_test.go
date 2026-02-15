package panels

import (
	"strings"
	"testing"
)

func TestShortTimestamp(t *testing.T) {
	tests := []struct {
		name string
		ts   string
		want string
	}{
		{"empty", "", "--:--:--"},
		{"ISO 8601", "2025-01-15T14:30:00Z", "14:30:00"},
		{"ISO with offset", "2025-01-15T14:30:00+05:00", "14:30:00"},
		{"time only", "14:30:00", "14:30:00"},
		{"short string", "abc", "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortTimestamp(tt.ts)
			if got != tt.want {
				t.Errorf("shortTimestamp(%q) = %q, want %q", tt.ts, got, tt.want)
			}
		})
	}
}

func TestEntryTypeBadge(t *testing.T) {
	types := []string{"decision", "finding", "observation", "note", "action", "task", "error", "", "unknown"}
	for _, et := range types {
		t.Run(et, func(t *testing.T) {
			got := entryTypeBadge(et)
			if got == "" {
				t.Errorf("entryTypeBadge(%q) returned empty string", et)
			}
		})
	}
}

func TestEntryTypeBadgeDecision(t *testing.T) {
	got := entryTypeBadge("decision")
	if !strings.Contains(got, "decision") {
		t.Errorf("entryTypeBadge('decision') = %q, does not contain 'decision'", got)
	}
}

func TestEntryTypeBadgeEmpty(t *testing.T) {
	got := entryTypeBadge("")
	if !strings.Contains(got, "note") {
		t.Errorf("entryTypeBadge('') = %q, expected fallback to 'note'", got)
	}
}
