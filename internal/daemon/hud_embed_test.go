package daemon

import (
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvBoolTrue(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"true", true}, {"TRUE", true}, {"True", true},
		{"1", true},
		{"yes", true}, {"YES", true},
		{"on", true},
		{"  true  ", true},
		{"", false},
		{"0", false},
		{"false", false},
		{"no", false},
		{"off", false},
		{"enabled", false}, // explicit allowlist — not a fuzzy match
		{"1.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := envBoolTrue(tt.in); got != tt.want {
				t.Errorf("envBoolTrue(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

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

func TestFirstPositiveInt(t *testing.T) {
	if got := firstPositiveInt(2, "3"); got != 2 {
		t.Fatalf("configured value should win, got %d", got)
	}
	if got := firstPositiveInt(0, "3"); got != 3 {
		t.Fatalf("env value not parsed, got %d", got)
	}
	if got := firstPositiveInt(0, "-1"); got != 0 {
		t.Fatalf("non-positive env value should be ignored, got %d", got)
	}
}

func TestFirstPositiveFloat(t *testing.T) {
	if got := firstPositiveFloat(0.5, "1.5"); got != 0.5 {
		t.Fatalf("configured value should win, got %f", got)
	}
	if got := firstPositiveFloat(0, "0.75"); got != 0.75 {
		t.Fatalf("env value not parsed, got %f", got)
	}
	if got := firstPositiveFloat(0, "nope"); got != 0 {
		t.Fatalf("invalid env value should be ignored, got %f", got)
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
