package hud

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShaderInstallPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("home directory unavailable in test environment")
	}
	want := filepath.Join(home, ".config", "loom", "loom-vibrancy.glsl")
	if got := shaderInstallPath(); got != want {
		t.Fatalf("shaderInstallPath() = %q, want %q", got, want)
	}
}

func TestGenerateGhosttyConfig_HasQuotedPaths(t *testing.T) {
	cfg := GenerateGhosttyConfig()
	if !strings.Contains(cfg, `quick-terminal-size = 380px`) {
		t.Fatalf("expected quick-terminal-size to include px unit in config:\n%s", cfg)
	}
	if !strings.Contains(cfg, `# quick-terminal-command = "loom hud --tui"`) {
		t.Fatalf("expected commented quoted quick-terminal command in config:\n%s", cfg)
	}

	shaderPath := shaderInstallPath()
	wantShader := fmt.Sprintf("custom-shader = %q", shaderPath)
	if !strings.Contains(cfg, wantShader) {
		t.Fatalf("expected quoted custom-shader path %q in config", wantShader)
	}
}
