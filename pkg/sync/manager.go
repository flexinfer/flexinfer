package sync

import (
	"fmt"
	"os"
	"path/filepath"
)

// Profile defines the configuration for a specific tool profile.
type Profile struct {
	Name            string
	RepoDir         string   // Relative to repo root
	HomeDir         string   // Absolute path or relative to home
	WorkspaceDir    string   // Additional workspace-relative dir to copy to (e.g., ".vscode")
	Excludes        []string
	SecretFiles     []string
	GeneratorTarget string // Target name for the generator (e.g. "codex")
	GeneratedFile   string // Filename generated (e.g. "config.toml")
}

// Manager handles synchronization operations.
type Manager struct {
	RepoRoot string
	HomeDir  string
	Profiles map[string]*Profile
}

// NewManager creates a new sync manager.
func NewManager(repoRoot string) (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get user home: %w", err)
	}

	m := &Manager{
		RepoRoot: repoRoot,
		HomeDir:  home,
		Profiles: make(map[string]*Profile),
	}

	// Register default profiles
	m.registerProfiles()

	return m, nil
}

func (m *Manager) registerProfiles() {
	m.Profiles["codex"] = &Profile{
		Name:            "codex",
		RepoDir:         ".codex",
		HomeDir:         ".codex",
		Excludes:        []string{"auth.json", "sessions", "backups"},
		SecretFiles:     []string{"auth.json"},
		GeneratorTarget: "codex",
		GeneratedFile:   "config.toml",
	}

	m.Profiles["kilocode"] = &Profile{
		Name:            "kilocode",
		RepoDir:         ".kilocode",
		HomeDir:         ".kilocode",
		Excludes:        []string{"auth.json", "sessions", "backups"},
		SecretFiles:     []string{"auth.json"},
		GeneratorTarget: "kilocode",
		GeneratedFile:   "config.toml",
	}

	m.Profiles["claude"] = &Profile{
		Name:            "claude",
		RepoDir:         ".claude",
		HomeDir:         ".claude",
		Excludes:        []string{"auth.json", "sessions", "backups"},
		SecretFiles:     []string{"auth.json"},
		GeneratorTarget: "claude", // Uses mcp.json format (same as vscode)
		GeneratedFile:   "mcp.json",
	}

	m.Profiles["claude_desktop"] = &Profile{
		Name:            "claude_desktop",
		RepoDir:         "claude_desktop_config",
		HomeDir:         "Library/Application Support/Claude",
		Excludes:        []string{"backups"},
		SecretFiles:     []string{},
		GeneratorTarget: "claude_desktop",
		GeneratedFile:   "claude_desktop_config.json",
	}

	m.Profiles["gemini"] = &Profile{
		Name:            "gemini",
		RepoDir:         ".gemini",
		HomeDir:         ".gemini",
		Excludes:        []string{"auth.json", "sessions", "backups"},
		SecretFiles:     []string{"auth.json"},
		GeneratorTarget: "gemini",
		GeneratedFile:   "config.toml",
	}

	m.Profiles["antigravity"] = &Profile{
		Name:    "antigravity",
		RepoDir: ".antigravity",
		HomeDir: ".antigravity",
		Excludes: []string{
			"auth.json", "sessions", "backups", "extensions",
			"antigravity", "argv.json", "logs", "CachedData",
		},
		SecretFiles:     []string{"auth.json"},
		GeneratorTarget: "antigravity", // Uses mcp.json format (VSCode fork)
		GeneratedFile:   "mcp.json",
	}

	m.Profiles["vscode"] = &Profile{
		Name: "vscode",
		// Generated config goes to .vscode-mcp/ first, then synced to:
		// 1. ~/Library/Application Support/Code/User (global VSCode config)
		// 2. .vscode/ in workspace (for VSCode MCP extension to find)
		RepoDir:      ".vscode-mcp",
		HomeDir:      "Library/Application Support/Code/User",
		WorkspaceDir: ".vscode", // Also copy to workspace .vscode/ for local MCP config
		Excludes: []string{
			"globalStorage",
			"workspaceStorage",
			"History",
			"caches",
			"CachedData",
			"logs",
		},
		SecretFiles:     []string{},
		GeneratorTarget: "vscode",
		GeneratedFile:   "mcp.json",
	}
}

// GetProfile returns a profile by name.
func (m *Manager) GetProfile(name string) (*Profile, error) {
	p, ok := m.Profiles[name]
	if !ok {
		return nil, fmt.Errorf("profile not found: %s", name)
	}
	return p, nil
}

// Get returns a profile by name, or nil if not found.
func (m *Manager) Get(name string) *Profile {
	return m.Profiles[name]
}

// List returns all profile names.
func (m *Manager) List() []string {
	names := make([]string, 0, len(m.Profiles))
	for name := range m.Profiles {
		names = append(names, name)
	}
	return names
}

// ResolveRepoPath returns the absolute path to the repo directory for a profile.
func (m *Manager) ResolveRepoPath(p *Profile) string {
	return filepath.Join(m.RepoRoot, p.RepoDir)
}

// ResolveHomePath returns the absolute path to the home directory for a profile.
func (m *Manager) ResolveHomePath(p *Profile) string {
	if filepath.IsAbs(p.HomeDir) {
		return p.HomeDir
	}
	return filepath.Join(m.HomeDir, p.HomeDir)
}
