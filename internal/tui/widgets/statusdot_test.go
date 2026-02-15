package widgets

import (
	"strings"
	"testing"
)

func TestStatusDot(t *testing.T) {
	tests := []struct {
		status   string
		wantChar string
	}{
		{"ok", "●"},
		{"healthy", "●"},
		{"OK", "●"},      // case insensitive
		{"Healthy", "●"}, // case insensitive
		{"warning", "●"},
		{"degraded", "●"},
		{"error", "●"},
		{"down", "●"},
		{"idle", "○"},
		{"active", "◉"},
		{"unknown", "●"},
		{"", "●"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := StatusDot(tt.status)
			if !strings.Contains(got, tt.wantChar) {
				t.Errorf("StatusDot(%q) = %q, does not contain %q", tt.status, got, tt.wantChar)
			}
		})
	}
}

func TestStatusDotNonEmpty(t *testing.T) {
	statuses := []string{"ok", "healthy", "warning", "degraded", "error", "down", "idle", "active", "random"}
	for _, s := range statuses {
		got := StatusDot(s)
		if got == "" {
			t.Errorf("StatusDot(%q) returned empty string", s)
		}
	}
}
