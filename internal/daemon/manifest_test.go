package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestHashTools_EmptySlice(t *testing.T) {
	if got := hashTools(nil); got != "" {
		t.Errorf("hashTools(nil) = %q, want empty", got)
	}
	if got := hashTools([]mcp.Tool{}); got != "" {
		t.Errorf("hashTools([]) = %q, want empty", got)
	}
}

func TestHashTools_Deterministic(t *testing.T) {
	tools := []mcp.Tool{
		{Name: "git_status", Description: "Show status"},
		{Name: "git_diff", Description: "Show diff"},
	}
	h1 := hashTools(tools)
	h2 := hashTools(tools)
	if h1 == "" {
		t.Fatal("expected non-empty hash for non-empty tools")
	}
	if h1 != h2 {
		t.Errorf("hash not deterministic: %q vs %q", h1, h2)
	}
}

func TestHashTools_DifferentToolsDifferentHash(t *testing.T) {
	tools1 := []mcp.Tool{{Name: "a"}}
	tools2 := []mcp.Tool{{Name: "b"}}
	if hashTools(tools1) == hashTools(tools2) {
		t.Error("different tools should produce different hashes")
	}
}

func TestManifestManager_Load_Corrupted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte("not valid yaml: [[["), 0600)

	m := NewManifestManager()
	m.path = path

	if err := m.Load(); err != nil {
		t.Errorf("Load of corrupted file should silently reset, got %v", err)
	}
}
