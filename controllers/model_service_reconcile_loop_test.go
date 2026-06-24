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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

// TestEnsureService_RuntimeManagedPortIsIdempotent is the regression guard for
// the radeonvii (gfx906) multi-model embedding-plane Service reconcile hot loop.
//
// A runtime-managed model is reconciled by two paths in a single reconcile: the
// early, management-mode-agnostic ensureService (writes b.Port()) and the
// runtime-networking path (writes pkgrt.RuntimePortForBackend). For a llama.cpp
// backend b.Port() is the runtime API port (8080) while RuntimePortForBackend is
// the runtime backend port (8000), so the two writers used to flap Spec.Ports
// every reconcile. Each Service write fires the Owns(&Service{}) watch and
// re-enqueues the Model — a self-sustaining ~10-write/sec loop across the three
// co-Active members (bge-large, bge-reranker, gemma4-e4b).
//
// Once the runtime path owns the port and the selector is cleared, a subsequent
// early ensureService call must be a no-op: it must preserve the runtime port
// and not bump resourceVersion.
func TestEnsureService_RuntimeManagedPortIsIdempotent(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme(kubernetes) error = %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme(flexinfer) error = %v", err)
	}

	// llama.cpp: Port() == RuntimeAPIPort (8080) != RuntimeBackendPort (8000),
	// exactly the radeonvii embedding-plane mismatch.
	b, ok := backend.Get("llamacpp")
	if !ok {
		t.Fatal("llamacpp backend not found")
	}
	runtimePort := pkgrt.RuntimePortForBackend(b)
	if b.Port() == runtimePort {
		t.Fatalf("test precondition: backend port %d must differ from runtime port %d", b.Port(), runtimePort)
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "bge-large-radeonvii", Namespace: "flexinfer-system"},
		Spec:       aiv1alpha2.ModelSpec{Backend: "llamacpp"},
	}

	cl := fake.NewClientBuilder().WithScheme(s).Build()
	r := &ModelReconciler{Client: cl, Scheme: s}
	ctx := context.Background()

	get := func() *corev1.Service {
		svc := &corev1.Service{}
		if err := cl.Get(ctx, client.ObjectKeyFromObject(model), svc); err != nil {
			t.Fatalf("get service: %v", err)
		}
		return svc
	}

	// Establish the steady state of a runtime-managed Service: early create,
	// runtime path takes ownership of the port, selector is cleared.
	if err := r.ensureService(ctx, model, b); err != nil {
		t.Fatalf("initial ensureService: %v", err)
	}
	if err := r.ensureServiceWithPort(ctx, model, b, runtimePort, true); err != nil {
		t.Fatalf("runtime ensureServiceWithPort: %v", err)
	}
	if err := r.removeRuntimeServiceSelector(ctx, model); err != nil {
		t.Fatalf("removeRuntimeServiceSelector: %v", err)
	}

	settled := get()
	if settled.Spec.Selector != nil {
		t.Fatalf("runtime-managed service should have nil selector, got %v", settled.Spec.Selector)
	}
	if got := settled.Spec.Ports[0].Port; got != runtimePort {
		t.Fatalf("runtime port not established: got %d, want %d", got, runtimePort)
	}
	rvSteady := settled.ResourceVersion

	// The early ensureService call (enforcePort=false) on the now runtime-managed
	// Service must NOT re-assert b.Port(): no write, resourceVersion unchanged,
	// port preserved. This is the exact write that drove the hot loop.
	if err := r.ensureService(ctx, model, b); err != nil {
		t.Fatalf("steady-state ensureService: %v", err)
	}
	after := get()
	if after.ResourceVersion != rvSteady {
		t.Fatalf("early ensureService rewrote runtime-managed service: resourceVersion %s -> %s (expected no-op)",
			rvSteady, after.ResourceVersion)
	}
	if got := after.Spec.Ports[0].Port; got != runtimePort {
		t.Fatalf("early ensureService flapped the port: got %d, want %d", got, runtimePort)
	}

	// The runtime path call is likewise idempotent at steady state.
	if err := r.ensureServiceWithPort(ctx, model, b, runtimePort, true); err != nil {
		t.Fatalf("steady-state runtime ensureServiceWithPort: %v", err)
	}
	if rv := get().ResourceVersion; rv != rvSteady {
		t.Fatalf("steady-state runtime ensureServiceWithPort rewrote service: resourceVersion %s -> %s (expected no-op)",
			rvSteady, rv)
	}
}

// TestEnsureService_DeploymentManagedPortIsEnforced verifies the fix does not
// regress deployment-managed Services: when the selector is present (no runtime
// owns the port), the early ensureService call still enforces the backend port.
func TestEnsureService_DeploymentManagedPortIsEnforced(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme(kubernetes) error = %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme(flexinfer) error = %v", err)
	}

	b, ok := backend.Get("llamacpp")
	if !ok {
		t.Fatal("llamacpp backend not found")
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "deploy-model", Namespace: "flexinfer-system"},
		Spec:       aiv1alpha2.ModelSpec{Backend: "llamacpp"},
	}

	// Deployment-managed Service: selector present, stale port.
	existing := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: model.Name, Namespace: model.Namespace},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app": model.Name},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 1234}},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()
	r := &ModelReconciler{Client: cl, Scheme: s}
	ctx := context.Background()

	if err := r.ensureService(ctx, model, b); err != nil {
		t.Fatalf("ensureService: %v", err)
	}

	svc := &corev1.Service{}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(model), svc); err != nil {
		t.Fatalf("get service: %v", err)
	}
	if got := svc.Spec.Ports[0].Port; got != b.Port() {
		t.Fatalf("deployment-managed port not enforced: got %d, want %d", got, b.Port())
	}
}
