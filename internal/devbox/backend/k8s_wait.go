package backend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// waitForPodRunning watches until the pod reaches Running phase or timeout.
// Uses the Watch API for sub-second latency instead of polling.
func (k *K8sBackend) waitForPodRunning(ctx context.Context, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	watcher, err := k.clientset.CoreV1().Pods(k.namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + name,
	})
	if err != nil {
		return fmt.Errorf("watch pod: %w", err)
	}
	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		if event.Type == watch.Deleted {
			return fmt.Errorf("pod %s was deleted before reaching Running", name)
		}
		pod, ok := event.Object.(*corev1.Pod)
		if !ok {
			continue
		}
		switch pod.Status.Phase {
		case corev1.PodRunning:
			return nil
		case corev1.PodFailed, corev1.PodSucceeded:
			return fmt.Errorf("pod entered terminal phase: %s", podFailureReason(pod))
		}
		// Early exit on image pull errors
		for _, cs := range pod.Status.ContainerStatuses {
			if w := cs.State.Waiting; w != nil {
				if w.Reason == "ErrImagePull" || w.Reason == "ImagePullBackOff" {
					return fmt.Errorf("image pull error: %s — %s", w.Reason, w.Message)
				}
			}
		}
	}
	return fmt.Errorf("watch closed for pod %s", name)
}

// podFailureReason extracts a diagnostic string from a failed pod's container statuses.
func podFailureReason(pod *corev1.Pod) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if t := cs.State.Terminated; t != nil {
			parts := []string{fmt.Sprintf("exit_code=%d", t.ExitCode)}
			if t.Reason != "" {
				parts = append(parts, "reason="+t.Reason)
			}
			if t.Message != "" {
				parts = append(parts, "message="+t.Message)
			}
			return strings.Join(parts, " ")
		}
	}
	if pod.Status.Message != "" {
		return pod.Status.Message
	}
	return string(pod.Status.Phase)
}

// waitForPodDone watches until the pod reaches Succeeded or Failed, or timeout.
// Uses the Watch API for sub-second latency instead of polling.
// Returns early on image pull errors to avoid waiting the full timeout.
func (k *K8sBackend) waitForPodDone(ctx context.Context, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	watcher, err := k.clientset.CoreV1().Pods(k.namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + name,
	})
	if err != nil {
		return fmt.Errorf("watch pod: %w", err)
	}
	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		if event.Type == watch.Deleted {
			return fmt.Errorf("pod %s was deleted before completion", name)
		}
		pod, ok := event.Object.(*corev1.Pod)
		if !ok {
			continue
		}
		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			return nil
		case corev1.PodFailed:
			return fmt.Errorf("build pod failed: %s", podFailureReason(pod))
		}
		// Early exit on image pull errors
		for _, cs := range pod.Status.ContainerStatuses {
			if w := cs.State.Waiting; w != nil {
				if w.Reason == "ErrImagePull" || w.Reason == "ImagePullBackOff" {
					return fmt.Errorf("image pull error: %s — %s", w.Reason, w.Message)
				}
			}
		}
	}
	return fmt.Errorf("watch closed for pod %s", name)
}

// getPodLogs reads the last 100 lines from the buildah container.
func (k *K8sBackend) getPodLogs(ctx context.Context, podName string) (string, error) {
	tailLines := int64(100)
	req := k.clientset.CoreV1().Pods(k.namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: "buildah",
		TailLines: &tailLines,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("get logs: %w", err)
	}
	defer stream.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		return "", fmt.Errorf("read logs: %w", err)
	}
	return buf.String(), nil
}

// deletePod deletes a pod with zero grace period (immediate).
func (k *K8sBackend) deletePod(ctx context.Context, name string) error {
	gracePeriod := int64(0)
	err := k.clientset.CoreV1().Pods(k.namespace).Delete(ctx, name, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("delete pod: %w", err)
	}
	return nil
}

// parseExitCode extracts the exit code from a K8s exec error.
// Returns 1 as default for non-zero exits when code can't be parsed.
func parseExitCode(err error) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	// K8s exec errors look like: "command terminated with exit code 2"
	if strings.Contains(msg, "exit code") {
		var code int
		if _, scanErr := fmt.Sscanf(msg[strings.LastIndex(msg, "exit code")+len("exit code "):], "%d", &code); scanErr == nil {
			return code
		}
	}
	return 1
}

// surfaceExecStreamError writes the exec stream error text into stderrBuf
// when neither buffer captured any output from the command itself. This
// turns silent failures (pod gone, container terminating, exec channel
// rejected) into something actionable. Skipped when the buffers already
// have content — the command's own output takes priority.
//
// stdoutBuf may be nil for streaming-exec callers that maintain a tail
// ring buffer outside; pass nil and gate on your own totalLines counter.
func surfaceExecStreamError(stdoutBuf, stderrBuf *bytes.Buffer, streamErr error) {
	if streamErr == nil || stderrBuf == nil {
		return
	}
	if stderrBuf.Len() > 0 {
		return
	}
	if stdoutBuf != nil && stdoutBuf.Len() > 0 {
		return
	}
	stderrBuf.WriteString("exec error: ")
	stderrBuf.WriteString(streamErr.Error())
	stderrBuf.WriteByte('\n')
}

// isNotFound returns true if the error is a K8s "not found" error.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if apierrors.IsNotFound(err) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}
