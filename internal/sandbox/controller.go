package sandbox

import "context"

// Controller is the unified interface for sandbox lifecycle and execution.
// It abstracts over different backends (K8s, Docker) and exec transports
// (WebSocket, SPDY).
type Controller interface {
	// Build builds a sandbox image from a Dockerfile.
	Build(ctx context.Context, req BuildRequest) (*BuildResult, error)

	// Exec runs a command and returns the full result after completion.
	Exec(ctx context.Context, req ExecRequest) (*ExecResult, error)

	// StreamExec runs a command and streams output chunks to the channel.
	// The channel is closed when the command completes. The returned
	// ExecResult contains the final exit code and summary.
	StreamExec(ctx context.Context, req ExecRequest, chunks chan<- ExecChunk) (*ExecResult, error)

	// Start starts a sandbox container/pod.
	Start(ctx context.Context, req StartRequest) (*StartResult, error)

	// Stop stops and removes a sandbox container/pod.
	Stop(ctx context.Context, id string) error

	// Status returns the current state of a sandbox.
	Status(ctx context.Context, id string) (*StatusResult, error)
}
