// Package registry loads and parses the MCP server registry.
package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Registry holds the parsed registry configuration.
type Registry struct {
	Version int       `yaml:"version"`
	Servers []*Server `yaml:"servers"`
}

// Server defines an MCP server in the registry.
type Server struct {
	Name       string            `yaml:"name"`
	Categories []string          `yaml:"categories,omitempty"`
	Common     *TargetSpec       `yaml:"common,omitempty"`
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
