package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/crb2nu/loom/pkg/generator"
	"github.com/crb2nu/loom/pkg/registry"
)

// SyncToHome syncs configuration from repo to home directory.
func (m *Manager) SyncToHome(profileName string, backup bool, regen bool, repoOnly bool, hubMode bool, hubURL string, loomMode bool, loomBinary string) error {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return err
	}

	repoPath := m.ResolveRepoPath(p)
	homePath := m.ResolveHomePath(p)

	if regen {
		if err := m.Regenerate(p, hubMode, hubURL, loomMode, loomBinary); err != nil {
			return fmt.Errorf("regenerate failed: %w", err)
		}
	}

	if repoOnly {
		fmt.Printf("Skipping sync to home for %s (repo-only)\n", profileName)
		return nil
	}

	if !Exists(repoPath) {
		return fmt.Errorf("repo directory not found: %s", repoPath)
	}

	fmt.Printf("Syncing %s -> %s\n", repoPath, homePath)

	if backup && Exists(homePath) {
		if err := m.Backup(profileName, "home"); err != nil {
			return fmt.Errorf("backup failed: %w", err)
		}
	}

	if err := CopyDir(repoPath, homePath, p.Excludes); err != nil {
		return err
	}

	// Also copy to workspace directory if specified (e.g., .vscode/ for local MCP config)
	if p.WorkspaceDir != "" {
		workspacePath := filepath.Join(m.RepoRoot, p.WorkspaceDir)
		// Only copy the generated file, not the whole directory
		srcFile := filepath.Join(repoPath, p.GeneratedFile)
		if Exists(srcFile) {
			if err := os.MkdirAll(workspacePath, 0755); err != nil {
				return fmt.Errorf("create workspace dir: %w", err)
			}
			dstFile := filepath.Join(workspacePath, p.GeneratedFile)
			if err := CopyFile(srcFile, dstFile); err != nil {
				return fmt.Errorf("copy to workspace: %w", err)
			}
			fmt.Printf("Also copied to %s\n", dstFile)
		}
	}

	return nil
}

// SyncAll syncs all profiles.
func (m *Manager) SyncAll(backup bool, regen bool, repoOnly bool, hubMode bool, hubURL string, loomMode bool, loomBinary string) error {
	var names []string
	for name := range m.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := m.SyncToHome(name, backup, regen, repoOnly, hubMode, hubURL, loomMode, loomBinary); err != nil {
			return fmt.Errorf("sync %s: %w", name, err)
		}
	}
	return nil
}

// Regenerate generates the configuration for a profile and updates the repo directory.
func (m *Manager) Regenerate(p *Profile, hubMode bool, hubURL string, loomMode bool, loomBinary string) error {
	if p.GeneratorTarget == "" {
		return fmt.Errorf("profile %s has no generator target", p.Name)
	}

	// Load registry - prefer local override, then home directory
	regPath := registry.FindRegistryOrDefault(filepath.Join(m.RepoRoot, "mcp", "context", "registry.yaml"))
	reg, err := registry.Load(regPath)
	if err != nil {
		return fmt.Errorf("load registry from %s: %w", regPath, err)
	}
	fmt.Printf("Using registry: %s\n", regPath)

	// Create temp dir
	tmpDir, err := os.MkdirTemp("", "loom-gen")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// Generate
	fmt.Printf("Regenerating config for %s...\n", p.Name)
	err = generator.GenerateConfigsWithPath(reg, regPath, tmpDir, []string{p.GeneratorTarget}, hubMode, hubURL, loomMode, loomBinary)
	if err != nil {
		return err
	}

	// Copy generated file to repo dir
	genPath := filepath.Join(tmpDir, p.GeneratorTarget, p.GeneratedFile)
	if !Exists(genPath) {
		// Try without target subdir (some generators might behave differently? No, configs.go uses target subdir)
		// Wait, configs.go: destDir := filepath.Join(outputDir, target) or "vscode" or "claude_desktop"
		return fmt.Errorf("generated file not found: %s", genPath)
	}

	repoPath := m.ResolveRepoPath(p)
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		return err
	}

	destFile := filepath.Join(repoPath, p.GeneratedFile)
	if err := CopyFile(genPath, destFile); err != nil {
		return err
	}

	fmt.Printf("Updated %s\n", destFile)
	return nil
}

// PullFromHome pulls configuration from home directory to repo.
func (m *Manager) PullFromHome(profileName string, backup bool) error {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return err
	}

	repoPath := m.ResolveRepoPath(p)
	homePath := m.ResolveHomePath(p)

	if !Exists(homePath) {
		return fmt.Errorf("home directory not found: %s", homePath)
	}

	fmt.Printf("Pulling %s -> %s\n", homePath, repoPath)

	if backup && Exists(repoPath) {
		if err := m.Backup(profileName, "repo"); err != nil {
			return fmt.Errorf("backup failed: %w", err)
		}
	}

	return CopyDir(homePath, repoPath, p.Excludes)
}

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

	// Determine backup dir (usually ~/.<profile>/backups)
	// But scripts put them in ~/.codex/backups for codex.
	// Let's standardize on ~/.config/loom/backups/<profile> or keep it local?
	// The bash script used $HOME/.codex/backups.

	backupRoot := filepath.Join(m.ResolveHomePath(p), "backups")
	if p.Name == "claude_desktop" {
		// Claude desktop backups might need a different place if home dir is Application Support
		backupRoot = filepath.Join(m.HomeDir, ".config", "loom", "backups", "claude_desktop")
	}

	timestamp := time.Now().Format("20060102_150405")
	backupPath := filepath.Join(backupRoot, fmt.Sprintf("%s_%s_%s", p.Name, source, timestamp))

	fmt.Printf("Creating backup at %s\n", backupPath)

	// Merge default excludes with profile excludes
	excludes := []string{"backups", "sessions"}
	excludes = append(excludes, p.Excludes...)

	return CopyDir(srcPath, backupPath, excludes)
}

// Validate checks if the configuration is valid.
func (m *Manager) Validate(profileName string) error {
	p, err := m.GetProfile(profileName)
	if err != nil {
		return err
	}

	homePath := m.ResolveHomePath(p)
	if !Exists(homePath) {
		return fmt.Errorf("config directory not found: %s", homePath)
	}

	// Check for config.toml or mcp.json
	configToml := filepath.Join(homePath, "config.toml")
	mcpJson := filepath.Join(homePath, "mcp.json")
	claudeJson := filepath.Join(homePath, "claude_desktop_config.json")

	found := false
	if Exists(configToml) {
		fmt.Printf("✓ Found config.toml\n")
		found = true
	}
	if Exists(mcpJson) {
		fmt.Printf("✓ Found mcp.json\n")
		found = true
	}
	if Exists(claudeJson) {
		fmt.Printf("✓ Found claude_desktop_config.json\n")
		found = true
	}

	if !found {
		return fmt.Errorf("no configuration file found in %s", homePath)
	}

	return nil
}
