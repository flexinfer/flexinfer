package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	loomExecutablePath = os.Executable
	loomLookPath       = exec.LookPath
	loomUserHomeDir    = os.UserHomeDir
)

func resolveStableLoomBinary(explicit string) string {
	if trimmed := strings.TrimSpace(explicit); trimmed != "" {
		return trimmed
	}

	exe := ""
	if resolved, err := loomExecutablePath(); err == nil && strings.TrimSpace(resolved) != "" {
		exe = strings.TrimSpace(resolved)
		if !isEphemeralGoRunBinary(exe) {
			return exe
		}
	}

	if path, err := loomLookPath("loom"); err == nil && strings.TrimSpace(path) != "" && !isEphemeralGoRunBinary(path) {
		return strings.TrimSpace(path)
	}

	if home, err := loomUserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		candidate := filepath.Join(strings.TrimSpace(home), ".local", "bin", "loom")
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}

	return exe
}

func isEphemeralGoRunBinary(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if clean == "" {
		return false
	}
	return strings.Contains(clean, "/go-build") && strings.Contains(clean, "/exe/")
}
