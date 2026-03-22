package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/devbox/backend"
	"github.com/crb2nu/loom/pkg/validate"
)

// asyncExec represents a running or completed async exec.
type asyncExec struct {
	ID          string              `json:"id"`
	Project     string              `json:"project"`
	Command     string              `json:"command"`
	Status      string              `json:"status"` // "running", "completed", "failed"
	Result      *backend.ExecResult `json:"result,omitempty"`
	Error       string              `json:"error,omitempty"`
	StartedAt   time.Time           `json:"started_at"`
	CompletedAt *time.Time          `json:"completed_at,omitempty"`
	cancel      context.CancelFunc
}

// asyncRegistry tracks in-flight and recently completed async execs.
type asyncRegistry struct {
	mu    sync.RWMutex
	execs map[string]*asyncExec
}

func newAsyncRegistry() *asyncRegistry {
	return &asyncRegistry{
		execs: make(map[string]*asyncExec),
	}
}

func (r *asyncRegistry) add(exec *asyncExec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.execs[exec.ID] = exec
}

func (r *asyncRegistry) get(id string) *asyncExec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.execs[id]
}

// cleanup removes completed execs older than maxAge.
func (r *asyncRegistry) cleanup(maxAge time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	for id, e := range r.execs {
		if e.Status == "running" {
			continue
		}

		completedAt := e.StartedAt
		if e.CompletedAt != nil {
			completedAt = *e.CompletedAt
		}
		if completedAt.Before(cutoff) {
			delete(r.execs, id)
		}
	}
}

// cleanupLoop runs periodic cleanup of completed async execs.
func (r *asyncRegistry) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.cleanup(10 * time.Minute)
		}
	}
}

func generateExecID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *manager) handleExecAsync(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	command := v.Required("command")
	timeoutStr := v.String("timeout", "10m")
	agentID := v.String("agent_id", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("invalid timeout: %w", err)), nil
	}

	projectDir, projectName, err := m.resolveProject(project)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	key := storeKey(projectName, agentID)
	mu := m.projectLock(key)
	mu.Lock()
	containerID, err := m.ensureRunning(ctx, projectDir, projectName, agentID)
	mu.Unlock()
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure sandbox: %w", err)), nil
	}

	m.totalExecs.Add(1)
	execID := generateExecID()
	execCtx, cancel := context.WithTimeout(context.Background(), timeout)

	ae := &asyncExec{
		ID:        execID,
		Project:   projectName,
		Command:   command,
		Status:    "running",
		StartedAt: time.Now(),
		cancel:    cancel,
	}
	m.asyncExecs.add(ae)

	// Touch before exec so reaper won't kill the container
	_ = m.store.TouchLastUsed(key)
	m.incActiveExecs(key)

	m.asyncWg.Add(1)
	go func() {
		defer m.asyncWg.Done()
		defer m.decActiveExecs(key)
		defer cancel()

		result, err := m.backend.Exec(execCtx, backend.ExecOpts{
			ContainerID: containerID,
			Command:     command,
			WorkDir:     m.projectWorkDir(projectDir),
			TimeoutSec:  int(timeout.Seconds()),
			MaxLines:    100, // async gets more output
		})

		_ = m.store.TouchLastUsed(key)

		m.asyncExecs.mu.Lock()
		defer m.asyncExecs.mu.Unlock()
		completedAt := time.Now()
		ae.CompletedAt = &completedAt
		if err != nil {
			ae.Status = "failed"
			ae.Error = err.Error()
		} else {
			ae.Status = "completed"
			ae.Result = result
		}
	}()

	return mcp.JSONResult(map[string]any{
		"exec_id": execID,
		"status":  "running",
		"project": projectName,
		"command": command,
	})
}

func (m *manager) handleExecPoll(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	execID := v.Required("exec_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ae := m.asyncExecs.get(execID)
	if ae == nil {
		return mcp.ErrorResult(fmt.Errorf("exec %q not found (may have expired)", execID)), nil
	}

	result := map[string]any{
		"exec_id":    ae.ID,
		"status":     ae.Status,
		"project":    ae.Project,
		"command":    ae.Command,
		"started_at": ae.StartedAt.Format(time.RFC3339),
		"elapsed_ms": time.Since(ae.StartedAt).Milliseconds(),
	}
	if ae.CompletedAt != nil {
		result["completed_at"] = ae.CompletedAt.Format(time.RFC3339)
	}

	if ae.Result != nil {
		result["exit_code"] = ae.Result.ExitCode
		result["stdout_tail"] = ae.Result.StdoutTail
		result["stderr_tail"] = ae.Result.StderrTail
		result["duration_ms"] = ae.Result.DurationMs
	}
	if ae.Error != "" {
		result["error"] = ae.Error
	}

	return mcp.JSONResult(result)
}
