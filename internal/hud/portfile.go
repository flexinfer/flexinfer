package hud

import (
	"fmt"
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

// WritePortFile persists the active HUD port for CLI discovery.
func WritePortFile(port int) (string, error) {
	portFile := PortFilePath()
	if err := os.MkdirAll(filepath.Dir(portFile), 0o755); err != nil {
		return portFile, fmt.Errorf("create port file dir: %w", err)
	}
	if err := os.WriteFile(portFile, []byte(fmt.Sprintf("%d", port)), 0o644); err != nil {
		return portFile, fmt.Errorf("write port file: %w", err)
	}
	return portFile, nil
}

// RemovePortFile deletes the HUD port file if it exists.
func RemovePortFile() error {
	portFile := PortFilePath()
	if err := os.Remove(portFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
