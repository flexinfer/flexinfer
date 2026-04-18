package proxy

import (
	"errors"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestDetectStalledLoad(t *testing.T) {
	now := time.Now()
	past := func(d time.Duration) *metav1.Time {
		t := metav1.NewTime(now.Add(-d))
		return &t
	}

	tests := []struct {
		name      string
		model     *aiv1alpha2.Model
		threshold time.Duration
		wantStall bool
	}{
		{
			name:      "nil model",
			model:     nil,
			threshold: 120 * time.Second,
			wantStall: false,
		},
		{
			name: "phase not Loading",
			model: &aiv1alpha2.Model{
				Status: aiv1alpha2.ModelStatus{
					Phase:             aiv1alpha2.ModelPhaseReady,
					LoadingSubstage:   aiv1alpha2.LoadingSubstageLoadingWeights,
					LoadingProgressAt: past(10 * time.Minute),
				},
			},
			threshold: 120 * time.Second,
			wantStall: false,
		},
		{
			name: "substage not LoadingWeights",
			model: &aiv1alpha2.Model{
				Status: aiv1alpha2.ModelStatus{
					Phase:             aiv1alpha2.ModelPhaseLoading,
					LoadingSubstage:   aiv1alpha2.LoadingSubstageImagePulling,
					LoadingProgressAt: past(10 * time.Minute),
				},
			},
			threshold: 120 * time.Second,
			wantStall: false,
		},
		{
			name: "progress timestamp missing",
			model: &aiv1alpha2.Model{
				Status: aiv1alpha2.ModelStatus{
					Phase:           aiv1alpha2.ModelPhaseLoading,
					LoadingSubstage: aiv1alpha2.LoadingSubstageLoadingWeights,
				},
			},
			threshold: 120 * time.Second,
			wantStall: false,
		},
		{
			name: "progress fresh enough",
			model: &aiv1alpha2.Model{
				Status: aiv1alpha2.ModelStatus{
					Phase:             aiv1alpha2.ModelPhaseLoading,
					LoadingSubstage:   aiv1alpha2.LoadingSubstageLoadingWeights,
					LoadingProgressAt: past(10 * time.Second),
				},
			},
			threshold: 120 * time.Second,
			wantStall: false,
		},
		{
			name: "stalled past threshold",
			model: &aiv1alpha2.Model{
				Status: aiv1alpha2.ModelStatus{
					Phase:             aiv1alpha2.ModelPhaseLoading,
					LoadingSubstage:   aiv1alpha2.LoadingSubstageLoadingWeights,
					LoadingProgressAt: past(10 * time.Minute),
					Message:           "loading weights (31/34 shards, 141.75s/it)",
				},
			},
			threshold: 120 * time.Second,
			wantStall: true,
		},
		{
			name: "exactly at threshold is not stall yet",
			model: &aiv1alpha2.Model{
				Status: aiv1alpha2.ModelStatus{
					Phase:             aiv1alpha2.ModelPhaseLoading,
					LoadingSubstage:   aiv1alpha2.LoadingSubstageLoadingWeights,
					LoadingProgressAt: past(119 * time.Second),
				},
			},
			threshold: 120 * time.Second,
			wantStall: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectStalledLoad(tc.model, tc.threshold)
			if tc.wantStall && got == nil {
				t.Fatalf("wanted stall, got nil")
			}
			if !tc.wantStall && got != nil {
				t.Fatalf("unexpected stall: %v", got)
			}
			if tc.wantStall {
				if got.Substage != aiv1alpha2.LoadingSubstageLoadingWeights {
					t.Fatalf("unexpected substage: %q", got.Substage)
				}
				if got.ProgressAge <= tc.threshold {
					t.Fatalf("progress age should exceed threshold, got %v", got.ProgressAge)
				}
			}
		})
	}
}

func TestIsStalledLoadError(t *testing.T) {
	s := &StalledLoadError{
		Model:       "foo",
		Substage:    aiv1alpha2.LoadingSubstageLoadingWeights,
		ProgressAge: 5 * time.Minute,
		Message:     "loading weights (3/8, 90s/it)",
	}

	got, ok := isStalledLoadError(s)
	if !ok || got != s {
		t.Fatalf("direct sentinel should match, got=%v ok=%v", got, ok)
	}

	wrapped := fmt.Errorf("activation attempt failed: %w", s)
	got, ok = isStalledLoadError(wrapped)
	if !ok || got != s {
		t.Fatalf("wrapped sentinel should match, got=%v ok=%v", got, ok)
	}

	other := errors.New("unrelated")
	if _, ok := isStalledLoadError(other); ok {
		t.Fatalf("unrelated error should not match")
	}

	if _, ok := isStalledLoadError(nil); ok {
		t.Fatalf("nil should not match")
	}
}

func TestStalledLoadErrorString(t *testing.T) {
	s := &StalledLoadError{
		Model:       "gemma4-26b-a4b-gptq",
		Substage:    aiv1alpha2.LoadingSubstageLoadingWeights,
		ProgressAge: 8*time.Minute + 47*time.Second,
		Message:     "loading weights (31/34 shards, 141.75s/it)",
	}
	msg := s.Error()
	for _, want := range []string{
		"gemma4-26b-a4b-gptq",
		"LoadingWeights",
		"8m47s",
		"31/34 shards",
	} {
		if !contains(msg, want) {
			t.Fatalf("error string missing %q: %q", want, msg)
		}
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
