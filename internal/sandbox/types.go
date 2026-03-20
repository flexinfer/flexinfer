// Package sandbox provides a unified controller interface for sandbox
// environments, abstracting over K8s pod exec and Docker backends.
package sandbox

import "time"

// BuildRequest describes a sandbox image build.
type BuildRequest struct {
	Tag        string // image tag (e.g., "mcp/devbox/loom-core:a3b9c1d")
	Dockerfile []byte // generated Dockerfile content
	ContextDir string // build context directory
}

// BuildResult describes the outcome of an image build.
type BuildResult struct {
	ImageTag string `json:"image_tag"`
	Cached   bool   `json:"cached"`
}

// ExecRequest describes a command to execute inside a sandbox.
type ExecRequest struct {
	ContainerID string            // target container/pod
	Command     string            // shell command to run
	WorkDir     string            // working directory (default: "/workspace")
	Env         map[string]string // additional env vars
	TimeoutSec  int               // execution timeout in seconds
	MaxLines    int               // max tail lines to return
}

// ExecResult holds structured output of a command execution.
type ExecResult struct {
	ExitCode    int    `json:"exit_code"`
	StdoutLines int    `json:"stdout_lines"`
	StderrLines int    `json:"stderr_lines"`
	StdoutTail  string `json:"stdout_tail"`
	StderrTail  string `json:"stderr_tail,omitempty"`
	DurationMs  int64  `json:"duration_ms"`
	Truncated   bool   `json:"truncated"`
	OOMKilled   bool   `json:"oom_killed,omitempty"`
}

// ExecChunk represents a streaming output fragment from an exec command.
type ExecChunk struct {
	Stream    string    `json:"stream"`    // "stdout" or "stderr"
	Data      string    `json:"data"`      // chunk content
	Timestamp time.Time `json:"timestamp"` // when this chunk was received
}

// StartRequest describes how to start a sandbox container/pod.
type StartRequest struct {
	Name     string            // container/pod name
	ImageTag string            // image to use
	WorkDir  string            // working directory inside container
	Env      map[string]string // environment variables
	MemoryMB int               // memory limit in MB (0 = no limit)
	CPUs     float64           // CPU limit (0 = no limit)
	Network  bool              // enable networking
	AgentID  string            // owning agent ID
}

// StartResult describes a started sandbox.
type StartResult struct {
	ContainerID string `json:"container_id"`
}

// StatusResult describes the current state of a sandbox.
type StatusResult struct {
	Running bool   `json:"running"`
	Status  string `json:"status"` // "running", "exited", "not_found"
}
