package daemon

import (
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstNonEmpty_ReturnsFirst(t *testing.T) {
	got := firstNonEmpty("hello", "world")
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestFirstNonEmpty_SkipsEmpty(t *testing.T) {
	got := firstNonEmpty("", "second")
	if got != "second" {
		t.Errorf("got %q, want %q", got, "second")
	}
}

func TestFirstNonEmpty_SkipsWhitespace(t *testing.T) {
	got := firstNonEmpty("  ", "\t", "real")
	if got != "real" {
		t.Errorf("got %q, want %q", got, "real")
	}
}

func TestFirstNonEmpty_AllEmpty(t *testing.T) {
	got := firstNonEmpty("", "  ", "\t")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFirstNonEmpty_NoArgs(t *testing.T) {
	got := firstNonEmpty()
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFirstNonEmpty_SingleValue(t *testing.T) {
	got := firstNonEmpty("only")
	if got != "only" {
		t.Errorf("got %q, want %q", got, "only")
	}
}

func TestWriteEmbeddedHUDPortFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	writeEmbeddedHUDPortFile(logger, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4312})

	portFile := filepath.Join(tmpDir, "loom", "hud.port")
	data, err := os.ReadFile(portFile)
	if err != nil {
		t.Fatalf("expected embedded HUD port file: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "4312" {
		t.Fatalf("port file contents = %q, want %q", got, "4312")
	}
}
