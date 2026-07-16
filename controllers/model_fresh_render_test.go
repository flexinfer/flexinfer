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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// TestFreshModelForRender reproduces the stale-cache propagation bug: a
// spec.config edit (maxModelLen 32768 -> 65536) reaches the API server, but the
// reconcile's cached Model still carries the old value, so ensureDeployment
// renders the stale arg and never rolls the Deployment until a manual delete.
// freshModelForRender must bypass the cache via APIReader and return the current
// spec; without an APIReader it must fall back to the passed model.
func TestFreshModelForRender(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add k8s scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("add flexinfer scheme: %v", err)
	}

	// The API server holds the NEW config.
	apiModel := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "m", Namespace: "ns"},
		Spec:       aiv1alpha2.ModelSpec{Config: &apiextensionsv1.JSON{Raw: []byte(`{"maxModelLen":65536}`)}},
	}
	fc := fake.NewClientBuilder().WithScheme(s).WithObjects(apiModel).Build()

	// A stale cached Model still reports the old value (what the informer served).
	stale := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "m", Namespace: "ns"},
		Spec:       aiv1alpha2.ModelSpec{Config: &apiextensionsv1.JSON{Raw: []byte(`{"maxModelLen":32768}`)}},
	}

	t.Run("APIReader bypasses stale cache and returns current spec", func(t *testing.T) {
		r := &ModelReconciler{APIReader: fc}
		got := r.freshModelForRender(context.Background(), stale)
		if v := got.Spec.GetConfigMap()["maxModelLen"]; v != float64(65536) {
			t.Fatalf("expected fresh maxModelLen 65536, got %v (stale cache not bypassed)", v)
		}
	})

	t.Run("nil APIReader falls back to the passed model", func(t *testing.T) {
		r := &ModelReconciler{}
		got := r.freshModelForRender(context.Background(), stale)
		if v := got.Spec.GetConfigMap()["maxModelLen"]; v != float64(32768) {
			t.Fatalf("expected fallback maxModelLen 32768, got %v", v)
		}
	})

	t.Run("APIReader read miss falls back to the passed model", func(t *testing.T) {
		empty := fake.NewClientBuilder().WithScheme(s).Build()
		r := &ModelReconciler{APIReader: empty}
		got := r.freshModelForRender(context.Background(), stale)
		if v := got.Spec.GetConfigMap()["maxModelLen"]; v != float64(32768) {
			t.Fatalf("expected fallback maxModelLen 32768 on read miss, got %v", v)
		}
	})
}

func TestEnsureDeploymentRevalidatesFreshLlamaCppFeatureConfig(t *testing.T) {
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add k8s scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("add flexinfer scheme: %v", err)
	}

	// Reconcile saw a safe cached object, but the API server now contains a
	// certificate-gated opt-in. The final Deployment render must revalidate the
	// fresh object rather than launch it under the stale validation result.
	fresh := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "m", Namespace: "ns"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "pvc://models/model.gguf",
			Config:  &apiextensionsv1.JSON{Raw: []byte(`{"dedicatedDeployment":true,"slotSavePath":"/models/.flexinfer/slots/m"}`)},
		},
	}
	apiReader := fake.NewClientBuilder().WithScheme(s).WithObjects(fresh).Build()
	workloadClient := fake.NewClientBuilder().WithScheme(s).Build()
	r := &ModelReconciler{Client: workloadClient, APIReader: apiReader, Scheme: s}
	stale := fresh.DeepCopy()
	stale.Spec.Config = nil
	b, ok := backend.Get(backend.NameLlamaCpp)
	if !ok {
		t.Fatal("llamacpp backend not registered")
	}

	err := r.ensureDeployment(context.Background(), stale, b, backend.GPUVendorAMD, "gfx906", 1)
	if err == nil || !strings.Contains(err.Error(), "validating refreshed llama.cpp feature certificates") {
		t.Fatalf("ensureDeployment error = %v, want refreshed certificate rejection", err)
	}
	var deployments appsv1.DeploymentList
	if err := workloadClient.List(context.Background(), &deployments); err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if len(deployments.Items) != 0 {
		t.Fatalf("uncertified fresh config rendered %d deployments", len(deployments.Items))
	}
}
