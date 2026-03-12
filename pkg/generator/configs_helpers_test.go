package generator

import (
	"path/filepath"
	"testing"
)

func TestInferRegistryRoot(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"standard layout", "/workspace/platform/gitops/mcp/context/registry.yaml", "/workspace/platform/gitops"},
		{"non-standard", "/some/path/registry.yaml", "/some/path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := inferRegistryRoot(tc.input)
			if got != tc.expected {
				t.Errorf("inferRegistryRoot(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestInferWorkspaceRoot(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty returns empty", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := InferWorkspaceRoot(tc.input)
			if tc.input == "" && got != "" {
				t.Errorf("InferWorkspaceRoot('') = %q, want ''", got)
			}
		})
	}
}

func TestSortStrings(t *testing.T) {
	input := []string{"c", "a", "b"}
	sortStrings(input)
	if input[0] != "a" || input[1] != "b" || input[2] != "c" {
		t.Errorf("sortStrings() = %v, want [a b c]", input)
	}
}

func TestHubWrapperCandidates(t *testing.T) {
	got := hubWrapperCandidates("/workspace", "/registry")
	if len(got) == 0 {
		t.Fatal("expected non-empty candidates list")
	}
	// Should include multiple candidates
	if len(got) < 2 {
		t.Errorf("expected at least 2 candidates, got %d: %v", len(got), got)
	}
}

func TestHubWrapperCandidates_WithWorkspace(t *testing.T) {
	got := hubWrapperCandidates("/workspace", "")
	found := false
	for _, c := range got {
		if filepath.Base(c) == "loom-hub-wrapper" || c == filepath.Join("/workspace", hubWrapperWorkspaceBinaryPath) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected workspace-based candidate in %v", got)
	}
}

func TestNormalizeLoomBinary(t *testing.T) {
	if got := normalizeLoomBinary(""); got != "loom" {
		t.Fatalf("normalizeLoomBinary(\"\") = %q, want %q", got, "loom")
	}
	if got := normalizeLoomBinary(" /tmp/loom "); got != "/tmp/loom" {
		t.Fatalf("normalizeLoomBinary trims explicit paths, got %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	got := shellQuote("/tmp/loom binary")
	want := "'/tmp/loom binary'"
	if got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}
