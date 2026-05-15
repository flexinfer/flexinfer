package backend

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestSurfaceExecStreamError_EmptyBuffersGetStreamErrText(t *testing.T) {
	// Mirrors the canary failure: K8s exec stream errors out for a
	// reason that doesn't include "exit code N" (pod gone, container
	// terminating). Without surfacing, the operator sees `cmd exited 1
	// (no output)`. With surfacing, the actual cause lands in stderr.
	var stdout, stderr bytes.Buffer
	streamErr := errors.New("pods \"buildah-build-abc\" not found")
	surfaceExecStreamError(&stdout, &stderr, streamErr)
	if !strings.Contains(stderr.String(), "pods \"buildah-build-abc\" not found") {
		t.Errorf("stderr did not capture streamErr: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "exec error:") {
		t.Errorf("stderr missing 'exec error:' prefix: %q", stderr.String())
	}
}

func TestSurfaceExecStreamError_PreservesExistingStderr(t *testing.T) {
	// When the command itself wrote to stderr before the stream errored,
	// the command's own output takes priority — we don't bury it under
	// the exec error.
	var stdout, stderr bytes.Buffer
	stderr.WriteString("real error from command\n")
	surfaceExecStreamError(&stdout, &stderr, errors.New("connection reset"))
	if strings.Contains(stderr.String(), "exec error:") {
		t.Errorf("should not overwrite existing stderr: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "real error from command") {
		t.Errorf("real stderr lost: %q", stderr.String())
	}
}

func TestSurfaceExecStreamError_PreservesExistingStdout(t *testing.T) {
	// Same idea but for stdout — if the command produced output we want
	// to keep, we don't pollute stderr with a misleading exec-level error
	// that landed after the command had already started reporting.
	var stdout, stderr bytes.Buffer
	stdout.WriteString("partial result\n")
	surfaceExecStreamError(&stdout, &stderr, errors.New("connection reset"))
	if stderr.Len() != 0 {
		t.Errorf("stderr should stay empty when stdout has content: %q", stderr.String())
	}
}

func TestSurfaceExecStreamError_NilStreamErrIsNoop(t *testing.T) {
	var stdout, stderr bytes.Buffer
	surfaceExecStreamError(&stdout, &stderr, nil)
	if stderr.Len() != 0 || stdout.Len() != 0 {
		t.Errorf("nil err should leave buffers untouched, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestSurfaceExecStreamError_NilStdoutBufAllowed(t *testing.T) {
	// stream_exec.go maintains stdout as a ring buffer outside this helper
	// and passes nil; the helper must not panic and must still surface
	// the stream error in stderr.
	var stderr bytes.Buffer
	surfaceExecStreamError(nil, &stderr, errors.New("upgrade request failed"))
	if !strings.Contains(stderr.String(), "upgrade request failed") {
		t.Errorf("nil stdout path lost streamErr: %q", stderr.String())
	}
}

func TestPodFailureReason_Terminated(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 137,
							Reason:   "OOMKilled",
							Message:  "container used too much memory",
						},
					},
				},
			},
		},
	}
	got := podFailureReason(pod)
	if got != "exit_code=137 reason=OOMKilled message=container used too much memory" {
		t.Errorf("got %q", got)
	}
}

func TestPodFailureReason_TerminatedExitCodeOnly(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
						},
					},
				},
			},
		},
	}
	got := podFailureReason(pod)
	if got != "exit_code=1" {
		t.Errorf("got %q, want %q", got, "exit_code=1")
	}
}

func TestPodFailureReason_PodMessage(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Message: "node was drained",
		},
	}
	got := podFailureReason(pod)
	if got != "node was drained" {
		t.Errorf("got %q, want %q", got, "node was drained")
	}
}

func TestPodFailureReason_PhaseOnly(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
		},
	}
	got := podFailureReason(pod)
	if got != "Failed" {
		t.Errorf("got %q, want %q", got, "Failed")
	}
}

func TestPodFailureReason_MultipleContainers(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{},
					},
				},
				{
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 2,
							Reason:   "Error",
						},
					},
				},
			},
		},
	}
	got := podFailureReason(pod)
	if got != "exit_code=2 reason=Error" {
		t.Errorf("got %q, want %q", got, "exit_code=2 reason=Error")
	}
}

func TestPodFailureReason_EmptyStatus(t *testing.T) {
	pod := &corev1.Pod{}
	got := podFailureReason(pod)
	if got != "" {
		t.Errorf("got %q, want empty (default phase)", got)
	}
}

func TestWaitForPodRunningReturnsWhenPodDeleted(t *testing.T) {
	k := testK8sBackend()
	clientset := k8sfake.NewSimpleClientset()
	podWatch := watch.NewFake()
	clientset.PrependWatchReactor("pods", func(action k8stesting.Action) (bool, watch.Interface, error) {
		return true, podWatch, nil
	})
	k.clientset = clientset

	errCh := make(chan error, 1)
	go func() {
		errCh <- k.waitForPodRunning(context.Background(), "spawn-pod", time.Minute)
	}()

	podWatch.Delete(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "spawn-pod", Namespace: k.namespace}})

	err := <-errCh
	if err == nil || !strings.Contains(err.Error(), "deleted before reaching Running") {
		t.Fatalf("waitForPodRunning error = %v, want deleted pod error", err)
	}
}

func TestWaitForPodDoneReturnsWhenPodDeleted(t *testing.T) {
	k := testK8sBackend()
	clientset := k8sfake.NewSimpleClientset()
	podWatch := watch.NewFake()
	clientset.PrependWatchReactor("pods", func(action k8stesting.Action) (bool, watch.Interface, error) {
		return true, podWatch, nil
	})
	k.clientset = clientset

	errCh := make(chan error, 1)
	go func() {
		errCh <- k.waitForPodDone(context.Background(), "build-pod", time.Minute)
	}()

	podWatch.Delete(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "build-pod", Namespace: k.namespace}})

	err := <-errCh
	if err == nil || !strings.Contains(err.Error(), "deleted before completion") {
		t.Fatalf("waitForPodDone error = %v, want deleted pod error", err)
	}
}
