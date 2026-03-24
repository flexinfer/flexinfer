package backend

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
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
