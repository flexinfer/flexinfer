package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecMode controls which transport protocol the executor uses.
type ExecMode string

const (
	// ExecModeWebSocket uses the WebSocket-based executor (default).
	ExecModeWebSocket ExecMode = "websocket"
	// ExecModeSPDY uses the legacy SPDY executor.
	ExecModeSPDY ExecMode = "spdy"
)

// WSExecutor wraps a K8s clientset and rest config to execute commands
// in pods using WebSocket or SPDY transport.
type WSExecutor struct {
	clientset  kubernetes.Interface
	restConfig *restclient.Config
	namespace  string
	mode       ExecMode
}

// NewWSExecutor creates an executor that uses the specified transport mode.
// If mode is empty, it reads the DEVBOX_EXEC_MODE environment variable,
// defaulting to WebSocket.
func NewWSExecutor(clientset kubernetes.Interface, config *restclient.Config, namespace string, mode ExecMode) *WSExecutor {
	if mode == "" {
		mode = execModeFromEnv()
	}
	return &WSExecutor{
		clientset:  clientset,
		restConfig: config,
		namespace:  namespace,
		mode:       mode,
	}
}

// Mode returns the active exec transport mode.
func (w *WSExecutor) Mode() ExecMode {
	return w.mode
}

// execModeFromEnv reads DEVBOX_EXEC_MODE and returns the appropriate mode.
func execModeFromEnv() ExecMode {
	val := strings.ToLower(os.Getenv("DEVBOX_EXEC_MODE"))
	if val == "spdy" {
		return ExecModeSPDY
	}
	return ExecModeWebSocket
}

// Exec runs a command in a pod and returns the buffered result.
func (w *WSExecutor) Exec(ctx context.Context, req ExecRequest) (*ExecResult, error) {
	timeout := 5 * time.Minute
	if req.TimeoutSec > 0 {
		timeout = time.Duration(req.TimeoutSec) * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	shellCmd := buildShellCommand(req)
	execURL := w.buildExecURL(req.ContainerID, shellCmd, false)

	executor, err := w.createExecutor(execURL)
	if err != nil {
		return nil, fmt.Errorf("create executor: %w", err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	streamErr := executor.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
	})

	durationMs := time.Since(start).Milliseconds()

	exitCode := 0
	if streamErr != nil {
		if execCtx.Err() != nil {
			return &ExecResult{
				ExitCode:   124,
				StdoutTail: "command timed out",
				DurationMs: durationMs,
			}, nil
		}
		exitCode = parseExitCode(streamErr)
	}

	maxLines := req.MaxLines
	if maxLines <= 0 {
		maxLines = 20
	}

	stdoutTail, stdoutTotal, stdoutTrunc := truncateOutput(stdoutBuf.String(), maxLines)
	stderrTail, stderrTotal, stderrTrunc := truncateOutput(stderrBuf.String(), maxLines)

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

// StreamExec runs a command and sends output chunks to the channel.
// The channel is closed when the command completes.
func (w *WSExecutor) StreamExec(ctx context.Context, req ExecRequest, chunks chan<- ExecChunk) (*ExecResult, error) {
	defer close(chunks)

	timeout := 5 * time.Minute
	if req.TimeoutSec > 0 {
		timeout = time.Duration(req.TimeoutSec) * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	shellCmd := buildShellCommand(req)
	execURL := w.buildExecURL(req.ContainerID, shellCmd, false)

	executor, err := w.createExecutor(execURL)
	if err != nil {
		return nil, fmt.Errorf("create executor: %w", err)
	}

	stdoutWriter := NewStreamWriter("stdout", chunks)
	stderrWriter := NewStreamWriter("stderr", chunks)
	defer stdoutWriter.Close()
	defer stderrWriter.Close()

	// Also buffer for the summary result.
	var stdoutBuf, stderrBuf bytes.Buffer
	stdoutMulti := &multiWriter{writers: []writerCloser{
		{w: &stdoutBuf},
		{w: stdoutWriter},
	}}
	stderrMulti := &multiWriter{writers: []writerCloser{
		{w: &stderrBuf},
		{w: stderrWriter},
	}}

	streamErr := executor.StreamWithContext(execCtx, remotecommand.StreamOptions{
		Stdout: stdoutMulti,
		Stderr: stderrMulti,
	})

	durationMs := time.Since(start).Milliseconds()

	exitCode := 0
	if streamErr != nil {
		if execCtx.Err() != nil {
			return &ExecResult{
				ExitCode:   124,
				StdoutTail: "command timed out",
				DurationMs: durationMs,
			}, nil
		}
		exitCode = parseExitCode(streamErr)
	}

	maxLines := req.MaxLines
	if maxLines <= 0 {
		maxLines = 20
	}

	stdoutTail, stdoutTotal, stdoutTrunc := truncateOutput(stdoutBuf.String(), maxLines)
	stderrTail, stderrTotal, stderrTrunc := truncateOutput(stderrBuf.String(), maxLines)

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

// createExecutor creates the appropriate remotecommand.Executor based on mode.
func (w *WSExecutor) createExecutor(execURL *url.URL) (remotecommand.Executor, error) {
	switch w.mode {
	case ExecModeSPDY:
		return remotecommand.NewSPDYExecutor(w.restConfig, "POST", execURL)
	default:
		return remotecommand.NewWebSocketExecutor(w.restConfig, "GET", execURL.String())
	}
}

// buildExecURL constructs the pod exec URL for the K8s API.
func (w *WSExecutor) buildExecURL(podName, shellCmd string, stdin bool) *url.URL {
	opts := &corev1.PodExecOptions{
		Container: "devbox",
		Command:   []string{"sh", "-c", shellCmd},
		Stdout:    true,
		Stderr:    true,
		Stdin:     stdin,
	}

	req := w.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(w.namespace).
		SubResource("exec").
		VersionedParams(opts, scheme.ParameterCodec)

	return req.URL()
}

// buildShellCommand constructs the shell command string with env vars and workdir.
func buildShellCommand(req ExecRequest) string {
	shellCmd := req.Command
	if len(req.Env) > 0 {
		var envPrefix strings.Builder
		for k, v := range req.Env {
			envPrefix.WriteString(fmt.Sprintf("export %s=%q; ", k, v))
		}
		shellCmd = envPrefix.String() + shellCmd
	}
	if req.WorkDir != "" {
		shellCmd = fmt.Sprintf("cd %q && %s", req.WorkDir, shellCmd)
	}
	return shellCmd
}

// parseExitCode extracts exit code from K8s exec error messages.
func parseExitCode(err error) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	// K8s exec errors look like: "command terminated with exit code 2"
	if strings.Contains(msg, "exit code") {
		var code int
		idx := strings.LastIndex(msg, "exit code")
		if _, scanErr := fmt.Sscanf(msg[idx+len("exit code "):], "%d", &code); scanErr == nil {
			return code
		}
	}
	return 1
}

// truncateOutput keeps only the last maxLines lines from output.
func truncateOutput(output string, maxLines int) (string, int, bool) {
	if output == "" {
		return "", 0, false
	}
	lines := strings.Split(output, "\n")
	total := len(lines)
	if total > 0 && lines[total-1] == "" {
		lines = lines[:total-1]
		total = len(lines)
	}
	if maxLines <= 0 || total <= maxLines {
		return strings.Join(lines, "\n"), total, false
	}
	tail := lines[total-maxLines:]
	return strings.Join(tail, "\n"), total, true
}

// writerCloser wraps an io.Writer for use in multiWriter.
type writerCloser struct {
	w interface{ Write([]byte) (int, error) }
}

// multiWriter writes to multiple writers simultaneously.
type multiWriter struct {
	writers []writerCloser
}

func (mw *multiWriter) Write(p []byte) (int, error) {
	for _, w := range mw.writers {
		if _, err := w.w.Write(p); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}
