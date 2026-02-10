package hud

import (
	"os"
	"path/filepath"
)

// PortFilePath returns the path to the HUD port file.
// The port file is written by the HUD after binding and removed on shutdown.
// CLI commands read it to discover the HUD port dynamically.
//
// Path: ~/.config/loom/hud.port (next to the daemon socket).
func PortFilePath() string {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "loom", "hud.port")
}
