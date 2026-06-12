package controllers

import (
	"context"
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func newWatchScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add api scheme: %v", err)
	}
	return s
}

func newGPUProfile(name, ns, arch string) *aiv1alpha2.GPUProfile {
	return &aiv1alpha2.GPUProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       aiv1alpha2.GPUProfileSpec{Architecture: arch},
	}
}

func newArchModel(name, ns string, vendor aiv1alpha2.GPUVendor, nodeSelector map[string]string) *aiv1alpha2.Model {
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       aiv1alpha2.ModelSpec{NodeSelector: nodeSelector},
	}
	if vendor != "" {
		m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: vendor}
	}
	return m
}

// newAMDGPUNode builds a Ready node advertising one AMD GPU with the given
// architecture label, addressable via kubernetes.io/hostname.
func newAMDGPUNode(name, arch string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"kubernetes.io/hostname":       name,
				"gpu.amd.com/gpu-architecture": arch,
			},
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceName("amd.com/gpu"): resource.MustParse("1"),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func requestNames(t *testing.T, r *ModelReconciler, profile *aiv1alpha2.GPUProfile, wantNS string) []string {
	t.Helper()
	requests := r.requestsForGPUProfileModels(context.Background(), profile)
	names := make([]string, 0, len(requests))
	for _, req := range requests {
		if req.Namespace != wantNS {
			t.Fatalf("unexpected namespace in request: %+v", req)
		}
		names = append(names, req.Name)
	}
	sort.Strings(names)
	return names
}

func TestRequestsForGPUProfileModels_SelectorArchIsAuthoritative(t *testing.T) {
	s := newWatchScheme(t)
	ns := "flexinfer-system"

	selMatch := newArchModel("bge-embed-radeonvii", ns, aiv1alpha2.GPUVendorAMD,
		map[string]string{"flexinfer.ai/gpu.arch": "gfx906"})
	// Explicit selector arch must exclude the model even though no gfx1100
	// node exists (detectGPU would error and conservatively include it).
	selOther := newArchModel("gemma4-26b-7900xtx", ns, aiv1alpha2.GPUVendorAMD,
		map[string]string{"flexinfer.ai/gpu.arch": "gfx1100"})
	cpuOnly := newArchModel("kokoro-tts-cpu", ns, "", nil)
	otherNS := newArchModel("clone-in-other-ns", "other-ns", aiv1alpha2.GPUVendorAMD,
		map[string]string{"flexinfer.ai/gpu.arch": "gfx906"})

	fakeClient := fake.NewClientBuilder().WithScheme(s).
		WithRuntimeObjects(selMatch, selOther, cpuOnly, otherNS).Build()
	r := &ModelReconciler{Client: fakeClient, Scheme: s}

	names := requestNames(t, r, newGPUProfile("gfx906", ns, "gfx906"), ns)
	want := []string{"bge-embed-radeonvii"}
	if !equalStrings(names, want) {
		t.Fatalf("requests = %v, want %v", names, want)
	}
}

func TestRequestsForGPUProfileModels_NodeDetectedArch(t *testing.T) {
	s := newWatchScheme(t)
	ns := "flexinfer-system"

	on906 := newArchModel("whisper-on-906", ns, aiv1alpha2.GPUVendorAMD,
		map[string]string{"kubernetes.io/hostname": "node-906"})
	on1100 := newArchModel("qwen-on-1100", ns, aiv1alpha2.GPUVendorAMD,
		map[string]string{"kubernetes.io/hostname": "node-1100"})
	// No NVIDIA node exists: detectGPU errors and the model is enqueued
	// conservatively (extra idempotent reconcile beats missed propagation).
	unresolvable := newArchModel("ollama-embed-nvidia", ns, aiv1alpha2.GPUVendorNVIDIA, nil)

	fakeClient := fake.NewClientBuilder().WithScheme(s).
		WithRuntimeObjects(on906, on1100, unresolvable,
			newAMDGPUNode("node-906", "gfx906"), newAMDGPUNode("node-1100", "gfx1100")).
		Build()
	r := &ModelReconciler{Client: fakeClient, Scheme: s}

	names := requestNames(t, r, newGPUProfile("gfx906", ns, "gfx906"), ns)
	want := []string{"ollama-embed-nvidia", "whisper-on-906"}
	if !equalStrings(names, want) {
		t.Fatalf("requests = %v, want %v", names, want)
	}
}

func TestRequestsForGPUProfileModels_ArchFallsBackToProfileName(t *testing.T) {
	s := newWatchScheme(t)
	ns := "flexinfer-system"

	model := newArchModel("bge-embed-radeonvii", ns, aiv1alpha2.GPUVendorAMD,
		map[string]string{"flexinfer.ai/gpu.arch": "gfx906"})
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(model).Build()
	r := &ModelReconciler{Client: fakeClient, Scheme: s}

	names := requestNames(t, r, newGPUProfile("gfx906", ns, ""), ns)
	want := []string{"bge-embed-radeonvii"}
	if !equalStrings(names, want) {
		t.Fatalf("requests = %v, want %v", names, want)
	}
}

func TestRequestsForGPUProfileModels_PrimesProfileCache(t *testing.T) {
	s := newWatchScheme(t)
	ns := "flexinfer-system"

	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	profiles := &GPUProfileReconciler{}
	r := &ModelReconciler{Client: fakeClient, Scheme: s, GPUProfiles: profiles}

	profile := newGPUProfile("gfx906", ns, "gfx906")
	profile.Spec.VRAMMB = 16368

	if _, ok := profiles.Lookup("gfx906"); ok {
		t.Fatal("profile cache unexpectedly warm before map function ran")
	}
	r.requestsForGPUProfileModels(context.Background(), profile)

	spec, ok := profiles.Lookup("gfx906")
	if !ok {
		t.Fatal("map function did not prime the profile spec cache")
	}
	if spec.VRAMMB != 16368 {
		t.Fatalf("primed spec VRAMMB = %d, want 16368", spec.VRAMMB)
	}
	if _, ok := profiles.LookupProfile("gfx906"); !ok {
		t.Fatal("map function did not prime the full-object cache")
	}
}

func TestRequestsForGPUProfileModels_NoFanoutForNonProfileObject(t *testing.T) {
	r := &ModelReconciler{}
	if got := r.requestsForGPUProfileModels(context.Background(), &corev1.Pod{}); len(got) != 0 {
		t.Fatalf("requests = %v, want empty for non-GPUProfile object", got)
	}
	if got := r.requestsForGPUProfileModels(context.Background(), nil); len(got) != 0 {
		t.Fatalf("requests = %v, want empty for nil object", got)
	}
}

func TestGPUProfileSpecChangePredicate(t *testing.T) {
	oldProfile := newGPUProfile("gfx906", "flexinfer-system", "gfx906")
	oldProfile.Generation = 1
	newSameGen := oldProfile.DeepCopy()
	newSpecEdit := oldProfile.DeepCopy()
	newSpecEdit.Generation = 2

	if !gpuProfileSpecChange.Create(event.CreateEvent{Object: oldProfile}) {
		t.Fatal("create event should fan out")
	}
	if gpuProfileSpecChange.Update(event.UpdateEvent{ObjectOld: oldProfile, ObjectNew: newSameGen}) {
		t.Fatal("status-only update (same generation) should not fan out")
	}
	if !gpuProfileSpecChange.Update(event.UpdateEvent{ObjectOld: oldProfile, ObjectNew: newSpecEdit}) {
		t.Fatal("spec edit (generation bump) should fan out")
	}
	if gpuProfileSpecChange.Delete(event.DeleteEvent{Object: oldProfile}) {
		t.Fatal("delete event should not fan out (cache-prime would race removal)")
	}
}
