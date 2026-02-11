// Package backend provides container runtime backends for devbox sandboxes.
package backend

import "context"

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
}

// BuildOpts configures an image build.
type BuildOpts struct {
	Tag        string // image tag (e.g., "mcp/devbox/loom-core:a3b9c1d")
	Dockerfile []byte // generated Dockerfile content
	ContextDir string // build context directory (project dir)
}

// BuildResult describes the outcome of an image build.
type BuildResult struct {
	ImageTag string `json:"image_tag"`
	Cached   bool   `json:"cached"`
}

// StartOpts configures a sandbox container start.
type StartOpts struct {
	Name     string            // container name (e.g., "devbox-loom-core")
	ImageTag string            // image to use
	WorkDir  string            // working directory inside container (default: "/workspace")
	Mounts   []Mount           // bind mounts
	Env      map[string]string // environment variables
	MemoryMB int               // memory limit in MB (0 = no limit)
	CPUs     float64           // CPU limit (0 = no limit)
	Network  bool              // enable networking
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
