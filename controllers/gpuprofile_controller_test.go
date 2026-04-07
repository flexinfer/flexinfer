package controllers

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func gpuProfileInt32Ptr(v int32) *int32 { return &v }

func TestGPUProfileLookupOrFetch_LoadsFromAPIOnColdCache(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	profile := &aiv1alpha2.GPUProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gfx1100",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.GPUProfileSpec{
			Architecture:      "gfx1100",
			MaxCPUMemoryGB:    gpuProfileInt32Ptr(44),
			MaxGPUMemoryGB:    gpuProfileInt32Ptr(18),
			ContainerMemoryGB: gpuProfileInt32Ptr(48),
		},
	}

	r := &GPUProfileReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build(),
		Scheme: scheme,
	}

	got, ok, err := r.LookupOrFetch(context.Background(), "flexinfer-system", "gfx1100")
	if err != nil {
		t.Fatalf("LookupOrFetch returned error: %v", err)
	}
	if !ok {
		t.Fatal("LookupOrFetch did not find gfx1100 profile")
	}
	if got.MaxCPUMemoryGB == nil || *got.MaxCPUMemoryGB != 44 {
		t.Fatalf("MaxCPUMemoryGB = %v, want 44", got.MaxCPUMemoryGB)
	}
	if got.MaxGPUMemoryGB == nil || *got.MaxGPUMemoryGB != 18 {
		t.Fatalf("MaxGPUMemoryGB = %v, want 18", got.MaxGPUMemoryGB)
	}

	cached, ok := r.Lookup("gfx1100")
	if !ok {
		t.Fatal("Lookup did not return cached profile after fetch")
	}
	if cached.MaxGPUMemoryGB == nil || *cached.MaxGPUMemoryGB != 18 {
		t.Fatalf("cached MaxGPUMemoryGB = %v, want 18", cached.MaxGPUMemoryGB)
	}
}
