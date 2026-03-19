package sync

import (
	"fmt"
	"os"
	"path/filepath"
)

// Profile defines the configuration for a specific tool profile.
type Profile struct {
	Name            string
	RepoDir         string // Relative to repo root
	HomeDir         string // Absolute path or relative to home
	WorkspaceDir    string // Additional workspace-relative dir to copy to (e.g., ".vscode")
	Excludes        []string
	SecretFiles     []string
	GeneratorTarget string // Target name for the generator (e.g. "codex")
	GeneratedFile   string // Filename generated (e.g. "config.toml")
	// HomeGeneratedFile overrides the primary generated filename when syncing
	// to home. This is useful when a platform expects a different filename than
	// the generated artifact kept in-repo.
	HomeGeneratedFile string
	// ExtraGeneratedFiles lists additional files produced by the generator
	// (e.g. "settings.json" for lifecycle hooks). These are synced alongside
	// GeneratedFile but are optional — missing extras are silently skipped.
	ExtraGeneratedFiles []string
	// GeneratedDirectToHome writes generated config files directly into the home
	// profile directory and treats the repo mirror as stale cache to be cleaned.
	GeneratedDirectToHome bool
	// SyncGeneratedOnly limits sync/backup/status to GeneratedFile only.
	// This is important for profiles whose HomeDir points at large application
	// directories (e.g. VS Code/Claude Desktop) where we only manage mcp.json.
	SyncGeneratedOnly bool
	SkillsTarget      string // Target name for skills generator (mirrors GeneratorTarget)
	SkillsManifest    string // Filename of skills manifest (e.g., ".loom-skills-manifest.json")
	// SkillsHomePath sets the final home skills base path used for ${SKILL_PATH}
	// resolution in generated skill instructions.
	SkillsHomePath string
	// SkillsDirectToHome generates skills directly into the home directory instead
	// of the repo directory. This avoids duplication when the CLI discovers skills
	// from both repo and home (e.g. Gemini CLI reading ~/.gemini/skills/ and
	// <repo>/.gemini/skills/ simultaneously).
	SkillsDirectToHome bool

	// DefaultLoomMode generates a single loom proxy entry instead of individual servers.
	// Useful for platforms that can't resolve template patterns at runtime (e.g. Claude Code).
	DefaultLoomMode bool
	// DefaultResolveSecrets resolves ${keychain:}, ${env:}, ${secret:} at generation time.
	// Useful for platforms that pass templates as literal strings (e.g. Codex, Kilocode).
	DefaultResolveSecrets bool
}

// Manager handles synchronization operations.
type Manager struct {
	RepoRoot   string
	HomeDir    string
	Profiles   map[string]*Profile
	SkipSkills bool // When true, skip skills generation during Regenerate
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
		Name:                  "codex",
		RepoDir:               ".codex",
		HomeDir:               ".codex",
		Excludes:              []string{"auth.json", "sessions", "backups"},
		SecretFiles:           []string{"auth.json"},
		GeneratorTarget:       "codex",
		GeneratedFile:         "config.toml",
		GeneratedDirectToHome: true,
		SyncGeneratedOnly:     true,
		SkillsTarget:          "codex",
		SkillsManifest:        ".loom-skills-manifest.json",
		SkillsDirectToHome:    true,
		SkillsHomePath:        "$HOME/.codex/skills",
		DefaultLoomMode:       true,
		DefaultResolveSecrets: true,
	}

	m.Profiles["kilocode"] = &Profile{
		Name:                  "kilocode",
		RepoDir:               ".kilocode",
		HomeDir:               ".kilocode",
		Excludes:              []string{"auth.json", "sessions", "backups"},
		SecretFiles:           []string{"auth.json"},
		GeneratorTarget:       "kilocode",
		GeneratedFile:         "config.toml",
		SyncGeneratedOnly:     true,
		SkillsTarget:          "kilocode",
		SkillsManifest:        ".loom-skills-manifest.json",
		SkillsDirectToHome:    true,
		SkillsHomePath:        "$HOME/.kilocode/skills",
		DefaultLoomMode:       true,
		DefaultResolveSecrets: true,
	}

	m.Profiles["claude"] = &Profile{
		Name:                "claude",
		RepoDir:             ".claude",
		HomeDir:             ".claude",
		Excludes:            []string{"auth.json", "sessions", "backups"},
		SecretFiles:         []string{"auth.json"},
		GeneratorTarget:     "claude", // Uses mcp.json format (same as vscode)
		GeneratedFile:       "mcp.json",
		ExtraGeneratedFiles: []string{"settings.json"}, // Lifecycle hooks
		SyncGeneratedOnly:   true,
		SkillsTarget:        "claude",
		SkillsManifest:      ".loom-skills-manifest.json",
		SkillsDirectToHome:  true,
		SkillsHomePath:      "$HOME/.claude/commands",
		DefaultLoomMode:     true,
	}

	m.Profiles["claude_desktop"] = &Profile{
		Name:              "claude_desktop",
		RepoDir:           "claude_desktop_config",
		HomeDir:           "Library/Application Support/Claude",
		Excludes:          []string{"backups"},
		SecretFiles:       []string{},
		GeneratorTarget:   "claude_desktop",
		GeneratedFile:     "claude_desktop_config.json",
		SyncGeneratedOnly: true,
		DefaultLoomMode:   true,
	}

	m.Profiles["gemini"] = &Profile{
		Name:                "gemini",
		RepoDir:             ".gemini",
		HomeDir:             ".gemini",
		Excludes:            []string{"auth.json", "sessions", "backups"},
		SecretFiles:         []string{"auth.json"},
		GeneratorTarget:     "gemini",
		GeneratedFile:       "config.toml",
		ExtraGeneratedFiles: []string{"settings.json"}, // Lifecycle hooks
		SyncGeneratedOnly:   true,
		SkillsTarget:        "gemini",
		SkillsManifest:      ".loom-skills-manifest.json",
		SkillsHomePath:      "$HOME/.gemini/skills",
		SkillsDirectToHome:  true,
		DefaultLoomMode:     true,
	}

	m.Profiles["antigravity"] = &Profile{
		Name:    "antigravity",
		RepoDir: ".antigravity",
		HomeDir: ".gemini/antigravity",
		Excludes: []string{
			"auth.json", "sessions", "backups", "extensions",
			"antigravity", "argv.json", "logs", "CachedData",
		},
		SecretFiles:         []string{"auth.json"},
		GeneratorTarget:     "antigravity", // Uses mcp.json format (VSCode fork)
		GeneratedFile:       "mcp.json",
		HomeGeneratedFile:   "mcp_config.json",
		ExtraGeneratedFiles: []string{"settings.json"}, // Stub for sync architecture consistency
		SyncGeneratedOnly:   true,
		SkillsTarget:        "gemini",
		SkillsManifest:      ".loom-skills-manifest.json",
		SkillsHomePath:      "$HOME/.gemini/antigravity/skills",
		SkillsDirectToHome:  true,
		DefaultLoomMode:     true,
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
		SecretFiles:       []string{},
		GeneratorTarget:   "vscode",
		GeneratedFile:     "mcp.json",
		SyncGeneratedOnly: true,
		DefaultLoomMode:   true,
	}

	m.Profiles["zed"] = &Profile{
		Name:               "zed",
		RepoDir:            ".zed",
		HomeDir:            "Library/Application Support/Zed",
		GeneratorTarget:    "zed",
		GeneratedFile:      "mcp.json",
		SyncGeneratedOnly:  true,
		SkillsTarget:       "zed",
		SkillsManifest:     ".loom-skills-manifest.json",
		SkillsDirectToHome: true,
		SkillsHomePath:     "$HOME/.config/zed/skills",
		DefaultLoomMode:    true,
	}

	m.Profiles["opencode"] = &Profile{
		Name:                "opencode",
		RepoDir:             ".opencode",
		HomeDir:             ".config/opencode",
		GeneratorTarget:     "opencode",
		GeneratedFile:       "opencode.json",
		ExtraGeneratedFiles: []string{filepath.Join("plugins", "loom-hooks.ts")},
		SyncGeneratedOnly:   true,
		SkillsTarget:        "opencode",
		SkillsManifest:      ".loom-skills-manifest.json",
		SkillsDirectToHome:  true,
		SkillsHomePath:      "$HOME/.config/opencode/skills",
		DefaultLoomMode:     true,
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
