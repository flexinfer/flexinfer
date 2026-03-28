// ops_codex.go — Codex platform sync: snapshot and ensure operations.
package sync

import (
	"fmt"
	"path/filepath"
)

type codexConfigSnapshot struct {
	config []byte
}

func readCodexConfigSnapshot(homePath string) codexConfigSnapshot {
	return codexConfigSnapshot{
		config: readTOMLSnapshot(filepath.Join(homePath, "config.toml")),
	}
}

func ensureCodexConfigFiles(homePath string, snapshot codexConfigSnapshot) error {
	if err := ensureProfileTOMLFile(homePath, "codex", "config.toml", snapshot.config, []byte("[mcp_servers]\n")); err != nil {
		return fmt.Errorf("config.toml: %w", err)
	}
	return nil
}
