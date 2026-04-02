// Package e2e provides end-to-end tests for FlexInfer.
package e2e

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func stringPtr(s string) *string {
	return &s
}

func modelCachePromotableForServing(md *aiv1alpha1.ModelDeployment, cache *aiv1alpha1.ModelCache) bool {
	if md == nil || md.Spec.ModelCacheRef == nil || *md.Spec.ModelCacheRef == "" {
		return false
	}
	if cache == nil || cache.Name != *md.Spec.ModelCacheRef {
		return false
	}
	return cache.Status.Phase == aiv1alpha1.ModelCachePhaseReady
}

func TestModelCachePromotionGate(t *testing.T) {
	prevClient := k8sClient
	prevTimeouts := timeouts
	t.Cleanup(func() {
		k8sClient = prevClient
		timeouts = prevTimeouts
	})

	timeouts.ModelReady = 100 * time.Millisecond
	timeouts.PollInterval = 10 * time.Millisecond

	scheme := runtime.NewScheme()
	if err := aiv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	readyCache := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ready-cache",
			Namespace: "gate-ns",
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Phase: aiv1alpha1.ModelCachePhaseReady,
		},
	}
	deployment := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "serving-model",
			Namespace: "gate-ns",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:       "ollama",
			Model:         "llama3.2:1b",
			ModelCacheRef: stringPtr(readyCache.Name),
		},
	}

	k8sClient = fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(readyCache, deployment).
		Build()

	fixture := &Fixture{t: t, name: "promotion-gate"}
	ctx := context.Background()

	gotCache, err := fixture.WaitForModelCacheReady(ctx, readyCache.Name, readyCache.Namespace)
	if err != nil {
		t.Fatalf("ready cache did not satisfy promotion gate: %v", err)
	}
	if !modelCachePromotableForServing(deployment, gotCache) {
		t.Fatalf("expected deployment %s to be promotable with ready cache %s", deployment.Name, gotCache.Name)
	}

	if modelCachePromotableForServing(&aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "ollama",
			Model:   "llama3.2:1b",
		},
	}, gotCache) {
		t.Fatal("deployment without ModelCacheRef should not pass the promotion gate")
	}

	pendingCache := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pending-cache",
			Namespace: "gate-ns",
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Phase: aiv1alpha1.ModelCachePhasePending,
		},
	}

	k8sClient = fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pendingCache).
		Build()

	if _, err := fixture.WaitForModelCacheReady(ctx, pendingCache.Name, pendingCache.Namespace); err == nil {
		t.Fatal("pending cache unexpectedly satisfied the promotion gate")
	}
}
