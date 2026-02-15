package tui

import "testing"

func TestNewLayout(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		wantContentH  int
		wantContentW  int
		wantCompact   bool
	}{
		{"normal terminal", 120, 40, 38, 120, false},
		{"narrow terminal", 50, 30, 28, 50, true},
		{"compact threshold", 60, 20, 18, 60, false},
		{"below compact", 59, 20, 18, 59, true},
		{"zero height", 80, 0, 1, 80, false},
		{"height 1", 80, 1, 1, 80, false},
		{"height 2", 80, 2, 1, 80, false},
		{"height 3", 80, 3, 1, 80, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewLayout(tt.width, tt.height)
			if l.ContentH != tt.wantContentH {
				t.Errorf("ContentH = %d, want %d", l.ContentH, tt.wantContentH)
			}
			if l.ContentW != tt.wantContentW {
				t.Errorf("ContentW = %d, want %d", l.ContentW, tt.wantContentW)
			}
			if l.Compact != tt.wantCompact {
				t.Errorf("Compact = %v, want %v", l.Compact, tt.wantCompact)
			}
			if l.Width != tt.width {
				t.Errorf("Width = %d, want %d", l.Width, tt.width)
			}
			if l.Height != tt.height {
				t.Errorf("Height = %d, want %d", l.Height, tt.height)
			}
		})
	}
}

func TestNewLayoutHeaderHelp(t *testing.T) {
	l := NewLayout(100, 50)
	if l.HeaderH != 1 {
		t.Errorf("HeaderH = %d, want 1", l.HeaderH)
	}
	if l.HelpH != 1 {
		t.Errorf("HelpH = %d, want 1", l.HelpH)
	}
}
