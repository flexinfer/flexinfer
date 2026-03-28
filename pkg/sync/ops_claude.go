// ops_claude.go — Claude platform sync: snapshot and ensure operations.
package sync

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type claudeConfigSnapshot struct {
	mcp      []byte
	settings []byte
}

func readClaudeConfigSnapshot(homePath string) claudeConfigSnapshot {
	return claudeConfigSnapshot{
		mcp:      readJSONSnapshot(filepath.Join(homePath, "mcp.json")),
		settings: readJSONSnapshot(filepath.Join(homePath, "settings.json")),
	}
}

func ensureClaudeConfigFiles(homePath string, snapshot claudeConfigSnapshot) error {
	var errs []string

	if err := ensureProfileJSONFile(homePath, "claude", "mcp.json", snapshot.mcp, []byte("{\"mcpServers\":{}}\n")); err != nil {
		errs = append(errs, fmt.Sprintf("mcp.json: %v", err))
	}
	if err := ensureProfileJSONFile(homePath, "claude", "settings.json", snapshot.settings, []byte("{}\n")); err != nil {
		errs = append(errs, fmt.Sprintf("settings.json: %v", err))
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
