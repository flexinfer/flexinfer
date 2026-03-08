package main

import (
	"strings"
	"testing"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tc := range tests {
		got := formatBytes(tc.input)
		if got != tc.expected {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestSplitToolName(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"server__tool", []string{"server", "tool"}},
		{"simple", []string{"simple"}},
		{"a__b__c", []string{"a", "b__c"}},
		{"__leading", []string{"", "leading"}},
		{"trailing__", []string{"trailing", ""}},
	}
	for _, tc := range tests {
		got := splitToolName(tc.input)
		if len(got) != len(tc.expected) {
			t.Fatalf("splitToolName(%q) = %v, want %v", tc.input, got, tc.expected)
		}
		for i := range got {
			if got[i] != tc.expected[i] {
				t.Errorf("splitToolName(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.expected[i])
			}
		}
	}
}

func TestFormatCheck(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ok", "ok"},
		{"stale", "STALE"},
		{"drift", "DRIFT"},
		{"errors", "ERRORS"},
		{"missing", "missing"},
		{"n/a", "n/a"},
		{"unknown", "unknown"},
	}
	for _, tc := range tests {
		got := formatCheck(tc.input)
		if got != tc.expected {
			t.Errorf("formatCheck(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestNormalizeJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(string) bool
	}{
		{"valid json normalizes", `{"key":"val"}`, func(s string) bool { return s == `{"key":"val"}` }},
		{"invalid json passthrough", "not json", func(s string) bool { return s == "not json" }},
		{"empty object", "{}", func(s string) bool { return s == "{}" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeJSON([]byte(tc.input))
			if !tc.check(got) {
				t.Errorf("normalizeJSON(%q) = %q", tc.input, got)
			}
		})
	}
}

func TestKeepalivePIDPath(t *testing.T) {
	got := keepalivePIDPath("test-agent")
	if !strings.Contains(got, "loom-keepalive-test-agent.pid") {
		t.Errorf("keepalivePIDPath('test-agent') = %q, expected to contain PID filename", got)
	}
}

func TestInferGitNamespace(t *testing.T) {
	// This calls git, so just verify it returns something sensible in a git repo
	got := inferGitNamespace()
	if got == "" {
		t.Skip("not in a git repo")
	}
	if !strings.Contains(got, "/") && got != "loom-core" {
		// Either "repo/branch" or just "repo" if detached HEAD
		t.Logf("inferGitNamespace() = %q", got)
	}
}
