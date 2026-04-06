package backend

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// StreamExecOpts configures a streaming command execution that delivers stdout
// lines incrementally via a callback.
type StreamExecOpts struct {
	ContainerID string
	Command     string
	WorkDir     string
	Env         map[string]string
	TimeoutSec  int
	OnLine      func(line []byte) // called for each complete stdout line
	OnStderr    func(line []byte) // optional: called for each stderr line
}

// lineCallbackWriter is an io.Writer that calls a callback for each
// newline-terminated line. Partial lines are buffered until the next newline.
type lineCallbackWriter struct {
	buf    bytes.Buffer
	onLine func(line []byte)
}

// Write implements io.Writer. It scans incoming data for newlines and calls
// onLine for each complete line.
func (w *lineCallbackWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		idx := bytes.IndexByte(p, '\n')
		if idx < 0 {
			// No newline found -- buffer the remainder.
			w.buf.Write(p)
			break
		}
		// Write everything up to (but not including) the newline to the buffer,
		// then deliver the complete line.
		w.buf.Write(p[:idx])
		if w.onLine != nil {
			// Deliver a copy so the callback can retain it safely.
			line := make([]byte, w.buf.Len())
			copy(line, w.buf.Bytes())
			w.onLine(line)
		}
		w.buf.Reset()
		p = p[idx+1:]
	}
	return total, nil
}

// Flush delivers any remaining buffered content as a final line.
func (w *lineCallbackWriter) Flush() {
	if w.buf.Len() > 0 && w.onLine != nil {
		line := make([]byte, w.buf.Len())
		copy(line, w.buf.Bytes())
		w.onLine(line)
		w.buf.Reset()
	}
}

// StreamExec runs a command in a K8s pod and streams stdout line-by-line
// to opts.OnLine. It returns an ExecResult with aggregate stats after the
// command completes.
//
// This function takes K8s client/config/namespace directly rather than
// depending on the K8sBackend struct, so it can be called from the spawn
// orchestrator without modifying the Backend interface.
func StreamExec(_ context.Context, clientset kubernetes.Interface, restConfig *rest.Config, namespace string, nfsFlush bool, opts StreamExecOpts) (*ExecResult, error) {
	// Build timeout context -- detach from caller context so proxy timeouts
	// don't kill long-running agent spawns.
	timeout := 5 * time.Minute
	if opts.TimeoutSec > 0 {
		timeout = time.Duration(opts.TimeoutSec) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()

	// Build the shell command (same pattern as K8sBackend.Exec).
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

	// NFS cache flush: force kernel to re-validate file attributes.
	if nfsFlush && opts.WorkDir != "" {
		flushCmd := fmt.Sprintf("stat -f %q >/dev/null 2>&1; ", opts.WorkDir)
		shellCmd = flushCmd + shellCmd
	}

	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(opts.ContainerID).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "devbox",
			Command:   []string{"sh", "-c", shellCmd},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := newExecForMode(restConfig, req.URL())
	if err != nil {
		return nil, fmt.Errorf("create executor: %w", err)
	}

	// Set up streaming stdout writer with tail buffer.
	const tailSize = 20
	tailBuf := make([]string, 0, tailSize)
	totalLines := 0

	stdoutWriter := &lineCallbackWriter{
		onLine: func(line []byte) {
			totalLines++
			// Maintain a ring of the last tailSize lines.
			if len(tailBuf) < tailSize {
				tailBuf = append(tailBuf, string(line))
			} else {
				tailBuf[totalLines%tailSize] = string(line)
			}
			// Forward to caller callback.
			if opts.OnLine != nil {
				opts.OnLine(line)
			}
		},
	}

	// Set up stderr writer (optional callback + capture).
	var stderrBuf bytes.Buffer
	var stderrWriter *lineCallbackWriter
	if opts.OnStderr != nil {
		stderrWriter = &lineCallbackWriter{
			onLine: func(line []byte) {
				stderrBuf.Write(line)
				stderrBuf.WriteByte('\n')
				opts.OnStderr(line)
			},
		}
	}

	var stderrTarget = func() interface{ Write([]byte) (int, error) } {
		if stderrWriter != nil {
			return stderrWriter
		}
		return &stderrBuf
	}()

	streamErr := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: stdoutWriter,
		Stderr: stderrTarget,
	})

	// Flush any remaining buffered partial lines.
	stdoutWriter.Flush()
	if stderrWriter != nil {
		stderrWriter.Flush()
	}

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
		exitCode = parseExitCode(streamErr)
	}

	// Build tail string from the tail buffer.
	var stdoutTail string
	if totalLines <= tailSize {
		stdoutTail = strings.Join(tailBuf, "\n")
	} else {
		// Reconstruct from ring buffer in order.
		ordered := make([]string, tailSize)
		start := (totalLines + 1) % tailSize
		for i := 0; i < tailSize; i++ {
			ordered[i] = tailBuf[(start+i)%tailSize]
		}
		stdoutTail = strings.Join(ordered, "\n")
	}

	stderrTail, stderrTotal, stderrTrunc := TruncateOutput(stderrBuf.String(), tailSize)

	return &ExecResult{
		ExitCode:    exitCode,
		StdoutLines: totalLines,
		StderrLines: stderrTotal,
		StdoutTail:  stdoutTail,
		StderrTail:  stderrTail,
		DurationMs:  durationMs,
		Truncated:   totalLines > tailSize || stderrTrunc,
		OOMKilled:   exitCode == 137,
	}, nil
}
