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

func TestWriteAndRemovePortFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	portFile, err := WritePortFile(4312)
	if err != nil {
		t.Fatalf("WritePortFile() error = %v", err)
	}

	data, err := os.ReadFile(portFile)
	if err != nil {
		t.Fatalf("read port file: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "4312" {
		t.Fatalf("port file contents = %q, want %q", got, "4312")
	}

	if err := RemovePortFile(); err != nil {
		t.Fatalf("RemovePortFile() error = %v", err)
	}
	if _, err := os.Stat(portFile); !os.IsNotExist(err) {
		t.Fatalf("expected port file to be removed, stat err = %v", err)
	}
}
