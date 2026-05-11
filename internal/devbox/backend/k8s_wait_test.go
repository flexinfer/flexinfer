package backend

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

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
