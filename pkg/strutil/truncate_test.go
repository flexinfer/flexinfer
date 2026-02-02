package strutil

import "testing"

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{"short string", "hi", 8, "hi"},
		{"exact length", "hello", 5, "hello"},
		{"truncate with ellipsis", "hello world", 8, "hello..."},
		{"very short max", "hello", 3, "hel"},
		{"max 4 with ellipsis", "hello", 4, "h..."},
		{"empty string", "", 5, ""},
		{"max 0", "hello", 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.s, tt.maxLen)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
			if len(got) > tt.maxLen {
				t.Errorf("Truncate(%q, %d) = %q (len %d), exceeds maxLen", tt.s, tt.maxLen, got, len(got))
			}
		})
	}
}

func TestTruncateNoEllipsis(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{"short string", "hi", 8, "hi"},
		{"truncate", "hello world", 5, "hello"},
		{"exact length", "hello", 5, "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateNoEllipsis(tt.s, tt.maxLen)
			if got != tt.want {
				t.Errorf("TruncateNoEllipsis(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestTruncateSingleLine(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{"with newlines", "hello\nworld", 15, "hello world"},
		{"truncate newlines", "hello\nworld", 8, "hello..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateSingleLine(tt.s, tt.maxLen)
			if got != tt.want {
				t.Errorf("TruncateSingleLine(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestTruncateBytes(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		maxBytes int
		want     string
	}{
		{"ascii short", "hello", 10, "hello"},
		{"ascii truncate", "hello world", 8, "hello..."},
		{"utf8 char boundary", "café☕test", 8, "café..."},
		{"utf8 multi byte", "日本語テスト", 12, "日本語..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateBytes(tt.s, tt.maxBytes)
			if len(got) > tt.maxBytes {
				t.Errorf("TruncateBytes(%q, %d) = %q (len %d), exceeds maxBytes", tt.s, tt.maxBytes, got, len(got))
			}
		})
	}
}
