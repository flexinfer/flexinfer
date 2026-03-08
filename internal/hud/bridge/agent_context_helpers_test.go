package bridge

import (
	"testing"
)

func TestEstimateContextChars(t *testing.T) {
	tests := []struct {
		name     string
		entry    ContextEntry
		minChars int
	}{
		{
			"empty entry",
			ContextEntry{},
			0,
		},
		{
			"with content",
			ContextEntry{Title: "Test", Content: "Hello world", FilePath: "/a/b.go"},
			22, // 4 + 11 + 7
		},
		{
			"with line numbers",
			ContextEntry{Title: "Test", LineStart: 10, LineEnd: 20},
			16, // 4 + 12 for line numbers
		},
		{
			"with metadata fields",
			ContextEntry{Title: "T", EntryType: "decision", Timestamp: "2024-01-01"},
			19, // 1 + 8 + 10
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := estimateContextChars(tc.entry)
			if got < tc.minChars {
				t.Errorf("estimateContextChars() = %d, want >= %d", got, tc.minChars)
			}
		})
	}
}

func TestEstimateContextTokens(t *testing.T) {
	tests := []struct {
		chars    int
		expected int
	}{
		{0, 0},
		{-1, 0},
		{4, 1},
		{8, 2},
		{100, 25},
		{1, 1}, // (1+3)/4 = 1
		{5, 2}, // (5+3)/4 = 2
	}
	for _, tc := range tests {
		got := estimateContextTokens(tc.chars)
		if got != tc.expected {
			t.Errorf("estimateContextTokens(%d) = %d, want %d", tc.chars, got, tc.expected)
		}
	}
}

func TestIsFileInjectionEntry(t *testing.T) {
	tests := []struct {
		name      string
		entry     ContextEntry
		entryType string
		expected  bool
	}{
		{"file_read type", ContextEntry{}, "file_read", true},
		{"code_context type", ContextEntry{}, "code_context", true},
		{"FILE_READ uppercase", ContextEntry{}, "FILE_READ", true},
		{"decision type no path", ContextEntry{}, "decision", false},
		{"decision type with path", ContextEntry{FilePath: "/a/b.go"}, "decision", true},
		{"empty type with path", ContextEntry{FilePath: "/a/b.go"}, "", true},
		{"empty type no path", ContextEntry{}, "", false},
		{"whitespace type", ContextEntry{}, "  file_read  ", true},
		{"whitespace-only path", ContextEntry{FilePath: "   "}, "note", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isFileInjectionEntry(tc.entry, tc.entryType)
			if got != tc.expected {
				t.Errorf("isFileInjectionEntry(%v, %q) = %v, want %v", tc.entry, tc.entryType, got, tc.expected)
			}
		})
	}
}

func TestParsePositiveIntEnv(t *testing.T) {
	t.Run("valid positive", func(t *testing.T) {
		t.Setenv("TEST_PARSE_INT", "42")
		v, ok := parsePositiveIntEnv("TEST_PARSE_INT")
		if !ok || v != 42 {
			t.Errorf("got (%d, %v), want (42, true)", v, ok)
		}
	})

	t.Run("zero", func(t *testing.T) {
		t.Setenv("TEST_PARSE_INT", "0")
		_, ok := parsePositiveIntEnv("TEST_PARSE_INT")
		if ok {
			t.Error("expected ok=false for zero")
		}
	})

	t.Run("negative", func(t *testing.T) {
		t.Setenv("TEST_PARSE_INT", "-5")
		_, ok := parsePositiveIntEnv("TEST_PARSE_INT")
		if ok {
			t.Error("expected ok=false for negative")
		}
	})

	t.Run("non-numeric", func(t *testing.T) {
		t.Setenv("TEST_PARSE_INT", "abc")
		_, ok := parsePositiveIntEnv("TEST_PARSE_INT")
		if ok {
			t.Error("expected ok=false for non-numeric")
		}
	})

	t.Run("missing env", func(t *testing.T) {
		_, ok := parsePositiveIntEnv("TEST_PARSE_INT_MISSING_XYZ")
		if ok {
			t.Error("expected ok=false for missing env")
		}
	})
}
