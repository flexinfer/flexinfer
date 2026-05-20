package controllers

import (
	"context"
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestStartupSweepShouldPoke(t *testing.T) {
	cases := []struct {
		name  string
		phase aiv1alpha2.ModelPhase
		want  bool
	}{
		{"empty phase is swept (brand-new Model after controller restart)", "", true},
		{"Pending is swept", aiv1alpha2.ModelPhasePending, true},
		{"Loading is swept", aiv1alpha2.ModelPhaseLoading, true},
		{"Ready is not swept", aiv1alpha2.ModelPhaseReady, false},
		{"Idle is not swept", aiv1alpha2.ModelPhaseIdle, false},
		{"Preempted is not swept", aiv1alpha2.ModelPhasePreempted, false},
		{"Failed is not swept", aiv1alpha2.ModelPhaseFailed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := startupSweepShouldPoke(tc.phase); got != tc.want {
				t.Fatalf("startupSweepShouldPoke(%q) = %v, want %v", tc.phase, got, tc.want)
			}
		})
	}
}

func TestRequestsForSharedGroupSiblings_FansOutToSiblings(t *testing.T) {
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add api scheme: %v", err)
	}

	groupName := "7900xtx-textgen"
	ns := "flexinfer-system"

	deleted := newSharedModel("whisper-kill-test-v2", ns, groupName)
	siblingA := newSharedModel("gemma4-26b-a4b-gptq", ns, groupName)
	siblingB := newSharedModel("qwen35-27b-opus-distill-gptq", ns, groupName)
	otherGroup := newSharedModel("flux-fill-inpainting", ns, "radeonvii-models")
	exclusive := newSharedModel("ollama-embed-gtx980ti", ns, "")
	otherNs := newSharedModel("clone-in-other-ns", "other-ns", groupName)

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(siblingA, siblingB, otherGroup, exclusive, otherNs).
		Build()
	r := &ModelReconciler{Client: fakeClient, Scheme: s}

	requests := r.requestsForSharedGroupSiblings(context.Background(), deleted)

	names := make([]string, 0, len(requests))
	for _, req := range requests {
		if req.Namespace != ns {
			t.Fatalf("unexpected namespace in request: %+v", req)
		}
		names = append(names, req.Name)
	}
	sort.Strings(names)
	want := []string{"gemma4-26b-a4b-gptq", "qwen35-27b-opus-distill-gptq"}
	if !equalStrings(names, want) {
		t.Fatalf("requests = %v, want %v", names, want)
	}
}

func TestRequestsForSharedGroupSiblings_NoFanoutForExclusiveModel(t *testing.T) {
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add api scheme: %v", err)
	}

	deleted := newSharedModel("standalone", "flexinfer-system", "")
	sibling := newSharedModel("unrelated-shared", "flexinfer-system", "some-group")

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(sibling).
		Build()
	r := &ModelReconciler{Client: fakeClient, Scheme: s}

	if got := r.requestsForSharedGroupSiblings(context.Background(), deleted); len(got) != 0 {
		t.Fatalf("requests = %v, want empty for non-shared deleted Model", got)
	}
}

func TestRequestsForSharedGroupSiblings_NoFanoutForNonModelObject(t *testing.T) {
	r := &ModelReconciler{}
	if got := r.requestsForSharedGroupSiblings(context.Background(), &corev1.Pod{}); len(got) != 0 {
		t.Fatalf("requests = %v, want empty for non-Model object", got)
	}
	if got := r.requestsForSharedGroupSiblings(context.Background(), nil); len(got) != 0 {
		t.Fatalf("requests = %v, want empty for nil object", got)
	}
}

func newSharedModel(name, ns, shared string) *aiv1alpha2.Model {
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: aiv1alpha2.ModelSpec{},
	}
	if shared != "" {
		model.Spec.GPU = &aiv1alpha2.GPUSpec{Shared: shared}
	}
	return model
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
