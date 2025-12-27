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
	Name         string
	Cmd          *exec.Cmd
	Transport    mcp.Transport
	StartedAt    time.Time
	LastActivity time.Time // Last time this server was used
	stdin        io.WriteCloser
	stdout       io.ReadCloser
}

// ExpandFunc is a function that expands variables in a string.
type ExpandFunc func(string) string

// Manager manages local MCP server processes.
type Manager struct {
	registry   *registry.Registry
	target     string
	expandFunc ExpandFunc
	mu         sync.Mutex
	procs      map[string]*Process
}

// NewManager creates a new process manager.
func NewManager(reg *registry.Registry, target string) *Manager {
	return &Manager{
		registry:   reg,
		target:     target,
		expandFunc: func(s string) string { return s }, // Default: no expansion
		procs:      make(map[string]*Process),
	}
}

// SetExpandFunc sets the function used to expand variables in commands.
func (m *Manager) SetExpandFunc(fn ExpandFunc) {
	m.expandFunc = fn
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

	// Expand variables in command
	command := m.expandFunc(spec.Command)

	// Build command with expanded args
	args := make([]string, len(spec.Args))
	for i, arg := range spec.Args {
		args[i] = m.expandFunc(fmt.Sprint(arg))
	}

	cmd := exec.CommandContext(ctx, command, args...)

	// Set environment with expanded values
	cmd.Env = os.Environ()
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, m.expandFunc(v)))
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

	now := time.Now()
	proc := &Process{
		Name:         serverName,
		Cmd:          cmd,
		Transport:    mcp.NewStdioTransport(stdout, stdin),
		StartedAt:    now,
		LastActivity: now,
		stdin:        stdin,
		stdout:       stdout,
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

// MarkActivity updates the last activity time for a server.
// Call this when a tool is invoked on the server.
func (m *Manager) MarkActivity(serverName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if proc, ok := m.procs[serverName]; ok {
		proc.LastActivity = time.Now()
	}
}

// ReapIdle terminates processes that have been idle for longer than timeout.
// Returns the names of servers that were reaped.
func (m *Manager) ReapIdle(timeout time.Duration) []string {
	m.mu.Lock()
	var toReap []string
	now := time.Now()

	for name, proc := range m.procs {
		if now.Sub(proc.LastActivity) > timeout {
			toReap = append(toReap, name)
		}
	}
	m.mu.Unlock()

	// Stop outside the lock to avoid holding it too long
	for _, name := range toReap {
		m.Stop(name)
	}

	return toReap
}

// Count returns the number of running processes.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.procs)
}

// IdleInfo returns information about process idle times.
type IdleInfo struct {
	Name         string
	IdleDuration time.Duration
	StartedAt    time.Time
}

// GetIdleInfo returns idle information for all running processes.
func (m *Manager) GetIdleInfo() []IdleInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	info := make([]IdleInfo, 0, len(m.procs))
	for name, proc := range m.procs {
		info = append(info, IdleInfo{
			Name:         name,
			IdleDuration: now.Sub(proc.LastActivity),
			StartedAt:    proc.StartedAt,
		})
	}
	return info
}
