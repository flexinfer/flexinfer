/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// TestUpdateStatusFromDeployment_IdleNoOpDoesNotRewrite is the regression guard
// for the idle-model reconcile hot loop: a steady scale-to-zero model used to
// issue an unconditional Status().Update on every reconcile, which advanced the
// object's resourceVersion (field-manager bookkeeping) and re-triggered the
// unfiltered For(&Model{}) watch — a self-sustaining ~1Hz loop. The second
// reconcile of an unchanged idle model must not write status, so resourceVersion
// stays put.
func TestUpdateStatusFromDeployment_IdleNoOpDoesNotRewrite(t *testing.T) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		appsv1.AddToScheme,
		aiv1alpha2.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("AddToScheme() error = %v", err)
		}
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "idle-model", Namespace: "flexinfer-system"},
		Spec:       aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://weights"},
		Status:     aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseIdle},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "idle-model", Namespace: "flexinfer-system"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(0))},
	}

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		WithObjects(model, deployment).
		Build()
	r := &ModelReconciler{Client: cl, Scheme: s}
	ctx := context.Background()

	get := func() *aiv1alpha2.Model {
		got := &aiv1alpha2.Model{}
		if err := cl.Get(ctx, client.ObjectKeyFromObject(model), got); err != nil {
			t.Fatalf("get model: %v", err)
		}
		return got
	}

	// First reconcile normalizes the status (phase/condition/endpoint) — a write
	// is expected here.
	m1 := get()
	if err := r.updateStatusFromDeployment(ctx, m1); err != nil {
		t.Fatalf("first updateStatusFromDeployment: %v", err)
	}
	rvAfterFirst := get().ResourceVersion

	// Second reconcile of the now-steady idle model must be a no-op: no status
	// write, so resourceVersion is unchanged. Without the statusUpdateIfChanged
	// guard the fake client (like the apiserver) bumps resourceVersion on the
	// unconditional Update, which is exactly what re-armed the watch loop.
	m2 := get()
	if err := r.updateStatusFromDeployment(ctx, m2); err != nil {
		t.Fatalf("second updateStatusFromDeployment: %v", err)
	}
	rvAfterSecond := get().ResourceVersion

	if rvAfterFirst != rvAfterSecond {
		t.Fatalf("idle reconcile rewrote status: resourceVersion %s -> %s (expected no-op)",
			rvAfterFirst, rvAfterSecond)
	}
	if got := get().Status.Phase; got != aiv1alpha2.ModelPhaseIdle {
		t.Fatalf("phase = %q, want Idle", got)
	}
}

// TestShouldEmitVRAMPressure_Throttled verifies the per-model cooldown collapses
// the VRAMPressure warning to one emission per window, even across many
// reconciles, while a distinct model (different UID) is unaffected.
func TestShouldEmitVRAMPressure_Throttled(t *testing.T) {
	r := &ModelReconciler{}
	a := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "a", UID: "uid-a"}}
	b := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "b", UID: "uid-b"}}

	if !r.shouldEmitVRAMPressure(a) {
		t.Fatal("first emit for model a should be allowed")
	}
	for i := 0; i < 5; i++ {
		if r.shouldEmitVRAMPressure(a) {
			t.Fatalf("emit %d for model a within cooldown should be suppressed", i)
		}
	}
	if !r.shouldEmitVRAMPressure(b) {
		t.Fatal("first emit for model b (distinct UID) should be allowed")
	}
}

func TestSteadyIdleDeployment(t *testing.T) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		appsv1.AddToScheme,
		aiv1alpha2.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("AddToScheme() error = %v", err)
		}
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "parked", Namespace: "flexinfer-system"},
		Status:     aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseIdle},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: model.Name, Namespace: model.Namespace},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(0))},
	}
	r := &ModelReconciler{Client: fake.NewClientBuilder().WithScheme(s).WithObjects(deployment).Build()}

	steady, err := r.steadyIdleDeployment(context.Background(), model)
	if err != nil {
		t.Fatalf("steadyIdleDeployment() error = %v", err)
	}
	if !steady {
		t.Fatal("zero-replica idle Deployment should be steady")
	}

	deployment.Spec.Replicas = ptr.To(int32(1))
	if err := r.Update(context.Background(), deployment); err != nil {
		t.Fatalf("update Deployment: %v", err)
	}
	steady, err = r.steadyIdleDeployment(context.Background(), model)
	if err != nil {
		t.Fatalf("steadyIdleDeployment() after scale-up error = %v", err)
	}
	if steady {
		t.Fatal("running Deployment must reconcile down to zero")
	}
}
