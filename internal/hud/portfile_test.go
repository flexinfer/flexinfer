package hud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortFilePath_ContainsLoomDir(t *testing.T) {
	path := PortFilePath()

	if !strings.Contains(path, "loom") {
		t.Fatalf("expected port file path to contain 'loom', got %q", path)
	}
	if !strings.HasSuffix(path, "hud.port") {
		t.Fatalf("expected port file path to end with 'hud.port', got %q", path)
	}
}

func TestPortFilePath_RespectsXDGConfigHome(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	path := PortFilePath()
	want := filepath.Join(tmpDir, "loom", "hud.port")
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}

func TestPortFilePath_FallsBackToHomeConfig(t *testing.T) {
	// Clear XDG_CONFIG_HOME to test fallback.
	t.Setenv("XDG_CONFIG_HOME", "")

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("home directory unavailable")
	}

	path := PortFilePath()
	want := filepath.Join(home, ".config", "loom", "hud.port")
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}

func TestPortFilePath_IsAbsolute(t *testing.T) {
	path := PortFilePath()
	if !filepath.IsAbs(path) {
		t.Fatalf("expected absolute path, got %q", path)
	}
}
