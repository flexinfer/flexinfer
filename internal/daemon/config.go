// Package daemon provides configuration file support.
package daemon

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// FileConfig represents the daemon configuration file structure.
// This file can be updated by the VS Code extension to control daemon behavior.
type FileConfig struct {
	// Hub configuration
	Hub HubConfig `yaml:"hub"`

	// RepoRoot is the root path for ${repo} variable expansion in the registry.
	// If not set, it will be derived from the registry path (legacy behavior).
	RepoRoot string `yaml:"repo_root,omitempty"`

	// Debug enables debug logging
	Debug bool `yaml:"debug"`
}

// HubConfig configures the MCP hub connection.
type HubConfig struct {
	// URL is the WebSocket URL for the MCP hub (e.g., wss://mcp.flexinfer.ai/ws)
	URL string `yaml:"url"`

	// Enabled controls whether hub fallback is enabled
	Enabled bool `yaml:"enabled"`

	// Profile is the hub profile to use (e.g., "codex", "claude")
	Profile string `yaml:"profile"`

	// CFAccessClientID for Cloudflare Access authentication
	CFAccessClientID string `yaml:"cf_access_client_id,omitempty"`

	// CFAccessClientSecret for Cloudflare Access authentication
	CFAccessClientSecret string `yaml:"cf_access_client_secret,omitempty"`
}

// DefaultFileConfig returns the default configuration.
func DefaultFileConfig() FileConfig {
	return FileConfig{
		Hub: HubConfig{
			URL:     "wss://mcp.flexinfer.ai/ws",
			Enabled: true,
			Profile: "codex",
		},
		Debug: false,
	}
}

// LoadConfigFile loads configuration from ~/.config/loom/config.yaml.
// If the file doesn't exist, it returns the default configuration.
func LoadConfigFile() (FileConfig, error) {
	configPath := getConfigPath()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultFileConfig(), nil
		}
		return FileConfig{}, err
	}

	var cfg FileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, err
	}

	// Apply defaults for missing values
	if cfg.Hub.URL == "" {
		cfg.Hub.URL = "wss://mcp.flexinfer.ai/ws"
	}
	if cfg.Hub.Profile == "" {
		cfg.Hub.Profile = "codex"
	}

	return cfg, nil
}

// SaveConfigFile saves configuration to ~/.config/loom/config.yaml.
func SaveConfigFile(cfg FileConfig) error {
	configPath := getConfigPath()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0600)
}

func getConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "loom", "config.yaml")
}
