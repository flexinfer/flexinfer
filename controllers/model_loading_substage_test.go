package controllers

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestDeriveLoadingSubstage(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		wantSub  aiv1alpha2.LoadingSubstage
		wantMsg  string // substring match when non-empty
		wantNone bool   // assert both sub and msg are empty
	}{
		{
			name:     "nil pod",
			pod:      nil,
			wantNone: true,
		},
		{
			name: "container pulling image",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "model",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{
								Reason:  "ImagePullBackOff",
								Message: "pull access denied for harbor/flex:foo",
							},
						},
					}},
				},
			},
			wantSub: aiv1alpha2.LoadingSubstageImagePulling,
			wantMsg: "pulling image for model",
		},
		{
			name: "container creating",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "model",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"},
						},
					}},
				},
			},
			wantSub: aiv1alpha2.LoadingSubstageInitializing,
			wantMsg: "ContainerCreating",
		},
		{
			name: "container running not ready",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{{
						Name:  "model",
						Ready: false,
						State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
					}},
				},
			},
			wantSub: aiv1alpha2.LoadingSubstageInitializing,
			wantMsg: "readiness probe not passing",
		},
		{
			name: "container running and ready → no substage",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{{
						Name:  "model",
						Ready: true,
						State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
					}},
				},
			},
			wantNone: true,
		},
		{
			name: "pod succeeded → no substage",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
			},
			wantNone: true,
		},
		{
			name: "init container pulling overrides primary state",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					InitContainerStatuses: []corev1.ContainerStatus{{
						Name: "wait-for-cache",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull", Message: "registry down"},
						},
					}},
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "model",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"},
						},
					}},
				},
			},
			wantSub: aiv1alpha2.LoadingSubstageImagePulling,
			wantMsg: "pulling init image for wait-for-cache",
		},
		{
			name: "unknown waiting reason falls back to Initializing",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "model",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: "WeirdReason"},
						},
					}},
				},
			},
			wantSub: aiv1alpha2.LoadingSubstageInitializing,
			wantMsg: "WeirdReason",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sub, msg := deriveLoadingSubstage(tc.pod)
			if tc.wantNone {
				if sub != "" || msg != "" {
					t.Fatalf("wanted empty substage/msg, got sub=%q msg=%q", sub, msg)
				}
				return
			}
			if sub != tc.wantSub {
				t.Fatalf("substage: want %q got %q", tc.wantSub, sub)
			}
			if tc.wantMsg != "" && !contains(msg, tc.wantMsg) {
				t.Fatalf("message: want contains %q got %q", tc.wantMsg, msg)
			}
		})
	}
}

func TestTruncateMessage(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"short kept", "hi", "hi"},
		{"strips newline", "first line\nsecond", "first line"},
		{"truncates long", repeat("x", 200), repeat("x", 159) + "…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateMessage(tc.in)
			if got != tc.want {
				t.Fatalf("want %q got %q", tc.want, got)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && stringIndex(haystack, needle) >= 0
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
