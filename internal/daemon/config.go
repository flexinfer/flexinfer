// Package daemon provides configuration file support.
package daemon

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// validAuthTypes lists accepted values for HTTP.Auth.Type.
var validAuthTypes = map[string]bool{
	"":       true, // none / localhost-only
	"token":  true,
	"oidc":   true,
	"mtls":   true,
	"oauth2": true,
}

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

	// Policy controls gateway request/response policy enforcement hooks
	Policy GatewayPolicyConfig `yaml:"policy,omitempty"`

	// Audit controls structured tool call logging
	Audit AuditConfig `yaml:"audit,omitempty"`

	// Cost controls usage tracking and attribution
	Cost CostConfig `yaml:"cost,omitempty"`

	// Health controls health monitor settings
	Health HealthConfig `yaml:"health,omitempty"`

	// Proxy controls proxy-side truncation and heartbeat settings
	Proxy ProxyConfig `yaml:"proxy,omitempty"`

	// Routing controls per-server routing preferences
	Routing RoutingConfig `yaml:"routing,omitempty"`

	// HUD configures agent HUD connectivity for CLI commands
	HUD HUDConfig `yaml:"hud,omitempty"`

	// Debug enables debug logging
	Debug bool `yaml:"debug"`
}

// HUDConfig controls how `loom agent` CLI commands connect to the HUD.
type HUDConfig struct {
	// URL is the full HUD base URL (e.g., "https://192.168.50.227").
	// When set, overrides the default http://127.0.0.1:{port}.
	URL string `yaml:"url,omitempty"`

	// Host is the Host header override for internal ingress access.
	// Required when URL points to an IP-based ingress (e.g., "hud.flexinfer.ai").
	Host string `yaml:"host,omitempty"`

	// CFAccessClientID for Cloudflare Access authentication to the HUD.
	// Falls back to hub.cf_access_client_id if not set.
	CFAccessClientID string `yaml:"cf_access_client_id,omitempty"`

	// CFAccessClientSecret for Cloudflare Access authentication to the HUD.
	// Falls back to hub.cf_access_client_secret if not set.
	CFAccessClientSecret string `yaml:"cf_access_client_secret,omitempty"`
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

	// OAuth controls the built-in OAuth 2.1 authorization server.
	OAuth OAuthConfig `yaml:"oauth,omitempty"`
}

// HTTPAuthConfig controls authentication for the Streamable HTTP listener.
type HTTPAuthConfig struct {
	// Type is the authentication type: "token", "oidc", "mtls", "oauth2", or "" (none/localhost-only).
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

	// PoolMaxIdle is the maximum idle connections per server for the local pool (default: 2)
	PoolMaxIdle int `yaml:"pool_max_idle,omitempty"`

	// PoolMaxOpen is the maximum open connections per server for the local pool (default: 10)
	PoolMaxOpen int `yaml:"pool_max_open,omitempty"`

	// PoolIdleTimeoutMinutes is the idle timeout for local pool connections (default: 5)
	PoolIdleTimeoutMinutes int `yaml:"pool_idle_timeout_minutes,omitempty"`

	// HubPoolMaxIdle is the maximum idle connections per server for the hub pool (default: 2)
	HubPoolMaxIdle int `yaml:"hub_pool_max_idle,omitempty"`

	// HubPoolMaxOpen is the maximum open connections per server for the hub pool (default: 10)
	HubPoolMaxOpen int `yaml:"hub_pool_max_open,omitempty"`

	// HubPoolIdleTimeoutMinutes is the idle timeout for hub pool connections (default: 5)
	HubPoolIdleTimeoutMinutes int `yaml:"hub_pool_idle_timeout_minutes,omitempty"`

	// RefreshConcurrency is the max parallel server refreshes during tool cache updates (default: 6)
	RefreshConcurrency int `yaml:"refresh_concurrency,omitempty"`

	// MaxConcurrentCalls is the daemon-wide cap on simultaneous in-flight tool calls (0 = unlimited)
	MaxConcurrentCalls int `yaml:"max_concurrent_calls,omitempty"`

	// PoolStaleThresholdSeconds is how long an idle pool connection can sit before
	// being discarded on next Get. Default 120 (2 min). Set to -1 to disable.
	PoolStaleThresholdSeconds int `yaml:"pool_stale_threshold_seconds,omitempty"`
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

	// DisableOnAuthFailure disables hub fallback if discovery is auth-gated.
	DisableOnAuthFailure bool `yaml:"disable_on_auth_failure,omitempty"`

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

// HealthConfig controls the health monitor settings.
type HealthConfig struct {
	// CheckIntervalSeconds between health checks (default: 30)
	CheckIntervalSeconds int `yaml:"check_interval_seconds,omitempty"`

	// DeepProbeIntervalMinutes between full process-spawning probes (default: 5).
	// Between deep probes, lightweight pool-based probes are used to reduce
	// CPU/memory churn. Set to 0 to always use deep probes.
	DeepProbeIntervalMinutes int `yaml:"deep_probe_interval_minutes,omitempty"`

	// HealthyThreshold consecutive successes to mark healthy (default: 2)
	HealthyThreshold int `yaml:"healthy_threshold,omitempty"`

	// UnhealthyThreshold consecutive failures to mark unhealthy (default: 3)
	UnhealthyThreshold int `yaml:"unhealthy_threshold,omitempty"`

	// RestartThreshold failures before auto-restart (default: 3)
	RestartThreshold int `yaml:"restart_threshold,omitempty"`

	// MaxRestarts before giving up (default: 3)
	MaxRestarts int `yaml:"max_restarts,omitempty"`

	// RestartCooldownMinutes between restart attempts (default: 5)
	RestartCooldownMinutes int `yaml:"restart_cooldown_minutes,omitempty"`
}

// ToHealthMonitorConfig converts HealthConfig to HealthMonitorConfig,
// applying defaults for zero values.
func (c *HealthConfig) ToHealthMonitorConfig() HealthMonitorConfig {
	cfg := DefaultHealthMonitorConfig()
	if c == nil {
		return cfg
	}
	if c.CheckIntervalSeconds > 0 {
		cfg.CheckInterval = time.Duration(c.CheckIntervalSeconds) * time.Second
	}
	if c.HealthyThreshold > 0 {
		cfg.HealthyThreshold = c.HealthyThreshold
	}
	if c.UnhealthyThreshold > 0 {
		cfg.UnhealthyThreshold = c.UnhealthyThreshold
	}
	if c.RestartThreshold > 0 {
		cfg.RestartThreshold = c.RestartThreshold
	}
	if c.MaxRestarts > 0 {
		cfg.MaxRestarts = c.MaxRestarts
	}
	if c.RestartCooldownMinutes > 0 {
		cfg.RestartCooldown = time.Duration(c.RestartCooldownMinutes) * time.Minute
	}
	if c.DeepProbeIntervalMinutes > 0 {
		cfg.DeepProbeInterval = time.Duration(c.DeepProbeIntervalMinutes) * time.Minute
	} else if c.DeepProbeIntervalMinutes < 0 {
		cfg.DeepProbeInterval = 0 // explicit disable → always deep probe
	}
	return cfg
}

// ProxyConfig controls proxy-side truncation and heartbeat settings.
type ProxyConfig struct {
	// MaxToolResultBytes is the max size for text tool results (default: 48000)
	MaxToolResultBytes int `yaml:"max_tool_result_bytes,omitempty"`

	// MaxImageResultBytes is the max size for image tool results (default: 1500000)
	MaxImageResultBytes int `yaml:"max_image_result_bytes,omitempty"`

	// MaxResourceBytes is the max size for resource reads (default: 64000)
	MaxResourceBytes int `yaml:"max_resource_bytes,omitempty"`

	// HeartbeatIntervalMs is the minimum interval between proxy heartbeats (default: 5000)
	HeartbeatIntervalMs int `yaml:"heartbeat_interval_ms,omitempty"`

	// IdleExitSeconds is how long an idle proxy waits for MCP messages before exiting (default: 1800)
	IdleExitSeconds int `yaml:"idle_exit_seconds,omitempty"`
}

// RoutingConfig controls per-server routing preferences.
type RoutingConfig struct {
	// Preferences maps server names to routing preference strings.
	// Valid values: "local-only", "hub-only", "prefer-local", "prefer-hub", "health-based"
	Preferences map[string]string `yaml:"preferences,omitempty"`
}

// GetPoolConfig returns a pool.Config for the local connection pool.
func (c *ResourceConfig) GetPoolConfig() (maxIdle, maxOpen int, idleTimeout time.Duration) {
	maxIdle = 2
	if c.PoolMaxIdle > 0 {
		maxIdle = c.PoolMaxIdle
	}
	maxOpen = 10
	if c.PoolMaxOpen > 0 {
		maxOpen = c.PoolMaxOpen
	}
	idleTimeout = 5 * time.Minute
	if c.PoolIdleTimeoutMinutes > 0 {
		idleTimeout = time.Duration(c.PoolIdleTimeoutMinutes) * time.Minute
	}
	return
}

// GetHubPoolConfig returns a pool.Config for the hub connection pool.
func (c *ResourceConfig) GetHubPoolConfig() (maxIdle, maxOpen int, idleTimeout time.Duration) {
	maxIdle = 2
	if c.HubPoolMaxIdle > 0 {
		maxIdle = c.HubPoolMaxIdle
	}
	maxOpen = 10
	if c.HubPoolMaxOpen > 0 {
		maxOpen = c.HubPoolMaxOpen
	}
	idleTimeout = 5 * time.Minute
	if c.HubPoolIdleTimeoutMinutes > 0 {
		idleTimeout = time.Duration(c.HubPoolIdleTimeoutMinutes) * time.Minute
	}
	return
}

// GetRefreshConcurrency returns the tool refresh concurrency limit.
func (c *ResourceConfig) GetRefreshConcurrency() int {
	if c.RefreshConcurrency > 0 {
		return c.RefreshConcurrency
	}
	return 6
}

// GetMaxConcurrentCalls returns the daemon-wide concurrent call limit.
// Returns 0 for unlimited (the default).
func (c *ResourceConfig) GetMaxConcurrentCalls() int {
	if c.MaxConcurrentCalls < 0 {
		return 0
	}
	return c.MaxConcurrentCalls
}

// GetPoolStaleThreshold returns the duration after which an idle pool connection
// is considered stale and discarded before use. Default is 2 minutes.
// Returns 0 (disabled) when explicitly set to -1.
func (c *ResourceConfig) GetPoolStaleThreshold() time.Duration {
	if c.PoolStaleThresholdSeconds < 0 {
		return 0
	}
	if c.PoolStaleThresholdSeconds == 0 {
		return 2 * time.Minute
	}
	return time.Duration(c.PoolStaleThresholdSeconds) * time.Second
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
		RBAC:   DefaultRBACConfig(),
		Policy: DefaultGatewayPolicyConfig(),
		Audit:  DefaultAuditConfig(),
		Cost:   DefaultCostConfig(),
		Debug:  false,
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
	cfg, warnings, err := LoadConfigFileWithWarnings()
	for _, w := range warnings {
		slog.Warn(w)
	}
	return cfg, err
}

// LoadConfigFileWithWarnings loads configuration and returns any validation
// warnings separately (useful for testing without slog side-effects).
func LoadConfigFileWithWarnings() (FileConfig, []string, error) {
	configPath := getConfigPath()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultFileConfig(), nil, nil
		}
		return FileConfig{}, nil, err
	}

	return parseAndValidateConfig(data)
}

// parseAndValidateConfig decodes YAML data into FileConfig and returns
// validation warnings for unknown keys and invalid field values.
func parseAndValidateConfig(data []byte) (FileConfig, []string, error) {
	var warnings []string

	// First pass: strict decode to detect unknown keys.
	var strict FileConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&strict); err != nil {
		// If the error is about unknown fields, collect it as a warning
		// and fall through to a lenient parse so the daemon still starts.
		if strings.Contains(err.Error(), "not found") ||
			strings.Contains(err.Error(), "unknown") {
			warnings = append(warnings,
				fmt.Sprintf("config: unknown key in config.yaml: %v", err))
		} else {
			// True syntax error — hard fail.
			return FileConfig{}, nil, err
		}
	}

	// Second pass (or reuse strict result): lenient decode so unknown
	// keys don't block startup.
	var cfg FileConfig
	if len(warnings) == 0 {
		cfg = strict
	} else {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, warnings, err
		}
	}

	// Apply defaults for missing values.
	if cfg.Hub.URL == "" {
		cfg.Hub.URL = "wss://mcp.flexinfer.ai/ws"
	}
	if cfg.Hub.Profile == "" {
		cfg.Hub.Profile = "codex"
	}

	// Validate HTTP auth type.
	if !validAuthTypes[cfg.HTTP.Auth.Type] {
		warnings = append(warnings,
			fmt.Sprintf("config: unknown http.auth.type %q (valid: token, oidc, mtls, oauth2, or empty)",
				cfg.HTTP.Auth.Type))
	}

	return cfg, warnings, nil
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
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Warn("config: unable to determine home directory, using current directory", "error", err)
	}
	return filepath.Join(home, ".config", "loom", "config.yaml")
}
