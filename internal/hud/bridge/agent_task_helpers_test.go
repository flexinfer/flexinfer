package bridge

import (
	"testing"
)

func TestMergeDispatchTags(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{"empty adds dispatched", nil, []string{"dispatched"}},
		{"prepends dispatched", []string{"ci", "backend"}, []string{"dispatched", "ci", "backend"}},
		{"deduplicates dispatched", []string{"dispatched", "ci"}, []string{"dispatched", "ci"}},
		{"trims whitespace", []string{" ci ", "  backend  "}, []string{"dispatched", "ci", "backend"}},
		{"filters empty strings", []string{"", "ci", "  ", "backend"}, []string{"dispatched", "ci", "backend"}},
		{"deduplicates tags", []string{"ci", "ci", "backend"}, []string{"dispatched", "ci", "backend"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeDispatchTags(tc.input)
			if len(got) != len(tc.expected) {
				t.Fatalf("len = %d, want %d: %v", len(got), len(tc.expected), got)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("index %d = %q, want %q", i, got[i], tc.expected[i])
				}
			}
		})
	}
}
