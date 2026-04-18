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

func TestParseVLLMLoadProgress(t *testing.T) {
	type result struct {
		sub aiv1alpha2.LoadingSubstage
		msg string
	}
	cases := []struct {
		name string
		in   string
		want result
	}{
		{
			name: "empty",
			in:   "",
			want: result{sub: "", msg: ""},
		},
		{
			name: "no recognizable signal",
			in:   "INFO random unrelated line\nINFO another line",
			want: result{sub: "", msg: ""},
		},
		{
			name: "shard progress (happy path)",
			in: "(EngineCore pid=431) Loading safetensors checkpoint shards:  50% Completed | 17/34 [00:36<00:33,  2.00s/it]\n" +
				"(EngineCore pid=431) Loading safetensors checkpoint shards:  53% Completed | 18/34 [00:39<00:33,  2.10s/it]\n",
			want: result{sub: aiv1alpha2.LoadingSubstageLoadingWeights, msg: "loading weights (18/34 shards, 2.10s/it)"},
		},
		{
			name: "shard progress captures stall (last line wins)",
			in: "Loading safetensors checkpoint shards:  88% Completed | 30/34 [00:59<00:07,  1.92s/it]\n" +
				"Loading safetensors checkpoint shards:  91% Completed | 31/34 [08:47<07:05, 141.75s/it]\n",
			want: result{sub: aiv1alpha2.LoadingSubstageLoadingWeights, msg: "loading weights (31/34 shards, 141.75s/it)"},
		},
		{
			name: "compiling takes precedence over shards",
			in: "Loading safetensors checkpoint shards: 100% Completed | 34/34 [01:10<00:00,  2.06s/it]\n" +
				"INFO [compilation.py:290] Capturing CUDA graphs for [1, 2, 4, 8]\n",
			want: result{sub: aiv1alpha2.LoadingSubstageCompiling, msg: "compiling kernels / capturing graphs"},
		},
		{
			name: "health check takes precedence over compiling and shards",
			in: "Loading safetensors checkpoint shards: 100% Completed | 34/34 [01:10<00:00,  2.06s/it]\n" +
				"Capturing CUDA graphs for [1,2]\n" +
				"INFO:     Uvicorn running on http://0.0.0.0:8000\n",
			want: result{sub: aiv1alpha2.LoadingSubstageHealthCheckPending, msg: "backend HTTP server up, awaiting readiness probe"},
		},
		{
			name: "unparseable shard line falls through to generic",
			in:   "Loading safetensors checkpoint shards: mystery output\n",
			want: result{sub: aiv1alpha2.LoadingSubstageLoadingWeights, msg: "loading weights"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub, msg := parseVLLMLoadProgress(tc.in)
			if sub != tc.want.sub || msg != tc.want.msg {
				t.Fatalf("parseVLLMLoadProgress:\n  in=%q\n  got  sub=%q msg=%q\n  want sub=%q msg=%q",
					tc.in, sub, msg, tc.want.sub, tc.want.msg)
			}
		})
	}
}

func TestIsRunningNotReady(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{name: "nil", pod: nil, want: false},
		{name: "no statuses", pod: &corev1.Pod{}, want: false},
		{
			name: "running and ready",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{
					Ready: true,
					State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				}},
			}},
			want: false,
		},
		{
			name: "running not ready",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{
					Ready: false,
					State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				}},
			}},
			want: true,
		},
		{
			name: "waiting only",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{
					State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
				}},
			}},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isRunningNotReady(tc.pod)
			if got != tc.want {
				t.Fatalf("want %v got %v", tc.want, got)
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
