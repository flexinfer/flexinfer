// Package registry loads and parses the MCP server registry.
package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds the loom configuration from ~/.config/loom/config.yaml
type Config struct {
	RepoRoot string `yaml:"repo_root"`
	Hub      struct {
		URL     string `yaml:"url"`
		Enabled bool   `yaml:"enabled"`
	} `yaml:"hub"`
	Debug bool `yaml:"debug"`
}

// LoadConfig loads the loom config from ~/.config/loom/config.yaml
func LoadConfig() (*Config, error) {
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".config", "loom", "config.yaml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return &Config{}, nil // Return empty config if not found
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return &cfg, nil
}

// GetRepoRoot returns the repository root path (workspace root, not loom-core).
// The registry uses ${repo} to mean the workspace root (e.g., ~/workspace),
// since paths in registry are like ${repo}/services/loom-core/bin/...
// Priority:
// 1. Explicit repo_root from config.yaml
// 2. If registry was loaded from ./mcp/context/registry.yaml, derive from that
// 3. Default to ~/workspace
func GetRepoRoot(registryPath string) string {
	home, _ := os.UserHomeDir()

	// Check config.yaml first
	cfg, err := LoadConfig()
	if err == nil && cfg.RepoRoot != "" {
		if strings.HasPrefix(cfg.RepoRoot, "~/") {
			return filepath.Join(home, cfg.RepoRoot[2:])
		}
		return cfg.RepoRoot
	}

	// If registry is in mcp/context/registry.yaml, repo root is two levels up
	if strings.HasSuffix(registryPath, filepath.Join("mcp", "context", "registry.yaml")) {
		return filepath.Dir(filepath.Dir(filepath.Dir(registryPath)))
	}

	// Default to workspace root (not loom-core, since registry paths include services/loom-core)
	return filepath.Join(home, "workspace")
}

// Registry holds the parsed registry configuration.
type Registry struct {
	Version int       `yaml:"version"`
	Servers []*Server `yaml:"servers"`
}

// Server defines an MCP server in the registry.
type Server struct {
	Name       string                 `yaml:"name"`
	Categories []string               `yaml:"categories,omitempty"`
	Common     *TargetSpec            `yaml:"common,omitempty"`
	Targets    map[string]*TargetSpec `yaml:"targets,omitempty"`
}

// TargetSpec defines a server's configuration for a specific target.
type TargetSpec struct {
	Description string            `yaml:"description,omitempty"`
	Command     string            `yaml:"command,omitempty"`
	Args        []any             `yaml:"args,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
	Hint        string            `yaml:"hint,omitempty"`
	Timeout     int               `yaml:"timeout,omitempty"`
	AlwaysAllow []string          `yaml:"always_allow,omitempty"`
	Type        string            `yaml:"type,omitempty"`
}

// Load reads and parses a registry YAML file.
func Load(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}

	var reg Registry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}

	return &reg, nil
}

// FindDefaultPath returns the default registry path in a gitops workspace.
func FindDefaultPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, "mcp", "context", "registry.yaml")
}

// FindRegistry searches for a registry file with the following priority:
// 1. Local directory override: ./mcp/context/registry.yaml
// 2. Home directory default: ~/.config/loom/registry.yaml
// 3. Legacy workspace paths as fallback
// Returns the path and whether it was found.
func FindRegistry() (string, bool) {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()

	candidates := []string{
		// Local override (current directory)
		filepath.Join(cwd, "mcp", "context", "registry.yaml"),
		// Home directory default
		filepath.Join(home, ".config", "loom", "registry.yaml"),
		// Legacy paths for backwards compatibility
		filepath.Join(home, "workspace", "gitops", "mcp", "context", "registry.yaml"),
		filepath.Join(home, "workspace", "platform", "gitops", "mcp", "context", "registry.yaml"),
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}

	return "", false
}

// FindRegistryOrDefault returns a registry path, using FindRegistry with a fallback to
// the provided default path if no registry is found.
func FindRegistryOrDefault(defaultPath string) string {
	if path, found := FindRegistry(); found {
		return path
	}
	return defaultPath
}

// GetServerSpec returns the effective spec for a server and target, merging common with target-specific config.
func (r *Registry) GetServerSpec(serverName, target string) (*TargetSpec, error) {
	var server *Server
	for _, s := range r.Servers {
		if s.Name == serverName {
			server = s
			break
		}
	}
	if server == nil {
		return nil, fmt.Errorf("server %q not found", serverName)
	}

	// Start with common config
	spec := &TargetSpec{}
	if server.Common != nil {
		*spec = *server.Common
	}

	// Merge target-specific config
	if target != "" && server.Targets != nil {
		if targetSpec, ok := server.Targets[target]; ok {
			mergeSpec(spec, targetSpec)
		}
	}

	return spec, nil
}

// mergeSpec merges src into dst, with src values taking precedence.
func mergeSpec(dst, src *TargetSpec) {
	if src.Description != "" {
		dst.Description = src.Description
	}
	if src.Command != "" {
		dst.Command = src.Command
	}
	if len(src.Args) > 0 {
		dst.Args = src.Args
	}
	if src.Env != nil {
		if dst.Env == nil {
			dst.Env = make(map[string]string)
		}
		for k, v := range src.Env {
			dst.Env[k] = v
		}
	}
	if src.Hint != "" {
		dst.Hint = src.Hint
	}
	if src.Timeout > 0 {
		dst.Timeout = src.Timeout
	}
	if len(src.AlwaysAllow) > 0 {
		dst.AlwaysAllow = src.AlwaysAllow
	}
	if src.Type != "" {
		dst.Type = src.Type
	}
}

// ListServers returns all server names in the registry.
func (r *Registry) ListServers() []string {
	names := make([]string, len(r.Servers))
	for i, s := range r.Servers {
		names[i] = s.Name
	}
	return names
}

// ListServersByCategory returns servers that have the given category.
func (r *Registry) ListServersByCategory(category string) []*Server {
	var result []*Server
	for _, s := range r.Servers {
		for _, c := range s.Categories {
			if strings.EqualFold(c, category) {
				result = append(result, s)
				break
			}
		}
	}
	return result
}

// IsLocalOnly returns true if the server should only run locally.
func (s *Server) IsLocalOnly() bool {
	for _, c := range s.Categories {
		if strings.EqualFold(c, "local-only") || strings.EqualFold(c, "filesystem") {
			return true
		}
	}
	return false
}

// IsHubCapable returns true if the server can run on the hub.
func (s *Server) IsHubCapable() bool {
	for _, c := range s.Categories {
		if strings.EqualFold(c, "hub") || strings.EqualFold(c, "cloud") {
			return true
		}
	}
	return !s.IsLocalOnly()
}
