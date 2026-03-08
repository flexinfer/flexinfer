package sync

import (
	"testing"
)

func TestIsSubPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		dir      string
		expected bool
	}{
		{"child path", "/a/b/c", "/a/b", true},
		{"same path", "/a/b", "/a/b", false},
		{"parent path", "/a", "/a/b", false},
		{"unrelated", "/x/y", "/a/b", false},
		{"empty path", "", "/a", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isSubPath(tc.path, tc.dir)
			if got != tc.expected {
				t.Errorf("isSubPath(%q, %q) = %v, want %v", tc.path, tc.dir, got, tc.expected)
			}
		})
	}
}
