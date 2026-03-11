// Package registry provides catalog state management for enabling/disabling servers.
package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// CatalogState tracks which servers the user has explicitly disabled.
// Servers not listed in DisabledServers are enabled by default.
type CatalogState struct {
	DisabledServers []string `yaml:"disabled_servers,omitempty"`
}

// CatalogStatePath returns the path to the user's catalog state file.
func CatalogStatePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "loom", "catalog-state.yaml")
}

// LoadCatalogState loads the catalog state from disk.
// Returns an empty state (all servers enabled) if the file does not exist.
func LoadCatalogState() (*CatalogState, error) {
	path := CatalogStatePath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &CatalogState{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read catalog state: %w", err)
	}

	var cs CatalogState
	if err := yaml.Unmarshal(data, &cs); err != nil {
		return nil, fmt.Errorf("parse catalog state: %w", err)
	}
	return &cs, nil
}

// Save writes the catalog state to disk.
func (cs *CatalogState) Save() error {
	path := CatalogStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(cs)
	if err != nil {
		return fmt.Errorf("marshal catalog state: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// IsDisabled returns true if the named server is explicitly disabled.
func (cs *CatalogState) IsDisabled(name string) bool {
	for _, s := range cs.DisabledServers {
		if s == name {
			return true
		}
	}
	return false
}

// Enable removes a server from the disabled list.
// Returns true if the server was previously disabled.
func (cs *CatalogState) Enable(name string) bool {
	for i, s := range cs.DisabledServers {
		if s == name {
			cs.DisabledServers = append(cs.DisabledServers[:i], cs.DisabledServers[i+1:]...)
			return true
		}
	}
	return false
}

// Disable adds a server to the disabled list.
// Returns true if the server was not already disabled.
func (cs *CatalogState) Disable(name string) bool {
	if cs.IsDisabled(name) {
		return false
	}
	cs.DisabledServers = append(cs.DisabledServers, name)
	sort.Strings(cs.DisabledServers)
	return true
}

// EnabledServers returns servers from the registry that are not disabled.
func (cs *CatalogState) EnabledServers(reg *Registry) []*Server {
	if reg == nil {
		return nil
	}
	result := make([]*Server, 0, len(reg.Servers))
	for _, srv := range reg.Servers {
		if srv != nil && !cs.IsDisabled(srv.Name) {
			result = append(result, srv)
		}
	}
	return result
}
