// Package daemon provides the persistent tool manifest.
package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/crb2nu/loom/pkg/mcp"
	"gopkg.in/yaml.v3"
)

// ToolManifest persists the aggregated tool list across daemon restarts.
// This enables instant tool availability on startup without spawning servers.
type ToolManifest struct {
	Version   int                       `yaml:"version"`
	UpdatedAt time.Time                 `yaml:"updated_at"`
	Servers   map[string]ServerManifest `yaml:"servers"`
}

// ServerManifest stores cached tools for a single server.
type ServerManifest struct {
	Tools     []mcp.Tool `yaml:"tools"`
	FetchedAt time.Time  `yaml:"fetched_at"`
	Hash      string     `yaml:"hash"` // Hash of tools for change detection
}

// ManifestManager handles loading and saving the tool manifest.
type ManifestManager struct {
	mu       sync.RWMutex
	manifest *ToolManifest
	path     string
	dirty    bool
}

// NewManifestManager creates a new manifest manager.
func NewManifestManager() *ManifestManager {
	home, _ := os.UserHomeDir()
	return &ManifestManager{
		path: filepath.Join(home, ".config", "loom", "manifest.yaml"),
		manifest: &ToolManifest{
			Version: 1,
			Servers: make(map[string]ServerManifest),
		},
	}
}

// Load reads the manifest from disk.
func (m *ManifestManager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			// No manifest yet - that's OK
			return nil
		}
		return err
	}

	var manifest ToolManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		// Corrupted manifest - start fresh
		return nil
	}

	m.manifest = &manifest
	return nil
}

// Save writes the manifest to disk.
func (m *ManifestManager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.dirty {
		return nil
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(m.path), 0700); err != nil {
		return err
	}

	data, err := yaml.Marshal(m.manifest)
	if err != nil {
		return err
	}

	if err := os.WriteFile(m.path, data, 0600); err != nil {
		return err
	}

	m.dirty = false
	return nil
}

// GetAllTools returns all cached tools from all servers, properly namespaced.
func (m *ManifestManager) GetAllTools() []mcp.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var tools []mcp.Tool
	for _, server := range m.manifest.Servers {
		tools = append(tools, server.Tools...)
	}
	return tools
}

// GetServerTools returns cached tools for a specific server.
func (m *ManifestManager) GetServerTools(serverName string) ([]mcp.Tool, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	server, ok := m.manifest.Servers[serverName]
	if !ok {
		return nil, false
	}
	return server.Tools, true
}

// UpdateServerTools updates the cached tools for a server.
// The tools should already be namespaced (server__toolname format).
func (m *ManifestManager) UpdateServerTools(serverName string, tools []mcp.Tool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.manifest.Servers[serverName] = ServerManifest{
		Tools:     tools,
		FetchedAt: time.Now(),
		Hash:      hashTools(tools),
	}
	m.manifest.UpdatedAt = time.Now()
	m.dirty = true
}

// RemoveServer removes a server from the manifest.
func (m *ManifestManager) RemoveServer(serverName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.manifest.Servers, serverName)
	m.manifest.UpdatedAt = time.Now()
	m.dirty = true
}

// GetServerHash returns the hash for a server's tools (for change detection).
func (m *ManifestManager) GetServerHash(serverName string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if server, ok := m.manifest.Servers[serverName]; ok {
		return server.Hash
	}
	return ""
}

// IsStale returns true if the server's cache is older than maxAge.
func (m *ManifestManager) IsStale(serverName string, maxAge time.Duration) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	server, ok := m.manifest.Servers[serverName]
	if !ok {
		return true
	}
	return time.Since(server.FetchedAt) > maxAge
}

// LastUpdated returns when the manifest was last updated.
func (m *ManifestManager) LastUpdated() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.manifest.UpdatedAt
}

// ServerCount returns the number of servers in the manifest.
func (m *ManifestManager) ServerCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.manifest.Servers)
}

// hashTools computes a hash of the tool list for change detection.
func hashTools(tools []mcp.Tool) string {
	if len(tools) == 0 {
		return ""
	}

	// Serialize tools to JSON (stable since tool names are unique)
	data, err := json.Marshal(tools)
	if err != nil {
		return ""
	}

	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:8]) // First 8 bytes is enough
}
