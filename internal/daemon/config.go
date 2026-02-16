// Package daemon provides configuration file support.
package daemon

import (
	"os"
	"path/filepath"
	"time"

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

	// Resources controls process management settings
	Resources ResourceConfig `yaml:"resources,omitempty"`

	// Context controls tool filtering and profile settings
	Context ContextConfig `yaml:"context,omitempty"`

	// Cache controls response caching for read-only tools
	Cache CacheConfig `yaml:"cache,omitempty"`

	// Sandbox controls devbox sandbox settings
	Sandbox SandboxConfig `yaml:"sandbox,omitempty"`

	// HTTP controls the Streamable HTTP listener
	HTTP HTTPConfig `yaml:"http,omitempty"`

	// RBAC controls role-based access control for tool calls
	RBAC RBACConfig `yaml:"rbac,omitempty"`

	// Audit controls structured tool call logging
	Audit AuditConfig `yaml:"audit,omitempty"`

	// Cost controls usage tracking and attribution
	Cost CostConfig `yaml:"cost,omitempty"`

	// Debug enables debug logging
	Debug bool `yaml:"debug"`
}

// HTTPConfig controls the Streamable HTTP listener.
type HTTPConfig struct {
	// SessionTimeoutMinutes is how long idle HTTP sessions are kept (default: 30).
	SessionTimeoutMinutes int `yaml:"session_timeout_minutes,omitempty"`

	// MaxSessions limits concurrent HTTP sessions (default: 1000).
	MaxSessions int `yaml:"max_sessions,omitempty"`

	// AllowedOrigins restricts which origins can connect (empty = all allowed).
	AllowedOrigins []string `yaml:"allowed_origins,omitempty"`

	// TLSCertFile is the path to the TLS certificate file.
	TLSCertFile string `yaml:"tls_cert_file,omitempty"`

	// TLSKeyFile is the path to the TLS private key file.
	TLSKeyFile string `yaml:"tls_key_file,omitempty"`

	// Auth controls authentication for the HTTP listener.
	Auth HTTPAuthConfig `yaml:"auth,omitempty"`
}

// HTTPAuthConfig controls authentication for the Streamable HTTP listener.
type HTTPAuthConfig struct {
	// Type is the authentication type: "token", "oidc", "mtls", or "" (none/localhost-only).
	Type string `yaml:"type,omitempty"`

	// TokenSecretKey is the secret store key containing the bearer token (for type=token).
	TokenSecretKey string `yaml:"token_secret_key,omitempty"`

	// OIDCIssuer is the OIDC provider URL (for type=oidc).
	OIDCIssuer string `yaml:"oidc_issuer,omitempty"`

	// OIDCClientID is the OIDC client ID (for type=oidc).
	OIDCClientID string `yaml:"oidc_client_id,omitempty"`

	// TLSClientCA is the path to the CA certificate for client cert validation (for type=mtls).
	TLSClientCA string `yaml:"tls_client_ca,omitempty"`

	// AllowedCommonNames restricts which client certificate CNs are accepted (for type=mtls).
	AllowedCommonNames []string `yaml:"allowed_common_names,omitempty"`
}

// SandboxConfig controls devbox sandbox environments.
type SandboxConfig struct {
	// Enabled activates sandbox functionality
	Enabled bool `yaml:"enabled"`

	// Backend selects the container runtime ("docker" or "k8s")
	Backend string `yaml:"backend,omitempty"`

	// WorkspaceRoot is the root directory for project discovery (default: ~/workspace)
	WorkspaceRoot string `yaml:"workspace_root,omitempty"`

	// Registry is the container image registry (default: registry.harbor.lan)
	Registry string `yaml:"registry,omitempty"`

	// ImagePrefix is the image name prefix (default: mcp/devbox)
	ImagePrefix string `yaml:"image_prefix,omitempty"`

	// CacheDir stores sandbox state and build cache (default: ~/.cache/loom/devbox)
	CacheDir string `yaml:"cache_dir,omitempty"`

	// DefaultLimits sets default resource limits for sandboxes
	DefaultLimits SandboxLimits `yaml:"default_limits,omitempty"`

	// IdleTimeout before stopping unused sandboxes (default: 30m)
	IdleTimeout string `yaml:"idle_timeout,omitempty"`

	// MaxTailLines controls output truncation (default: 20)
	MaxTailLines int `yaml:"max_tail_lines,omitempty"`

	// K8s holds Kubernetes-specific configuration
	K8s K8sSandboxConfig `yaml:"k8s,omitempty"`
}

// SandboxLimits defines resource limits for sandbox containers.
type SandboxLimits struct {
	// CPU cores limit (default: 2)
	CPU float64 `yaml:"cpu,omitempty"`

	// MemoryMB memory limit in megabytes (default: 1024)
	MemoryMB int `yaml:"memory_mb,omitempty"`

	// Timeout is the default command execution timeout (default: 5m)
	Timeout string `yaml:"timeout,omitempty"`
}

// K8sSandboxConfig holds Kubernetes-specific sandbox settings.
type K8sSandboxConfig struct {
	// Kubeconfig path for the K8s cluster
	Kubeconfig string `yaml:"kubeconfig,omitempty"`

	// Namespace to create sandbox pods in (default: devbox)
	Namespace string `yaml:"namespace,omitempty"`

	// StorageClass for workspace PVCs (default: longhorn)
	StorageClass string `yaml:"storage_class,omitempty"`
}

// ResourceConfig controls process and connection resource limits.
type ResourceConfig struct {
	// MaxProcesses limits concurrent server processes (0 = unlimited)
	MaxProcesses int `yaml:"max_processes,omitempty"`

	// IdleTimeoutMinutes before terminating unused servers (default: 5)
	IdleTimeoutMinutes int `yaml:"idle_timeout_minutes,omitempty"`

	// ManifestTTLMinutes is how long cached tools are considered fresh (default: 5)
	ManifestTTLMinutes int `yaml:"manifest_ttl_minutes,omitempty"`
}

// ContextConfig controls tool filtering and profile selection.
type ContextConfig struct {
	// ActiveProfile is the current tool profile (dev, k8s-ops, research, full)
	ActiveProfile string `yaml:"active_profile,omitempty"`

	// AutoDetect enables context-aware profile selection based on cwd
	AutoDetect bool `yaml:"auto_detect,omitempty"`

	// EnrichDescriptions adds usage hints to tool descriptions
	EnrichDescriptions bool `yaml:"enrich_descriptions,omitempty"`

	// CustomProfilePath points to custom profile definitions
	CustomProfilePath string `yaml:"custom_profile_path,omitempty"`
}

// HubConfig configures the MCP hub connection.
type HubConfig struct {
	// URL is the WebSocket URL for the MCP hub (e.g., wss://mcp.flexinfer.ai/ws)
	URL string `yaml:"url"`

	// Enabled controls whether hub fallback is enabled
	Enabled bool `yaml:"enabled"`

	// PreferHub forces routing to prefer hub over local servers when available.
	PreferHub bool `yaml:"prefer_hub,omitempty"`

	// Profile is the hub profile to use (e.g., "codex", "claude")
	Profile string `yaml:"profile"`

	// CFAccessClientID for Cloudflare Access authentication
	CFAccessClientID string `yaml:"cf_access_client_id,omitempty"`

	// CFAccessClientSecret for Cloudflare Access authentication
	CFAccessClientSecret string `yaml:"cf_access_client_secret,omitempty"`

	// ReconnectIntervalSeconds between reconnection attempts (default: 5)
	ReconnectIntervalSeconds int `yaml:"reconnect_interval_seconds,omitempty"`

	// PingIntervalSeconds for keepalive pings (default: 30)
	PingIntervalSeconds int `yaml:"ping_interval_seconds,omitempty"`

	// MaxRetries before giving up on hub connection (default: 3)
	MaxRetries int `yaml:"max_retries,omitempty"`
}

// DefaultFileConfig returns the default configuration.
func DefaultFileConfig() FileConfig {
	return FileConfig{
		Hub: HubConfig{
			URL:                      "wss://mcp.flexinfer.ai/ws",
			Enabled:                  true,
			PreferHub:                false,
			Profile:                  "codex",
			ReconnectIntervalSeconds: 5,
			PingIntervalSeconds:      30,
			MaxRetries:               3,
		},
		Resources: ResourceConfig{
			MaxProcesses:       0, // Unlimited
			IdleTimeoutMinutes: 5,
			ManifestTTLMinutes: 5,
		},
		Context: ContextConfig{
			ActiveProfile:      "full",
			AutoDetect:         false,
			EnrichDescriptions: false,
		},
		Cache: DefaultCacheConfig(),
		HTTP: HTTPConfig{
			SessionTimeoutMinutes: 30,
			MaxSessions:           1000,
		},
		RBAC:  DefaultRBACConfig(),
		Audit: DefaultAuditConfig(),
		Cost:  DefaultCostConfig(),
		Debug: false,
	}
}

// GetIdleTimeout returns the idle timeout duration.
func (c *ResourceConfig) GetIdleTimeout() time.Duration {
	if c.IdleTimeoutMinutes <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(c.IdleTimeoutMinutes) * time.Minute
}

// GetManifestTTL returns the manifest TTL duration.
func (c *ResourceConfig) GetManifestTTL() time.Duration {
	if c.ManifestTTLMinutes <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(c.ManifestTTLMinutes) * time.Minute
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
