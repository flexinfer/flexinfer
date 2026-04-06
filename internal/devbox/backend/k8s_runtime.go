package backend

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// execMode returns the executor transport mode from the DEVBOX_EXEC_MODE env var.
// Default: "websocket". Set DEVBOX_EXEC_MODE=spdy to use the legacy SPDY transport.
func execMode() string {
	return strings.ToLower(os.Getenv("DEVBOX_EXEC_MODE"))
}

// newExecForMode creates a remotecommand.Executor using the appropriate transport.
// Default is WebSocket; set DEVBOX_EXEC_MODE=spdy to fall back to SPDY.
func newExecForMode(config *rest.Config, execURL *url.URL) (remotecommand.Executor, error) {
	if execMode() == "spdy" {
		return remotecommand.NewSPDYExecutor(config, "POST", execURL)
	}
	return remotecommand.NewWebSocketExecutor(config, "GET", execURL.String())
}

func (k *K8sBackend) Start(ctx context.Context, opts StartOpts) (*StartResult, error) {
	registryTag := k.registryTag(opts.ImageTag)

	// Check if a matching pod already exists and is running — reuse it.
	existing, err := k.clientset.CoreV1().Pods(k.namespace).Get(ctx, opts.Name, metav1.GetOptions{})
	if err == nil && existing.Status.Phase == corev1.PodRunning {
		// Pod exists and is running — check if image matches.
		if len(existing.Spec.Containers) > 0 && existing.Spec.Containers[0].Image == registryTag {
			return &StartResult{ContainerID: existing.Name}, nil
		}
		// Image mismatch — stop and recreate.
		_ = k.Stop(ctx, opts.Name)
	} else if err == nil {
		// Pod exists but not running — delete it.
		_ = k.Stop(ctx, opts.Name)
	}
	// If not found, proceed to create.

	pod := k.buildPodSpec(opts, registryTag)

	created, err := k.clientset.CoreV1().Pods(k.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create pod: %w", err)
	}

	// Wait for pod to be Running; cleanup dangling pod on failure
	if err := k.waitForPodRunning(ctx, opts.Name, 120*time.Second); err != nil {
		_ = k.Stop(ctx, opts.Name) // cleanup dangling pod
		return nil, fmt.Errorf("pod not ready: %w", err)
	}

	return &StartResult{ContainerID: created.Name}, nil
}

func (k *K8sBackend) Exec(_ context.Context, opts ExecOpts) (*ExecResult, error) {
	// Detach from the MCP request context so proxy timeouts don't kill
	// long-running test suites or builds. Use the exec's own timeout.
	timeout := 5 * time.Minute
	if opts.TimeoutSec > 0 {
		timeout = time.Duration(opts.TimeoutSec) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()

	// Build the command with workdir and env vars prepended
	shellCmd := opts.Command
	if len(opts.Env) > 0 {
		var envPrefix strings.Builder
		for k, v := range opts.Env {
			envPrefix.WriteString(fmt.Sprintf("export %s=%q; ", k, v))
		}
		shellCmd = envPrefix.String() + shellCmd
	}
	if opts.WorkDir != "" {
		shellCmd = fmt.Sprintf("cd %q && %s", opts.WorkDir, shellCmd)
	}

	// NFS cache flush: force the kernel to re-validate file attributes so
	// `make` sees correct mtimes after local edits synced via rsync.
	// This is lightweight (~1ms) and prepended to every exec.
	if k.nfsFlush && opts.WorkDir != "" {
		flushCmd := fmt.Sprintf("stat -f %q >/dev/null 2>&1; ", opts.WorkDir)
		shellCmd = flushCmd + shellCmd
	}

	req := k.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(opts.ContainerID).
		Namespace(k.namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "devbox",
			Command:   []string{"sh", "-c", shellCmd},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := newExecForMode(k.restConfig, req.URL())
	if err != nil {
		return nil, fmt.Errorf("create executor: %w", err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	streamErr := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
	})

	durationMs := time.Since(start).Milliseconds()

	exitCode := 0
	if streamErr != nil {
		if ctx.Err() != nil {
			return &ExecResult{
				ExitCode:   124,
				StdoutTail: "command timed out",
				DurationMs: durationMs,
			}, nil
		}
		// Extract exit code from error message if possible
		exitCode = parseExitCode(streamErr)
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
		OOMKilled:   exitCode == 137,
	}, nil
}

func (k *K8sBackend) Stop(ctx context.Context, id string) error {
	gracePeriod := int64(5)
	err := k.clientset.CoreV1().Pods(k.namespace).Delete(ctx, id, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("delete pod: %w", err)
	}
	return nil
}

func (k *K8sBackend) Status(ctx context.Context, id string) (*StatusResult, error) {
	pod, err := k.clientset.CoreV1().Pods(k.namespace).Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		if isNotFound(err) {
			return &StatusResult{Running: false, Status: "not_found"}, nil
		}
		return nil, fmt.Errorf("get pod: %w", err)
	}

	status := strings.ToLower(string(pod.Status.Phase))
	return &StatusResult{
		Running: pod.Status.Phase == corev1.PodRunning,
		Status:  status,
	}, nil
}

func (k *K8sBackend) Pause(_ context.Context, _ string) error {
	return ErrNotSupported
}

func (k *K8sBackend) Resume(_ context.Context, _ string) error {
	return ErrNotSupported
}

func (k *K8sBackend) ReadFile(ctx context.Context, id, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req := k.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(id).
		Namespace(k.namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "devbox",
			Command:   []string{"cat", path},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := newExecForMode(k.restConfig, req.URL())
	if err != nil {
		return nil, fmt.Errorf("create executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		return nil, fmt.Errorf("read file %q: %w (%s)", path, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (k *K8sBackend) WriteFile(ctx context.Context, id, path string, content []byte, mode string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if mode == "" {
		mode = "0644"
	}
	shellCmd := fmt.Sprintf("mkdir -p \"$(dirname %q)\" && cat > %q && chmod %s %q", path, path, mode, path)
	req := k.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(id).
		Namespace(k.namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "devbox",
			Command:   []string{"sh", "-c", shellCmd},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := newExecForMode(k.restConfig, req.URL())
	if err != nil {
		return fmt.Errorf("create executor: %w", err)
	}

	var stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  bytes.NewReader(content),
		Stderr: &stderr,
	}); err != nil {
		return fmt.Errorf("write file %q: %w (%s)", path, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
