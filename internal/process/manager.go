// Package process manages local MCP server processes.
package process

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/crb2nu/loom/pkg/mcp"
	"github.com/crb2nu/loom/pkg/registry"
)

// Process represents a running MCP server process.
type Process struct {
	Name      string
	Cmd       *exec.Cmd
	Transport mcp.Transport
	StartedAt time.Time
	stdin     io.WriteCloser
	stdout    io.ReadCloser
}

// Manager manages local MCP server processes.
type Manager struct {
	registry *registry.Registry
	target   string
	mu       sync.Mutex
	procs    map[string]*Process
}

// NewManager creates a new process manager.
func NewManager(reg *registry.Registry, target string) *Manager {
	return &Manager{
		registry: reg,
		target:   target,
		procs:    make(map[string]*Process),
	}
}

// Start starts a local MCP server process.
func (m *Manager) Start(ctx context.Context, serverName string) (*Process, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already running
	if proc, ok := m.procs[serverName]; ok {
		if proc.Cmd.Process != nil {
			return proc, nil
		}
	}

	// Get server spec
	spec, err := m.registry.GetServerSpec(serverName, m.target)
	if err != nil {
		return nil, fmt.Errorf("get server spec: %w", err)
	}

	if spec.Command == "" {
		return nil, fmt.Errorf("server %s has no command defined", serverName)
	}

	// Build command
	args := make([]string, len(spec.Args))
	for i, arg := range spec.Args {
		args[i] = fmt.Sprint(arg)
	}

	cmd := exec.CommandContext(ctx, spec.Command, args...)

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Get pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	// Start process
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start process: %w", err)
	}

	proc := &Process{
		Name:      serverName,
		Cmd:       cmd,
		Transport: mcp.NewStdioTransport(stdout, stdin),
		StartedAt: time.Now(),
		stdin:     stdin,
		stdout:    stdout,
	}

	m.procs[serverName] = proc
	return proc, nil
}

// Stop stops a running MCP server process.
func (m *Manager) Stop(serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	proc, ok := m.procs[serverName]
	if !ok {
		return nil
	}

	delete(m.procs, serverName)

	// Close transport
	proc.Transport.Close()
	proc.stdin.Close()
	proc.stdout.Close()

	// Kill process
	if proc.Cmd.Process != nil {
		proc.Cmd.Process.Kill()
		proc.Cmd.Wait()
	}

	return nil
}

// StopAll stops all running processes.
func (m *Manager) StopAll() {
	m.mu.Lock()
	names := make([]string, 0, len(m.procs))
	for name := range m.procs {
		names = append(names, name)
	}
	m.mu.Unlock()

	for _, name := range names {
		m.Stop(name)
	}
}

// Get returns a running process if it exists.
func (m *Manager) Get(serverName string) (*Process, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	proc, ok := m.procs[serverName]
	return proc, ok
}

// List returns all running process names.
func (m *Manager) List() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.procs))
	for name := range m.procs {
		names = append(names, name)
	}
	return names
}

// Dial creates a new connection to an MCP server, starting it if needed.
func (m *Manager) Dial(ctx context.Context, serverName string) (mcp.Transport, error) {
	proc, err := m.Start(ctx, serverName)
	if err != nil {
		return nil, err
	}
	return proc.Transport, nil
}
