package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DockerBackend implements Backend using the local Docker CLI.
type DockerBackend struct {
	dockerPath string
}

// NewDockerBackend creates a new Docker backend.
func NewDockerBackend() (*DockerBackend, error) {
	path, err := exec.LookPath("docker")
	if err != nil {
		return nil, fmt.Errorf("docker CLI not found in PATH: %w", err)
	}
	return &DockerBackend{dockerPath: path}, nil
}

func (d *DockerBackend) Health(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, d.dockerPath, "info", "--format", "{{.ServerVersion}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker not available: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *DockerBackend) Build(ctx context.Context, opts BuildOpts) (*BuildResult, error) {
	if opts.PreferExisting {
		inspect := exec.CommandContext(ctx, d.dockerPath, "image", "inspect", opts.Tag)
		if err := inspect.Run(); err == nil {
			return &BuildResult{ImageTag: opts.Tag, Cached: true}, nil
		}
	}

	// Write Dockerfile to a temp file in the context directory
	tmpDir, err := os.MkdirTemp("", "devbox-build-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, opts.Dockerfile, 0600); err != nil {
		return nil, fmt.Errorf("write Dockerfile: %w", err)
	}

	args := []string{"build", "-t", opts.Tag, "-f", dockerfilePath, opts.ContextDir}
	cmd := exec.CommandContext(ctx, d.dockerPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker build failed: %w\n%s", err, string(out))
	}

	cached := strings.Contains(string(out), "CACHED")
	return &BuildResult{ImageTag: opts.Tag, Cached: cached}, nil
}

func (d *DockerBackend) Start(ctx context.Context, opts StartOpts) (*StartResult, error) {
	// Stop existing container with same name (idempotent)
	_ = d.Stop(ctx, opts.Name)

	args := []string{"run", "-d", "--name", opts.Name}

	// Apply container labels (symmetry with K8s backend pod labels).
	managedBy := "mcp-devbox"
	if opts.ManagedByOverride != "" {
		managedBy = opts.ManagedByOverride
	}
	args = append(args, "--label", "app.kubernetes.io/managed-by="+managedBy)
	args = append(args, "--label", "devbox/project="+opts.Name)
	if opts.AgentID != "" {
		args = append(args, "--label", "devbox/agent-id="+opts.AgentID)
	}
	for k, v := range opts.ExtraLabels {
		args = append(args, "--label", k+"="+v)
	}

	for _, m := range opts.Mounts {
		flag := fmt.Sprintf("%s:%s", m.Host, m.Container)
		if m.ReadOnly {
			flag += ":ro"
		}
		args = append(args, "-v", flag)
	}

	for k, v := range opts.Env {
		args = append(args, "-e", k+"="+v)
	}

	if opts.MemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", opts.MemoryMB))
	}
	if opts.CPUs > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%.1f", opts.CPUs))
	}
	if !opts.Network {
		args = append(args, "--network", "none")
	}

	args = append(args, opts.ImageTag, "sleep", "infinity")

	cmd := exec.CommandContext(ctx, d.dockerPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker run failed: %w\n%s", err, string(out))
	}

	containerID := strings.TrimSpace(string(out))
	return &StartResult{ContainerID: containerID}, nil
}

func (d *DockerBackend) Exec(ctx context.Context, opts ExecOpts) (*ExecResult, error) {
	if opts.TimeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.TimeoutSec)*time.Second)
		defer cancel()
	}

	start := time.Now()

	workDir := opts.WorkDir
	if workDir == "" {
		workDir = "/workspace"
	}
	args := []string{"exec", "-w", workDir}
	for k, v := range opts.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, opts.ContainerID, "sh", "-c", opts.Command)

	cmd := exec.CommandContext(ctx, d.dockerPath, args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	durationMs := time.Since(start).Milliseconds()

	exitCode := 0
	oomKilled := false

	if err != nil {
		if ctx.Err() != nil {
			return &ExecResult{
				ExitCode:   124, // timeout convention
				StdoutTail: "command timed out",
				DurationMs: durationMs,
			}, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			if exitCode == 137 {
				oomKilled = true
			}
		} else {
			return nil, fmt.Errorf("exec failed: %w", err)
		}
	}

	maxLines := opts.MaxLines
	if maxLines <= 0 {
		maxLines = 20
	}

	stdoutTail, stdoutTotal, stdoutTrunc := TruncateOutput(stdoutBuf.String(), maxLines)
	stderrTail, stderrTotal, stderrTrunc := TruncateOutput(stderrBuf.String(), maxLines)

	return &ExecResult{
		ExitCode:    exitCode,
		StdoutLines: stdoutTotal,
		StderrLines: stderrTotal,
		StdoutTail:  stdoutTail,
		StderrTail:  stderrTail,
		DurationMs:  durationMs,
		Truncated:   stdoutTrunc || stderrTrunc,
		OOMKilled:   oomKilled,
	}, nil
}

func (d *DockerBackend) Stop(ctx context.Context, id string) error {
	// Stop then remove; ignore errors (container may not exist)
	stopCmd := exec.CommandContext(ctx, d.dockerPath, "stop", "-t", "5", id)
	_ = stopCmd.Run()

	rmCmd := exec.CommandContext(ctx, d.dockerPath, "rm", "-f", id)
	_ = rmCmd.Run()

	return nil
}

func (d *DockerBackend) Status(ctx context.Context, id string) (*StatusResult, error) {
	cmd := exec.CommandContext(ctx, d.dockerPath, "inspect", "--format", "{{.State.Status}}", id)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &StatusResult{Running: false, Status: "not_found"}, nil
	}

	status := strings.TrimSpace(string(out))
	return &StatusResult{
		Running: status == "running",
		Status:  status,
	}, nil
}

func (d *DockerBackend) Pause(ctx context.Context, id string) error {
	cmd := exec.CommandContext(ctx, d.dockerPath, "pause", id)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker pause failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *DockerBackend) Resume(ctx context.Context, id string) error {
	cmd := exec.CommandContext(ctx, d.dockerPath, "unpause", id)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker unpause failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *DockerBackend) ReadFile(ctx context.Context, id, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, d.dockerPath, "exec", id, "cat", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("read file %q: %w (%s)", path, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (d *DockerBackend) CleanupBuilds(_ context.Context, _ time.Duration) (int, error) {
	return 0, nil // Docker builds don't leave pods behind
}

func (d *DockerBackend) WriteFile(ctx context.Context, id, path string, content []byte, mode string) error {
	if mode == "" {
		mode = "0644"
	}
	// Use sh -c to write content via stdin and set permissions
	shellCmd := fmt.Sprintf("cat > %q && chmod %s %q", path, mode, path)
	cmd := exec.CommandContext(ctx, d.dockerPath, "exec", "-i", id, "sh", "-c", shellCmd)
	cmd.Stdin = bytes.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("write file %q: %w (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}
