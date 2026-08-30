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
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// TestSyncParkedDeploymentAnnotations_LabelRemovalConverges is the regression
// guard for the scaled-to-zero litellm ghost (observed 2026-08-30 removing the
// qwen35-9b-ablit-rp serviceLabel): the steady-idle fast path returns before
// ensureDeployment, so a spec.serviceLabels edit re-stamped the Service but
// left the parked Deployment's flexinfer.ai/service-labels and
// litellm.flexinfer.ai/aliases annotations stale — and litellm's manager,
// which gathers aliases from Deployment annotations, kept advertising the
// removed label until a manual kubectl annotate. A parked model's Deployment
// annotations must converge on spec changes without an activation.
func TestSyncParkedDeploymentAnnotations_LabelRemovalConverges(t *testing.T) {
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

	b, ok := backend.Get("vllm")
	if !ok {
		t.Fatal("backend vllm not found")
	}

	// Model created with two service labels, then scaled to zero (Idle) and the
	// spec mutated to drop "borrowed-label". The Deployment still carries the
	// pre-edit stamp.
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "parked-rp", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			Backend:       "vllm",
			Source:        "pvc://weights",
			ServiceLabels: []string{"keep-label"},
			LiteLLM:       &aiv1alpha2.LiteLLMSpec{Enabled: ptr.To(true)},
		},
		Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseIdle},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name,
			Namespace: model.Namespace,
			Annotations: map[string]string{
				// Unmanaged annotation must survive the sync untouched.
				"deployment.kubernetes.io/revision": "7",
				AnnotationServiceLabels:             "keep-label,borrowed-label",
				AnnotationLiteLLMAliases:            "borrowed-label,keep-label",
				AnnotationLiteLLMServedModel:        model.Name,
			},
		},
		Spec: appsv1.DeploymentSpec{Replicas: ptr.To(int32(0))},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(model, deployment).Build()
	r := &ModelReconciler{Client: cl, Scheme: s}
	ctx := context.Background()

	// The staged scenario is exactly the steady-idle fast path — the branch
	// that used to return without touching the Deployment.
	steady, err := r.steadyIdleDeployment(ctx, model)
	if err != nil {
		t.Fatalf("steadyIdleDeployment() error = %v", err)
	}
	if !steady {
		t.Fatal("scenario must be the steady-idle fast path (zero-replica Deployment)")
	}

	if err := r.syncParkedDeploymentAnnotations(ctx, model, b); err != nil {
		t.Fatalf("syncParkedDeploymentAnnotations() error = %v", err)
	}

	got := &appsv1.Deployment{}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(deployment), got); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	if v := got.Annotations[AnnotationServiceLabels]; v != "keep-label" {
		t.Errorf("%s = %q, want %q", AnnotationServiceLabels, v, "keep-label")
	}
	if v := got.Annotations[AnnotationLiteLLMAliases]; strings.Contains(v, "borrowed-label") {
		t.Errorf("%s = %q still advertises the removed label", AnnotationLiteLLMAliases, v)
	}
	if v := got.Annotations[AnnotationLiteLLMAliases]; !strings.Contains(v, "keep-label") {
		t.Errorf("%s = %q lost the kept label", AnnotationLiteLLMAliases, v)
	}
	if v := got.Annotations["deployment.kubernetes.io/revision"]; v != "7" {
		t.Errorf("unmanaged annotation clobbered: revision = %q, want %q", v, "7")
	}
	// Convergence must not activate the lane.
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 0 {
		t.Errorf("replicas = %v, want 0 (annotation sync must not activate the lane)", got.Spec.Replicas)
	}
}

// TestSyncParkedDeploymentAnnotations_SteadyStateIsNoOp guards the idle
// reconcile-loop discipline: once the annotations match the spec, repeat syncs
// must not write (a write would bump resourceVersion and re-arm the
// Owns(&Deployment{}) watch — the same self-sustaining loop class fixed for
// idle status updates).
func TestSyncParkedDeploymentAnnotations_SteadyStateIsNoOp(t *testing.T) {
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

	b, ok := backend.Get("vllm")
	if !ok {
		t.Fatal("backend vllm not found")
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "parked-steady", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			Backend:       "vllm",
			Source:        "pvc://weights",
			ServiceLabels: []string{"keep-label"},
		},
		Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseIdle},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: model.Name, Namespace: model.Namespace},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(0))},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(model, deployment).Build()
	r := &ModelReconciler{Client: cl, Scheme: s}
	ctx := context.Background()

	// First sync converges the stale (empty) stamp.
	if err := r.syncParkedDeploymentAnnotations(ctx, model, b); err != nil {
		t.Fatalf("first syncParkedDeploymentAnnotations() error = %v", err)
	}
	converged := &appsv1.Deployment{}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(deployment), converged); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	if v := converged.Annotations[AnnotationServiceLabels]; v != "keep-label" {
		t.Fatalf("%s = %q, want %q", AnnotationServiceLabels, v, "keep-label")
	}
	rvAfterFirst := converged.ResourceVersion

	// Second sync of the converged Deployment must be a no-op write.
	if err := r.syncParkedDeploymentAnnotations(ctx, model, b); err != nil {
		t.Fatalf("second syncParkedDeploymentAnnotations() error = %v", err)
	}
	after := &appsv1.Deployment{}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(deployment), after); err != nil {
		t.Fatalf("get Deployment after second sync: %v", err)
	}
	if after.ResourceVersion != rvAfterFirst {
		t.Fatalf("steady sync rewrote Deployment: resourceVersion %s -> %s (expected no-op)",
			rvAfterFirst, after.ResourceVersion)
	}
}

// TestSyncParkedDeploymentAnnotations_MissingDeploymentIsNoError covers the
// window where a parked model's Deployment was deleted out of band: the sync
// must not fail the reconcile (the normal flow recreates the Deployment on
// activation).
func TestSyncParkedDeploymentAnnotations_MissingDeploymentIsNoError(t *testing.T) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		appsv1.AddToScheme,
		aiv1alpha2.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("AddToScheme() error = %v", err)
		}
	}

	b, ok := backend.Get("vllm")
	if !ok {
		t.Fatal("backend vllm not found")
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "parked-gone", Namespace: "flexinfer-system"},
		Spec:       aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://weights", ServiceLabels: []string{"keep-label"}},
		Status:     aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseIdle},
	}

	r := &ModelReconciler{Client: fake.NewClientBuilder().WithScheme(s).WithObjects(model).Build(), Scheme: s}
	if err := r.syncParkedDeploymentAnnotations(context.Background(), model, b); err != nil {
		t.Fatalf("syncParkedDeploymentAnnotations() with no Deployment error = %v", err)
	}
}
