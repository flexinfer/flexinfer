package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func defaultSocketPath() string {
	home, _ := os.UserHomeDir()
	socketPath := filepath.Join(home, ".config", "loom", "loom.sock")
	if envSocket := os.Getenv("LOOM_SOCKET"); envSocket != "" {
		socketPath = envSocket
	}
	return socketPath
}

func reloadDaemonAfterSecretChange(socketPath, reason string) {
	if socketPath == "" {
		socketPath = defaultSocketPath()
	}
	if _, err := call(socketPath, "loom/reload", nil); err != nil {
		fmt.Fprintf(os.Stderr, "Note: daemon reload skipped after %s: %v\n", reason, err)
		return
	}
	fmt.Fprintf(os.Stderr, "Daemon reloaded after %s\n", reason)
}
