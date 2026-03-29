// ops_validate.go — Validation and backup operations: Backup, Validate.
package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/crb2nu/loom/pkg/validator"
)

// Backup creates a timestamped backup of the profile configuration.
func (m *Manager) Backup(profileName string, source string) error {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return err
	}

	var srcPath string
	if source == "repo" {
		srcPath = m.ResolveRepoPath(p)
	} else {
		srcPath = m.ResolveHomePath(p)
	}

	if !Exists(srcPath) {
		return fmt.Errorf("source not found: %s", srcPath)
	}

	backupRoot := filepath.Join(m.ResolveHomePath(p), "backups")
	if p.SyncGeneratedOnly {
		// Avoid writing backups into large application config directories (VS Code, Claude Desktop, etc.)
		backupRoot = filepath.Join(m.HomeDir, ".config", "loom", "backups", p.Name)
	}

	timestamp := time.Now().Format("20060102_150405")
	backupPath := filepath.Join(backupRoot, fmt.Sprintf("%s_%s_%s", p.Name, source, timestamp))

	fmt.Printf("Creating backup at %s\n", backupPath)

	if p.SyncGeneratedOnly {
		if p.GeneratedFile == "" {
			return fmt.Errorf("profile %s has no generated file", p.Name)
		}
		if err := os.MkdirAll(backupPath, 0755); err != nil {
			return err
		}
		fileName := p.GeneratedFile
		if source != "repo" {
			fileName = primaryHomeGeneratedFile(p)
		}
		srcFile := filepath.Join(srcPath, fileName)
		if !Exists(srcFile) {
			return fmt.Errorf("generated file not found: %s", srcFile)
		}
		return CopyFile(srcFile, filepath.Join(backupPath, fileName))
	}

	// Merge default excludes with profile excludes
	excludes := []string{"backups", "sessions"}
	excludes = append(excludes, p.Excludes...)

	return CopyDir(srcPath, backupPath, excludes)
}

// Validate checks if the configuration is valid using schema and runtime validation.
func (m *Manager) Validate(profileName string) error {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return err
	}

	homePath := m.ResolveHomePath(p)
	if !Exists(homePath) {
		return fmt.Errorf("config directory not found: %s", homePath)
	}

	// Determine config file based on profile
	var configFile string
	var target string
	switch p.GeneratorTarget {
	case "codex", "kilocode", "gemini":
		configFile = filepath.Join(homePath, "config.toml")
		target = p.GeneratorTarget
	case "claude_desktop":
		configFile = filepath.Join(homePath, "claude_desktop_config.json")
		target = "claude_desktop"
	case "claude", "vscode", "antigravity":
		fileName := primaryHomeGeneratedFile(p)
		if fileName == "" {
			fileName = "mcp.json"
		}
		configFile = filepath.Join(homePath, fileName)
		target = p.GeneratorTarget
	default:
		// Fallback: check for known config files
		if Exists(filepath.Join(homePath, "config.toml")) {
			configFile = filepath.Join(homePath, "config.toml")
			target = "codex"
		} else if Exists(filepath.Join(homePath, "mcp_config.json")) {
			configFile = filepath.Join(homePath, "mcp_config.json")
			target = "antigravity"
		} else if Exists(filepath.Join(homePath, "mcp.json")) {
			configFile = filepath.Join(homePath, "mcp.json")
			target = "claude"
		} else if Exists(filepath.Join(homePath, "claude_desktop_config.json")) {
			configFile = filepath.Join(homePath, "claude_desktop_config.json")
			target = "claude_desktop"
		}
	}

	if configFile == "" || !Exists(configFile) {
		return fmt.Errorf("no configuration file found in %s", homePath)
	}

	fmt.Printf("Validating %s...\n", configFile)

	// Perform schema and runtime validation
	v := validator.New(m.RepoRoot, m.HomeDir)
	result, err := v.ValidateFile(target, configFile)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Print validation results
	if result.Valid && !result.HasWarnings() {
		fmt.Printf("✓ Configuration is valid\n")
		return nil
	}

	for _, verr := range result.Errors {
		if verr.Severity == validator.SeverityError {
			fmt.Fprintf(os.Stderr, "ERROR %s: %s\n", verr.Field, verr.Message)
		} else {
			fmt.Fprintf(os.Stderr, "WARN  %s: %s\n", verr.Field, verr.Message)
		}
	}

	if result.HasErrors() {
		return fmt.Errorf("configuration has %d error(s)", result.ErrorCount())
	}

	fmt.Printf("✓ Configuration is valid (with %d warning(s))\n", result.WarningCount())
	return nil
}
