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
	Version    int               `yaml:"version"`
	EnvAliases map[string]EnvVar `yaml:"env_aliases,omitempty"`
	Servers    []*Server         `yaml:"servers"`
}

// EnvVar defines an environment variable with fallback names.
type EnvVar struct {
	// Fallbacks are alternative env var names to try if the primary is empty.
	// Checked in order until one is found.
	Fallbacks []string `yaml:"fallbacks,omitempty"`
}

// Server defines an MCP server in the registry.
type Server struct {
	Name       string                 `yaml:"name"`
	Categories []string               `yaml:"categories,omitempty"`
	Common     *TargetSpec            `yaml:"common,omitempty"`
	Targets    map[string]*TargetSpec `yaml:"targets,omitempty"`
}

// ToolSchema defines a tool's schema for static tool advertisement.
type ToolSchema struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description,omitempty"`
	InputSchema InputSchema `yaml:"inputSchema,omitempty"`
}

// InputSchema defines the JSON Schema for tool inputs.
type InputSchema struct {
	Type       string         `yaml:"type"`
	Properties map[string]any `yaml:"properties,omitempty"`
	Required   []string       `yaml:"required,omitempty"`
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
	Tools       []ToolSchema      `yaml:"tools,omitempty"` // Static tool schemas for instant availability
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
	if len(src.Tools) > 0 {
		dst.Tools = src.Tools
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

// GetStaticTools returns all static tool schemas from the registry for a given target.
// Tools are namespaced as server__toolname for MCP compatibility.
// Returns tools with their full schemas ready for tools/list response.
func (r *Registry) GetStaticTools(target string) []ToolSchema {
	var tools []ToolSchema

	for _, server := range r.Servers {
		spec, err := r.GetServerSpec(server.Name, target)
		if err != nil || spec == nil {
			continue
		}

		// Namespace each tool with server name
		for _, tool := range spec.Tools {
			namespacedTool := ToolSchema{
				Name:        server.Name + "__" + tool.Name,
				Description: tool.Description,
				InputSchema: tool.InputSchema,
			}
			tools = append(tools, namespacedTool)
		}
	}

	return tools
}

// HasStaticTools returns true if any server has static tool schemas defined.
func (r *Registry) HasStaticTools() bool {
	for _, server := range r.Servers {
		if server.Common != nil && len(server.Common.Tools) > 0 {
			return true
		}
		for _, spec := range server.Targets {
			if spec != nil && len(spec.Tools) > 0 {
				return true
			}
		}
	}
	return false
}

// ResolveEnv looks up an environment variable, checking fallback aliases if defined.
// Returns the value and whether it was found.
func (r *Registry) ResolveEnv(name string) (string, bool) {
	// Try primary name first
	if val := os.Getenv(name); val != "" {
		return val, true
	}

	// Check if we have fallback aliases
	if r.EnvAliases != nil {
		if alias, ok := r.EnvAliases[name]; ok {
			for _, fallback := range alias.Fallbacks {
				if val := os.Getenv(fallback); val != "" {
					return val, true
				}
			}
		}
	}

	return "", false
}

// GetEnvWithFallback returns the value for an env var, checking fallbacks.
// Returns empty string if not found.
func (r *Registry) GetEnvWithFallback(name string) string {
	val, _ := r.ResolveEnv(name)
	return val
}

// DefaultEnvAliases returns commonly used environment variable aliases.
// These are used as defaults if no env_aliases section is defined.
func DefaultEnvAliases() map[string]EnvVar {
	return map[string]EnvVar{
		"GITLAB_PERSONAL_ACCESS_TOKEN": {Fallbacks: []string{"GITLAB_PAT", "GITLAB_TOKEN"}},
		"GRAFANA_API_TOKEN":            {Fallbacks: []string{"GRAFANA_API_KEY", "GRAFANA_TOKEN"}},
		"MORPH_QDRANT_API_KEY":         {Fallbacks: []string{"MORPH_API_KEY", "QDRANT_API_KEY"}},
		"QDRANT_API_KEY":               {Fallbacks: []string{"MORPH_API_KEY"}},
		"GITHUB_TOKEN":                 {Fallbacks: []string{"GITHUB_PERSONAL_ACCESS_TOKEN", "GH_TOKEN"}},
		"GITHUB_PERSONAL_ACCESS_TOKEN": {Fallbacks: []string{"GITHUB_TOKEN", "GH_TOKEN"}},
	}
}

// MergeDefaultAliases merges default aliases into the registry's env_aliases.
// Registry-defined aliases take precedence over defaults.
func (r *Registry) MergeDefaultAliases() {
	defaults := DefaultEnvAliases()
	if r.EnvAliases == nil {
		r.EnvAliases = defaults
		return
	}
	// Add defaults that aren't already defined
	for name, alias := range defaults {
		if _, exists := r.EnvAliases[name]; !exists {
			r.EnvAliases[name] = alias
		}
	}
}
