// Package backend provides container runtime backends for devbox sandboxes.
package backend

import (
	"context"
	"errors"
	"time"
)

// ErrNotSupported is returned when a backend doesn't support an operation.
var ErrNotSupported = errors.New("operation not supported by this backend")

// Backend defines the interface for container runtimes (Docker, K8s).
type Backend interface {
	// Build builds a container image from a generated Dockerfile.
	Build(ctx context.Context, opts BuildOpts) (*BuildResult, error)

	// Start starts a persistent sandbox container for a project.
	Start(ctx context.Context, opts StartOpts) (*StartResult, error)

	// Exec runs a command in a running sandbox container.
	Exec(ctx context.Context, opts ExecOpts) (*ExecResult, error)

	// Stop stops and removes a sandbox container.
	Stop(ctx context.Context, id string) error

	// Status returns the status of a sandbox container.
	Status(ctx context.Context, id string) (*StatusResult, error)

	// Health checks if the backend runtime is available.
	Health(ctx context.Context) error

	// Pause freezes a running container for instant resume later.
	// Returns ErrNotSupported if the backend doesn't support pausing.
	Pause(ctx context.Context, id string) error

	// Resume unfreezes a paused container (~5ms for Docker).
	// Returns ErrNotSupported if the backend doesn't support resuming.
	Resume(ctx context.Context, id string) error

	// ReadFile reads a file from inside a running container.
	ReadFile(ctx context.Context, id, path string) ([]byte, error)

	// WriteFile writes content to a file inside a running container.
	WriteFile(ctx context.Context, id, path string, content []byte, mode string) error

	// CleanupBuilds deletes completed build pods and associated ConfigMaps
	// older than maxAge. Returns the number of resources cleaned up.
	CleanupBuilds(ctx context.Context, maxAge time.Duration) (int, error)
}

// BuildOpts configures an image build.
type BuildOpts struct {
	Tag            string // image tag (e.g., "mcp/devbox/loom-core:a3b9c1d")
	Dockerfile     []byte // generated Dockerfile content
	ContextDir     string // build context directory (project dir)
	PreferExisting bool   // when true, return/reuse Tag if it already exists
}

// BuildResult describes the outcome of an image build.
type BuildResult struct {
	ImageTag string `json:"image_tag"`
	Cached   bool   `json:"cached"`
}

// SecretEnvVar describes an environment variable sourced from a K8s Secret.
type SecretEnvVar struct {
	Name       string // env var name (e.g., "ANTHROPIC_API_KEY")
	SecretName string // K8s secret name (e.g., "agent-api-keys")
	SecretKey  string // key within the secret
}

// SecretMount mounts individual keys from a K8s Secret as files in the container.
type SecretMount struct {
	SecretName string // K8s secret name (e.g., "agent-auth-tokens")
	MountPath  string // container directory to mount into (e.g., "/root/.codex")
	Items      []SecretMountItem
}

// SecretMountItem maps a single key from a Secret to a file path within the mount.
type SecretMountItem struct {
	Key  string // key in the Secret (e.g., "codex-auth-json")
	Path string // relative filename within MountPath (e.g., "auth.json")
}

// StartOpts configures a sandbox container start.
type StartOpts struct {
	Name         string            // container name (e.g., "devbox-loom-core")
	ImageTag     string            // image to use
	WorkDir      string            // working directory inside container (default: "/workspace")
	Mounts       []Mount           // bind mounts
	Env          map[string]string // environment variables
	SecretEnv    []SecretEnvVar    // env vars sourced from K8s secrets (K8s backend only)
	SecretMounts []SecretMount     // files from K8s secrets mounted into the container
	MemoryMB     int               // memory limit in MB (0 = no limit)
	CPUs         float64           // CPU limit (0 = no limit)
	Network      bool              // enable networking
	AgentID      string            // owning agent ID (used as pod label in K8s backend)

	// ManagedByOverride, if non-empty, replaces the default "mcp-devbox"
	// value for the app.kubernetes.io/managed-by label. Spawn pods set this
	// to "loom-spawn" so the reconciler can discover them.
	ManagedByOverride string

	// ExtraLabels are merged into the pod/container labels after defaults.
	// Caller-provided keys win over defaults if there is a collision.
	ExtraLabels map[string]string
}

// Mount describes a bind mount.
type Mount struct {
	Host      string // host path
	Container string // container path
	ReadOnly  bool   // read-only mount
}

// StartResult describes a started container.
type StartResult struct {
	ContainerID string `json:"container_id"`
}

// ExecOpts configures command execution in a sandbox.
type ExecOpts struct {
	ContainerID string            // target container
	Command     string            // shell command to run
	WorkDir     string            // working directory (default: "/workspace")
	Env         map[string]string // additional env vars
	TimeoutSec  int               // execution timeout in seconds
	MaxLines    int               // max tail lines to return
}

// StatusResult describes the current state of a sandbox container.
type StatusResult struct {
	Running bool   `json:"running"`
	Status  string `json:"status"` // "running", "exited", "not_found"
}
